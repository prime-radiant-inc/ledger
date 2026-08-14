package cmd

import (
	"encoding/json"
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

// TestInitFromSubdirectoryErrors is round-1 review finding 1 (critical): init
// run from a subdirectory of a git repo must not misclassify it as non-git
// and create a shadow bare store — it must hard-error, naming the real repo
// root, and create nothing anywhere.
func TestInitFromSubdirectoryErrors(t *testing.T) {
	dir := initRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	so, se, code := run(t, sub, "init")
	if code != 4 {
		t.Fatalf("expected exit 4, got %d: stdout=%s stderr=%s", code, so, se)
	}
	if !strings.Contains(se, "bad_value") {
		t.Fatalf("expected bad_value error: %s", se)
	}
	if !strings.Contains(se, filepath.Base(dir)) {
		t.Fatalf("error should name the repo root: %s", se)
	}
	if _, err := os.Stat(filepath.Join(sub, ".ledger.git")); err == nil {
		t.Fatal("must not create a shadow store inside a repo subdirectory")
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger.toml")); err == nil {
		t.Fatal("must not write the breadcrumb from the wrong location either")
	}
}

// TestInitAtRootStillWorks pins the companion case for the fix above: a repo
// root (not a subdirectory) must still init normally.
func TestInitAtRootStillWorks(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger.toml")); err != nil {
		t.Fatal("breadcrumb missing at repo root")
	}
}

// TestInitRepoJSONHasDoctrineFields is round-1 review finding 2 (important):
// init's instructional content was TTY-only, so the agent-primary non-TTY
// mode got none of it. The bootstrap hint, the CLAUDE.md/AGENTS.md stanza,
// the admin-doc pointer, and (repo case) the commit hint must all ride in
// the JSON payload too.
func TestInitRepoJSONHasDoctrineFields(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(so), &doc); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, so)
	}
	for _, field := range []string{"bootstrap_hint", "stanza", "admin_doc", "commit_hint"} {
		v, ok := doc[field].(string)
		if !ok || v == "" {
			t.Fatalf("missing/empty %q in JSON payload: %v", field, doc)
		}
	}
	if !strings.Contains(doc["stanza"].(string), "Ledger") {
		t.Fatalf("stanza should carry the actual CLAUDE.md/AGENTS.md text: %v", doc["stanza"])
	}
	if !strings.Contains(doc["commit_hint"].(string), "commit") {
		t.Fatalf("commit_hint should say to commit the breadcrumb: %v", doc["commit_hint"])
	}
	if !strings.Contains(doc["admin_doc"].(string), "admin.md") {
		t.Fatalf("admin_doc should point at the runbook: %v", doc["admin_doc"])
	}
}

// TestInitBareJSONHasDoctrineFields covers the bare-store case for the same
// finding: bootstrap_hint/stanza/admin_doc apply there too (no commit_hint —
// there's no breadcrumb file to commit).
func TestInitBareJSONHasDoctrineFields(t *testing.T) {
	dir := t.TempDir()
	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(so), &doc); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, so)
	}
	for _, field := range []string{"bootstrap_hint", "stanza", "admin_doc"} {
		v, ok := doc[field].(string)
		if !ok || v == "" {
			t.Fatalf("missing/empty %q in JSON payload: %v", field, doc)
		}
	}
	if _, present := doc["commit_hint"]; present {
		t.Fatalf("bare store has no breadcrumb; commit_hint should be absent: %v", doc)
	}
}
