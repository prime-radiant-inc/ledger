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

	"ledger/internal/gitx"
	"ledger/internal/model"
)

type Expect int

const (
	ExpectPresent Expect = iota
	ExpectAbsent
)

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

// Events reads the whole chain with exactly two subprocesses: one `git log`
// for the commit list, one `cat-file --batch` requesting <sha>:event.json
// and <sha>:meta.json for every commit. git resolves each rev:path
// server-side, so no per-commit ls-tree is needed. A commit with no
// meta.json (every commit but the create) comes back "missing" for that
// request and is simply skipped.
func (s Store) Events(slug string) ([]model.Event, model.Meta, error) {
	var meta model.Meta
	out, _, code := s.Repo.Git("", "log", "--reverse", "--format=%H", ref(slug))
	if code != 0 || out == "" {
		return nil, meta, fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	commits := strings.Split(out, "\n")
	reqs := make([]string, 0, len(commits)*2)
	for _, c := range commits {
		reqs = append(reqs, c+":event.json", c+":meta.json")
	}
	contents, present := s.catBatch(reqs)
	evs := make([]model.Event, 0, len(commits))
	for i, c := range commits {
		evIdx, metaIdx := 2*i, 2*i+1
		if !present[evIdx] {
			continue // torn/foreign commit: skip, never crash a read
		}
		var ev model.Event
		if err := json.Unmarshal([]byte(contents[evIdx]), &ev); err != nil {
			continue
		}
		ev.ID = c[:10]
		if present[metaIdx] {
			json.Unmarshal([]byte(contents[metaIdx]), &meta)
		}
		evs = append(evs, ev)
	}
	return evs, meta, nil
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

func (s Store) Append(slug string, ev model.Event, extra map[string]string, expect Expect) (string, error) {
	tip, err := s.casLoop(slug, expect, func(parent string) (string, error) {
		return s.BuildCommit(slug, parent, ev, extra)
	})
	if err != nil {
		return "", err
	}
	return tip[:10], nil
}

// ClaimLostError is AppendExpect's precondition failure: field's latest
// "set" event on the target key no longer matches the caller's --expect
// (an id prefix, or "none" meaning "no prior event"). Winner is the event
// that landed instead (its zero value when the field has no prior event at
// all — the --expect-non-none-but-nothing-there case).
type ClaimLostError struct {
	Field  string
	Winner model.Event
}

func (e *ClaimLostError) Error() string { return "claim_lost: " + e.Field + "@" + e.Winner.ID }

// latestFieldEvent is the most recent "set" event that touched field on key
// within evs (chronological) — the field-scoped version stamp a guarded
// write's --expect claims against. A set that touched a *different* field on
// the same key doesn't count: that's what makes the precondition
// field-scoped rather than key-scoped, so an unrelated field's write (e.g. a
// triage label) never invalidates an in-flight claim on another field (e.g.
// status).
func latestFieldEvent(evs []model.Event, key, field string) (model.Event, bool) {
	var found model.Event
	ok := false
	for _, ev := range evs {
		if ev.Type != "set" || ev.Key != key {
			continue
		}
		if _, touched := ev.Fields[field]; touched {
			found, ok = ev, true
		}
	}
	return found, ok
}

// AppendExpect is Append with a conditional-write precondition, field-scoped
// to ev.Key's field: the commit only lands if field's latest set-event id on
// this key still has expectSpec as a prefix, or — when expectSpec is
// "none" — if field has no prior set-event on this key at all (the
// first-touch sentinel). Unlike a check made once before the loop, the
// precondition here runs inside casLoop's build callback — called fresh on
// every CAS retry — so it always re-reads the chain from git immediately
// before attempting to land. If another writer's commit races in between
// this read and the ref-CAS below, that CAS fails (old != current ref) and
// casLoop retries, which re-reads and re-validates against the now-current
// chain rather than trusting stale data. The precondition can therefore
// never pass against a state that the landing commit's CAS doesn't also
// certify — genuinely atomic, not a check-then-act race. This makes
// claiming (and first-edge writes under "none") first-wins, unlike Append's
// plain last-wins.
//
// Performance note: this re-reads the full chain per retry attempt, same as
// the v2 spike. Narrowing that read to the target key/field, or reusing the
// fold casLoop's caller already holds, is real-implementation scope, not
// this throwaway spike's.
func (s Store) AppendExpect(slug string, ev model.Event, field, expectSpec string, expect Expect) (string, error) {
	return s.AppendExpectChecked(slug, ev, field, expectSpec, expect, nil)
}

// SignalCheck is rule 5's interference gate, run against the same fresh read
// AppendExpectChecked's field-scoped precondition already fetched (rule 7:
// never a pre-loop snapshot — check runs inside the CAS retry loop on every
// attempt, exactly like the precondition it rides alongside). It returns the
// override marker to record on the landing event ("" when no signal stood,
// or the caller didn't need to override one) or a domain error that aborts
// the write outright — not retried, the same treatment casLoop already gives
// ClaimLostError, since a signal check failure is a decision, not a race.
type SignalCheck func(evs []model.Event) (override string, err error)

// AppendExpectChecked is AppendExpect with an optional SignalCheck spliced
// into the same fresh-read build callback: field-scoped CAS first (rules
// 3-4), then, only if that passes, the caller's domain check (rule 5). check
// may be nil, in which case this is exactly AppendExpect's old body.
func (s Store) AppendExpectChecked(slug string, ev model.Event, field, expectSpec string, expect Expect,
	check SignalCheck) (string, error) {
	tip, err := s.casLoop(slug, expect, func(parent string) (string, error) {
		evs, _, evErr := s.Events(slug)
		if evErr != nil {
			return "", evErr
		}
		winner, ok := latestFieldEvent(evs, ev.Key, field)
		var lost bool
		if expectSpec == "none" {
			lost = ok // a prior event exists: "none" claimed it didn't
		} else {
			lost = !ok || !strings.HasPrefix(winner.ID, expectSpec)
		}
		if lost {
			return "", &ClaimLostError{Field: field, Winner: winner}
		}
		toWrite := ev
		if check != nil {
			override, cErr := check(evs)
			if cErr != nil {
				return "", cErr
			}
			toWrite.Override = override
		}
		return s.BuildCommit(slug, parent, toWrite, nil)
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
func (s Store) AppendChain(slug string, evs []model.Event, firstExtra map[string]string, expect Expect,
	remap func(ev *model.Event, priorIDs map[string]string)) ([]string, error) {
	var shas []string
	_, err := s.casLoop(slug, expect, func(parent string) (string, error) {
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

// casLoop is the shared CAS-retry skeleton behind Append and AppendChain:
// re-read the current head, let build construct the new tip commit(s)
// parented on it, then try to move the ref. build may be called more than
// once (once per attempt) if another writer wins the race.
func (s Store) casLoop(slug string, expect Expect, build func(parent string) (tip string, err error)) (string, error) {
	for attempt := 0; attempt < 30; attempt++ {
		cur, ok := s.head(slug)
		if expect == ExpectAbsent && ok {
			return "", fmt.Errorf("%w: %s", ErrSlugExists, slug)
		}
		if expect == ExpectPresent && !ok {
			return "", fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
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
