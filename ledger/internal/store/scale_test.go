package store

import (
	"fmt"
	"testing"
	"time"

	"ledger/internal/board"
	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/scaletest"
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
// Fixtures come from internal/scaletest, shared with the real-
// setPrecondition scale tests in internal/cmd/scale_test.go — the two
// suites measure against literally the same event-chain shape.
// ---------------------------------------------------------------------

// TestScaleReadyEnvelopeBound is spec test 16's `ready` half: the full
// envelope — store read + board.Build + Envelope, exactly what cmd/ready.go
// runs — must complete within 140ms at the parent spec's 5,000-event scale
// (2x ready's own measured 70ms baseline).
//
// Measured three times against the same seeded store (no reseed between
// samples — only the read+fold+Envelope work is timed) and asserted on the
// MEDIAN, not the best-of: a median stays honest under sustained slowness
// (a genuine regression still fails) while shrugging off the single-sample
// load spikes this measurement is prone to on a busy machine (confirmed via
// baseline comparison against pre-rev-17 code showing the identical
// spike pattern — a machine-load artifact, not a code regression). All
// three samples are logged so the test report shows the spread.
func TestScaleReadyEnvelopeBound(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}
	s := testStore(t)
	scaletest.Seed(t, s.Repo, "board", scaletest.Churn(5000), map[string]string{"meta.json": "{}"})
	s.Repo.Git("", "gc", "--quiet")

	var samples [3]time.Duration
	var lastEnv board.Envelope
	var evCount int
	for i := range samples {
		start := time.Now()
		evs, _, err := s.Events("board")
		if err != nil {
			t.Fatal(err)
		}
		b := board.Build(scaletest.Meta(), evs)
		lastEnv = b.Envelope(time.Now(), 50, func(*board.Key) bool { return true })
		samples[i] = time.Since(start)
		evCount = len(evs)
	}
	t.Logf("ready envelope @%d events, 3 samples: %v (ready=%d held=%d blocked=%d attention=%d)",
		evCount, samples, len(lastEnv.Ready), len(lastEnv.Held), len(lastEnv.Blocked), len(lastEnv.Attention))

	median := medianDuration(samples[:])
	if median > 140*time.Millisecond {
		t.Fatalf("ready envelope median took %v (samples: %v), want < 140ms (spec: 2x the measured 70ms baseline)", median, samples)
	}
}

// medianDuration returns the middle value of d, sorted ascending. d is
// small (always 3 in this file's use) so an allocation-free insertion sort
// on a local copy is simpler than pulling in sort.Slice for three elements.
func medianDuration(d []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), d...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	return sorted[len(sorted)/2]
}

// TestRunPreconditionStopsAtFirstResolvedWindow is a STORE-LEVEL mechanism
// test: it drives runPrecondition/EventsWindow directly with a synthetic,
// single-field precondition (only target-key's latest status write, which
// this fixture plants as the very last event) to isolate the windowing
// PRIMITIVE from any particular Precondition's own field requirements.
//
// This is deliberately narrower than the real production claim: the real
// setPrecondition closure needs more than one field resolved per guarded
// write (see internal/cmd/scale_test.go's TestSetPreconditionScalingShape,
// which drives the actual closure and is the test that caught windowSizes'
// 64/256/1024 staircase regressing on a never-labeled key — fixed by
// collapsing windowSizes to a single 64-probe, see its doc comment). This
// test's job is narrower and stays valid regardless of that: does
// runPrecondition actually stop growing once a Precondition stops asking
// for more? Instrumented via BYTES moved through gitx, not subprocess
// COUNT: Events() is already exactly two subprocesses at any chain size,
// so only data volume reveals whether a read actually stayed narrow.
func TestRunPreconditionStopsAtFirstResolvedWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}
	dir := initRepo(t)
	s := Store{Repo: gitx.Repo{Dir: dir}}
	evs := scaletest.Churn(4998)
	evs = append(evs,
		scaletest.Event(len(evs), "target-key", "status", "open", "alice"),
		scaletest.Event(len(evs)+1, "target-key", "status", "in-progress", "alice"))
	scaletest.Seed(t, s.Repo, "board", evs, map[string]string{"meta.json": "{}"})
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
	ev := scaletest.Event(99999, "target-key", "status", "closed", "bob")
	if _, err := s.AppendChecked("board", &ev, pre, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	if !resolved {
		t.Fatal("precondition never resolved")
	}

	t.Logf("synthetic single-field precondition on a recently-touched key among 5000 events: %d subprocess calls, %d bytes moved", calls, byteCount)
	// The 64-event probe fully resolves target-key's latest status write
	// (it's the newest event in the whole chain): one chunk (one `git log -n
	// 64` plus one `cat-file --batch` for 64 event.json blobs, ~26KB
	// measured on this hardware) plus the CAS loop's own small,
	// constant-size calls (head read, hash-object/mktree/commit-tree/
	// update-ref/gc). 50KB gives headroom above that while staying two
	// orders of magnitude below a whole-chain fold of 5000 events (~2.8MB,
	// measured in TestScaleDegenerateWindowCases) — the assertion is on
	// SHAPE, not a tight byte count.
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
// own whole-chain fold, exactly as the spec states. Uses the same
// synthetic single-field precondition shape as
// TestRunPreconditionStopsAtFirstResolvedWindow, for the same reason: this
// measures the store-level windowing PRIMITIVE's degenerate cost, not any
// particular Precondition's field requirements.
func TestScaleDegenerateWindowCases(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}
	dir := initRepo(t)
	s := Store{Repo: gitx.Repo{Dir: dir}}
	evs := scaletest.Churn(5000)
	// ancient-key: seeded as the very FIRST event, never written again —
	// only reaching the chain root can prove that's still its state.
	ancient := scaletest.Event(-1, "ancient-key", "status", "open", "alice")
	evs = append([]model.Event{ancient}, evs...)
	scaletest.Seed(t, s.Repo, "board", evs, map[string]string{"meta.json": "{}"})
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
	ev := scaletest.Event(99998, "ancient-key", "status", "closed", "bob")
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
	ev2 := scaletest.Event(99997, "target-key", "blocked-by", "does-not-exist", "carol")
	if _, err := s.AppendChecked("board", &ev2, pre2, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	t.Logf("degenerate case (nonexistent blocked-by token, absence provable only at the chain root): %v, %d subprocess calls, %d bytes",
		time.Since(start), calls, byteCount)
}
