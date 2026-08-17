// Package store maps ledgers onto git phantom refs: refs/ledger/<slug>,
// one commit per event, CAS appends, batched reads.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ledger/internal/dag"
	"ledger/internal/gitx"
	"ledger/internal/model"
)

type ExpectMode int

const (
	ExpectPresent ExpectMode = iota
	ExpectAbsent
)

// Precondition runs against a fresh, backward-windowed read of a ledger's
// event chain inside every CAS retry attempt of AppendChecked — never a
// pre-loop snapshot (spec rule 7's atomicity contract). events is
// oldest-first; reachedRoot reports whether events already reaches the
// ledger's very first commit, i.e. whether this read holds the ledger's
// FULL history rather than a partial backward window. A Precondition that
// cannot yet decide from a partial window (some check's inputs haven't
// shown up in events, and their absence hasn't been proven either) returns
// ErrNeedsMoreHistory to ask runPrecondition for a bigger window — but MUST
// decide definitively once reachedRoot is true, since there is no more
// history to give. Returning any other error aborts the append with that
// error; nothing is written.
//
// SCOPE, stated plainly: this narrowing covers only the fresh read taken
// inside each CAS retry attempt (runPrecondition, below) — it is a property
// of AppendChecked's retry loop, not of the command that calls it. A
// command like `set` still resolves the ledger (and folds its full event
// history) once up front via Ctx.Load/PickLedger, and any of its own
// pre-append bookkeeping (e.g. an idempotency-key scan over that already-
// loaded history) runs before AppendChecked or this Precondition are ever
// invoked. Nothing about that up-front read scales with this contract; only
// the per-attempt precondition read does.
type Precondition func(events []model.Event, reachedRoot bool) error

// ErrNeedsMoreHistory is the sentinel a Precondition returns to request a
// larger backward window (spec rule 8's chunked-read contract): this is
// never a real failure and never reaches AppendChecked's caller —
// runPrecondition always retries with a bigger window, or the full chain,
// before giving up.
var ErrNeedsMoreHistory = errors.New("needs_more_history")

var (
	ErrUnknownLedger = errors.New("unknown_ledger")
	ErrSlugExists    = errors.New("slug_exists")
	ErrCASExhausted  = errors.New("cas_exhausted")
)

type Store struct{ Repo gitx.Repo }

func ref(slug string) string { return "refs/ledger/" + slug }

// Resolution is what Resolve settled on.
type Resolution struct {
	Store Store
	// Note is non-empty only in the same-directory collision case, when a
	// single directory holds both .ledger.git and .git — callers print which
	// one was chosen.
	Note string
	// Shadowed is the path of a store of the *other* kind sitting strictly
	// higher in the ancestry, which the choice above shadowed. Ambient
	// resolution only; empty when nothing of the other kind is up there.
	Shadowed string
}

// Resolve implements the spec's store-resolution order:
// --store flag > $LEDGER_DIR > nearest ancestor holding .ledger.git or .git
// (.ledger.git beats .git within one directory).
func Resolve(storeFlag string) (Resolution, error) {
	if storeFlag != "" {
		return Resolution{Store: storeFor(storeFlag)}, nil
	}
	if d := os.Getenv("LEDGER_DIR"); d != "" {
		return Resolution{Store: storeFor(d)}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Resolution{}, err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		lg := filepath.Join(dir, ".ledger.git")
		gt := filepath.Join(dir, ".git")
		lgOK := exists(lg)
		gtOK := exists(gt)
		if lgOK && gtOK {
			return Resolution{Store: Store{Repo: gitx.Repo{Dir: lg}},
				Note: fmt.Sprintf("using store %s (a git repo is also here)", lg)}, nil
		}
		if lgOK {
			return Resolution{Store: Store{Repo: gitx.Repo{Dir: lg}}, Shadowed: shadowedAbove(dir, false)}, nil
		}
		if gtOK {
			return Resolution{Store: Store{Repo: gitx.Repo{Dir: dir}}, Shadowed: shadowedAbove(dir, true)}, nil
		}
		if dir == filepath.Dir(dir) {
			return Resolution{}, fmt.Errorf("no git repo or .ledger.git found from %s upward", cwd)
		}
	}
}

// shadowedAbove continues the ancestor walk above the directory Resolve
// stopped at, looking for a store of the other kind: a bare .ledger.git
// above a chosen repo (wantBare), or a repo above a chosen bare store.
// Field failure it exists for: a misplaced `ledger init` put a bare store in
// a sandbox root, every read inside the project repo resolved to the repo's
// own empty store, and nothing ever named the other one. Same-kind ancestors
// are skipped — a repo inside a repo is the ordinary nested-checkout case,
// where nearest-wins is the intended answer, not a surprise. Stat calls only,
// and it stops at the filesystem root, exactly where Resolve's own walk does.
func shadowedAbove(dir string, wantBare bool) string {
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		if wantBare {
			if lg := filepath.Join(d, ".ledger.git"); exists(lg) {
				return lg
			}
		} else if exists(filepath.Join(d, ".git")) {
			return d
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

func storeFor(path string) Store {
	if strings.HasSuffix(path, ".ledger.git") || strings.HasSuffix(path, ".git") {
		return Store{Repo: gitx.Repo{Dir: path}}
	}
	if exists(filepath.Join(path, ".ledger.git")) {
		return Store{Repo: gitx.Repo{Dir: filepath.Join(path, ".ledger.git")}}
	}
	return Store{Repo: gitx.Repo{Dir: path}}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func (s Store) Slugs() ([]string, error) {
	out, stderr, code := s.Repo.Git("", "for-each-ref", "--format=%(refname)", "refs/ledger/")
	if code != 0 {
		return nil, fmt.Errorf("git_failed: %s", stderr)
	}
	var slugs []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		slugs = append(slugs, strings.TrimPrefix(line, "refs/ledger/"))
	}
	return slugs, nil
}

func (s Store) head(slug string) (string, bool) {
	out, _, code := s.Repo.Git("", "rev-parse", "-q", "--verify", ref(slug))
	return out, code == 0
}

func (s Store) HeadID(slug string) (string, error) {
	h, ok := s.head(slug)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	return h[:10], nil
}

// Events reads the whole chain in the spec's pinned fold order (Addition
// 1: Kahn's topological sort, ready set keyed on parsed timestamp then full
// sha, sentinels contracted out before the sort) rather than git's own
// traversal order, which is merge-parent-dependent and wrong on a divergent
// (synced) DAG. It is a thin wrapper over EventsDAG that discards the
// dag.Result.
func (s Store) Events(slug string) ([]model.Event, model.Meta, error) {
	evs, meta, _, err := s.EventsDAG(slug)
	return evs, meta, err
}

// EventsDAG is Events plus the dag.Result the fold was computed from
// (Children/Roots over the sentinel-contracted DAG) — sync's merge-target
// and contested-ancestry logic needs the DAG shape, not just the flattened
// event list.
//
// Reads the whole chain with exactly two subprocesses: one `git log` for
// the commit list AND every commit's parents (traversal order is
// irrelevant — the fold order comes from dag.Sort, not from git), one
// `cat-file --batch` requesting <sha>:event.json and <sha>:meta.json for
// every commit. git resolves each rev:path server-side, so no per-commit
// ls-tree is needed. meta.json is read independently of event.json's
// presence — a torn commit's missing event never hides a meta.json sitting
// alongside it. A commit is a sentinel (contracted out of the fold, never
// dropped) when its event.json is missing or unparseable, or its event
// type is "sync".
func (s Store) EventsDAG(slug string) ([]model.Event, model.Meta, dag.Result, error) {
	var meta model.Meta
	out, _, code := s.Repo.Git("", "log", "--format=%H%x09%P", ref(slug))
	if code != 0 || out == "" {
		return nil, meta, dag.Result{}, fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	lines := strings.Split(out, "\n")
	commits := make([]string, len(lines))
	parents := make([][]string, len(lines))
	for i, line := range lines {
		c, p, _ := strings.Cut(line, "\t")
		commits[i] = c
		if p = strings.TrimSpace(p); p != "" {
			parents[i] = strings.Fields(p)
		}
	}

	reqs := make([]string, 0, len(commits)*2)
	for _, c := range commits {
		reqs = append(reqs, c+":event.json", c+":meta.json")
	}
	contents, present := s.catBatch(reqs)

	nodes := make([]dag.Node, len(commits))
	byEvent := make(map[string]model.Event, len(commits))
	for i, c := range commits {
		evIdx, metaIdx := 2*i, 2*i+1
		if present[metaIdx] {
			json.Unmarshal([]byte(contents[metaIdx]), &meta)
		}
		node := dag.Node{SHA: c, Parents: parents[i]}
		var ev model.Event
		if !present[evIdx] {
			node.IsSentinel = true // torn/foreign commit: contract out, never crash a read
		} else if err := json.Unmarshal([]byte(contents[evIdx]), &ev); err != nil {
			node.IsSentinel = true
		} else {
			ev.ID = c[:10]
			node.TS = ev.TS
			node.IsSentinel = ev.Type == "sync"
			byEvent[c] = ev
		}
		nodes[i] = node
	}

	result := dag.Sort(nodes)
	evs := make([]model.Event, 0, len(result.Order))
	for _, sha := range result.Order {
		evs = append(evs, byEvent[sha])
	}
	return evs, meta, result, nil
}

// windowProbeSize is the sanctioned chunked backward-read shape (spec rule
// 8): runPrecondition tries one window of this size, bounded `git log -n
// <n>` plus one `cat-file --batch` — never a per-event subprocess — before
// falling back to Events' whole-chain fold.
//
// A single 64-event probe, not a 64/256/1024 staircase: review caught an
// intermediate-tier staircase actively losing to a plain whole-chain read
// on the canonical guarded write. Rule 5's human signal is key-scoped and
// gates every guarded write regardless of which field it touches, so a
// Precondition needs the target key's labels resolved on essentially every
// guarded write — and most keys are never labeled at all, which (per rule
// 8's own stated asymmetry) can only be PROVEN by walking to the chain
// root. On that walk, 256 and 1024 are never big enough either — a
// never-labeled key's absence proof only resolves at the root, however far
// that is — so a 64/256/1024 staircase paid for three wasted reads before
// falling back to Events' own whole-chain fold anyway: worse than just
// reading the whole chain once (internal/cmd/scale_test.go's
// TestSetPreconditionScalingShape measures both shapes side by side at the
// parent spec's 5,000-event scale — see its doc comment for this
// sandbox's numbers). One 64-event probe keeps the win for the
// actually-common case (a recently-touched key whose every needed fact —
// including labels — resolves in the newest 64 events) while paying for
// the inherent absence-proof cost exactly once, not three times over.
const windowProbeSize = 64

// EventsWindow reads the most recent n commits (oldest-first within the
// window), with the same two-subprocess shape as Events: one bounded `git
// log -n <n>` for the commit list (also carrying each commit's parent hash,
// to detect the chain root) plus one `cat-file --batch` for their
// event.json blobs. meta.json is never requested here — a Precondition
// never needs it (callers already hold Meta from the ledger's own load), so
// skipping it halves EventsWindow's cat-file traffic versus Events.
// reachedRoot reports whether the window's OLDEST commit is the ledger's
// true root (no parent) — the fact a Precondition needs to tell "not found
// in this window" apart from "provably absent" (spec rule 8's degenerate
// absence-proof case). This is the rev-14 windowed-read primitive: the
// parent spec's only batched read is Events' whole-chain fold: a
// Precondition whose inputs resolve from a key's recent history never pays
// for it.
func (s Store) EventsWindow(slug string, n int) (events []model.Event, reachedRoot bool, err error) {
	out, _, code := s.Repo.Git("", "log", "--reverse", "-n", strconv.Itoa(n), "--format=%H%x09%P", ref(slug))
	if code != 0 || out == "" {
		return nil, false, fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	lines := strings.Split(out, "\n")
	commits := make([]string, len(lines))
	for i, line := range lines {
		c, _, _ := strings.Cut(line, "\t")
		commits[i] = c
	}
	_, parents, _ := strings.Cut(lines[0], "\t") // lines[0] is the window's oldest commit (--reverse)
	reachedRoot = strings.TrimSpace(parents) == ""

	reqs := make([]string, len(commits))
	for i, c := range commits {
		reqs[i] = c + ":event.json"
	}
	contents, present := s.catBatch(reqs)
	events = make([]model.Event, 0, len(commits))
	for i, c := range commits {
		if !present[i] {
			continue // torn/foreign commit: skip, never crash a read
		}
		var ev model.Event
		if err := json.Unmarshal([]byte(contents[i]), &ev); err != nil {
			continue
		}
		ev.ID = c[:10]
		events = append(events, ev)
	}
	return events, reachedRoot, nil
}

// Committers reads every commit's committer name in one `git log` pass and
// keys the result by 10-char event id, matching Events' id truncation — the
// committer name holds the harness marker (terminal/claude-code/codex/etc.),
// distinct from the event's own recorded author.
func (s Store) Committers(slug string) (map[string]string, error) {
	out, _, code := s.Repo.Git("", "log", `--format=%H %cn`, ref(slug))
	if code != 0 || out == "" {
		return nil, fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, cn, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		m[sha[:10]] = cn
	}
	return m, nil
}

// catBatch fetches every id (an object sha, or a "<rev>:<path>" spec) with a
// single `git cat-file --batch` call. cat-file replies in request order, so
// results are returned positionally as parallel slices — never keyed by the
// echoed identifier, which is unreliable when the same id repeats. Uses
// GitRaw (not Git) because parsing relies on exact declared byte sizes;
// Git's trailing-newline trim would corrupt a payload that itself ends in
// "\n". present[i] is false when git reports that request missing.
func (s Store) catBatch(ids []string) (contents []string, present []bool) {
	contents = make([]string, len(ids))
	present = make([]bool, len(ids))
	if len(ids) == 0 {
		return contents, present
	}
	out, _, _ := s.Repo.GitRaw(strings.Join(ids, "\n"), "cat-file", "--batch")
	rest := out
	for i := range ids {
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		hdr := strings.Fields(rest[:nl])
		rest = rest[nl+1:]
		if len(hdr) >= 2 && hdr[len(hdr)-1] == "missing" {
			continue
		}
		if len(hdr) < 3 {
			continue
		}
		size := 0
		fmt.Sscanf(hdr[2], "%d", &size)
		if size > len(rest) {
			size = len(rest)
		}
		contents[i] = rest[:size]
		present[i] = true
		rest = strings.TrimPrefix(rest[size:], "\n")
	}
	return contents, present
}

func (s Store) Append(slug string, ev model.Event, extra map[string]string, mode ExpectMode) (string, error) {
	tip, err := s.casLoop(slug, mode, nil, func(parent string) (string, error) {
		return s.BuildCommit(slug, parent, ev, extra)
	})
	if err != nil {
		return "", err
	}
	return tip[:10], nil
}

// AppendChecked is Append plus a per-attempt precondition: pre runs against
// a fresh, backward-windowed read (runPrecondition) inside every CAS retry
// attempt, after the attempt's fresh head read and before the commit is
// built — never against a snapshot taken before the loop started, and never
// reused across attempts (spec rule 7). A pre error aborts the append with
// that error; nothing is written. Append delegates here with pre == nil,
// which skips the fresh-read-and-check step entirely (no behavior or cost
// change for existing callers). ev is a pointer, not a value: within one
// attempt, pre and the commit built from ev are the same struct, so a
// precondition that computes something tool-derived (rule 5's override
// record) can attach it to *ev before that attempt's build step reads it.
// That guarantee is strictly within-attempt, though — *ev is the same
// struct across every retry (never recreated per attempt), so a
// precondition writing a per-attempt-computed field onto *ev MUST set it
// unconditionally on every invocation (including "nothing to record" as an
// explicit reset), never only inside the branch that has something to
// write; otherwise a losing attempt's value survives untouched into a
// later, winning attempt whose own fresh computation found nothing, and
// the commit built from that winning attempt carries an attribution that
// never existed on the state that actually landed.
func (s *Store) AppendChecked(slug string, ev *model.Event, pre Precondition, mode ExpectMode) (string, error) {
	tip, err := s.casLoop(slug, mode, pre, func(parent string) (string, error) {
		return s.BuildCommit(slug, parent, *ev, nil)
	})
	if err != nil {
		return "", err
	}
	return tip[:10], nil
}

// AppendChain lands N events as one parent-chained sequence of commits under
// a single ref CAS: either the whole chain lands or none of it does. Used
// where multiple events must be atomic together on one ref — e.g. `close
// --as-state superseded`'s close+link pair, which must never be observable
// mid-write as "closed but not yet linked" — and `import`'s full replay,
// which must never leave a half-created, permanently unusable slug behind
// (no delete verb, slugs never reused) after a bad input file. firstExtra is
// attached only to the chain's first commit (e.g. import's meta.json on a
// from-scratch ledger); pass nil for chains that extend an existing one.
//
// remap, when non-nil, is called for every event immediately before its
// commit is built, given the ids already assigned earlier in this same
// chain-build attempt (keyed by each earlier event's own ImportedFrom, i.e.
// its pre-import identity) — the hook import uses to rewrite a rollup's
// Children from old ids to the new ones its children just received, since
// children always precede their rollup in chain order and so already have a
// new id by the time their parent event's commit is built. Pass nil where no
// such rewrite is needed. priorIDs is rebuilt from scratch on every CAS
// retry, so remap must stay a pure function of (ev, priorIDs).
//
// Returned ids are in event order.
func (s Store) AppendChain(slug string, evs []model.Event, firstExtra map[string]string, mode ExpectMode,
	remap func(ev *model.Event, priorIDs map[string]string)) ([]string, error) {
	var shas []string
	_, err := s.casLoop(slug, mode, nil, func(parent string) (string, error) {
		shas = shas[:0]
		priorIDs := map[string]string{}
		p := parent
		for i, ev := range evs {
			if remap != nil {
				remap(&ev, priorIDs)
			}
			var extra map[string]string
			if i == 0 {
				extra = firstExtra
			}
			csha, err := s.BuildCommit(slug, p, ev, extra)
			if err != nil {
				return "", err
			}
			shas = append(shas, csha)
			if ev.ImportedFrom != "" {
				priorIDs[ev.ImportedFrom] = csha[:10]
			}
			p = csha
		}
		return shas[len(shas)-1], nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(shas))
	for i, sha := range shas {
		ids[i] = sha[:10]
	}
	return ids, nil
}

// casLoop is the shared CAS-retry skeleton behind Append, AppendChain, and
// AppendChecked: re-read the current head, run pre (if any) against a fresh,
// windowed read (runPrecondition), let build construct the new tip
// commit(s) parented on it, then try to move the ref. build (and pre) may
// run more than once — once per attempt — if another writer wins the race.
func (s Store) casLoop(slug string, mode ExpectMode, pre Precondition, build func(parent string) (tip string, err error)) (string, error) {
	for attempt := 0; attempt < 30; attempt++ {
		cur, ok := s.head(slug)
		if mode == ExpectAbsent && ok {
			return "", fmt.Errorf("%w: %s", ErrSlugExists, slug)
		}
		if mode == ExpectPresent && !ok {
			return "", fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
		}
		if pre != nil {
			if err := s.runPrecondition(slug, ok, pre); err != nil {
				return "", err
			}
		}
		parent := ""
		if ok {
			parent = cur
		}
		tip, err := build(parent)
		if err != nil {
			return "", err
		}
		if _, _, code := s.Repo.Git("", "update-ref", ref(slug), tip, cur); code == 0 {
			s.GCAuto()
			return tip, nil
		}
		time.Sleep(time.Duration(attempt) * 10 * time.Millisecond)
	}
	return "", fmt.Errorf("%w: %s", ErrCASExhausted, slug)
}

// runPrecondition drives pre against a fresh backward-windowed read
// (windowProbeSize), then falls back to Events' whole-chain read if pre
// still returns ErrNeedsMoreHistory and the window hasn't already reached
// the chain root (spec rule 8) — the whole-chain read is always decisive.
// ok reports whether the ledger has any commits at all; pre still runs once
// against an empty, root-reached read when it doesn't (a first-ever
// write's --expect none case).
func (s Store) runPrecondition(slug string, ok bool, pre Precondition) error {
	if !ok {
		return pre(nil, true)
	}
	events, reachedRoot, err := s.EventsWindow(slug, windowProbeSize)
	if err != nil {
		return err
	}
	if result := pre(events, reachedRoot); reachedRoot || result != ErrNeedsMoreHistory {
		return result
	}
	events, _, err = s.Events(slug)
	if err != nil {
		return err
	}
	return pre(events, true)
}

// BuildCommit writes one event's blobs/tree/commit-tree without touching any
// ref — the shared plumbing behind Append's single-ref CAS loop and the
// cross-ref supersede transaction in cmd.createSuperseding. parent == ""
// means a root commit (the ledger's creation commit). The returned sha is
// full-length (40 hex chars): Append truncates it for its own callers, but
// the supersede transaction needs the untruncated form for CAS Old values.
func (s Store) BuildCommit(slug, parent string, ev model.Event, extra map[string]string) (string, error) {
	body, err := json.MarshalIndent(ev, "", " ")
	if err != nil {
		return "", err
	}
	entries := []string{}
	files := map[string]string{"event.json": string(body)}
	for k, v := range extra {
		files[k] = v
	}
	for name, content := range files {
		blob, se, code := s.Repo.Git(content, "hash-object", "-w", "--stdin")
		if code != 0 {
			return "", fmt.Errorf("git_failed: %s", se)
		}
		entries = append(entries, "100644 blob "+blob+"\t"+name)
	}
	tree, se, code := s.Repo.Git(strings.Join(entries, "\n")+"\n", "mktree")
	if code != 0 {
		return "", fmt.Errorf("git_failed: %s", se)
	}
	args := append(gitx.IdentityArgs(ev.Author, committerMarker(ev)),
		"commit-tree", tree, "-m", ev.Type+":"+ev.Key)
	if parent != "" {
		args = append(args, "-p", parent)
	}
	csha, se, code := s.Repo.Git("", args...)
	if code != 0 {
		return "", fmt.Errorf("git_failed: %s", se)
	}
	_ = slug // reserved: commit content doesn't need the slug today, kept for signature symmetry with Append
	return csha, nil
}

type TxStep struct{ Ref, New, Old string }

// Transaction commits all steps or none, via git's ref-transaction protocol
// (start/prepare/commit over `update-ref --stdin`). Any CAS mismatch aborts
// the whole batch — used by create --supersedes to move the predecessor's
// ref and create the successor's ref atomically.
func (s Store) Transaction(steps []TxStep) error {
	var b strings.Builder
	b.WriteString("start\n")
	for _, st := range steps {
		b.WriteString(fmt.Sprintf("update %s %s %s\n", st.Ref, st.New, st.Old))
	}
	b.WriteString("prepare\ncommit\n")
	_, stderr, code := s.Repo.Git(b.String(), "update-ref", "--stdin")
	if code != 0 {
		return fmt.Errorf("transaction aborted: %s", stderr)
	}
	return nil
}

func committerMarker(ev model.Event) string {
	if ev.CommitterOverride != "" {
		return ev.CommitterOverride
	}
	return model.HarnessMarker()
}

// GCAuto keeps stores packed: plumbing never triggers git's own auto-gc
// (measured: 3 loose objects per event, unbounded). Best-effort by design.
func (s Store) GCAuto() { s.Repo.Git("", "gc", "--auto", "--quiet") }
