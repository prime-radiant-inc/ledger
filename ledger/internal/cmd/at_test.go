package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/store"
)

// setupAtBoard makes a minimal ready-capable board (one key, one status
// field) with the given staleness horizon — at_test.go's own fixture,
// separate from ready_test.go's seven-key seedReadyEnvelope, since these
// tests only need one key to carry a claim.
func setupAtBoard(t *testing.T, staleAfter string) string {
	t.Helper()
	return setupReady(t, "--stale-after", staleAfter)
}

// claimAt directly appends an in-progress claim event stamped with an
// explicit ts — bypassing `set`'s own clock so a test can stage an
// arbitrary claim time (an ahead-writer's clock, or a plain fixed ts)
// without waiting on the wall clock. Mirrors cursor_test.go's buildChain
// pattern of injecting raw events for fixture control.
func claimAt(t *testing.T, dir, key, ts, author string) {
	t.Helper()
	s := store.Store{Repo: gitx.Repo{Dir: dir}}
	ev := model.Event{Type: "set", Key: key, Author: author, TS: ts,
		Fields: map[string]string{"status": "in-progress"}}
	if _, err := s.Append("issues", ev, nil, store.ExpectPresent); err != nil {
		t.Fatalf("claimAt: %v", err)
	}
}

// heldEntry0 decodes ready's JSON payload and returns held[0], failing the
// test if held doesn't have exactly one entry.
func heldEntry0(t *testing.T, so string) map[string]any {
	t.Helper()
	doc := mustJSON(t, so)
	held, _ := doc["held"].([]any)
	if len(held) != 1 {
		t.Fatalf("expected exactly one held entry: %v", doc)
	}
	return held[0].(map[string]any)
}

// TestAtFixesTheEvaluationClockOnReady: --at (sync spec Addition 4) exists
// on `ready` and moves the evaluation clock only, never the chain. A
// far-future --at makes an old claim stale; a --at BEFORE the claim's own
// ts renders it age 0s (pinned) rather than a negative age, and an age-0s
// claim is never stale.
func TestAtFixesTheEvaluationClockOnReady(t *testing.T) {
	dir := setupAtBoard(t, "2h")
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	seedID := mustEventID(t, dir, "k1")
	run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "alice")

	so := mustRun(t, dir, "ready", "--ledger", "issues", "--at", "2030-01-01T00:00:00.000")
	e := heldEntry0(t, so)
	if e["stale"] != true {
		t.Fatalf("--at far in the future must render the 2h-horizon claim stale: %v", e)
	}

	so2 := mustRun(t, dir, "ready", "--ledger", "issues", "--at", "2020-01-01T00:00:00.000")
	e2 := heldEntry0(t, so2)
	if e2["age"] != "0s" {
		t.Fatalf("an event newer than --at must render age 0s (pinned), got %v", e2["age"])
	}
	if e2["stale"] != false {
		t.Fatalf("an age-0s claim must never be stale: %v", e2)
	}
}

// TestAtAcceptedOnNotesFixesLatestAge: notes --latest is the flag's other
// home (Addition 4). Bypasses run() (which never sets TTY) to invoke
// runNotes directly with an explicit TTY Ctx, the same pattern
// TestShowTTYNoteSummaryOneLine and TestStatusTTYEscapesControlChars use —
// --latest's age line is TTY-only, absent from the JSON payload.
func TestAtAcceptedOnNotesFixesLatestAge(t *testing.T) {
	dir := seed(t) // write_test.go/read_test.go's demo ledger with notes
	// A note whose own ts is after --at renders age 0s (pinned), same
	// clamp rule as ready.
	future := model.Event{Type: "note", Kind: "gotcha", Author: "x", TS: "2030-06-01T00:00:00.000", Text: "future note"}
	s := store.Store{Repo: gitx.Repo{Dir: dir}}
	if _, err := s.Append("demo", future, nil, store.ExpectPresent); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	if err := runNotes(c, "gotcha", "", "", true, 10, "demo", "2025-01-01T00:00:00.000"); err != nil {
		t.Fatal(err)
	}
	rendered := buf.String()
	if !strings.Contains(rendered, "0s ago") {
		t.Fatalf("notes --at before the note's own ts must pin the age at 0s: %q", rendered)
	}
}

// TestAtRejectedOnReadOnlyVerbs: the flag's complete existence rule (sync
// spec Addition 4) — EXISTS on ready and notes only; every other read verb
// rejects it as bad_usage via flag absence (cobra's own unknown-flag error,
// mapped by root.go's isCobraUsageErr).
func TestAtRejectedOnReadOnlyVerbs(t *testing.T) {
	dir := seed(t)
	at := []string{"--at", "2030-01-01T00:00:00.000"}
	cases := [][]string{
		{"show"},
		{"status"},
		{"tail"},
		{"since"},
		{"render", "--to", dir + "/out.txt"},
		{"ls"},
		{"watch", "--timeout", "0.01"},
	}
	for _, args := range cases {
		full := append(append([]string{}, args...), at...)
		so, se, code := run(t, dir, full...)
		if code != 4 || !strings.Contains(se, "bad_usage") {
			t.Fatalf("--at on `%s` must be bad_usage exit 4: %d\n%s\n%s", args[0], code, so, se)
		}
	}
}

// TestAtRejectedOnWriteVerbs: --at on a write verb is rejected the same
// way — the spec's stated reason (Addition 4) is that a fake clock there
// would dissolve rule 5's `claim` signal and skip needs_override
// unrecorded, so set/close carry no --at flag at all: the rejection is
// flag absence, with no code path that could honor it even if parsing let
// it through. Nothing must be written by a rejected attempt.
func TestAtRejectedOnWriteVerbs(t *testing.T) {
	dir := setup(t)
	at := []string{"--at", "2030-01-01T00:00:00.000"}
	cases := [][]string{
		{"set", "t1", "status=open", "--expect", "none", "-m", "x", "--as", "a"},
		{"close", "demo", "--as-state", "abandoned"},
	}
	for _, args := range cases {
		full := append(append([]string{}, args...), at...)
		so, se, code := run(t, dir, full...)
		if code != 4 || !strings.Contains(se, "bad_usage") {
			t.Fatalf("--at on `%s` must be bad_usage exit 4: %d\n%s\n%s", args[0], code, so, se)
		}
	}
	// Confirm the rejected set attempt wrote nothing.
	so, _, _ := run(t, dir, "status")
	if strings.Contains(so, `"key":"t1"`) {
		t.Fatalf("a rejected write verb must not have written: %s", so)
	}
}

// TestBadAtValueOnReadyIsRejected: an unparseable --at is bad_value, never
// a silent revert to wall-clock now. The legacy (no-milliseconds) layout
// is accepted alongside the millisecond one (model.ParseTS's dual layout).
func TestBadAtValueOnReadyIsRejected(t *testing.T) {
	dir := setupAtBoard(t, "2h")
	_, se, code := run(t, dir, "ready", "--ledger", "issues", "--at", "yesterday")
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("an unparseable --at must be rejected: %d %s", code, se)
	}
	if _, _, code := run(t, dir, "ready", "--ledger", "issues", "--at", "2030-01-01T00:00:00"); code != 0 {
		t.Fatal("the legacy timestamp layout must be accepted by --at")
	}
}

// TestGeneralAgeClampWithNoAtFlag: the age clamp is a GENERAL rule (sync
// spec Addition 4/1(c)), not an --at rule — a peer host whose clock runs
// ahead needs no --at at all to hit it. An event ts 3h in the future
// renders age 0s in both `ready` (JSON) and `ls` (TTY) with no --at flag
// present anywhere in the command.
func TestGeneralAgeClampWithNoAtFlag(t *testing.T) {
	dir := setupAtBoard(t, "2h")
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	future := model.Now().Add(3 * time.Hour).UTC().Format(model.TSLayout)
	claimAt(t, dir, "k1", future, "alice")

	so := mustRun(t, dir, "ready", "--ledger", "issues")
	e := heldEntry0(t, so)
	if e["age"] != "0s" {
		t.Fatalf("a future-ts claim must clamp to age 0s with no --at: %v", e)
	}
	if e["stale"] != false {
		t.Fatalf("a clamped age must never be stale: %v", e)
	}

	// ls's age render is TTY-only; bypass run() the same way
	// TestShowTTYNoteSummaryOneLine does for show.
	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	if err := runLs(c, false); err != nil {
		t.Fatal(err)
	}
	rendered := buf.String()
	if !strings.Contains(rendered, "0s ago") {
		t.Fatalf("ls must also clamp a future last-write ts to 0s ago: %q", rendered)
	}
}

// TestAheadWriterClaimGoesStaleExactlySkewLateViaReady: the CLI-level
// counterpart of the board-package math test — this one exercises the
// actual funnel (ready.go's now := resolveAt/model.Now() routing) via the
// internal seam, at the same two horizons (Addition 1(c)'s asymmetric skew
// doctrine: an ahead-writer's claim goes stale exactly `skew` LATE at
// every horizon).
func TestAheadWriterClaimGoesStaleExactlySkewLateViaReady(t *testing.T) {
	skew := 30 * time.Minute
	base, err := model.ParseTS("2026-08-16T12:00:00.000")
	if err != nil {
		t.Fatal(err)
	}

	for _, horizon := range []time.Duration{time.Hour, 5 * time.Hour} {
		dir := setupAtBoard(t, horizon.String())
		run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
		// The ahead-writer's own clock reads base+skew when it claims —
		// stamp the claim event with that ts directly (equivalent to
		// nowFn reading base+skew at write time on that host).
		claimAt(t, dir, "k1", base.Add(skew).UTC().Format(model.TSLayout), "aheadhost")

		checkStale := func(realNow time.Time) bool {
			restore := model.SetNowForTest(func() time.Time { return realNow })
			defer restore()
			so := mustRun(t, dir, "ready", "--ledger", "issues")
			return heldEntry0(t, so)["stale"] == true
		}

		if checkStale(base.Add(horizon)) {
			t.Fatalf("horizon=%s: must not be stale at base+horizon — the ahead skew hasn't been exhausted yet", horizon)
		}
		if checkStale(base.Add(horizon).Add(skew).Add(-time.Millisecond)) {
			t.Fatalf("horizon=%s: must not be stale 1ms before skew-late", horizon)
		}
		if !checkStale(base.Add(horizon).Add(skew).Add(time.Millisecond)) {
			t.Fatalf("horizon=%s: must be stale exactly skew-late", horizon)
		}
	}
}
