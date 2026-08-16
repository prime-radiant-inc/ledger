package cmd

import (
	"strings"
	"testing"

	"ledger/internal/gitx"
	"ledger/internal/store"
)

// TestCreateReadyCapableShape: the spec's canonical board declaration
// succeeds end to end; a hand-broken all-or-nothing shape (--terminal on
// status without --guard status) is rejected at create time, exit 4,
// bad_value, naming the exact fix.
func TestCreateReadyCapableShape(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "create", "issues", "--scope", "s",
		"--field", "status=open,in-progress,closed,wontfix",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by",
		"--require-evidence", "status=closed", "--stale-after", "2h")
	if code != 0 {
		t.Fatal(se)
	}
	_, se, code = run(t, dir, "create", "broken", "--scope", "s",
		"--field", "status=open,in-progress,closed", "--terminal", "status=closed")
	if code != 4 || !strings.Contains(se, "bad_value") || !strings.Contains(se, "--guard status") {
		t.Fatalf("all-or-nothing shape must be enforced: %s", se)
	}
}

// TestCreateDeclarationsRoundTrip: every new Meta field the CLI accepts
// makes it into the persisted meta.json unchanged.
func TestCreateDeclarationsRoundTrip(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "create", "issues", "--scope", "s",
		"--field", "status=open,in-progress,closed,wontfix",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by",
		"--require-evidence", "status=closed", "--stale-after", "2h")
	if code != 0 {
		t.Fatal(se)
	}
	st := store.Store{Repo: gitx.Repo{Dir: dir}}
	_, meta, err := st.Events("issues")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.MultiFields) != 2 || meta.MultiFields[0] != "labels" || meta.MultiFields[1] != "blocked-by" {
		t.Fatalf("multi_fields round-trip: %v", meta.MultiFields)
	}
	if len(meta.Terminal["status"]) != 2 || !strings.Contains(strings.Join(meta.Terminal["status"], ","), "closed") {
		t.Fatalf("terminal round-trip: %v", meta.Terminal)
	}
	if len(meta.Guard) != 2 {
		t.Fatalf("guard round-trip: %v", meta.Guard)
	}
	if meta.StaleAfter != "2h" {
		t.Fatalf("stale_after round-trip: %v", meta.StaleAfter)
	}
}

// TestCreatePlainGuardedBoard: --guard without --terminal is a plain
// guarded board — ready-capability never opts in, so none of the
// all-or-nothing shape rules apply.
func TestCreatePlainGuardedBoard(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "create", "plain", "--scope", "s",
		"--field", "status=open,done", "--guard", "status")
	if code != 0 {
		t.Fatal(se)
	}
}

// TestCreateDeclarationRejections exercises each bad_value declaration
// rule through the real CLI flags (not just the model-level unit test).
func TestCreateDeclarationRejections(t *testing.T) {
	dir := initRepo(t)
	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"terminal not subset",
			[]string{"create", "c1", "--scope", "s", "--field", "status=open,in-progress,closed",
				"--terminal", "status=nope"},
			"subset"},
		{"guard undeclared field",
			[]string{"create", "c2", "--scope", "s", "--field", "status=open,in-progress,closed",
				"--guard", "priority"},
			"not a declared field"},
		{"bad stale-after",
			[]string{"create", "c3", "--scope", "s", "--field", "status=open,in-progress,closed",
				"--stale-after", "2 hours"},
			"ParseDuration"},
		{"multi-field collides with enum field",
			[]string{"create", "c4", "--scope", "s", "--field", "status=open,in-progress,closed",
				"--multi-field", "status"},
			"collides"},
	}
	for _, c := range cases {
		_, se, code := run(t, dir, c.args...)
		if code != 4 || !strings.Contains(se, "bad_value") || !strings.Contains(se, c.wantMsg) {
			t.Fatalf("%s: want bad_value mentioning %q: %s", c.name, c.wantMsg, se)
		}
	}
}
