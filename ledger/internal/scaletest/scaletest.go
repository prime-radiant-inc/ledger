// Package scaletest builds large synthetic event chains for scale tests in
// both internal/store (whole-chain read cost) and internal/cmd (the real
// setPrecondition closure that drives it) — kept in one place so both
// suites measure against literally the same fixture shape instead of
// independently drifting copies. Not used by any production code path;
// exists purely as shared test support, which is why it takes testing.TB
// rather than living under a _test.go file (which Go forbids importing
// across packages).
package scaletest

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ledger/internal/gitx"
	"ledger/internal/model"
)

// Meta is the ready-capable board shape every scale fixture assumes: two
// terminal values, labels and blocked-by both declared and guarded.
func Meta() model.Meta {
	return model.Meta{
		Fields:      map[string][]string{"status": {"open", "in-progress", "closed", "wontfix"}},
		Terminal:    map[string][]string{"status": {"closed", "wontfix"}},
		MultiFields: []string{"labels", "blocked-by"},
		Guard:       []string{"status", "blocked-by"},
		StaleAfter:  "2h",
	}
}

// Event builds one minimal "set" event, TS spaced a second apart by i so
// age/staleness math has something real to compute against.
func Event(i int, key, field, value, author string) model.Event {
	return model.Event{
		TS:   time.Unix(1750000000+int64(i), 0).UTC().Format(model.TSLayout),
		Type: "set", Key: key, Fields: map[string]string{field: value}, Author: author,
	}
}

// Churn returns n padding events at the parent spec's stated volume
// ("touch-base churn, which scales with wall-clock, not issue count"): 400
// keys seeded open, 40 of them claimed and repeatedly touched-base to fill
// out to n total. Callers append their own tail events afterward — the
// specific recently- (or long-ago-) touched-key scenario under test.
func Churn(n int) []model.Event {
	var evs []model.Event
	i := 0
	next := func(key, field, value, author string) {
		evs = append(evs, Event(i, key, field, value, author))
		i++
	}
	const seeded = 400
	const claimed = 40
	for k := 0; k < seeded; k++ {
		next(fmt.Sprintf("k-%d", k), "status", "open", "seeder")
	}
	for k := 0; k < claimed; k++ {
		next(fmt.Sprintf("k-%d", k), "status", "in-progress", "worker")
	}
	for len(evs) < n {
		k := fmt.Sprintf("k-%d", len(evs)%claimed)
		next(k, "status", "in-progress", "worker")
	}
	return evs
}

// Seed loads evs as one chain of commits under refs/ledger/<slug> via a
// single `git fast-import` invocation — a test-fixture-only bulk loader:
// production writes always go through store.BuildCommit/casLoop (one CAS
// round trip per event; measured ~2m34s for 5,000 events on typical
// hardware). fast-import builds the identical commit/tree/blob shape in
// ~80ms. Only the fixture LOADER differs — every event read back
// afterward through store.Events is indistinguishable from one
// store.Append would have produced. firstExtra attaches extra files
// (e.g. meta.json) to the first commit, matching store.AppendChain's own
// firstExtra convention.
func Seed(t testing.TB, repo gitx.Repo, slug string, evs []model.Event, firstExtra map[string]string) {
	t.Helper()
	im := &importer{mark: 1}
	im.chain(t, slug, evs, 0, firstExtra)
	im.run(t, repo)
}

// SeedMerged seeds the shape a true divergence leaves behind: base as one
// linear chain, then branchA and branchB both parented on base's tip, joined
// by ONE sync sentinel — exactly what `ledger sync` mints, and the only
// shape a contest can exist in. Both branches writing the same keys is what
// makes the merged board carry contests to measure against.
func SeedMerged(t testing.TB, repo gitx.Repo, slug string, base, branchA, branchB []model.Event, firstExtra map[string]string) {
	t.Helper()
	im := &importer{mark: 1}
	baseTip := im.chain(t, slug, base, 0, firstExtra)
	tipA := im.chain(t, slug, branchA, baseTip, nil)
	tipB := im.chain(t, slug, branchB, baseTip, nil)

	last := branchB[len(branchB)-1]
	sentinel := model.Event{TS: last.TS, Type: "sync", Author: "sync"}
	im.commit(t, slug, sentinel, tipA, tipB, nil)
	im.run(t, repo)
}

// importer accumulates one `git fast-import` stream. Marks are fast-import's
// own forward references: every blob and commit gets one, and a commit names
// its parents by theirs.
type importer struct {
	b    strings.Builder
	mark int
}

func (im *importer) blob(content string) int {
	m := im.mark
	im.mark++
	fmt.Fprintf(&im.b, "blob\nmark :%d\ndata %d\n%s\n", m, len(content), content)
	return m
}

// commit writes one commit carrying ev as event.json, parented on first
// (0 = a root commit) and, when second is non-zero, merging it — the
// two-parent shape of a sync sentinel. Returns its mark.
func (im *importer) commit(t testing.TB, slug string, ev model.Event, first, second int, extra map[string]string) int {
	t.Helper()
	body, err := json.MarshalIndent(ev, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	evMark := im.blob(string(body))
	extraMarks := map[string]int{}
	for name, content := range extra {
		extraMarks[name] = im.blob(content)
	}
	commitMark := im.mark
	im.mark++
	ts, err := model.ParseTS(ev.TS)
	if err != nil {
		ts = time.Now().UTC()
	}
	fmt.Fprintf(&im.b, "commit refs/ledger/%s\nmark :%d\n", slug, commitMark)
	fmt.Fprintf(&im.b, "author %s <author@ledger.invalid> %d +0000\n", ev.Author, ts.Unix())
	fmt.Fprintf(&im.b, "committer terminal <marker@ledger.invalid> %d +0000\n", ts.Unix())
	msg := ev.Type + ":" + ev.Key
	fmt.Fprintf(&im.b, "data %d\n%s\n", len(msg), msg)
	if first != 0 {
		fmt.Fprintf(&im.b, "from :%d\n", first)
	}
	if second != 0 {
		fmt.Fprintf(&im.b, "merge :%d\n", second)
	}
	fmt.Fprintf(&im.b, "M 100644 :%d event.json\n", evMark)
	for name, m := range extraMarks {
		fmt.Fprintf(&im.b, "M 100644 :%d %s\n", m, name)
	}
	im.b.WriteString("\n")
	return commitMark
}

// chain writes evs as one parent-chained run starting from parent (0 = a
// root), attaching firstExtra to the run's first commit. Returns the tip's
// mark.
func (im *importer) chain(t testing.TB, slug string, evs []model.Event, parent int, firstExtra map[string]string) int {
	t.Helper()
	for i, ev := range evs {
		var extra map[string]string
		if i == 0 {
			extra = firstExtra
		}
		parent = im.commit(t, slug, ev, parent, 0, extra)
	}
	return parent
}

func (im *importer) run(t testing.TB, repo gitx.Repo) {
	t.Helper()
	_, stderr, code := repo.GitRaw(im.b.String(), "fast-import", "--quiet")
	if code != 0 {
		t.Fatalf("fast-import: %s", stderr)
	}
}

// Branch returns n events one replica wrote during a partition: repeated
// claims by author on the same 40 keys Churn claims, so both branches race
// the SAME (key, status) pairs. start offsets the timestamps; handing both
// branches the same start is what makes their writes interleave in the fold
// the way a real merge's do.
//
// Every fifth event is a RENAME rather than a claim, so the branches race
// those same 40 keys' TITLE stream too. The rename stream is a contested
// stream of its own, so a fixture carrying only status writes would leave
// the cover-set pass's second stream unmeasured — and the whole point of
// the bound is to price the pass as it actually runs.
func Branch(start, n int, author string) []model.Event {
	evs := make([]model.Event, 0, n)
	renames := 0
	for i := 0; i < n; i++ {
		if i%5 == 4 {
			// Renames walk the 40 keys on their own counter, so the title
			// stream races on EVERY one of them rather than only on the keys
			// whose position happens to line up with the modulus.
			key := fmt.Sprintf("k-%d", renames%40)
			evs = append(evs, RenameEvent(start+i, key, author+"'s title "+fmt.Sprint(i), author))
			renames++
			continue
		}
		evs = append(evs, Event(start+i, fmt.Sprintf("k-%d", i%40), "status", "in-progress", author))
	}
	return evs
}

// RenameEvent builds one rename event — a "set" whose whole payload is the
// new title, which is how the wire carries a retitle.
func RenameEvent(i int, key, title, author string) model.Event {
	return model.Event{
		TS:   time.Unix(1750000000+int64(i), 0).UTC().Format(model.TSLayout),
		Type: "set", Key: key, Rename: title, Author: author,
	}
}
