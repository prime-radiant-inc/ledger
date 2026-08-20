package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test-plan item 10 — VOCABULARY. Vocabulary is configured, never assumed,
// and all three refusals name the failing flag, the declared vocabulary, and
// a fix that is TRUE.

// TestDoneDroppedBoardBridgesWithFlags: a legal ready-capable board whose
// terminals are done/dropped works end to end. Hard-coding closed/wontfix
// turned this board into a hard error at the first close.
func TestDoneDroppedBoardBridgesWithFlags(t *testing.T) {
	f := newFixture(t, "open,in-progress,done,dropped", "done", "dropped")
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "done", "shipped", "jesse", "commit:a1b2c3")
	f.syncOK("operator")
	if state, reason := f.issueState(1); state != "CLOSED" || reason != "COMPLETED" {
		t.Fatalf("done must close completed, got %s/%s", state, reason)
	}
	f.setStatusOverride("cache-warm", "dropped", "not doing it", "jesse")
	f.syncOK("operator")
	if state, reason := f.issueState(1); state != "CLOSED" || reason != "NOT_PLANNED" {
		t.Fatalf("dropped must close not-planned, got %s/%s", state, reason)
	}
	// And a GitHub reopen writes `open` — there is no reopen flag, because
	// the non-terminal vocabulary is pinned.
	f.converge("operator", 3)
	f.humanReopen(1, "mallory")
	f.syncOK("operator")
	if f.status("cache-warm") != openValue {
		t.Fatalf("a reopen must write %q, got %q", openValue, f.status("cache-warm"))
	}
	f.converge("operator", 3)
}

// TestMissingVocabularyIsRefusedAndTheFixIsNotVocabAdd: refusal (1). The
// remedy must tell the truth — `vocab add` is refused by the tool itself on
// a ready-capable board, so offering it is a dead end.
func TestMissingVocabularyIsRefusedAndTheFixIsNotVocabAdd(t *testing.T) {
	f := newFixture(t, "open,in-progress,done,dropped", "done", "dropped")
	f.done, f.notPlanned = "closed", "wontfix" // flags the board does not declare
	_, err := f.sync("operator", 0)
	if err == nil {
		t.Fatal("a board lacking the flags' values must be refused")
	}
	msg := err.Error()
	for _, want := range []string{"--done closed", "--not-planned wontfix", "done, dropped", "immutable", "export/import"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must name %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "vocab add status") {
		t.Fatalf("the remedy must NOT offer `vocab add` — the tool refuses it: %s", msg)
	}
	// And prove the remedy is honest: `vocab add` really is refused here.
	_, vErr := f.ledger("vocab", "add", "status", "closed", "-m", "trying to add")
	if vErr == nil {
		t.Fatal("`vocab add status` on a ready-capable board should be refused by the tool")
	}
}

// TestNonTerminalFlagValueIsRefused: refusal (2), the probed hole. Membership
// alone is not enough — `--done in-progress` passes a membership check and
// then CLOSES GitHub issues on a non-terminal board state.
func TestNonTerminalFlagValueIsRefused(t *testing.T) {
	f := newIssueFixture(t)
	f.done = "in-progress"
	_, err := f.sync("operator", 0)
	if err == nil {
		t.Fatal("--done in-progress must be refused")
	}
	if !strings.Contains(err.Error(), "--done in-progress is non-terminal") {
		t.Fatalf("the refusal must name the flag and why: %s", err)
	}
	if !strings.Contains(err.Error(), "open/in-progress") {
		t.Fatalf("the refusal must name the pinned non-terminal vocabulary: %s", err)
	}
}

// TestTwoFlagsNamingOneValueIsRefused: --done and --not-planned must name
// two DISTINCT terminals, or one close reason can never be expressed.
func TestTwoFlagsNamingOneValueIsRefused(t *testing.T) {
	f := newIssueFixture(t)
	f.notPlanned = "closed"
	_, err := f.sync("operator", 0)
	if err == nil {
		t.Fatal("two flags naming one value must be refused")
	}
	if !strings.Contains(err.Error(), "both name closed") {
		t.Fatalf("the refusal must say so: %s", err)
	}
}

// TestThreeTerminalBoardIsRefusedNamingTheUnmappedValue is round 4's
// Critical, kept as a regression. A legal three-terminal board passed every
// startup check while its third value mirrored to NOTHING forever and intake
// then overwrote the maintainer's `duplicate` with `closed` — probed end to
// end, exit 0 throughout.
func TestThreeTerminalBoardIsRefusedNamingTheUnmappedValue(t *testing.T) {
	f := newFixtureTerminals(t, "open,in-progress,closed,wontfix,duplicate", "closed", "wontfix",
		"closed,wontfix,duplicate")
	_, err := f.sync("operator", 0)
	if err == nil {
		t.Fatal("a board whose terminal set exceeds the two mirrored terminals must be refused")
	}
	msg := err.Error()
	for _, want := range []string{"duplicate", "mirror to nothing", "Multi-terminal mapping is v2"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must name %q: %s", want, msg)
		}
	}
	// Nothing was written on either side.
	if f.countIssues() != 0 {
		t.Fatalf("a refused run touched GitHub: %d issues", f.countIssues())
	}
}

// TestNotPlannedIntakesWithNoEvidence: evidence on a not-planned close is
// "pasted-string theater" — a decision not to do something has no artifact.
// A completed close does carry it.
func TestNotPlannedIntakesWithNoEvidence(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.seed("retry-storm", "fix the retry storm", "jesse")
	f.syncOK("operator")
	f.humanClose(1, true, "mallory")  // not planned
	f.humanClose(2, false, "mallory") // completed
	f.syncOK("operator")

	if f.status("cache-warm") != "wontfix" {
		t.Fatalf("NOT_PLANNED must map to the not-planned value, got %q", f.status("cache-warm"))
	}
	if f.status("retry-storm") != "closed" {
		t.Fatalf("COMPLETED must map to the done value, got %q", f.status("retry-storm"))
	}
	for _, ev := range f.chain() {
		if ev.Type != "set" || ev.Fields["status"] == "" {
			continue
		}
		switch ev.Key {
		case "cache-warm":
			if ev.Fields["status"] == "wontfix" && len(ev.Evidence) != 0 {
				t.Fatalf("a not-planned intake must carry NO evidence: %+v", ev.Evidence)
			}
		case "retry-storm":
			if ev.Fields["status"] == "closed" && len(ev.Evidence) == 0 {
				t.Fatalf("a completed intake must carry gh: evidence")
			}
		}
	}
}

// TestPlainBoardIsRefused: the terminality oracle (declared minus
// {open, in-progress}) is sound only because `create` pins the non-terminal
// vocabulary — and it pins it only on READY-CAPABLE boards. On a plain board
// the same vocabulary would read as terminal while `human` gates nothing and
// Law 3's whole refusal path silently never fires.
func TestPlainBoardIsRefused(t *testing.T) {
	f := newFixtureTerminals(t, "open,in-progress,closed,wontfix", "closed", "wontfix", "")
	_, err := f.sync("operator", 0)
	if err == nil {
		t.Fatal("a plain board must be refused")
	}
	if !strings.Contains(err.Error(), "not ready-capable") {
		t.Fatalf("the refusal must say why: %s", err)
	}
}

// TestOneBoardOneRepo: the bridge refuses a run whose state note names a
// different repo. Multi-repo bridging is v2 — and re-binding would re-import
// the other repo's mirrored history, since the marker is not board-scoped.
func TestOneBoardOneRepo(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	f.repo = "prime-radiant-inc/somewhere-else"
	_, err := f.sync("operator", 0)
	if err == nil {
		t.Fatal("a second repo must be refused")
	}
	for _, want := range []string{"prime-radiant-inc/fixture", "somewhere-else", "one repo, permanently"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must name %q: %s", want, err)
		}
	}
}

// TestSaturationIsRefusedAtExactlyTheLimit: outside the listing window every
// bulk map is zero-valued, which silently disables the comment dedupe, the
// state diff and adoption. The refusal fires at listing >= limit, so the real
// ceiling is limit-1.
func TestSaturationIsRefusedAtExactlyTheLimit(t *testing.T) {
	f := newIssueFixture(t)
	f.listLimit = 5
	for i := 0; i < 4; i++ {
		f.humanCreateIssue("issue number "+string(rune('a'+i)), "body", "mallory")
	}
	// Four issues against a limit of five: under the ceiling, so this runs.
	if _, err := f.sync("operator", 0); err != nil {
		t.Fatalf("four issues under a limit of five must run: %v", err)
	}
	f.humanCreateIssue("the fifth issue", "body", "mallory")
	_, err := f.sync("operator", 0)
	if err == nil {
		t.Fatal("a listing that saturates the window must be refused")
	}
	for _, want := range []string{"saturates", "--list-limit 10", "refusing to run blind"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must name %q: %s", want, err)
		}
	}
	// And the escape hatch is a CONSTANT, not a project.
	f.listLimit = 10
	if _, err := f.sync("operator", 0); err != nil {
		t.Fatalf("a bigger --list-limit must just work: %v", err)
	}
}

// TestCapabilityProbeRefusesAPreRev16Binary: Law 5's signals field fails
// closed, so against an old binary EVERY needs_override would take the
// refusal path. The operator is told to upgrade rather than left wondering.
func TestCapabilityProbeRefusesAPreRev16Binary(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "ledger-old")
	// A stand-in whose `set --help` has no --rename, exactly as a pre-rev-16
	// binary's does.
	script := "#!/bin/sh\nif [ \"$1\" = set ]; then echo 'Flags:'; echo '  -m, --message string'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(old, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	b := Board{Bin: old, Slug: "issues"}
	err := b.CheckCapable()
	if err == nil {
		t.Fatal("a pre-rev-16 binary must be refused by name")
	}
	for _, want := range []string{"rev 16", "--rename", "signals", "chit update"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must name %q: %s", want, err)
		}
	}
	// The real binary passes.
	if err := (Board{Bin: ledgerBin, Slug: "issues"}).CheckCapable(); err != nil {
		t.Fatalf("the shipped binary must pass the probe: %v", err)
	}
	// And a binary that cannot run at all is a different, named failure.
	if err := (Board{Bin: filepath.Join(dir, "nope"), Slug: "issues"}).CheckCapable(); err == nil ||
		!strings.Contains(err.Error(), "cannot run the chit binary") {
		t.Fatalf("a missing binary must be named: %v", err)
	}
}
