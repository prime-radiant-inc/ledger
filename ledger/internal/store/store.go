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
// (.ledger.git beats .git within one directory). note is non-empty when both
// kinds appear in the ancestry — callers print which store was chosen.
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
	var sawOther bool
	for dir := cwd; ; dir = filepath.Dir(dir) {
		lg := filepath.Join(dir, ".ledger.git")
		gt := filepath.Join(dir, ".git")
		lgOK := exists(lg)
		gtOK := exists(gt)
		if lgOK && gtOK {
			return Store{Repo: gitx.Repo{Dir: lg}}, fmt.Sprintf("using store %s (a git repo is also here)", lg), nil
		}
		if lgOK {
			note := ""
			if sawOther {
				note = "using store " + lg
			}
			return Store{Repo: gitx.Repo{Dir: lg}}, note, nil
		}
		if gtOK {
			note := ""
			if sawOther {
				note = "using repo " + dir
			}
			return Store{Repo: gitx.Repo{Dir: dir}}, note, nil
		}
		sawOther = sawOther || lgOK || gtOK
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

// Events reads the whole chain with two subprocesses total:
// one `git log` for (commit, tree) pairs, one `cat-file --batch` for all blobs.
func (s Store) Events(slug string) ([]model.Event, model.Meta, error) {
	var meta model.Meta
	out, _, code := s.Repo.Git("", "log", "--reverse", "--format=%H %T", ref(slug))
	if code != 0 || out == "" {
		return nil, meta, fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	type pair struct{ commit, tree string }
	var pairs []pair
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		pairs = append(pairs, pair{f[0], f[1]})
	}
	// Resolve every tree's event.json (and creation meta.json) blob ids via ls-tree
	// on the batch of trees, then fetch blob contents in one cat-file --batch.
	var reqs []string
	blobOf := make([]map[string]string, len(pairs))
	for i, p := range pairs {
		lst, _, _ := s.Repo.Git("", "ls-tree", p.tree)
		m := map[string]string{}
		for _, l := range strings.Split(lst, "\n") {
			tab := strings.SplitN(l, "\t", 2)
			if len(tab) != 2 {
				continue
			}
			m[tab[1]] = strings.Fields(tab[0])[2]
		}
		blobOf[i] = m
		reqs = append(reqs, m["event.json"])
		if b, ok := m["meta.json"]; ok {
			reqs = append(reqs, b)
		}
	}
	contents := s.catBatch(reqs)
	evs := make([]model.Event, 0, len(pairs))
	for i, p := range pairs {
		var ev model.Event
		if err := json.Unmarshal([]byte(contents[blobOf[i]["event.json"]]), &ev); err != nil {
			continue // torn/foreign commit: skip, never crash a read
		}
		ev.ID = p.commit[:10]
		if b, ok := blobOf[i]["meta.json"]; ok {
			json.Unmarshal([]byte(contents[b]), &meta)
		}
		evs = append(evs, ev)
	}
	return evs, meta, nil
}

func (s Store) catBatch(ids []string) map[string]string {
	res := map[string]string{}
	if len(ids) == 0 {
		return res
	}
	out, _, _ := s.Repo.Git(strings.Join(ids, "\n"), "cat-file", "--batch")
	// format per object: "<sha> blob <size>\n<content>\n"
	rest := out
	for rest != "" {
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		hdr := strings.Fields(rest[:nl])
		rest = rest[nl+1:]
		if len(hdr) < 3 {
			continue
		}
		size := 0
		fmt.Sscanf(hdr[2], "%d", &size)
		if size > len(rest) {
			size = len(rest)
		}
		res[hdr[0]] = rest[:size]
		rest = strings.TrimPrefix(rest[size:], "\n")
	}
	return res
}

func (s Store) Append(slug string, ev model.Event, extra map[string]string, expect Expect) (string, error) {
	body, err := json.MarshalIndent(ev, "", " ")
	if err != nil {
		return "", err
	}
	for attempt := 0; attempt < 30; attempt++ {
		cur, ok := s.head(slug)
		if expect == ExpectAbsent && ok {
			return "", fmt.Errorf("%w: %s", ErrSlugExists, slug)
		}
		if expect == ExpectPresent && !ok {
			return "", fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
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
		if ok {
			args = append(args, "-p", cur)
		}
		csha, se, code := s.Repo.Git("", args...)
		if code != 0 {
			return "", fmt.Errorf("git_failed: %s", se)
		}
		old := cur // "" when creating
		if _, _, code := s.Repo.Git("", "update-ref", ref(slug), csha, old); code == 0 {
			s.GCAuto()
			return csha[:10], nil
		}
		time.Sleep(time.Duration(attempt) * 10 * time.Millisecond)
	}
	return "", ErrCASExhausted
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
