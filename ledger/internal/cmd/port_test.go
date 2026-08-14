package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// execGit runs git -C dir <args...> and returns trimmed stdout.
func execGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func TestExportImportRoundtrip(t *testing.T) {
	dir := seed(t)
	f := filepath.Join(t.TempDir(), "demo.jsonl")
	_, _, code := run(t, dir, "export", "demo", "--to", f)
	if code != 0 {
		t.Fatal("export")
	}
	data, _ := os.ReadFile(f)
	if !strings.Contains(string(data), "ledger_export") {
		t.Fatal("header line")
	}
	so, _, code := run(t, dir, "import", f, "--slug", "demo-copy")
	if code != 0 {
		t.Fatal(so)
	}
	// payload equality: spine folds identically
	a, _, _ := run(t, dir, "status", "--ledger", "demo")
	b, _, _ := run(t, dir, "status", "--ledger", "demo-copy")
	da, db := mustJSON(t, a), mustJSON(t, b)
	ra, rb := da["rows"].([]any), db["rows"].([]any)
	if len(ra) != len(rb) {
		t.Fatalf("row counts differ: %d %d", len(ra), len(rb))
	}
	for i := range ra {
		ma, mb := ra[i].(map[string]any), rb[i].(map[string]any)
		for _, k := range []string{"key", "field", "value", "note", "by"} {
			if ma[k] != mb[k] {
				t.Fatalf("payload drift on %s: %v vs %v", k, ma[k], mb[k])
			}
		}
	}
	_, se, code := run(t, dir, "import", f, "--slug", "demo")
	if code != 4 || !strings.Contains(se, "slug_exists") {
		t.Fatalf("import refuses existing slugs: %s", se)
	}
}

func TestImportedCommitterMarker(t *testing.T) {
	dir := seed(t)
	f := filepath.Join(t.TempDir(), "d.jsonl")
	run(t, dir, "export", "demo", "--to", f)
	run(t, dir, "import", f, "--slug", "d2")
	out, _ := execGit(dir, "log", "-1", "--format=%cn", "refs/ledger/d2")
	if out != "imported" {
		t.Fatalf("import provenance: %q (must render as (imported), never the importing harness)", out)
	}
}
