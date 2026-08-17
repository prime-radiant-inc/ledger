package cmd

import (
	"fmt"
	"strings"
	"testing"
)

// setupReady makes a ready-capable board (the spec's canonical shape:
// --guard status --guard blocked-by, status opted into terminal semantics).
func setupReady(t *testing.T) string {
	dir := initRepo(t)
	run(t, dir, "create", "issues", "--scope", "test",
		"--field", "status=open,in-progress,closed,wontfix",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by")
	return dir
}

// setupPlainGuarded makes a plain board (no --terminal, so ReadyCapable is
// false) that still guards two fields — the CAS rules apply, but none of
// the ready-capable extras (key grammar, blocked-by existence, ready hints).
func setupPlainGuarded(t *testing.T) string {
	dir := initRepo(t)
	run(t, dir, "create", "plain", "--scope", "test",
		"--field", "status=open,done,failed",
		"--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by")
	return dir
}

// TestRule1GuardedFieldRequiresExpect: a set touching a guarded field
// without --expect is bad_usage with the exact pinned message.
func TestRule1GuardedFieldRequiresExpect(t *testing.T) {
	dir := setupPlainGuarded(t)
	_, se, code := run(t, dir, "set", "t1", "status=open", "--as", "a")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_usage" {
		t.Fatalf("%s", se)
	}
	want := "'status' is guarded on 'plain': every write must carry --expect <event-id> or --expect none"
	if doc["message"] != want {
		t.Fatalf("exact message: got %q want %q", doc["message"], want)
	}
}

// TestRule2TwoGuardedFieldsBadUsage: touching two guarded fields in one set
// is bad_usage regardless of --expect.
func TestRule2TwoGuardedFieldsBadUsage(t *testing.T) {
	dir := setupReady(t)
	_, se, code := run(t, dir, "set", "k1", "status=open", "blocked-by=x", "--expect", "none", "--as", "a")
	if code != 4 || !strings.Contains(se, "bad_usage") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestRule2LabelRideAlongLegal: an unguarded field (labels) riding along
// with a guarded field (status) in one set is legal.
func TestRule2LabelRideAlongLegal(t *testing.T) {
	dir := setupReady(t)
	so, se, code := run(t, dir, "set", "k1", "status=open", "labels=urgent", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatalf("label ride-along must be legal: %s %s", so, se)
	}
}

// TestExpectOnUnguardedSingleFieldCASes: --expect on a write touching zero
// guarded fields is real CAS for any single-field write — never ignored.
func TestExpectOnUnguardedSingleFieldCASes(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")

	so, se, code := run(t, dir, "set", "k1", "labels=urgent", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatalf("first labels write with --expect none: %s %s", so, se)
	}
	id1 := mustJSON(t, so)["id"].(string)

	so2, se2, code := run(t, dir, "set", "k1", "labels=urgent,important", "--expect", id1, "--as", "a")
	if code != 0 {
		t.Fatalf("labels write CAS against its own latest id must succeed: %s %s", so2, se2)
	}

	// stale --expect (id1 no longer latest) must produce claim_lost, not be
	// silently ignored.
	_, se3, code3 := run(t, dir, "set", "k1", "labels=other", "--expect", id1, "--as", "a")
	if code3 != 4 || !strings.Contains(se3, "claim_lost") {
		t.Fatalf("stale expect on unguarded labels write must CAS, not be ignored: %d %s", code3, se3)
	}
}

// TestClaimLostMessageFormat is the exact-string pinned format: event
// <winner-id> by <winner-author> (<field>=<winner-value>) beat you to '<key>'.
func TestClaimLostMessageFormat(t *testing.T) {
	dir := setupReady(t)
	so, se, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "seeder")
	if code != 0 {
		t.Fatal(so, se)
	}
	seedID := mustJSON(t, so)["id"].(string)

	so2, se2, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "winner")
	if code != 0 {
		t.Fatal(so2, se2)
	}
	winID := mustJSON(t, so2)["id"].(string)

	_, se3, code3 := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "loser")
	if code3 != 4 || !strings.Contains(se3, "claim_lost") {
		t.Fatalf("%d %s", code3, se3)
	}
	want := fmt.Sprintf("event %s by winner (status=in-progress) beat you to 'k1'", winID)
	if !strings.Contains(se3, want) {
		t.Fatalf("exact message: got %q want to contain %q", se3, want)
	}
	if !strings.Contains(se3, "re-run ledger ready and pick again") {
		t.Fatalf("non-terminal attempted value must get the re-run-ready hint: %s", se3)
	}
}

// TestClaimLostTerminalValueHint: when the attempted (losing) write's value
// is terminal, the hint is the failed-close variant, never "pick again"
// (which would tell a failed closer to abandon finished work).
func TestClaimLostTerminalValueHint(t *testing.T) {
	dir := setupReady(t)
	so, _, _ := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	seedID := mustJSON(t, so)["id"].(string)

	so2, _, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "claimant")
	if code != 0 {
		t.Fatal(so2)
	}
	claimID := mustJSON(t, so2)["id"].(string)

	// claimant's claim is live (no --stale-after declared, so never stale) —
	// evicting it is rule 5's claim signal, so this squat-break needs
	// --override (the "Break a squat" idiom).
	so3, _, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", claimID,
		"--override", "-m", "reclaiming: breaking the squat", "--as", "reclaimer")
	if code != 0 {
		t.Fatal(so3)
	}
	reclaimID := mustJSON(t, so3)["id"].(string)

	_, se, code := run(t, dir, "set", "k1", "status=closed", "--evidence", "commit:x", "--expect", claimID, "-m", "done", "--as", "claimant")
	if code != 4 || !strings.Contains(se, "claim_lost") {
		t.Fatalf("%d %s", code, se)
	}
	want := fmt.Sprintf("event %s by reclaimer (status=in-progress) beat you to 'k1'", reclaimID)
	if !strings.Contains(se, want) {
		t.Fatalf("exact message: got %q want to contain %q", se, want)
	}
	if !strings.Contains(se, "you were reclaimed while working — leave a handoff note; never re-close blind") {
		t.Fatalf("terminal-attempted-value hint: %s", se)
	}
}

// TestClaimLostBlockedByMismatchHint: a blocked-by CAS mismatch gets the
// edges-specific hint.
func TestClaimLostBlockedByMismatchHint(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "dep", "status=open", "--expect", "none", "-m", "dep", "--as", "a")
	run(t, dir, "set", "dep2", "status=open", "--expect", "none", "-m", "dep2", "--as", "a")

	so, se, code := run(t, dir, "set", "k1", "blocked-by=dep", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatal(so, se)
	}
	edgeID := mustJSON(t, so)["id"].(string)

	so2, se2, code := run(t, dir, "set", "k1", "blocked-by=dep,dep2", "--expect", edgeID, "--as", "winner")
	if code != 0 {
		t.Fatal(so2, se2)
	}
	winID := mustJSON(t, so2)["id"].(string)

	_, se3, code3 := run(t, dir, "set", "k1", "blocked-by=dep2", "--expect", edgeID, "--as", "loser")
	if code3 != 4 || !strings.Contains(se3, "claim_lost") {
		t.Fatalf("%d %s", code3, se3)
	}
	want := fmt.Sprintf("event %s by winner (blocked-by=dep,dep2) beat you to 'k1'", winID)
	if !strings.Contains(se3, want) {
		t.Fatalf("exact message: got %q want to contain %q", se3, want)
	}
	if !strings.Contains(se3, "re-read the key's edges and merge") {
		t.Fatalf("blocked-by mismatch hint: %s", se3)
	}
}

// TestClaimLostGenericHintOnPlainBoard: a plain board must never receive
// ready-capable advice, even on fields named "status"/"blocked-by" — every
// guarded field on a plain board gets the generic fallback hint.
func TestClaimLostGenericHintOnPlainBoard(t *testing.T) {
	dir := setupPlainGuarded(t)
	so, _, code := run(t, dir, "set", "t1", "status=open", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	id1 := mustJSON(t, so)["id"].(string)
	run(t, dir, "set", "t1", "status=done", "--expect", id1, "--as", "b")
	_, se, code := run(t, dir, "set", "t1", "status=failed", "--expect", id1, "--as", "c")
	if code != 4 || !strings.Contains(se, "claim_lost") {
		t.Fatalf("%d %s", code, se)
	}
	if !strings.Contains(se, "re-read 'status' and try again") {
		t.Fatalf("plain board must get generic hint, not ready-capable advice: %s", se)
	}
	if strings.Contains(se, "re-run ledger ready") {
		t.Fatalf("plain board must never see ready-capable hints: %s", se)
	}

	// blocked-by on a plain board is just another guarded multi-field: no
	// existence check, generic hint on mismatch, edge tokens unconstrained.
	so2, _, code := run(t, dir, "set", "t2", "blocked-by=nonexistent-token-is-fine-here", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatalf("plain board must skip blocked-by existence check: %s", so2)
	}
	id2 := mustJSON(t, so2)["id"].(string)
	run(t, dir, "set", "t2", "blocked-by=y", "--expect", id2, "--as", "b")
	_, se2, code2 := run(t, dir, "set", "t2", "blocked-by=z", "--expect", id2, "--as", "c")
	if code2 != 4 || !strings.Contains(se2, "re-read 'blocked-by' and try again") {
		t.Fatalf("plain board blocked-by must get generic hint too: %s", se2)
	}
}

// TestExpectNoneStatusCollisionHint: rule 4, first-write collision on the
// status field.
func TestExpectNoneStatusCollisionHint(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "first", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	winID := mustJSON(t, so)["id"].(string)

	_, se, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "collide", "--as", "b")
	if code != 4 || !strings.Contains(se, "claim_lost") {
		t.Fatalf("%d %s", code, se)
	}
	want := fmt.Sprintf("event %s by a (status=open) beat you to 'k1'", winID)
	if !strings.Contains(se, want) {
		t.Fatalf("exact message: got %q want to contain %q", se, want)
	}
	if !strings.Contains(se, "this key already exists — read it; if yours is a different issue, re-seed under a new key") {
		t.Fatalf("none-collision hint: %s", se)
	}
}

// TestExpectNoneBlockedByCollisionHint: rule 4, first-write collision on
// blocked-by gets the edges-specific collision hint, never a merge suggestion.
func TestExpectNoneBlockedByCollisionHint(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "dep", "status=open", "--expect", "none", "-m", "dep", "--as", "a")
	so, _, code := run(t, dir, "set", "k1", "blocked-by=dep", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	_, se, code := run(t, dir, "set", "k1", "blocked-by=dep", "--expect", "none", "--as", "b")
	if code != 4 || !strings.Contains(se, "claim_lost") {
		t.Fatalf("%d %s", code, se)
	}
	if !strings.Contains(se, "this key already has edges — read it; if yours is a different issue, re-seed under a new key") {
		t.Fatalf("blocked-by none-collision hint: %s", se)
	}
	if strings.Contains(se, "merge") {
		t.Fatalf("a name collision must never suggest a merge (that's the mismatch hint, not the collision hint): %s", se)
	}
}

// TestExpectOnNeverWrittenFieldNamesNoPhantomWinner: --expect <id> on a
// field with no prior event at all (a stale or guessed id pasted on a
// first-ever write) must never fabricate a winner's id/author/value — it
// gets its own claim_lost message naming the actual state and the fix.
func TestExpectOnNeverWrittenFieldNamesNoPhantomWinner(t *testing.T) {
	dir := setupReady(t)
	_, se, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", "deadbeef00", "-m", "claiming", "--as", "a")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "claim_lost" {
		t.Fatalf("%s", se)
	}
	wantMsg := "'status' has no prior event on 'k1' — nothing matches --expect deadbeef00"
	if doc["message"] != wantMsg {
		t.Fatalf("exact message: got %q want %q", doc["message"], wantMsg)
	}
	wantHint := "a first write takes --expect none"
	if doc["hint"] != wantHint {
		t.Fatalf("exact hint: got %q want %q", doc["hint"], wantHint)
	}
}

// TestNotesNeverInvalidateExpect: writing a note between reading an id and
// using it as --expect must never trip claim_lost — notes carry no Fields,
// so they never appear in board.Build's fold.
func TestNotesNeverInvalidateExpect(t *testing.T) {
	dir := setupReady(t)
	so, _, _ := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	id1 := mustJSON(t, so)["id"].(string)

	_, se, code := run(t, dir, "note", "-k", "comment", "--key", "k1", "-m", "just a note", "--as", "b")
	if code != 0 {
		t.Fatalf("note should succeed: %s", se)
	}

	so2, se2, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", id1, "-m", "claiming", "--as", "b")
	if code != 0 {
		t.Fatalf("a note between read and set must never invalidate --expect: %s %s", so2, se2)
	}
}

// TestFieldScopedAcrossOtherFields: an unrelated field's write between read
// and set must never invalidate a field-scoped --expect (spec rule 3's
// "field-scoped" clause, non-race half — the concurrent version is a Task
// 12 harness).
func TestFieldScopedAcrossOtherFields(t *testing.T) {
	dir := setupReady(t)
	so, _, _ := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	id1 := mustJSON(t, so)["id"].(string)

	_, se, code := run(t, dir, "set", "k1", "labels=urgent", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatalf("labels write should succeed: %s", se)
	}

	so2, se2, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", id1, "-m", "claiming", "--as", "a")
	if code != 0 {
		t.Fatalf("other fields' events must never invalidate status's --expect: %s %s", so2, se2)
	}
}

// TestMultiFieldTokenGrammarRejectsBadToken: a malformed comma token in a
// multi-field value is bad_value, naming the token.
func TestMultiFieldTokenGrammarRejectsBadToken(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	_, se, code := run(t, dir, "set", "k1", "labels=Urgent", "--as", "a")
	if code != 4 || !strings.Contains(se, "bad_value") || !strings.Contains(se, "Urgent") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestMultiFieldClearWithEmptyValue: "field=" clears a multi-field.
func TestMultiFieldClearWithEmptyValue(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	so, _, code := run(t, dir, "set", "k1", "labels=urgent", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	id := mustJSON(t, so)["id"].(string)

	so2, se2, code := run(t, dir, "set", "k1", "labels=", "--expect", id, "--as", "a")
	if code != 0 {
		t.Fatalf("labels= must clear: %s %s", so2, se2)
	}
	f := mustJSON(t, so2)["fields"].(map[string]any)
	if f["labels"] != "" {
		t.Fatalf("cleared value must round-trip empty: %v", f)
	}
}

// TestKeyGrammarRejectsNonKebabFirstWrite: ready-capable boards enforce the
// multi-field token grammar on key names at the key's first write.
func TestKeyGrammarRejectsNonKebabFirstWrite(t *testing.T) {
	dir := setupReady(t)
	_, se, code := run(t, dir, "set", "Bad_Key", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("%d %s", code, se)
	}
	want := "key 'Bad_Key' can't be referenced by blocked-by edges; use kebab-case"
	if !strings.Contains(se, want) {
		t.Fatalf("exact message: got %q want to contain %q", se, want)
	}
}

// TestKeyGrammarOnlyEnforcedAtFirstWrite: a validly-named key's later
// writes are unaffected by the grammar check (it only gates the first).
func TestKeyGrammarOnlyEnforcedAtFirstWrite(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	id := mustJSON(t, so)["id"].(string)
	so2, se2, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", id, "-m", "claiming", "--as", "a")
	if code != 0 {
		t.Fatal(so2, se2)
	}
}

// TestBlockedByUnknownKeyRejected: ready-capable boards validate blocked-by
// tokens as existing keys.
func TestBlockedByUnknownKeyRejected(t *testing.T) {
	dir := setupReady(t)
	_, se, code := run(t, dir, "set", "k1", "blocked-by=ghost", "--expect", "none", "--as", "a")
	if code != 4 || !strings.Contains(se, "unknown_key") || !strings.Contains(se, "ghost") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestBlockedByExistingKeySucceeds: an existing key (≥1 event) satisfies
// the existence check.
func TestBlockedByExistingKeySucceeds(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "dep", "status=open", "--expect", "none", "-m", "dep", "--as", "a")
	so, se, code := run(t, dir, "set", "k1", "blocked-by=dep", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatal(so, se)
	}
}

// TestPlainBoardSkipsKeyGrammarCheck: plain boards get none of the
// ready-capable behavior — no key grammar enforcement.
func TestPlainBoardSkipsKeyGrammarCheck(t *testing.T) {
	dir := setupPlainGuarded(t)
	so, se, code := run(t, dir, "set", "Not_Kebab", "status=open", "--expect", "none", "--as", "a")
	if code != 0 {
		t.Fatalf("plain boards must skip key grammar: %s %s", so, se)
	}
}

// TestUnknownFieldStillRejectsUndeclared: extending the declared-check to
// multi-fields must not open the door to arbitrary field names.
func TestUnknownFieldStillRejectsUndeclared(t *testing.T) {
	dir := setupReady(t)
	_, se, code := run(t, dir, "set", "k1", "severity=high", "--as", "a")
	if code != 4 || !strings.Contains(se, "unknown_field") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestExpectEmptyRejectedAsBadUsage: --expect "" (e.g. an unset shell
// variable interpolated into the flag) must never be treated as an
// unconditional write — strings.HasPrefix(id, "") is vacuously true for
// every id, so an empty --expect would otherwise silently bypass CAS
// entirely. It must be rejected as bad_usage, and the write must never
// land.
func TestExpectEmptyRejectedAsBadUsage(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)

	_, se, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", "", "-m", "x", "--as", "a")
	if code != 4 {
		t.Fatalf("empty --expect must be rejected, not silently succeed: %d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_usage" {
		t.Fatalf("%s", se)
	}
	want := "--expect requires an event id or the literal 'none' (got empty — an unset shell variable?)"
	if doc["message"] != want {
		t.Fatalf("exact message: got %q want %q", doc["message"], want)
	}

	// Nothing was written: seedID must still be the correct CAS target — if
	// the rejected write had landed anyway, this would lose the race with a
	// stale-expect claim_lost instead of succeeding.
	so2, se2, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "a")
	if code != 0 {
		t.Fatalf("rejected empty --expect must not have written anything: %s %s", so2, se2)
	}
}

// TestExpectShortPrefixRejectedAsBadUsage: --expect below git's own
// abbreviation floor (4 hex characters) is rejected as bad_usage rather
// than accepted as an unusably ambiguous CAS target.
func TestExpectShortPrefixRejectedAsBadUsage(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")

	_, se, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", "abc", "-m", "x", "--as", "a")
	if code != 4 {
		t.Fatalf("short --expect must be rejected: %d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_usage" {
		t.Fatalf("%s", se)
	}
}

// TestClaimLostNeverRelabeledByAuthorSubstring: mapStoreErr used to
// classify store errors by substring-matching err.Error() for
// "slug_exists"/"unknown_ledger". claim_lost's message embeds the winning
// author's name verbatim, so an author literally named "unknown_ledger"
// would get any later claim_lost against that write mis-relabeled as
// unknown_ledger. Classification must be immune to caller-controlled text.
func TestClaimLostNeverRelabeledByAuthorSubstring(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)

	so2, se2, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "unknown_ledger")
	if code != 0 {
		t.Fatal(so2, se2)
	}

	_, se, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "loser")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "claim_lost" {
		t.Fatalf("author 'unknown_ledger' embedded in the claim_lost message must never re-label the error: got %q, full: %s", doc["error"], se)
	}
}

// TestOverrideFlagRequiresMessage: --override's message discipline (spec
// rule 5) is unconditional, independent of board capability — even on a
// plain board, where rule 5 itself never applies, an empty -m is still
// bad_usage; a non-empty one parses cleanly and lands as a legal no-op
// (a plain board's guarded write never carries a standing signal to
// override).
func TestOverrideFlagRequiresMessage(t *testing.T) {
	dir := setupPlainGuarded(t)
	_, se, code := run(t, dir, "set", "t1", "status=open", "--expect", "none", "--override", "--as", "a")
	if code != 4 || !strings.Contains(se, "bad_usage") {
		t.Fatalf("--override without -m must be bad_usage: %d %s", code, se)
	}

	so, se2, code := run(t, dir, "set", "t1", "status=open", "--expect", "none", "--override", "-m", "reserved", "--as", "a")
	if code != 0 {
		t.Fatalf("--override with a message must parse cleanly on a plain board: %s %s", so, se2)
	}
}
