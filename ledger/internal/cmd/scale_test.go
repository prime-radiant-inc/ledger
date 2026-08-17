package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/scaletest"
	"ledger/internal/store"
)

// ---------------------------------------------------------------------
// setPrecondition's whole-chain read (set.go), exercised against the REAL
// closure — not a synthetic stand-in. Sync spec rev 7 Addition 5 deleted
// the windowed-read gate (windowResolved/keyTouched) that used to live
// here: every guarded write now reads the whole chain, unconditionally, on
// every CAS attempt. Two concerns survive that deletion, per review:
//
//  1. Decision correctness: does a guarded write still reach the CORRECT
//     decision when the fact it needs sits deep in history — far past
//     where the old 64-event window would have looked? TestSetPrecondition
//     CorrectDecisions below builds a >256-event chain with one "ancient"
//     fact buried at the root and asserts the CORRECT decision on each of
//     four checks: a claim on a human-labeled key still requires
//     --override, a duplicate --expect none seed still loses to an ancient
//     status write, a blocked-by token that only exists at the chain root
//     is still accepted, and a second status write on an ancient key is
//     never misread as the first (which would wrongly demand a title).
//     Whole-chain reads make all four trivially correct by construction
//     (nothing is ever out of view) — this test is the regression trap
//     against a narrowed read ever creeping back in.
//
//  2. Scaling shape: does the real closure's whole-chain read cost the SAME
//     regardless of whether the facts it needs are recent or ancient?
//     TestSetPreconditionWholeChainCost below drives the real closure
//     through AppendChecked with a counting gitx.Repo, covering both
//     directions: a key whose every needed fact (status AND labels)
//     resolves from the newest few events, and a key that resolves status
//     quickly but was NEVER labeled (rule 5's human signal is key-scoped
//     and gates every guarded write, so proving "never labeled" is an
//     absence proof that can only be settled at the chain root). Amendment
//     inventory item 1 retracts the issues spec's test 16 "not a full
//     re-fold per retry" clause: both directions must now cost about the
//     same — the whole chain — replacing the old windowed test's
//     small-vs-large split.
// ---------------------------------------------------------------------

// seedReadyBoard writes a ready-capable board's meta.json plus evs as one
// commit chain via scaletest.Seed (git fast-import — see its doc comment
// for why: an individual-event seed of a few hundred events is still slow
// enough to matter across four fixtures). Returns the resolved Store so
// callers can read real event ids back, or drive AppendChecked directly.
func seedReadyBoard(t *testing.T, dir, slug string, evs []model.Event) store.Store {
	t.Helper()
	res, err := store.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Store
	meta := model.Meta{
		Slug: slug, Scope: "scale test", Created: evs[0].TS, CreatedBy: "t",
		Fields:      map[string][]string{"status": {"open", "in-progress", "closed", "wontfix"}},
		FieldOrder:  []string{"status"},
		MultiFields: []string{"labels", "blocked-by"},
		Terminal:    map[string][]string{"status": {"closed", "wontfix"}},
		Guard:       []string{"status", "blocked-by"},
		StaleAfter:  "2h",
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	scaletest.Seed(t, s.Repo, slug, evs, map[string]string{"meta.json": string(metaJSON)})
	s.Repo.Git("", "gc", "--quiet")
	return s
}

// mustFieldEventID reads slug's full chain back (test setup only — the
// thing under test is the precondition's OWN read, not this one) and
// returns the id of the most recent event that wrote (key, field), for
// tests that need a real --expect target.
func mustFieldEventID(t *testing.T, s store.Store, slug, key, field string) string {
	t.Helper()
	evs, _, err := s.Events(slug)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Key == key {
			if _, ok := evs[i].Fields[field]; ok {
				return evs[i].ID
			}
		}
	}
	t.Fatalf("no event found for key=%s field=%s", key, field)
	return ""
}

// deepFixture places one "ancient" event at the chain root, then pads with
// padding more churn events — deep enough (>256, per review) that a single
// 64-event backward probe cannot see the ancient event; only reaching the
// chain root can.
func deepFixture(ancient model.Event, padding int) []model.Event {
	evs := []model.Event{ancient}
	evs = append(evs, scaletest.Churn(padding)...)
	return evs
}

// TestSetPreconditionCorrectDecisions is finding 1's regression trap: four
// writes, each requiring the precondition read to see an ancient fact 300
// events back in a chain the old 64-event window would have missed, each
// asserting the CORRECT decision. A narrowed read that missed the ancient
// fact would flip every one of these to the wrong answer.
func TestSetPreconditionCorrectDecisions(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}

	t.Run("ancient human label requires override", func(t *testing.T) {
		dir := initRepo(t)
		ancient := scaletest.Event(-1, "reserved-issue", "labels", "human", "alice")
		evs := deepFixture(ancient, 300)
		seedReadyBoard(t, dir, "issues", evs)

		so, se, code := run(t, dir, "set", "reserved-issue", "status=open", "--expect", "none",
			"-m", "reserved for a teammate", "--ledger", "issues")
		if code != 4 || !strings.Contains(se, "needs_override") {
			t.Fatalf("seeding a pre-human-labeled key must require --override even when the label is 300 events back: code=%d stdout=%q stderr=%q", code, so, se)
		}
	})

	t.Run("ancient status write beats a duplicate --expect none seed", func(t *testing.T) {
		dir := initRepo(t)
		ancient := scaletest.Event(-1, "old-issue", "status", "open", "alice")
		ancient.Text = "original title"
		evs := deepFixture(ancient, 300)
		seedReadyBoard(t, dir, "issues", evs)

		so, se, code := run(t, dir, "set", "old-issue", "status=open", "--expect", "none",
			"-m", "duplicate seed attempt", "--ledger", "issues")
		if code != 4 || !strings.Contains(se, "claim_lost") || !strings.Contains(se, "already exists") {
			t.Fatalf("--expect none against a key whose only status write is 300 events back must lose to it (claim_lost, 'already exists' hint): code=%d stdout=%q stderr=%q", code, so, se)
		}
	})

	t.Run("blocked-by token that only exists at the chain root is accepted", func(t *testing.T) {
		dir := initRepo(t)
		ancient := scaletest.Event(-1, "dep-x", "status", "open", "alice")
		ancient.Text = "ancient dependency"
		evs := deepFixture(ancient, 300)
		seedReadyBoard(t, dir, "issues", evs)

		so, se, code := run(t, dir, "set", "new-issue", "blocked-by=dep-x", "--expect", "none", "--ledger", "issues")
		if code != 0 {
			t.Fatalf("blocked-by=dep-x must succeed — dep-x exists (300 events back), it's just not in a 64-event window: code=%d stdout=%q stderr=%q", code, so, se)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(so), &doc); err != nil || doc["id"] == nil {
			t.Fatalf("expected a landed id: %s", so)
		}
	})

	t.Run("deep status write is not misread as the first one", func(t *testing.T) {
		dir := initRepo(t)
		ancient := scaletest.Event(-1, "long-issue", "status", "open", "alice")
		ancient.Text = "long issue title"
		evs := deepFixture(ancient, 300)
		s := seedReadyBoard(t, dir, "issues", evs)
		openID := mustFieldEventID(t, s, "issues", "long-issue", "status")

		// Claim it with NO -m: legal because this is the second status write
		// (title already set by the ancient seed), not the first. A gate that
		// can't see the ancient seed misreads this as the first status write
		// and wrongly demands a title, rejecting with empty_body.
		so, se, code := run(t, dir, "set", "long-issue", "status=in-progress", "--expect", openID, "--as", "alice", "--ledger", "issues")
		if code != 0 || strings.Contains(se, "empty_body") {
			t.Fatalf("claiming a key whose title was set 300 events back must not require -m again: code=%d stdout=%q stderr=%q", code, so, se)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(so), &doc); err != nil || doc["id"] == nil {
			t.Fatalf("expected a landed id: %s", so)
		}
	})
}

// TestSetPreconditionWholeChainCost is finding 2's real-closure regression
// test, and the direct replacement for the retracted issues-spec test 16
// clause ("not a full re-fold per retry" — amendment inventory item 1):
// drives the ACTUAL setPrecondition closure (not a synthetic stand-in)
// through AppendChecked with a counting gitx.Repo, at the parent spec's
// 5,000-event scale, in both directions the old windowed test used to tell
// apart — a key whose every needed fact is recent, and a key that was
// NEVER labeled (an absence proof that can only be settled at the chain
// root). Both subtests assert the SAME byte-count range: with the window
// gone, there is no cheap case left — every guarded write pays the
// whole-chain read, unconditionally, regardless of how recent its facts
// are. wholeChainBytes brackets that one shared cost.
func TestSetPreconditionWholeChainCost(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}

	// wholeChainBytes brackets a single whole-chain fold (one `git log` plus
	// one `cat-file --batch`) of ~5000 events plus the CAS loop's own small,
	// constant-size calls. The floor rules out a regression back to a
	// narrow window (which would move only tens of KB); the ceiling is
	// generous headroom above the whole chain's measured size on this
	// hardware (hardware-specific, per the task report's note on this
	// sandbox's git/subprocess overhead) — the assertion is on SHAPE (both
	// cases cost the same), not a tight byte count.
	const minBytes = 500_000
	const maxBytes = 4_500_000
	assertWholeChainCost := func(t *testing.T, label string, byteCount int64) {
		t.Helper()
		if byteCount < minBytes || byteCount > maxBytes {
			t.Fatalf("%s moved %d bytes — want [%d, %d] (every guarded write now reads the whole chain, unconditionally)",
				label, byteCount, minBytes, maxBytes)
		}
	}

	t.Run("status and labels both resolve from recent history", func(t *testing.T) {
		dir := initRepo(t)
		evs := scaletest.Churn(4997)
		n := len(evs)
		evs = append(evs,
			scaletest.Event(n, "target-key", "status", "open", "alice"),
			scaletest.Event(n+1, "target-key", "labels", "urgent", "alice"),
			scaletest.Event(n+2, "target-key", "status", "in-progress", "alice"),
		)
		s := seedReadyBoard(t, dir, "board", evs)
		claimID := mustFieldEventID(t, s, "board", "target-key", "status")
		led, err := (&Ctx{Store: s}).Load("board")
		if err != nil {
			t.Fatal(err)
		}

		var calls, byteCount int64
		counted := s
		counted.Repo = gitx.Repo{Dir: s.Repo.Dir, Calls: &calls, Bytes: &byteCount}

		fields := map[string]string{"status": "in-progress"}
		ev := model.NewEvent("set", "alice", counted.Repo)
		ev.Key, ev.Fields, ev.Text = "target-key", fields, "still on it"
		pre := setPrecondition("target-key", fields, "status", claimID, true, led.Meta, ev.Text, "alice", false, &ev.Override)
		if _, err := counted.AppendChecked("board", &ev, pre, store.ExpectPresent); err != nil {
			t.Fatalf("AppendChecked: %v", err)
		}
		t.Logf("real setPrecondition, status+labels both recent, among 5000 events: %d subprocess calls, %d bytes moved", calls, byteCount)
		assertWholeChainCost(t, "a guarded write whose every needed fact is recent", byteCount)
	})

	t.Run("never-labeled key pays the same whole-chain cost", func(t *testing.T) {
		dir := initRepo(t)
		evs := scaletest.Churn(4998)
		n := len(evs)
		evs = append(evs,
			scaletest.Event(n, "target-key2", "status", "open", "alice"),
			scaletest.Event(n+1, "target-key2", "status", "in-progress", "alice"),
		)
		s := seedReadyBoard(t, dir, "board", evs)
		claimID := mustFieldEventID(t, s, "board", "target-key2", "status")
		led, err := (&Ctx{Store: s}).Load("board")
		if err != nil {
			t.Fatal(err)
		}

		var calls, byteCount int64
		counted := s
		counted.Repo = gitx.Repo{Dir: s.Repo.Dir, Calls: &calls, Bytes: &byteCount}

		fields := map[string]string{"status": "in-progress"}
		ev := model.NewEvent("set", "alice", counted.Repo)
		ev.Key, ev.Fields, ev.Text = "target-key2", fields, "still on it"
		pre := setPrecondition("target-key2", fields, "status", claimID, true, led.Meta, ev.Text, "alice", false, &ev.Override)
		if _, err := counted.AppendChecked("board", &ev, pre, store.ExpectPresent); err != nil {
			t.Fatalf("AppendChecked: %v", err)
		}
		t.Logf("real setPrecondition, status recent but NEVER labeled, among 5000 events: %d subprocess calls, %d bytes moved", calls, byteCount)
		assertWholeChainCost(t, "a never-labeled key's absence proof", byteCount)
	})
}
