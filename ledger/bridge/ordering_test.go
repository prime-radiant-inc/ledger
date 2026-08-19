package main

import (
	"strings"
	"testing"
)

// Test-plan item 4 — ORDERING. Every case here is a regression from a probe
// that falsified an earlier reading of Law 1 or Law 2.

// TestSmokeFirstRunMirrorsAndConverges is the baseline the rest builds on: a
// seeded board reaches GitHub, and the run after it is a clean 0/0 whose
// reported cursor is the PERSISTED one.
func TestSmokeFirstRunMirrorsAndConverges(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")

	r := f.syncOK("operator")
	if f.countIssues() != 1 {
		t.Fatalf("want one issue, got %d: %s", f.countIssues(), mustJSON(t, r))
	}
	if !hasAction(r, "created for cache-warm") {
		t.Fatalf("no create action: %s", mustJSON(t, r))
	}
	if got := f.issueTitle(1); got != "warm the cache on boot" {
		t.Fatalf("issue title %q", got)
	}
	if r.Cursor == "" {
		t.Fatalf("a run that wrote must persist a cursor: %s", mustJSON(t, r))
	}

	second := f.syncOK("operator")
	if second.GHMutations != 0 || second.BoardWrites != 0 {
		t.Fatalf("second run must be 0/0: %s", mustJSON(t, second))
	}
	if second.Cursor != r.Cursor {
		t.Fatalf("a no-op run reports the PERSISTED cursor: was %q, got %q", r.Cursor, second.Cursor)
	}
	if f.countIssues() != 1 {
		t.Fatalf("second run minted an issue: %d", f.countIssues())
	}
}

// TestCloseMirrorsOnceWithNoBoardWrites is the round-1 falsification kept as
// a regression: a board-side close, ONE run, ONE GitHub close, ZERO board
// writes beyond bookkeeping, and no fabricated attribution. Intake-first
// ordering reverted the close and attributed the reversion to a person.
func TestCloseMirrorsOnceWithNoBoardWrites(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "closed", "fixed in a1b2c3", "jesse", "commit:a1b2c3")

	r := f.syncOK("operator")
	state, reason := f.issueState(1)
	if state != "CLOSED" || reason != "COMPLETED" {
		t.Fatalf("issue must be closed completed, got %s/%s: %s", state, reason, mustJSON(t, r))
	}
	if f.status("cache-warm") != "closed" {
		t.Fatalf("the board must still say closed, got %q", f.status("cache-warm"))
	}
	if ov := f.fabricatedOverrides(); len(ov) != 0 {
		t.Fatalf("no override may be fabricated, got %v", ov)
	}
	// Law 6: the close mirrors as a marked comment carrying the message and
	// the evidence, THEN the close.
	bodies := f.commentBodies(1)
	if len(bodies) != 1 {
		t.Fatalf("want exactly one comment, got %d: %v", len(bodies), bodies)
	}
	if !strings.Contains(bodies[0], "fixed in a1b2c3") || !strings.Contains(bodies[0], "commit:a1b2c3") {
		t.Fatalf("the close comment must carry the message and the evidence: %q", bodies[0])
	}
	if !strings.HasPrefix(bodies[0], "**jesse** (via ledger, ") {
		t.Fatalf("every bridge comment opens with the marker: %q", bodies[0])
	}
	f.converge("operator", 3)
	if len(f.commentBodies(1)) != 1 {
		t.Fatalf("converging re-posted the close comment: %v", f.commentBodies(1))
	}
}

// TestOrdinaryCloseFiresNoDivergenceWarning pins the honest comparison. The
// divergence test asks whether the remote differs from what the bridge LAST
// PUT THERE (MIRROREDVIEW), not from the outgoing value — the latter flags
// every ordinary close as a discarded human edit.
func TestOrdinaryCloseFiresNoDivergenceWarning(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")

	r := f.syncOK("operator")
	if r.Divergences != 0 {
		t.Fatalf("an ordinary close is not a divergence: %s", mustJSON(t, r))
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "discarded") {
			t.Fatalf("no discarded-edit accusation on an ordinary close: %q", w)
		}
	}
	if n := len(f.notes(kindHand)); n != 0 {
		t.Fatalf("an ordinary close wrote %d handoff notes", n)
	}
}

// TestDivergenceWarningFiresAgainstTheLastMirroredValue is the other half:
// a person really did act on GitHub, and their action is about to be
// overwritten. That must be warned and noted — once.
func TestDivergenceWarningFiresAgainstTheLastMirroredValue(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	// A person closes it as not planned on GitHub; the board simultaneously
	// closes it as done. The board wins this run, and the person is told.
	f.humanClose(1, true, "mallory")
	f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")

	r := f.syncOK("operator")
	if r.Divergences != 1 {
		t.Fatalf("want one divergence, got %d: %s", r.Divergences, mustJSON(t, r))
	}
	if !hasWarning(r, "was discarded") {
		t.Fatalf("the discarded GitHub edit must be warned: %s", mustJSON(t, r))
	}
	notes := f.notes(kindHand)
	if len(notes) != 1 {
		t.Fatalf("want one suppression note, got %d: %+v", len(notes), notes)
	}
	// The board's value has now been pushed over the discarded one, so the
	// divergence is RESOLVED: the record is pruned, the count drops to zero,
	// and the note is never rewritten. Pruning is itself a state change —
	// Law 1's fourth persistence disjunct — or the pruned record never lands
	// and the divergence's next real recurrence is silently swallowed.
	second := f.syncOK("operator")
	if len(f.notes(kindHand)) != 1 {
		t.Fatalf("the suppression note was rewritten: %+v", f.notes(kindHand))
	}
	if second.Divergences != 0 {
		t.Fatalf("the divergence resolved and must stop counting: %s", mustJSON(t, second))
	}
	if second.BoardWrites != 1 || !hasAction(second, "bridge-state") {
		t.Fatalf("pruning a record must persist state: %s", mustJSON(t, second))
	}
	f.converge("operator", 3)
	if len(f.notes(kindHand)) != 1 {
		t.Fatalf("converging rewrote the suppression note: %+v", f.notes(kindHand))
	}
}

// TestMirrorsToNothingDoesNotSuppressIntakeForever is the
// permanent-suppression falsification, pinned. A claim (open -> in-progress)
// mirrors to NOTHING, so without Law 1's third persistence disjunct the
// cursor never advances, the un-mirrored event sits in the drain forever,
// and the key's status aspect is off-limits to intake permanently: a claimed
// key that stops accepting GitHub closes.
func TestMirrorsToNothingDoesNotSuppressIntakeForever(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "in-progress", "picking this up", "ash")

	r := f.syncOK("operator")
	if r.GHMutations != 0 {
		t.Fatalf("a non-terminal transition mirrors to nothing: %s", mustJSON(t, r))
	}
	if state, _ := f.issueState(1); state != "OPEN" {
		t.Fatalf("the issue must still be open, got %s", state)
	}
	if len(f.commentBodies(1)) != 0 {
		t.Fatalf("a claim message must never reach GitHub: %v", f.commentBodies(1))
	}
	// The cursor MUST have advanced anyway, or the next run repeats this.
	if r.Cursor == "" {
		t.Fatalf("the drain carried a mirrorable event, so state must persist: %s", mustJSON(t, r))
	}
	// Now a human closes it on GitHub. The next run must accept it.
	f.humanClose(1, false, "mallory")
	second := f.syncOK("operator")
	if f.status("cache-warm") != "closed" {
		t.Fatalf("the GitHub close was permanently suppressed: status=%q report=%s",
			f.status("cache-warm"), mustJSON(t, second))
	}
}

// TestPushHoleRegression: a mirror-only run on replica A must publish its
// bookkeeping, or replica B mints a duplicate issue for the same key. `push`
// is always, selective, LAST — link notes and bridge state that never leave
// the replica make the sync-first law protect nothing.
func TestPushHoleRegression(t *testing.T) {
	a := newIssueFixture(t)
	remote := t.TempDir()
	a.git("init", "-q", "--bare", remote)
	a.git("remote", "add", "origin", remote)
	a.seed("cache-warm", "warm the cache on boot", "jesse")
	a.syncOK("operator")
	if a.countIssues() != 1 {
		t.Fatalf("setup: want one issue, got %d", a.countIssues())
	}

	// Replica B clones the same board and bridges the same repo.
	b := &fixture{t: t, dir: t.TempDir(), ghState: a.ghState, slug: a.slug, repo: a.repo,
		done: a.done, notPlanned: a.notPlanned, listLimit: defaultListLimit}
	b.git("init", "-q", ".")
	b.git("config", "user.email", "b@example.com")
	b.git("config", "user.name", "b")
	b.git("remote", "add", "origin", remote)
	b.ledgerOK("init")
	if _, err := b.board().runBare("sync"); err != nil {
		t.Fatalf("replica b sync: %v", err)
	}
	b.syncOK("operator")
	if got := b.countIssues(); got != 1 {
		t.Fatalf("replica b minted a duplicate issue: %d issues", got)
	}
}

// TestReclassificationReachesGitHubAndIsNeverReverted is round 4's Critical,
// probed three times over. Under the open/closed-BIT reading a
// done<->not-planned reclassification never reached GitHub, and intake then
// reverted the maintainer's reclassification with a fabricated
// `override: settled` and fabricated `gh:` evidence — once per human
// attempt, unbounded.
func TestReclassificationReachesGitHubAndIsNeverReverted(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")
	f.syncOK("operator")
	if state, reason := f.issueState(1); state != "CLOSED" || reason != "COMPLETED" {
		t.Fatalf("setup: %s/%s", state, reason)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		// The maintainer reclassifies on the board: done -> not planned. A
		// settled key needs their own --override; that one is a person's.
		f.setStatusOverride("cache-warm", "wontfix", "not worth it after all", "jesse")
		r := f.syncOK("operator")
		state, reason := f.issueState(1)
		if state != "CLOSED" || reason != "NOT_PLANNED" {
			t.Fatalf("attempt %d: the reclassification must reach GitHub, got %s/%s: %s",
				attempt, state, reason, mustJSON(t, r))
		}
		if f.status("cache-warm") != "wontfix" {
			t.Fatalf("attempt %d: the board was reverted to %q", attempt, f.status("cache-warm"))
		}
		if ov := f.fabricatedOverrides(); len(ov) != 0 {
			t.Fatalf("attempt %d: fabricated override(s) %v", attempt, ov)
		}
		f.converge("operator", 3)

		// And back the other way.
		f.setStatusOverride("cache-warm", "closed", "actually shipped", "jesse", "commit:d4e5f6")
		f.syncOK("operator")
		if state, reason := f.issueState(1); state != "CLOSED" || reason != "COMPLETED" {
			t.Fatalf("attempt %d: the reverse reclassification must reach GitHub, got %s/%s", attempt, state, reason)
		}
		if ov := f.fabricatedOverrides(); len(ov) != 0 {
			t.Fatalf("attempt %d: fabricated override(s) after reverse %v", attempt, ov)
		}
		f.converge("operator", 3)
	}
}

// TestOpenIssueOverClaimedKeyWritesNothing is the reopen-trigger Critical.
// The literal both-directions convergence reading un-claimed every claimed
// key with a fabricated auto-override PER RUN. An open issue over a
// non-terminal board status is the resting state of every live key, not a
// reopen.
func TestOpenIssueOverClaimedKeyWritesNothing(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "in-progress", "picking this up", "ash")
	f.converge("operator", 3)

	before := f.eventCount()
	for run := 0; run < 3; run++ {
		r := f.syncOK("operator")
		if r.BoardWrites != 0 || r.GHMutations != 0 {
			t.Fatalf("run %d wrote something for a resting open key: %s", run, mustJSON(t, r))
		}
	}
	if f.status("cache-warm") != "in-progress" {
		t.Fatalf("the claim was reverted to %q", f.status("cache-warm"))
	}
	if ov := f.fabricatedOverrides(); len(ov) != 0 {
		t.Fatalf("fabricated override(s) %v", ov)
	}
	if got := f.eventCount(); got != before {
		t.Fatalf("the chain grew by %d events across three no-op runs", got-before)
	}
}

// TestClosedIssueOverActiveBoardReopensWithFixedText: the other convergence
// direction. The mirror reopens with a FIXED text naming the divergence,
// never a board message — the only messages a non-terminal level carries are
// claim and touch-base messages, and those never reach GitHub.
func TestClosedIssueOverActiveBoardReopensWithFixedText(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")
	f.converge("operator", 3)
	if state, _ := f.issueState(1); state != "CLOSED" {
		t.Fatalf("setup: issue is %s", state)
	}

	// The board reopens it, and the reopening writer claims it in the same
	// breath — the message that must NOT be published.
	f.setStatusOverride("cache-warm", "open", "the fix regressed on staging", "jesse")
	f.setStatus("cache-warm", "in-progress", "I am on it — claiming", "ash")

	r := f.syncOK("operator")
	if state, _ := f.issueState(1); state != "OPEN" {
		t.Fatalf("the issue must be reopened, got %s: %s", state, mustJSON(t, r))
	}
	bodies := f.commentBodies(1)
	last := bodies[len(bodies)-1]
	if !strings.Contains(last, reopenText) {
		t.Fatalf("the reopen comment must be the FIXED text, got %q", last)
	}
	for _, b := range bodies {
		if strings.Contains(b, "claiming") {
			t.Fatalf("a claim message reached GitHub: %q", b)
		}
	}
	f.converge("operator", 3)
}
