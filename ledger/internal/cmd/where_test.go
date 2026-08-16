package cmd

import (
	"strings"
	"testing"
)

// seedWhere builds a ready-capable board (setupReady's shape) with two keys
// whose status/labels/blocked-by diverge enough to exercise exact match,
// membership, AND composition, and the no-value-doesn't-match case:
//   - k1: status=in-progress, labels=urgent,bug
//   - k2: status=in-progress, labels=bug          (no "urgent")
//   - k3: status=open,        labels=(none)        (never touches labels)
func seedWhere(t *testing.T) string {
	dir := setupReady(t)
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "k1 title", "--as", "a")
	so, _, _ := run(t, dir, "set", "k1", "status=in-progress", "--expect", mustEventID(t, dir, "k1"), "-m", "claim", "--as", "a")
	_ = so
	run(t, dir, "set", "k1", "labels=urgent,bug", "--as", "a")

	run(t, dir, "set", "k2", "status=open", "--expect", "none", "-m", "k2 title", "--as", "a")
	run(t, dir, "set", "k2", "status=in-progress", "--expect", mustEventID(t, dir, "k2"), "-m", "claim", "--as", "a")
	run(t, dir, "set", "k2", "labels=bug", "--as", "a")

	run(t, dir, "set", "k3", "status=open", "--expect", "none", "-m", "k3 title", "--as", "a")
	return dir
}

// mustEventID fetches the current status event id for key on dir — the
// --expect target for the claim write that follows the seed write.
func mustEventID(t *testing.T, dir, key string) string {
	t.Helper()
	so, _, code := run(t, dir, "status", key, "--field", "status")
	if code != 0 {
		t.Fatalf("status %s: %s", key, so)
	}
	doc := mustJSON(t, so)
	values := doc["values"].(map[string]any)
	row := values["status"].(map[string]any)
	return row["id"].(string)
}

func showKeys(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	so, se, code := run(t, dir, append([]string{"show"}, args...)...)
	if code != 0 {
		t.Fatalf("show %v: code=%d stderr=%s", args, code, se)
	}
	doc := mustJSON(t, so)
	rows := doc["rows"].([]any)
	seen := map[string]bool{}
	var keys []string
	for _, r := range rows {
		k := r.(map[string]any)["key"].(string)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

// TestWhereExactOnEnum: --where status=in-progress keeps only keys whose
// current status matches exactly.
func TestWhereExactOnEnum(t *testing.T) {
	dir := seedWhere(t)
	keys := showKeys(t, dir, "--where", "status=in-progress")
	if len(keys) != 2 || !contains2(keys, "k1") || !contains2(keys, "k2") {
		t.Fatalf("expected k1,k2: %v", keys)
	}
}

// TestWhereMembershipOnMulti: --where labels~=urgent keeps only keys whose
// labels set contains the token — k2 has "bug" but not "urgent", so it's
// excluded; k3 never touched labels, so it doesn't match either (no error).
func TestWhereMembershipOnMulti(t *testing.T) {
	dir := seedWhere(t)
	keys := showKeys(t, dir, "--where", "labels~=urgent")
	if len(keys) != 1 || keys[0] != "k1" {
		t.Fatalf("expected only k1: %v", keys)
	}
}

// TestWhereANDComposition: two clauses both must hold.
func TestWhereANDComposition(t *testing.T) {
	dir := seedWhere(t)
	keys := showKeys(t, dir, "--where", "status=in-progress", "--where", "labels~=bug")
	if len(keys) != 2 || !contains2(keys, "k1") || !contains2(keys, "k2") {
		t.Fatalf("both k1,k2 carry status=in-progress AND labels~=bug: %v", keys)
	}
	keys2 := showKeys(t, dir, "--where", "status=in-progress", "--where", "labels~=urgent")
	if len(keys2) != 1 || keys2[0] != "k1" {
		t.Fatalf("only k1 satisfies both clauses: %v", keys2)
	}
}

// TestWhereNoValueDoesntMatchNoError: k3 never wrote labels — filtering on
// labels~=anything excludes it silently, no error.
func TestWhereNoValueDoesntMatchNoError(t *testing.T) {
	dir := seedWhere(t)
	keys := showKeys(t, dir, "--where", "labels~=bug")
	if contains2(keys, "k3") {
		t.Fatalf("k3 has no labels value and must not match: %v", keys)
	}
}

// TestWhereExactOnMultiFieldBadUsage: "=" on a multi-field is bad_usage —
// sets have no exact-string identity.
func TestWhereExactOnMultiFieldBadUsage(t *testing.T) {
	dir := seedWhere(t)
	_, se, code := run(t, dir, "show", "--where", "labels=urgent")
	if code != 4 || !strings.Contains(se, "bad_usage") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestWhereMembershipOnEnumBadUsage: "~=" on an enum field is bad_usage.
func TestWhereMembershipOnEnumBadUsage(t *testing.T) {
	dir := seedWhere(t)
	_, se, code := run(t, dir, "show", "--where", "status~=open")
	if code != 4 || !strings.Contains(se, "bad_usage") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestWhereDoubleExactSameFieldBadUsage: two "=" clauses on one field can
// never both hold.
func TestWhereDoubleExactSameFieldBadUsage(t *testing.T) {
	dir := seedWhere(t)
	_, se, code := run(t, dir, "show", "--where", "status=open", "--where", "status=closed")
	if code != 4 || !strings.Contains(se, "bad_usage") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestWhereUndeclaredFieldUnknownField: a field never declared on the
// ledger is unknown_field, hint lists the declared fields.
func TestWhereUndeclaredFieldUnknownField(t *testing.T) {
	dir := seedWhere(t)
	_, se, code := run(t, dir, "show", "--where", "priority=high")
	if code != 4 || !strings.Contains(se, "unknown_field") {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	hint := doc["hint"].(string)
	if !strings.Contains(hint, "status") || !strings.Contains(hint, "labels") || !strings.Contains(hint, "blocked-by") {
		t.Fatalf("hint must list declared fields: %q", hint)
	}
}

// TestShowBareUnchangedByWhere: bare `show` (no --where) is unaffected by
// this feature — every key still appears.
func TestShowBareUnchangedByWhere(t *testing.T) {
	dir := seedWhere(t)
	keys := showKeys(t, dir)
	if len(keys) != 3 || !contains2(keys, "k1") || !contains2(keys, "k2") || !contains2(keys, "k3") {
		t.Fatalf("bare show must list every key: %v", keys)
	}
}

// seedWhereGenericEnum builds a ready-capable board that also declares a
// plain enum field ("review") the board doesn't dedicate a struct field to
// (unlike status) — the case the controller's ruling closes: a legal
// --where clause on any declared enum field must actually filter, not
// silently match nothing.
//   - k1: status=open, review=approved
//   - k2: status=open, review=pending
func seedWhereGenericEnum(t *testing.T) string {
	dir := initRepo(t)
	run(t, dir, "create", "issues", "--scope", "test",
		"--field", "status=open,in-progress,closed,wontfix",
		"--field", "review=pending,approved",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by")
	run(t, dir, "set", "k1", "status=open", "review=approved", "--expect", "none", "-m", "k1 title", "--as", "a")
	run(t, dir, "set", "k2", "status=open", "review=pending", "--expect", "none", "-m", "k2 title", "--as", "a")
	return dir
}

// TestWhereGenericEnumFieldFilters: --where on a declared enum field other
// than status/labels/blocked-by must filter for real — both the positive
// match and the negative exclusion.
func TestWhereGenericEnumFieldFilters(t *testing.T) {
	dir := seedWhereGenericEnum(t)
	keys := showKeys(t, dir, "--where", "review=approved")
	if len(keys) != 1 || keys[0] != "k1" {
		t.Fatalf("expected only k1 to match review=approved: %v", keys)
	}
	keys2 := showKeys(t, dir, "--where", "review=pending")
	if len(keys2) != 1 || keys2[0] != "k2" {
		t.Fatalf("expected only k2 to match review=pending: %v", keys2)
	}
}
