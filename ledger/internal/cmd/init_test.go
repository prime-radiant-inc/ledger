package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRepo(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger.toml")); err != nil {
		t.Fatal("breadcrumb missing")
	}
	cfg, _ := exec.Command("git", "-C", dir, "config", "core.logAllRefUpdates").Output()
	if strings.TrimSpace(string(cfg)) != "always" {
		t.Fatalf("reflog net: %q", cfg)
	}
	// init must not commit anything
	st, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if !strings.Contains(string(st), ".ledger.toml") {
		t.Fatal("breadcrumb must be left uncommitted")
	}
}

func TestInitBareStore(t *testing.T) {
	dir := t.TempDir() // NOT a git repo
	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger.git", "HEAD")); err != nil {
		t.Fatal("bare store missing")
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger.toml")); err == nil {
		t.Fatal("bare stores are self-describing; no breadcrumb")
	}
	// verbs work against it via resolution
	_, _, code = run(t, dir, "create", "board", "--scope", "x")
	if code != 0 {
		t.Fatal("create in bare store")
	}
}

func TestInitBareStoreConfig(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal("init")
	}
	cfg, _ := exec.Command("git", "-C", filepath.Join(dir, ".ledger.git"), "config", "core.logAllRefUpdates").Output()
	if strings.TrimSpace(string(cfg)) != "always" {
		t.Fatalf("bare reflog net: %q", cfg)
	}
}

func TestInitRepoIdempotent(t *testing.T) {
	dir := initRepo(t)
	if _, _, code := run(t, dir, "init"); code != 0 {
		t.Fatal("first init")
	}
	tomlPath := filepath.Join(dir, ".ledger.toml")
	if err := os.WriteFile(tomlPath, []byte("# hand-edited, do not clobber\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	body, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hand-edited") {
		t.Fatal("re-running init must not clobber an existing .ledger.toml")
	}
	if !strings.Contains(so, "already_initialized") {
		t.Fatalf("expected an already-initialized note: %s", so)
	}
	cfg, _ := exec.Command("git", "-C", dir, "config", "core.logAllRefUpdates").Output()
	if strings.TrimSpace(string(cfg)) != "always" {
		t.Fatalf("idempotent init must still refresh config: %q", cfg)
	}
}

func TestInitHooksWritesSnippetNotHarnessConfig(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "init", "--hooks")
	if code != 0 {
		t.Fatal(so)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".ledger-hooks.md"))
	if err != nil {
		t.Fatal("hooks snippet file missing")
	}
	if !strings.Contains(string(body), "SessionStart") {
		t.Fatalf("hooks snippet must mention SessionStart: %s", body)
	}
	// --hooks must never touch harness config directly
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err == nil {
		t.Fatal("--hooks must never auto-edit harness config")
	}
}

func TestInitWithoutHooksSkipsSnippet(t *testing.T) {
	dir := initRepo(t)
	if _, _, code := run(t, dir, "init"); code != 0 {
		t.Fatal("init")
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger-hooks.md")); err == nil {
		t.Fatal("plain init must not write the hooks snippet")
	}
}
