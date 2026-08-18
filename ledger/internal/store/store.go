// Package store maps ledgers onto git phantom refs: refs/ledger/<slug>,
// one commit per event, CAS appends, batched reads.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// Precondition runs against a fresh, whole-chain read of a ledger's event
// history inside every CAS retry attempt of AppendChecked — never a
// pre-loop snapshot (spec rule 7's atomicity contract). events is
// oldest-first and always holds the ledger's FULL history (sync spec rev 7
// Addition 5: guarded writes on merged history use whole-chain precondition
// reads, unconditionally — there is no windowed mode to fall back from).
// Returning an error aborts the append with that error; nothing is written.
//
// SCOPE, stated plainly: this re-read covers only the fresh read taken
// inside each CAS retry attempt (runPrecondition, below) — it is a property
// of AppendChecked's retry loop, not of the command that calls it. A
// command like `set` still resolves the ledger (and folds its full event
// history) once up front via Ctx.Load/PickLedger, and any of its own
// pre-append bookkeeping (e.g. an idempotency-key scan over that already-
// loaded history) runs before AppendChecked or this Precondition are ever
// invoked. Nothing about that up-front read scales with this contract; only
// the per-attempt precondition read does.
type Precondition func(events []model.Event) error

var (
	ErrUnknownLedger = errors.New("unknown_ledger")
	ErrSlugExists    = errors.New("slug_exists")
	ErrCASExhausted  = errors.New("cas_exhausted")
)

type Store struct{ Repo gitx.Repo }

func ref(slug string) string { return "refs/ledger/" + slug }

// Ref is a ledger's phantom ref name — the one place outside this package
// (sync/push) that has to name refs/ledger/<slug> directly, for CAS moves
// the plumbing here doesn't otherwise expose.
func Ref(slug string) string { return ref(slug) }

// TrackingRef is a synced remote's private tracking ref for one slug. The
// namespace is refs/ledger-remote/, deliberately NOT refs/remotes/, which
// git's own default branch refspec also populates (verified fatal collision
// when a branch is named ledger/<x>).
func TrackingRef(remote, slug string) string {
	return "refs/ledger-remote/" + remote + "/" + slug
}

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

func (s Store) head(slug string) (string, bool) { return s.RevParse(ref(slug)) }

// FullHead is head's exported form: a ledger's tip as a full 40-char sha,
// which a CAS old-value needs — the 10-char event id everything else passes
// around is not a legal CAS ticket.
func (s Store) FullHead(slug string) (string, bool) { return s.head(slug) }

// RevParse resolves any rev — a slug's own ref, or an arbitrary ref like a
// sync tracking ref refs/ledger-remote/<remote>/<slug> — to a full sha. ok
// is false when it doesn't resolve (an absent ref, a vanished tracking ref).
func (s Store) RevParse(rev string) (string, bool) {
	out, _, code := s.Repo.Git("", "rev-parse", "-q", "--verify", rev)
	return out, code == 0
}

func (s Store) HeadID(slug string) (string, error) {
	h, err := s.HeadSHA(slug)
	if err != nil {
		return "", err
	}
	return h[:10], nil
}

// HeadSHA is the ledger ref's tip commit, full 40 chars — what the cursor
// contract (sync spec rev 7, Addition 2) measures reachability against and
// what every drain emits as its cursor.
func (s Store) HeadSHA(slug string) (string, error) {
	h, ok := s.head(slug)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	return h, nil
}

// IsAncestor reports whether commit a is an ancestor of (or equal to)
// commit b. This is the whole of cursor validity: a cursor is a reachability
// token against the ref, never a fold position. git's --is-ancestor is
// reflexive, so a cursor that IS the tip is valid and drains empty. A rev git
// cannot resolve at all — a foreign or invented cursor — answers false rather
// than erroring: to a reachability question, "not in this history" and "not
// an ancestor" are the same answer, and both are reset_required.
func (s Store) IsAncestor(a, b string) bool {
	_, _, code := s.Repo.Git("", "merge-base", "--is-ancestor", a, b)
	return code == 0
}

// RangeNodes lists the commits of `cursor..tip` — every commit reachable from
// tip when cursor is empty — as dag.Nodes carrying SHA and Parents. Parents
// naming commits outside the range are kept as read and ignored by dag.Sort,
// which is what makes the range's sub-DAG *contracted*: an ancestor left
// outside the range stops constraining the events inside it.
//
// TS and IsSentinel are the caller's to fill from the events it already
// holds: knowing them here would mean re-reading every commit's event.json,
// which the caller's whole-chain read has already done.
func (s Store) RangeNodes(cursor, tip string) ([]dag.Node, error) {
	rev := tip
	if cursor != "" {
		rev = cursor + ".." + tip
	}
	out, stderr, code := s.Repo.Git("", "rev-list", "--parents", rev)
	if code != 0 {
		return nil, fmt.Errorf("git_failed: %s", stderr)
	}
	var nodes []dag.Node
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		nodes = append(nodes, dag.Node{SHA: f[0], Parents: f[1:]})
	}
	return nodes, nil
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
	return s.eventsDAG(ref(slug), slug)
}

// EventsDAGAt is EventsDAG generalized to an arbitrary ref instead of a
// slug's own refs/ledger/<slug> — sync's multi-root refusal and same-root
// check both have to run this exact fold-order read against a tracking ref
// (refs/ledger-remote/<remote>/<slug>) before any local ref is moved or
// created, since the fold's root set (not raw git ancestry) is what "root"
// means everywhere else in this codebase.
func (s Store) EventsDAGAt(refName string) ([]model.Event, model.Meta, dag.Result, error) {
	return s.eventsDAG(refName, refName)
}

// eventsDAG is EventsDAG/EventsDAGAt's shared read, generalized over the
// ref to read (refName) and the label an error names (label — the slug for
// EventsDAG, the ref itself for EventsDAGAt, which has no slug of its own).
func (s Store) eventsDAG(refName, label string) ([]model.Event, model.Meta, dag.Result, error) {
	var meta model.Meta
	out, _, code := s.Repo.Git("", "log", "--format=%H%x09%P", refName)
	if code != 0 || out == "" {
		return nil, meta, dag.Result{}, fmt.Errorf("%w: %s", ErrUnknownLedger, label)
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

// MetaAt reads the meta.json carried by a specific commit — sync's adoption
// and its two-creator root-mismatch/multi-root diagnoses all need a
// particular ROOT commit's own declarations and provenance, not the ref's
// aggregate meta (ambiguous once a chain has more than one root).
func (s Store) MetaAt(commit string) (model.Meta, bool) {
	var meta model.Meta
	contents, present := s.catBatch([]string{commit + ":meta.json"})
	if !present[0] {
		return meta, false
	}
	if err := json.Unmarshal([]byte(contents[0]), &meta); err != nil {
		return meta, false
	}
	return meta, true
}

// CommitAuthor reads a commit's git author name — the asserted author
// gitx.IdentityArgs set at write time — used as a creator fallback when a
// root commit carries no meta.json at all (a grafted or foreign root).
func (s Store) CommitAuthor(sha string) string {
	out, _, code := s.Repo.Git("", "log", "-1", "--format=%an", sha)
	if code != 0 {
		return ""
	}
	return out
}

// CAS moves refName from old to new via update-ref, failing (false) if the
// ref moved under us since old was read. old == "" requires the ref to be
// currently absent — the create case (sync's adoption). A single attempt:
// callers that need to retry across a race (fast-forward, adoption, the
// sentinel merge) re-read and retry themselves, exactly as casLoop does for
// Append.
func (s Store) CAS(refName, newSHA, old string) bool {
	_, _, code := s.Repo.Git("", "update-ref", refName, newSHA, old)
	return code == 0
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
// a fresh, whole-chain read (runPrecondition) inside every CAS retry
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
// whole-chain read (runPrecondition), let build construct the new tip
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

// runPrecondition drives pre against a fresh whole-chain read (spec rev 7
// Addition 5: every guarded write uses whole-chain precondition reads,
// unconditionally — no window, no merge detector, one mode). ok reports
// whether the ledger has any commits at all; pre still runs once against an
// empty read when it doesn't (a first-ever write's --expect none case).
func (s Store) runPrecondition(slug string, ok bool, pre Precondition) error {
	if !ok {
		return pre(nil)
	}
	events, _, err := s.Events(slug)
	if err != nil {
		return err
	}
	return pre(events)
}

// BuildCommit writes one event's blobs/tree/commit-tree without touching any
// ref — the shared plumbing behind Append's single-ref CAS loop and the
// cross-ref supersede transaction in cmd.createSuperseding. parent == ""
// means a root commit (the ledger's creation commit). The returned sha is
// full-length (40 hex chars): Append truncates it for its own callers, but
// the supersede transaction needs the untruncated form for CAS Old values.
func (s Store) BuildCommit(slug, parent string, ev model.Event, extra map[string]string) (string, error) {
	var parents []string
	if parent != "" {
		parents = []string{parent}
	}
	_ = slug // reserved: commit content doesn't need the slug today, kept for signature symmetry with Append
	return s.buildCommit(parents, ev, extra)
}

// BuildMerge writes a sentinel sync merge: a commit with two (or, after a
// re-parent-and-retry race, still two) parents whose tree carries only a
// type:"sync" event.json — sync's true-divergence case, minted under the
// caller's own ref CAS. Every read, fold, count and idempotency scan skips
// it, and the fold order (dag package) contracts it out of the DAG
// entirely: it exists to join two chains, never to say anything.
func (s Store) BuildMerge(parents []string, ev model.Event) (string, error) {
	return s.buildCommit(parents, ev, nil)
}

// buildCommit is BuildCommit and BuildMerge's shared plumbing: write the
// event's blob(s), tree, and commit object, touching no ref.
func (s Store) buildCommit(parents []string, ev model.Event, extra map[string]string) (string, error) {
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
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	csha, se, code := s.Repo.Git("", args...)
	if code != 0 {
		return "", fmt.Errorf("git_failed: %s", se)
	}
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
