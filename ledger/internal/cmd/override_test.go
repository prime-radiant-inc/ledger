package cmd

import (
	"strings"
	"sync"
	"testing"
	"time"

	"ledger/internal/dag"
	"ledger/internal/model"
	"ledger/internal/store"
)

// setupReadyStale makes a ready-capable board (setupReady's canonical
// shape) with a --stale-after horizon, for tests that need a claim to age
// past staleness.
func setupReadyStale(t *testing.T, staleAfter string) string {
	return setupReady(t, "--stale-after", staleAfter)
}

// TestCrossAuthorLiveClaimNeedsOverrideNamingClaimantAndAge: a live claim
// by a different author is a standing signal; the needs_override message
// names the claimant and the claim age, and the hint is the paste-ready fix.
func TestCrossAuthorLiveClaimNeedsOverrideNamingClaimantAndAge(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)
	so2, _, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "alice")
	if code != 0 {
		t.Fatal(so2)
	}
	claimID := mustJSON(t, so2)["id"].(string)

	_, se, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", claimID, "-m", "stealing", "--as", "bob")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "needs_override" {
		t.Fatalf("%s", se)
	}
	msg := doc["message"].(string)
	if !strings.Contains(msg, "'k1' has standing signal(s) that guard this write: claim (alice, ") {
		t.Fatalf("message must name claimant+age: %q", msg)
	}
	if doc["hint"] != `--override -m "<why>"` {
		t.Fatalf("hint: got %v", doc["hint"])
	}
}

// TestStaleReclaimLandsWithoutOverride: a stale claim dissolves the claim
// signal — reclaiming it needs no --override.
func TestStaleReclaimLandsWithoutOverride(t *testing.T) {
	dir := setupReadyStale(t, "10ms")
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)
	so2, _, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "alice")
	if code != 0 {
		t.Fatal(so2)
	}
	claimID := mustJSON(t, so2)["id"].(string)

	time.Sleep(30 * time.Millisecond)

	so3, se3, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", claimID,
		"-m", "reclaiming from alice: stale 30ms", "--as", "bob")
	if code != 0 {
		t.Fatalf("a stale claim must be reclaimable without --override: %d %s", code, se3)
	}
	if mustJSON(t, so3)["id"] == nil {
		t.Fatal(so3)
	}
}

// TestOwnCloseOnHumanKeyBlockedUntilOverride: the human label gates even
// the claimant's own close — needs_override until --override is supplied.
func TestOwnCloseOnHumanKeyBlockedUntilOverride(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)
	so3, _, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "a")
	if code != 0 {
		t.Fatal(so3)
	}
	claimID := mustJSON(t, so3)["id"].(string)
	// human is applied after the claim — it must still gate the close below;
	// the label doesn't need to predate the write it guards.
	_, _, code = run(t, dir, "set", "k1", "labels=human", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatal("labels=human should succeed")
	}

	_, se, code := run(t, dir, "set", "k1", "status=closed", "--evidence", "commit:abc", "--expect", claimID, "-m", "done", "--as", "a")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	want := "'k1' has standing signal(s) that guard this write: human (labeled 'human')"
	if doc["message"] != want {
		t.Fatalf("exact message: got %q want %q", doc["message"], want)
	}

	so4, se4, code := run(t, dir, "set", "k1", "status=closed", "--evidence", "commit:abc", "--expect", claimID,
		"--override", "-m", "done, per triage", "--as", "a")
	if code != 0 {
		t.Fatalf("override must land: %d %s", code, se4)
	}
	if mustJSON(t, so4)["id"] == nil {
		t.Fatal(so4)
	}
}

// TestBlockedByWriteNeverGatedByClaimOrSettledButHumanStillGates: spec rule
// 5's scope clause end-to-end (previously only proven at board.Signals unit
// level) — an edge edit against a claimed or settled key needs no
// --override (claim and settled gate status writes only), but the same edge
// edit against a human-labeled key does, and only --override lands it.
func TestBlockedByWriteNeverGatedByClaimOrSettledButHumanStillGates(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "dep", "status=open", "--expect", "none", "-m", "dep", "--as", "a")

	// Claimed key: a cross-author blocked-by edit needs no --override.
	so, _, code := run(t, dir, "set", "claimed", "status=open", "--expect", "none", "-m", "claimed key", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)
	so2, _, code := run(t, dir, "set", "claimed", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "alice")
	if code != 0 {
		t.Fatal(so2)
	}
	so3, se3, code := run(t, dir, "set", "claimed", "blocked-by=dep", "--expect", "none", "--as", "bob")
	if code != 0 {
		t.Fatalf("a claim must never gate a blocked-by write: %d %s", code, se3)
	}
	if mustJSON(t, so3)["id"] == nil {
		t.Fatal(so3)
	}

	// Settled key: a blocked-by edit needs no --override either.
	so4, _, code := run(t, dir, "set", "settled", "status=open", "--expect", "none", "-m", "settled key", "--as", "a")
	if code != 0 {
		t.Fatal(so4)
	}
	settledSeedID := mustJSON(t, so4)["id"].(string)
	_, _, code = run(t, dir, "set", "settled", "status=closed", "--evidence", "commit:x", "--expect", settledSeedID, "-m", "done", "--as", "a")
	if code != 0 {
		t.Fatal("close must succeed")
	}
	so5, se5, code := run(t, dir, "set", "settled", "blocked-by=dep", "--expect", "none", "--as", "bob")
	if code != 0 {
		t.Fatalf("settled must never gate a blocked-by write: %d %s", code, se5)
	}
	if mustJSON(t, so5)["id"] == nil {
		t.Fatal(so5)
	}

	// Human-labeled key: the same shape of write DOES need --override.
	so6, _, code := run(t, dir, "set", "reserved", "labels=human", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatal(so6)
	}
	_, se7, code := run(t, dir, "set", "reserved", "blocked-by=dep", "--expect", "none", "--as", "bob")
	if code != 4 {
		t.Fatalf("human must gate a blocked-by write: %d %s", code, se7)
	}
	doc := mustJSON(t, se7)
	want := "'reserved' has standing signal(s) that guard this write: human (labeled 'human')"
	if doc["message"] != want {
		t.Fatalf("exact message: got %q want %q", doc["message"], want)
	}

	so8, se8, code := run(t, dir, "set", "reserved", "blocked-by=dep", "--expect", "none", "--override", "-m", "reserved but linking a dep anyway", "--as", "bob")
	if code != 0 {
		t.Fatalf("override must land: %d %s", code, se8)
	}
	if mustJSON(t, so8)["id"] == nil {
		t.Fatal(so8)
	}
}

// TestSettledBlocksReResolutionAndChainShowsOverride: a terminal status
// signals for everyone, including the close's own author; re-resolving it
// lands only with --override, and the chain records override: settled.
func TestSettledBlocksReResolutionAndChainShowsOverride(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)
	so2, _, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "a")
	if code != 0 {
		t.Fatal(so2)
	}
	claimID := mustJSON(t, so2)["id"].(string)
	so3, _, code := run(t, dir, "set", "k1", "status=closed", "--evidence", "commit:abc", "--expect", claimID, "-m", "done", "--as", "a")
	if code != 0 {
		t.Fatal(so3)
	}
	closeID := mustJSON(t, so3)["id"].(string)

	_, se, code := run(t, dir, "set", "k1", "status=wontfix", "--expect", closeID, "-m", "actually a dup", "--as", "a")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	want := "'k1' has standing signal(s) that guard this write: settled (closed, evidence: yes)"
	if doc["message"] != want {
		t.Fatalf("exact message: got %q want %q", doc["message"], want)
	}

	so4, se4, code := run(t, dir, "set", "k1", "status=wontfix", "--expect", closeID,
		"--override", "-m", "dup of [[other-key]]", "--as", "a")
	if code != 0 {
		t.Fatalf("override must land: %d %s", code, se4)
	}
	newID := mustJSON(t, so4)["id"].(string)

	tailOut, tailErr, code := run(t, dir, "tail", "--raw", "--ledger", "issues")
	if code != 0 {
		t.Fatal(tailErr)
	}
	if !strings.Contains(tailOut, newID) {
		t.Fatalf("tail must contain the new event's id %q: %s", newID, tailOut)
	}
	if !strings.Contains(tailOut, `"override": "settled"`) {
		t.Fatalf("chain must show override: settled on the new event: %s", tailOut)
	}
}

// TestOverrideBlankMessageBadUsage: --override with an empty/whitespace -m
// is bad_usage, even on a write with no actual standing signal (own claim
// touch-base here, which is never a signal against its own author).
func TestOverrideBlankMessageBadUsage(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)

	_, se, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "--override", "-m", "   ", "--as", "a")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_usage" {
		t.Fatalf("%s", se)
	}
}

// TestOverrideWithNoStandingSignalIsLegalNoOp: --override with a valid
// message but no actual standing signal to override is legal — the write
// lands and the event records no override (nothing to name).
func TestOverrideWithNoStandingSignalIsLegalNoOp(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)

	// own claim: never a signal against its own author, so --override here
	// has nothing to override.
	so2, se2, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID,
		"--override", "-m", "claiming (override unnecessary here)", "--as", "a")
	if code != 0 {
		t.Fatalf("--override with no standing signal must still land: %d %s", code, se2)
	}
	newID := mustJSON(t, so2)["id"].(string)

	tailOut, tailErr, code := run(t, dir, "tail", "--raw", "--ledger", "issues")
	if code != 0 {
		t.Fatal(tailErr)
	}
	doc := mustJSON(t, tailOut)
	events := doc["events"].([]any)
	var found map[string]any
	for _, e := range events {
		ev := e.(map[string]any)
		if ev["id"] == newID {
			found = ev
		}
	}
	if found == nil {
		t.Fatalf("tail must contain the new event's id %q: %s", newID, tailOut)
	}
	if _, has := found["override"]; has {
		t.Fatalf("a legal no-op must record no override: %+v", found)
	}
}

// TestMultiSignalMessageAndRecording: multiple standing signals (claim and
// human) list together in the needs_override message and record together
// on the event as override: claim,human.
func TestMultiSignalMessageAndRecording(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)
	so2, _, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "alice")
	if code != 0 {
		t.Fatal(so2)
	}
	claimID := mustJSON(t, so2)["id"].(string)
	// human is applied after the claim lands (labels is unguarded, so this
	// alone never needs --override) — both claim and human must still stand
	// together against bob's close attempt below.
	_, _, code = run(t, dir, "set", "k1", "labels=human", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatal("labels=human should succeed")
	}

	_, se, code := run(t, dir, "set", "k1", "status=closed", "--evidence", "commit:abc", "--expect", claimID, "-m", "done", "--as", "bob")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	msg := doc["message"].(string)
	if !strings.Contains(msg, "claim (alice, ") || !strings.Contains(msg, "human (labeled 'human')") {
		t.Fatalf("multi-signal message must list both: %q", msg)
	}
	if strings.Index(msg, "claim (") > strings.Index(msg, "human (") {
		t.Fatalf("claim must precede human in fixed order: %q", msg)
	}

	so3, se3, code := run(t, dir, "set", "k1", "status=closed", "--evidence", "commit:abc", "--expect", claimID,
		"--override", "-m", "escalation handled", "--as", "bob")
	if code != 0 {
		t.Fatalf("override must land: %d %s", code, se3)
	}
	if mustJSON(t, so3)["id"] == nil {
		t.Fatal(so3)
	}

	tailOut, tailErr, code := run(t, dir, "tail", "--raw", "--ledger", "issues")
	if code != 0 {
		t.Fatal(tailErr)
	}
	if !strings.Contains(tailOut, `"override": "claim,human"`) {
		t.Fatalf("chain must record override: claim,human: %s", tailOut)
	}
}

// TestOverrideResetsAcrossLosingCASAttempt: setPrecondition's override
// bookkeeping must never survive a losing CAS attempt into the winning
// one's commit. *overrideOut points into the same *model.Event across
// every retry (AppendChecked never recreates it per attempt) — so if
// attempt 1 sees a standing claim signal and records it, but a competing
// write forces a retry and attempt 2's fresh read finds the claim gone
// stale (a tight --stale-after past a real forced retry, the exact
// scenario a slow backoff or a competing write can produce), the
// committed event must carry the attempt-2 truth (no override) — never a
// stale attribution left over from attempt 1's losing computation.
//
// This drives the real setPrecondition closure and the real
// store.AppendChecked through an actual two-attempt CAS retry, reusing
// store_test.go's TestAppendCheckedPreconditionRunsFreshPerAttempt
// technique: a sync.Once inside the wrapped precondition lands a
// competing write (forcing the ref to move out from under attempt 1's
// update-ref) and then sleeps past the stale horizon, so attempt 2's own
// fresh read legitimately finds zero standing signals.
func TestOverrideResetsAcrossLosingCASAttempt(t *testing.T) {
	dir := setupReadyStale(t, "300ms")
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)
	so2, _, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "alice")
	if code != 0 {
		t.Fatal(so2)
	}
	claimID := mustJSON(t, so2)["id"].(string)

	res, err := store.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Store
	ctx := &Ctx{Store: s}
	led, err := ctx.Load("issues")
	if err != nil {
		t.Fatal(err)
	}

	fields := map[string]string{"status": "in-progress"}
	ev := model.NewEvent("set", "bob", s.Repo)
	ev.Key, ev.Fields, ev.Text = "k1", fields, "reclaiming"

	// The real production closure: bob, --override with a real message,
	// against alice's live claim id.
	realPre := setPrecondition("k1", fields, "status", claimID, true, led.Meta, ev.Text, "bob", true, &ev.Override, &ev.ContestedResolved)

	var (
		once     sync.Once
		attempts int
	)
	pre := func(events []model.Event, d dag.Result) error {
		attempts++
		err := realPre(events, d)
		once.Do(func() {
			// A competing write on an unrelated field (never invalidates
			// status's CAS) forces attempt 1's update-ref to lose the race.
			competing := model.NewEvent("set", "carol", s.Repo)
			competing.Key, competing.Fields = "k1", map[string]string{"labels": "urgent"}
			if _, cerr := s.Append("issues", competing, nil, store.ExpectPresent); cerr != nil {
				t.Fatalf("competing append: %v", cerr)
			}
			// Deterministically push the claim past the 300ms stale
			// horizon before attempt 2's fresh read, rather than relying
			// on casLoop's own backoff timing.
			time.Sleep(400 * time.Millisecond)
		})
		return err
	}

	id, err := s.AppendChecked("issues", &ev, pre, store.ExpectPresent)
	if err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("pre ran %d time(s), want >=2 (the competing write must force a real retry)", attempts)
	}

	evs, _, err := s.Events("issues")
	if err != nil {
		t.Fatal(err)
	}
	var landed *model.Event
	for i := range evs {
		if evs[i].ID == id {
			landed = &evs[i]
		}
	}
	if landed == nil {
		t.Fatalf("committed event %q not found in chain", id)
	}
	if landed.Override != "" {
		t.Fatalf("attempt 2 found the claim stale (no standing signal) — the committed event must carry no override, got %q", landed.Override)
	}
}
