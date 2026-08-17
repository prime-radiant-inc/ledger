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
	var b strings.Builder
	mark := 1
	blobMark := func(content string) int {
		m := mark
		mark++
		fmt.Fprintf(&b, "blob\nmark :%d\ndata %d\n%s\n", m, len(content), content)
		return m
	}
	prevCommitMark := 0
	for i, ev := range evs {
		body, err := json.MarshalIndent(ev, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		evMark := blobMark(string(body))
		var extraMarks map[string]int
		if i == 0 && len(firstExtra) > 0 {
			extraMarks = map[string]int{}
			for name, content := range firstExtra {
				extraMarks[name] = blobMark(content)
			}
		}
		commitMark := mark
		mark++
		ts, err := model.ParseTS(ev.TS)
		if err != nil {
			ts = time.Now().UTC()
		}
		fmt.Fprintf(&b, "commit refs/ledger/%s\nmark :%d\n", slug, commitMark)
		fmt.Fprintf(&b, "author %s <author@ledger.invalid> %d +0000\n", ev.Author, ts.Unix())
		fmt.Fprintf(&b, "committer terminal <marker@ledger.invalid> %d +0000\n", ts.Unix())
		msg := ev.Type + ":" + ev.Key
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		if prevCommitMark != 0 {
			fmt.Fprintf(&b, "from :%d\n", prevCommitMark)
		}
		fmt.Fprintf(&b, "M 100644 :%d event.json\n", evMark)
		for name, m := range extraMarks {
			fmt.Fprintf(&b, "M 100644 :%d %s\n", m, name)
		}
		b.WriteString("\n")
		prevCommitMark = commitMark
	}
	_, stderr, code := repo.GitRaw(b.String(), "fast-import", "--quiet")
	if code != 0 {
		t.Fatalf("fast-import: %s", stderr)
	}
}
