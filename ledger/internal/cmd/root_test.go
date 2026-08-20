package cmd

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string { return initRepoAt(t, t.TempDir()) }

// initRepoAt makes dir a git repo with one empty commit — separate from
// initRepo so a test can place the repo somewhere specific (e.g. under a
// directory that also holds a bare store).
func initRepoAt(t *testing.T, dir string) string {
	t.Helper()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	return dir
}

// run executes the CLI in-process against dir; returns stdout, stderr, exit code.
func run(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	var so, se bytes.Buffer
	code := ExecuteArgs(append([]string{"--store", dir}, args...), &so, &se)
	return so.String(), se.String(), code
}

func TestHelpIsSafeAndListsVerbs(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "--help")
	if code != 0 || !strings.Contains(so, "create") || !strings.Contains(so, "watch") {
		t.Fatalf("help: %d %q", code, so)
	}
	// the round-1 disaster: probing help must never write
	slugs, _, _ := run(t, dir, "ls")
	var doc map[string]any
	json.Unmarshal([]byte(slugs), &doc)
	if l, _ := doc["ledgers"].([]any); len(l) != 0 {
		t.Fatalf("help had side effects: %v", doc)
	}
}

func TestUnknownVerbErrors(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "bogus-verb")
	if code == 0 || !strings.Contains(se, "unknown_verb") {
		t.Fatalf("unknown verb: %d %q", code, se)
	}
}

func TestNoOpenLedgerError(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "status")
	if code != 4 || !strings.Contains(se, "no_open_ledger") || !strings.Contains(se, "create") {
		t.Fatalf("no_open_ledger with create hint: %d %q", code, se)
	}
}

// TestPositionalSlugOnReadVerbSuggestsLedgerFlag: `chit show <slug>` is the
// slug-positional habit from set/close transferring wrongly (two eval agents
// made it independently). It must not read as an unknown *verb* — it's a
// usage error whose fix is the --ledger flag, named in the hint.
func TestPositionalSlugOnReadVerbSuggestsLedgerFlag(t *testing.T) {
	dir := setup(t) // ledger "demo" exists here
	// note rides along: it addresses its ledger by flag exactly like the
	// reads, and it carries no body flag here — the positional must be
	// caught by the args check before any body validation runs.
	for _, verb := range []string{"show", "tail", "notes", "watch", "render", "note"} {
		_, se, code := run(t, dir, verb, "demo")
		if code != 4 || !strings.Contains(se, "bad_usage") {
			t.Fatalf("%s <slug> must be bad_usage exit 4: %d %q", verb, code, se)
		}
		if !strings.Contains(se, "did you mean: chit "+verb+" --ledger demo?") {
			t.Fatalf("%s <slug> hint must name --ledger: %q", verb, se)
		}
		if strings.Contains(se, "empty_body") || strings.Contains(se, "conflicting_body") {
			t.Fatalf("%s <slug> must fail the args check, not body validation: %q", verb, se)
		}
	}
	// ls has no --ledger of its own; its fix is the verb that does.
	_, se, code := run(t, dir, "ls", "demo")
	if code != 4 || !strings.Contains(se, "did you mean: chit show --ledger demo?") {
		t.Fatalf("ls <slug> hint must point at show: %d %q", code, se)
	}
	// and a genuinely unknown subcommand stays unknown_verb
	_, se, code = run(t, dir, "frobnicate")
	if code != 4 || !strings.Contains(se, "unknown_verb") {
		t.Fatalf("unknown verb must stay unknown_verb: %d %q", code, se)
	}
}

// TestCobraUsageErrorsMapToBadUsage: a genuine cobra flag-parse error (an
// unknown flag, here) must not fall into the generic git_failed bucket
// (exit 1, empty hint); it's classified as bad_usage, exit 4, with a hint
// pointing at --help.
func TestCobraUsageErrorsMapToBadUsage(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "note", "--totally-bogus-flag", "x", "-m", "hi")
	if code != 4 || !strings.Contains(se, "bad_usage") || !strings.Contains(se, "--help shows usage") {
		t.Fatalf("unknown flag must map to bad_usage exit 4: %d %q", code, se)
	}
}
