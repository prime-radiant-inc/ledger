package store

import (
	"fmt"
	"testing"
	"time"

	"ledger/internal/dag"
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
// Correctness-at-scale tests. Fixtures come from internal/scaletest,
// shared with the setPrecondition scale tests in
// internal/cmd/scale_test.go — the two suites run against literally the
// same event-chain shape. These tests assert behavior (read class, byte
// movement, degenerate resolution), never wall-clock bounds: durations
// are logged for the curious, not asserted.
// ---------------------------------------------------------------------

// TestAppendCheckedPreconditionAlwaysWholeChain is the store-level half of
// the retracted issues-spec test 16 clause ("not a full re-fold per
// retry" — amendment inventory item 1): a guarded write's precondition read
// IS the full read, per attempt, unconditionally — there is no window
// primitive left to stay narrow. Drives a synthetic single-field
// precondition (only target-key's latest status write, which this fixture
// plants as the very last event, so a windowed read would have resolved it
// from a tiny recent slice) and asserts the read still moved close to the
// whole 5,000-event chain's bytes, not a narrow slice — proving the read
// is whole-chain even on the case a window would have made cheap.
func TestAppendCheckedPreconditionAlwaysWholeChain(t *testing.T) {
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
	pre := func(events []model.Event, _ dag.Result) error {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Key == "target-key" {
				if _, ok := events[i].Fields["status"]; ok {
					resolved = true
					return nil
				}
			}
		}
		t.Fatal("whole-chain read must resolve target-key's status in one call — nothing left to ask for more")
		return nil
	}
	ev := scaletest.Event(99999, "target-key", "status", "closed", "bob")
	if _, err := s.AppendChecked("board", &ev, pre, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	if !resolved {
		t.Fatal("precondition never resolved")
	}

	t.Logf("synthetic single-field precondition on a recently-touched key among 5000 events: %d subprocess calls, %d bytes moved", calls, byteCount)
	// Events() is exactly two subprocesses at any chain size (one `git log`,
	// one `cat-file --batch`), so a single-attempt guarded write's byte cost
	// is dominated by that whole-chain fold (~2.8MB at 5000 events, measured
	// below) plus the CAS loop's own small, constant-size calls
	// (hash-object/mktree/commit-tree/update-ref/gc). minBytes rules out a
	// regression back to a narrow window — a recently-touched key resolving
	// from a small slice would move only tens of KB, far under this floor.
	const minBytes = 500_000
	if byteCount < minBytes {
		t.Fatalf("conditional set on a recently-touched key moved only %d bytes — want >= %d (the precondition read must be the whole chain, not a narrow window)", byteCount, minBytes)
	}
}

// TestScaleDegenerateWindowCases logs (never asserts — spec: "degenerate
// cases stated in the test report") the two honest worst-case precondition
// reads rule 8 calls out: proving a long-untouched key's current state
// (its last write sits near the chain root) and proving a blocked-by token
// does NOT exist anywhere in history. Both are now the SAME cost as every
// other guarded write, since Addition 5 deleted the window: the whole-chain
// read pays this cost unconditionally, not just on these degenerate cases.
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
	pre := func(events []model.Event, _ dag.Result) error {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Key == "ancient-key" {
				return nil
			}
		}
		return nil // absence would be proven; never true in this fixture
	}
	ev := scaletest.Event(99998, "ancient-key", "status", "closed", "bob")
	if _, err := s.AppendChecked("board", &ev, pre, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	t.Logf("degenerate case (long-untouched key, resolves only at the chain root): %v, %d subprocess calls, %d bytes",
		time.Since(start), calls, byteCount)

	calls, byteCount = 0, 0
	start = time.Now()
	pre2 := func(events []model.Event, _ dag.Result) error {
		for _, e := range events {
			if e.Key == "does-not-exist" {
				return nil // would prove existence; never true in this fixture
			}
		}
		return nil // absence proven — the unknown_key degenerate case
	}
	ev2 := scaletest.Event(99997, "target-key", "blocked-by", "does-not-exist", "carol")
	if _, err := s.AppendChecked("board", &ev2, pre2, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	t.Logf("degenerate case (nonexistent blocked-by token, absence provable only at the chain root): %v, %d subprocess calls, %d bytes",
		time.Since(start), calls, byteCount)
}
