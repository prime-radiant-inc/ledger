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

// Resolve implements the spec's store-resolution order:
// --store flag > $LEDGER_DIR > nearest ancestor holding .ledger.git or .git
// (.ledger.git beats .git within one directory). note is non-empty only in
// that same-directory collision case, when a single directory holds both
// .ledger.git and .git — callers print which one was chosen.
func Resolve(storeFlag string) (Store, string, error) {
	if storeFlag != "" {
		return storeFor(storeFlag), "", nil
	}
	if d := os.Getenv("LEDGER_DIR"); d != "" {
		return storeFor(d), "", nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Store{}, "", err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		lg := filepath.Join(dir, ".ledger.git")
		gt := filepath.Join(dir, ".git")
		lgOK := exists(lg)
		gtOK := exists(gt)
		if lgOK && gtOK {
			return Store{Repo: gitx.Repo{Dir: lg}}, fmt.Sprintf("using store %s (a git repo is also here)", lg), nil
		}
		if lgOK {
			return Store{Repo: gitx.Repo{Dir: lg}}, "", nil
		}
		if gtOK {
			return Store{Repo: gitx.Repo{Dir: dir}}, "", nil
		}
		if dir == filepath.Dir(dir) {
			return Store{}, "", fmt.Errorf("no git repo or .ledger.git found from %s upward", cwd)
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

// AppendChain lands N events as one parent-chained sequence of commits under
// a single ref CAS: either the whole chain lands or none of it does. Used
// where multiple events must be atomic together on one ref — e.g. `close
// --as-state superseded`'s close+link pair, which must never be observable
// mid-write as "closed but not yet linked" — and `import`'s full replay,
// which must never leave a half-created, permanently unusable slug behind
// (no delete verb, slugs never reused) after a bad input file. firstExtra is
// attached only to the chain's first commit (e.g. import's meta.json on a
// from-scratch ledger); pass nil for chains that extend an existing one.
// Returned ids are in event order.
func (s Store) AppendChain(slug string, evs []model.Event, firstExtra map[string]string, expect Expect) ([]string, error) {
	var shas []string
	_, err := s.casLoop(slug, expect, func(parent string) (string, error) {
		shas = shas[:0]
		p := parent
		for i, ev := range evs {
			var extra map[string]string
			if i == 0 {
				extra = firstExtra
			}
			csha, err := s.BuildCommit(slug, p, ev, extra)
			if err != nil {
				return "", err
			}
			shas = append(shas, csha)
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
