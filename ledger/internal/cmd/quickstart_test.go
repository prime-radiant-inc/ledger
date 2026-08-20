package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestQuickstartPrintsColdConsumerDoctrine pins the cold-consumer content:
// exit 0, and the doctrine section a cold agent actually needs is there.
func TestQuickstartPrintsColdConsumerDoctrine(t *testing.T) {
	dir := initRepo(t)
	so, se, code := run(t, dir, "quickstart")
	if code != 0 {
		t.Fatalf("quickstart: exit %d\nstderr: %s", code, se)
	}
	if !strings.Contains(so, "chit quickstart") || !strings.Contains(so, "## Doctrine") {
		t.Fatalf("quickstart output missing cold-consumer markers: %q", so)
	}
}

// TestQuickstartOrchestratorDiffers pins that --orchestrator adds a
// distinct section rather than reprinting (or silently ignoring) the flag.
func TestQuickstartOrchestratorDiffers(t *testing.T) {
	dir := initRepo(t)
	cold, _, code := run(t, dir, "quickstart")
	if code != 0 {
		t.Fatalf("quickstart: exit %d", code)
	}
	orch, se, code := run(t, dir, "quickstart", "--orchestrator")
	if code != 0 {
		t.Fatalf("quickstart --orchestrator: exit %d\nstderr: %s", code, se)
	}
	if !strings.Contains(orch, "## Dictated grammar") {
		t.Fatalf("orchestrator output missing orchestrator-only marker: %q", orch)
	}
	if strings.Contains(cold, "## Dictated grammar") {
		t.Fatal("cold-consumer quickstart unexpectedly contains orchestrator content")
	}
	if orch == cold {
		t.Fatal("quickstart --orchestrator printed identical content to plain quickstart")
	}
}

// TestQuickstartNeedsNoStore is the point of the PersistentPreRunE
// exemption: a cold agent with no --store flag, no $LEDGER_DIR, and no git
// repo or .ledger.git anywhere in its cwd's ancestry must still be able to
// read the doctrine that tells it how to get started.
func TestQuickstartNeedsNoStore(t *testing.T) {
	dir := t.TempDir() // plain directory: no .git, no .ledger.git
	t.Chdir(dir)
	var so, se bytes.Buffer
	code := ExecuteArgs([]string{"quickstart"}, &so, &se)
	if code != 0 {
		t.Fatalf("quickstart with no --store outside any repo: exit %d\nstderr: %s", code, se.String())
	}
	if !strings.Contains(so.String(), "chit quickstart") {
		t.Fatalf("quickstart output missing expected content: %q", so.String())
	}
}
