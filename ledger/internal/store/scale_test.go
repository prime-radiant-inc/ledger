package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ledger/internal/board"
	"ledger/internal/gitx"
	"ledger/internal/model"
)

func TestScaleSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("scale smoke")
	}
	s := testStore(t)
	for l := 0; l < 30; l++ { // 30 ledgers x 60 events = 1800 events (CI-friendly slice of the 300x-spec probe)
		slug := fmt.Sprintf("led-%02d", l)
		s.Append(slug, model.Event{Type: "create", Author: "t"}, map[string]string{"meta.json": "{}"}, ExpectAbsent)
		for e := 0; e < 60; e++ {
			s.Append(slug, model.Event{Type: "set", Key: "k", Fields: map[string]string{"s": "v"}, Author: "t"}, nil, ExpectPresent)
		}
	}
	start := time.Now()
	slugs, _ := s.Slugs()
	for _, slug := range slugs {
		if _, _, err := s.Events(slug); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("full fold of 30x61 events took %v — reads are not batched enough", d)
	}
	// gc kept the store packed: loose objects bounded
	out, _, _ := s.Repo.Git("", "count-objects", "-v")
	t.Logf("count-objects:\n%s", out)
}

// ---------------------------------------------------------------------
// Rev-14 windowed-read scale tests (spec rule 8; test plan item 16).
//
// scaleChurn's 5,000-event fixtures are seeded via seedFast (git
// fast-import, one subprocess for the whole chain) rather than one
// s.Append per event: an individual-Append seed of 5,000 events measured
// ~2.5 minutes on this hardware (each Append is its own CAS round trip —
// head read, 3-subprocess commit build, update-ref); fast-import builds the
// identical commit/tree/blob shape in ~80ms. Only the fixture LOADER
// changes — every event read back through Events/EventsWindow afterward is
// indistinguishable from one Append would have produced.
// ---------------------------------------------------------------------

// scaleMeta is the ready-capable shape the scale fixtures below assume:
// two terminal values, both blocked-by and labels declared and guarded —
// close enough to "The board"'s own example to exercise every check
// setPrecondition and Envelope make.
func scaleMeta() model.Meta {
	return model.Meta{
		Fields:      map[string][]string{"status": {"open", "in-progress", "closed", "wontfix"}},
		Terminal:    map[string][]string{"status": {"closed", "wontfix"}},
		MultiFields: []string{"labels", "blocked-by"},
		Guard:       []string{"status", "blocked-by"},
		StaleAfter:  "2h",
	}
}

// scaleEvent builds one minimal "set" event, TS spaced a second apart by i
// so age/staleness math has something real to compute against.
func scaleEvent(i int, key, field, value, author string) model.Event {
	return model.Event{
		TS:   time.Unix(1750000000+int64(i), 0).UTC().Format(model.TSLayout),
		Type: "set", Key: key, Fields: map[string]string{field: value}, Author: author,
	}
}

// scaleChurn builds a total-event mixed-key fixture at the parent spec's
// stated volume ("including touch-base churn, which scales with
// wall-clock, not issue count"): 400 keys seeded open, 40 of them claimed
// and repeatedly touched-base to pad the chain, and finally "target-key"
// seeded and claimed as the LAST two events — the recently-touched key
// TestScaleConditionalSetNarrowsWindow claims against.
func scaleChurn(total int) []model.Event {
	var evs []model.Event
	i := 0
	next := func(key, field, value, author string) {
		evs = append(evs, scaleEvent(i, key, field, value, author))
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
	for len(evs) < total-2 { // touch-base churn, padding to total-2
		k := fmt.Sprintf("k-%d", len(evs)%claimed)
		next(k, "status", "in-progress", "worker")
	}
	next("target-key", "status", "open", "alice")
	next("target-key", "status", "in-progress", "alice")
	return evs
}

// seedFast builds a chain of len(evs) commits under ref(slug) via a single
// `git fast-import` invocation — a test-fixture-only bulk loader (real
// writes always go through BuildCommit/casLoop; this exists purely so scale
// tests can afford 5,000-event fixtures without paying ~3 subprocesses per
// event, see the package doc comment above). firstExtra attaches extra
// files (e.g. meta.json) to the first commit, matching AppendChain's own
// firstExtra convention.
func seedFast(t *testing.T, s Store, slug string, evs []model.Event, firstExtra map[string]string) {
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
	_, stderr, code := s.Repo.GitRaw(b.String(), "fast-import", "--quiet")
	if code != 0 {
		t.Fatalf("fast-import: %s", stderr)
	}
}

// TestScaleReadyEnvelopeBound is spec test 16's `ready` half: the full
// envelope — store read + board.Build + Envelope, exactly what cmd/ready.go
// runs — must complete within 140ms at the parent spec's 5,000-event scale
// (2x ready's own measured 70ms baseline).
func TestScaleReadyEnvelopeBound(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}
	s := testStore(t)
	seedFast(t, s, "board", scaleChurn(5000), map[string]string{"meta.json": "{}"})
	s.Repo.Git("", "gc", "--quiet")

	start := time.Now()
	evs, _, err := s.Events("board")
	if err != nil {
		t.Fatal(err)
	}
	b := board.Build(scaleMeta(), evs)
	env := b.Envelope(time.Now(), 50, func(*board.Key) bool { return true })
	d := time.Since(start)
	t.Logf("ready envelope @%d events: %v (ready=%d held=%d blocked=%d attention=%d)",
		len(evs), d, len(env.Ready), len(env.Held), len(env.Blocked), len(env.Attention))
	if d > 140*time.Millisecond {
		t.Fatalf("ready envelope took %v, want < 140ms (spec: 2x the measured 70ms baseline)", d)
	}
}

// TestScaleConditionalSetNarrowsWindow is spec rule 8's scaling-shape
// contract, RED against today's whole-chain precondition read and GREEN
// against the windowed one: a conditional set on a recently-touched key
// must resolve from a small backward window, never the whole 5,000-event
// chain. The precondition below only needs target-key's latest status
// write, which scaleChurn plants as the very last event — a windowed read
// resolves it in the smallest chunk (64) and never even looks at the other
// 4,999. Instrumented via BYTES moved through gitx, not subprocess COUNT:
// Events() is already exactly two subprocesses at any chain size (that's
// the whole-chain fold's own efficiency), so only data volume reveals
// whether a read actually stayed narrow.
func TestScaleConditionalSetNarrowsWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}
	dir := initRepo(t)
	s := Store{Repo: gitx.Repo{Dir: dir}}
	seedFast(t, s, "board", scaleChurn(5000), map[string]string{"meta.json": "{}"})
	s.Repo.Git("", "gc", "--quiet") // pack the fixture so the measured op's own gc --auto is a no-op

	var calls, byteCount int64
	s.Repo = gitx.Repo{Dir: dir, Calls: &calls, Bytes: &byteCount}

	resolved := false
	pre := func(events []model.Event, reachedRoot bool) error {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Key == "target-key" {
				if _, ok := events[i].Fields["status"]; ok {
					resolved = true
					return nil
				}
			}
		}
		if reachedRoot {
			resolved = true
			return nil
		}
		return ErrNeedsMoreHistory
	}
	ev := scaleEvent(99999, "target-key", "status", "closed", "bob")
	if _, err := s.AppendChecked("board", &ev, pre, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	if !resolved {
		t.Fatal("precondition never resolved")
	}

	t.Logf("conditional set on a recently-touched key among 5000 events: %d subprocess calls, %d bytes moved", calls, byteCount)
	// A window of 64 fully resolves target-key's latest status write (it's
	// the newest event in the whole chain): one chunk (one `git log -n 64`
	// plus one `cat-file --batch` for 64 event.json blobs, ~26KB measured on
	// this hardware) plus the CAS loop's own small, constant-size calls
	// (head read, hash-object/mktree/commit-tree/update-ref/gc). 50KB gives
	// headroom above that while staying two orders of magnitude below a
	// whole-chain fold of 5000 events (~2.8MB, measured in
	// TestScaleDegenerateWindowCases) — the assertion is on SHAPE, not a
	// tight byte count.
	const maxBytes = 50_000
	if byteCount > maxBytes {
		t.Fatalf("conditional set on a recently-touched key moved %d bytes — want < %d (a narrow window must stay small; the whole chain is ~2.8MB)", byteCount, maxBytes)
	}
}

// TestScaleDegenerateWindowCases logs (never asserts — spec: "degenerate
// cases stated in the test report") the two honest worst-case precondition
// reads rule 8 calls out: proving a long-untouched key's current state
// (its last write sits near the chain root) and proving a blocked-by token
// does NOT exist anywhere in history. Both can only be settled by walking
// the window all the way back — the windowed read degrades toward Events'
// own whole-chain fold, exactly as the spec states.
func TestScaleDegenerateWindowCases(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}
	dir := initRepo(t)
	s := Store{Repo: gitx.Repo{Dir: dir}}
	evs := scaleChurn(5000)
	// ancient-key: seeded as the very FIRST event, never written again —
	// only reaching the chain root can prove that's still its state.
	ancient := scaleEvent(-1, "ancient-key", "status", "open", "alice")
	evs = append([]model.Event{ancient}, evs...)
	seedFast(t, s, "board", evs, map[string]string{"meta.json": "{}"})
	s.Repo.Git("", "gc", "--quiet")

	var calls, byteCount int64
	s.Repo = gitx.Repo{Dir: dir, Calls: &calls, Bytes: &byteCount}

	start := time.Now()
	pre := func(events []model.Event, reachedRoot bool) error {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Key == "ancient-key" {
				return nil
			}
		}
		if reachedRoot {
			return nil
		}
		return ErrNeedsMoreHistory
	}
	ev := scaleEvent(99998, "ancient-key", "status", "closed", "bob")
	if _, err := s.AppendChecked("board", &ev, pre, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	t.Logf("degenerate case (long-untouched key, resolves only at the chain root): %v, %d subprocess calls, %d bytes",
		time.Since(start), calls, byteCount)

	calls, byteCount = 0, 0
	start = time.Now()
	pre2 := func(events []model.Event, reachedRoot bool) error {
		for _, e := range events {
			if e.Key == "does-not-exist" {
				return nil // would prove existence; never true in this fixture
			}
		}
		if reachedRoot {
			return nil // absence proven — the unknown_key degenerate case
		}
		return ErrNeedsMoreHistory
	}
	ev2 := scaleEvent(99997, "target-key", "blocked-by", "does-not-exist", "carol")
	if _, err := s.AppendChecked("board", &ev2, pre2, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	t.Logf("degenerate case (nonexistent blocked-by token, absence provable only at the chain root): %v, %d subprocess calls, %d bytes",
		time.Since(start), calls, byteCount)
}
