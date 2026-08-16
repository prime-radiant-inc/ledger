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
// setPrecondition's windowed-read gate (windowResolved/keyTouched, set.go),
// exercised against the REAL closure — not a synthetic stand-in. Two
// concerns, per review:
//
//  1. Decision correctness: does the gate actually change what a write
//     decides, on a chain deep enough that a single 64-event probe misses
//     the fact that matters? TestSetPreconditionCorrectDecisions below
//     builds a >256-event chain with one "ancient" fact buried past the
//     probe window and asserts the CORRECT decision on each of the four
//     checks windowResolved gates. Each assertion is a genuine regression
//     trap: with windowResolved weakened to `return true` unconditionally
//     (skip the gate, decide from whatever partial window the first probe
//     happened to return), all four decisions flip to the WRONG answer —
//     confirmed by re-running this file against a temporarily weakened
//     windowResolved (unconditional `return true`): a claim on a human key
//     is wrongly accepted, --expect none is wrongly accepted over an
//     existing value, a real blocked-by edge is wrongly rejected as
//     unknown_key, and a claim against a deep-history key is wrongly
//     rejected (claim_lost, "nothing matches --expect") because the target
//     field's own CAS resolution and the first-status-write check share the
//     identical underlying fact — a truncated window that can't see the
//     key's one-and-only status write breaks both at once, not just the
//     title check in isolation.
//
//  2. Scaling shape: does the real closure's OWN field requirements (not
//     just the field a synthetic test happens to check) actually stay
//     narrow on the common case? TestSetPreconditionScalingShape below
//     drives the real closure through AppendChecked with a counting
//     gitx.Repo, covering both directions: a key whose every needed fact
//     (status AND labels) resolves in the first 64-event probe, and a key
//     that resolves status quickly but was NEVER labeled — rule 5's human
//     signal is key-scoped and gates every guarded write, so proving "never
//     labeled" is an absence proof that can only be settled at the chain
//     root (spec rule 8's own stated asymmetry). This second case is what
//     caught the windowSizes 64/256/1024 staircase actively losing to a
//     plain whole-chain read (a never-labeled key paid for three wasted
//     probes before falling back anyway) — fixed by collapsing windowSizes
//     to a single 64-probe (see its doc comment in store.go).
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
// writes, each requiring windowResolved to walk past a single 64-event
// probe to reach an ancient fact, each asserting the DECISION a whole-chain
// read would also reach. Weakening windowResolved to `return true`
// unconditionally flips every one of these to the wrong answer (verified
// by hand during review).
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

// TestSetPreconditionScalingShape is finding 2's real-closure regression
// test: drives the ACTUAL setPrecondition closure (not a synthetic
// stand-in) through AppendChecked with a counting gitx.Repo, at the parent
// spec's 5,000-event scale, in both directions the review called out.
func TestSetPreconditionScalingShape(t *testing.T) {
	if testing.Short() {
		t.Skip("scale")
	}

	t.Run("status and labels both resolve in the first probe", func(t *testing.T) {
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
		const maxBytes = 60_000 // one 64-event probe resolves both fields; measured ~35KB
		if byteCount > maxBytes {
			t.Fatalf("a guarded write whose every needed fact is recent moved %d bytes — want < %d", byteCount, maxBytes)
		}
	})

	t.Run("never-labeled key falls back to the whole chain", func(t *testing.T) {
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
		// This is the honest absence-proof cost spec rule 8 states outright:
		// proving "never labeled" requires reaching the chain root, so this
		// pays close to a whole-chain read either way. On THIS hardware, at
		// this fixture's scale, that's ~3.89MB (one wasted 64-probe, ~26KB,
		// plus one whole-chain read) — measured directly against a
		// temporarily restored windowSizes = {64, 256, 1024} staircase for
		// comparison: ~4.40MB (the same whole-chain read, plus THREE wasted
		// probes instead of one). The absolute numbers are hardware-specific
		// (see the task report's note on this sandbox's git/subprocess
		// overhead running well above cited reference figures), but the
		// SHAPE claim — one wasted probe, not a multi-tier staircase — is
		// exactly what this bound catches: comfortably above the fixed
		// behavior, comfortably below a reintroduced staircase.
		const maxBytes = 4_100_000
		if byteCount > maxBytes {
			t.Fatalf("a never-labeled key's absence proof moved %d bytes — want < %d (one wasted 64-probe plus one whole-chain read, not a multi-tier staircase)", byteCount, maxBytes)
		}
	})
}
