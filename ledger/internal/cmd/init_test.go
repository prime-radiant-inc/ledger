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

// TestInitBreadcrumbRoundTripKeepsRemoteName: when init can resolve a
// default remote (here the sole configured one, named something other than
// "origin"), the committed breadcrumb records that NAME, active and
// uncommented — never a URL (round 5) — and reading it back via
// breadcrumbRemote reproduces the same name, the round trip a later clone's
// own `resolveRemote` depends on.
func TestInitBreadcrumbRoundTripKeepsRemoteName(t *testing.T) {
	dir := initRepo(t)
	git(t, dir, "remote", "add", "upstream", "https://example.invalid/x.git")
	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".ledger.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `remote = "upstream"`) {
		t.Fatalf("breadcrumb must record the resolved remote's name, uncommented: %s", body)
	}
	if strings.Contains(string(body), "https://") {
		t.Fatalf("breadcrumb must never carry a URL: %s", body)
	}
	if got := breadcrumbRemote(dir); got != "upstream" {
		t.Fatalf("breadcrumbRemote round-trip: got %q, want %q", got, "upstream")
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

// TestInitFromSubdirectoryResolvesToRepoRoot: init standing in a
// subdirectory of a git repo initializes the REPO — breadcrumb at the root,
// refspec installed, `path` naming the root — the same ancestor resolution
// every other verb already does from a subdirectory.
//
// Behavior before this test: a bad_value refusal (exit 4) naming the root and
// creating nothing, which is how the 2026-08-18 migration ended up with no
// breadcrumb anywhere (its `chit init` ran from ledger/ and its error was
// swallowed by a jq pipe). The refusal's own invariant survives: no shadow
// .ledger.git may ever appear in the subdirectory.
func TestInitFromSubdirectoryResolvesToRepoRoot(t *testing.T) {
	dir := initRepo(t)
	git(t, dir, "remote", "add", "upstream", "https://example.invalid/x.git")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEDGER_DIR", "")
	t.Chdir(sub)

	so, se, code := ambient(t, "init")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: stdout=%s stderr=%s", code, so, se)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(so), &doc); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, so)
	}
	wantRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotRoot, err := filepath.EvalSymlinks(doc["path"].(string))
	if err != nil {
		t.Fatalf("path %v: %v", doc["path"], err)
	}
	if gotRoot != wantRoot {
		t.Fatalf("path must name the repo root: got %q want %q", gotRoot, wantRoot)
	}
	if doc["kind"] != "repo" {
		t.Fatalf("kind must be repo: %s", so)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger.toml")); err != nil {
		t.Fatalf("breadcrumb must land at the repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, ".ledger.toml")); err == nil {
		t.Fatal("the breadcrumb must never be written in the subdirectory")
	}
	if _, err := os.Stat(filepath.Join(sub, ".ledger.git")); err == nil {
		t.Fatal("must not create a shadow store inside a repo subdirectory")
	}
	fetch, _ := exec.Command("git", "-C", dir, "config", "--get-all", "remote.upstream.fetch").Output()
	if !strings.Contains(string(fetch), "refs/ledger/") {
		t.Fatalf("init from a subdirectory must still install the refspec: %q", fetch)
	}
}

// TestInitStoreFlagAtSubdirectoryResolvesToRepoRoot: the same resolution when
// the subdirectory arrives as --store rather than as the cwd — one code path,
// so a --store pointed inside a repo can never mint a shadow bare store there
// either. --hooks rides along to the root with everything else.
func TestInitStoreFlagAtSubdirectoryResolvesToRepoRoot(t *testing.T) {
	dir := initRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	so, se, code := run(t, sub, "init", "--hooks")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: stdout=%s stderr=%s", code, so, se)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger.toml")); err != nil {
		t.Fatalf("breadcrumb must land at the repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger-hooks.md")); err != nil {
		t.Fatalf("--hooks must write at the repo root too: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, ".ledger.git")); err == nil {
		t.Fatal("must not create a shadow store inside a repo subdirectory")
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
	if !strings.Contains(doc["stanza"].(string), "## chit") {
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
