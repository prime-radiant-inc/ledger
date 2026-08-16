package cmd

import (
	"encoding/json"
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

// TestImportRevalidatesDeclarations: import is a second meta-minting path,
// so it must re-run the same board-declaration shape checks create does —
// not just replay whatever meta line the export file happens to carry. An
// untouched export of a ready-capable board must still import cleanly, with
// meta.json round-tripping byte-for-byte except slug. A hand-edited export
// with --guard status stripped from the meta line must be rejected exit 4
// bad_value naming --guard status, and no ledger must be minted under the
// target slug.
func TestImportRevalidatesDeclarations(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "create", "issues", "--scope", "s",
		"--field", "status=open,in-progress,closed,wontfix",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels",
		"--guard", "status",
		"--as", "me")
	if code != 0 {
		t.Fatal(se)
	}

	f := filepath.Join(t.TempDir(), "issues.jsonl")
	if _, se, code := run(t, dir, "export", "issues", "--to", f); code != 0 {
		t.Fatal(se)
	}

	// clean import: unmodified export imports fine, and meta.json
	// round-trips byte-for-byte except slug.
	if _, se, code := run(t, dir, "import", f, "--slug", "issues-copy"); code != 0 {
		t.Fatalf("clean import: %s", se)
	}
	origMeta, err := execGit(dir, "show", "refs/ledger/issues:meta.json")
	if err != nil {
		t.Fatal(err)
	}
	copyMeta, err := execGit(dir, "show", "refs/ledger/issues-copy:meta.json")
	if err != nil {
		t.Fatal(err)
	}
	wantCopyMeta := strings.Replace(origMeta, `"slug": "issues"`, `"slug": "issues-copy"`, 1)
	if copyMeta != wantCopyMeta {
		t.Fatalf("meta.json must round-trip byte-for-byte except slug:\norig: %s\ncopy: %s", origMeta, copyMeta)
	}

	// broken import: hand-edit the header line's meta to drop "guard" entirely.
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(data), "\n", 2)
	var header map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(header["meta"], &meta); err != nil {
		t.Fatal(err)
	}
	delete(meta, "guard")
	mb, _ := json.Marshal(meta)
	header["meta"] = mb
	hb, _ := json.Marshal(header)
	broken := filepath.Join(t.TempDir(), "broken.jsonl")
	if err := os.WriteFile(broken, []byte(string(hb)+"\n"+lines[1]), 0o644); err != nil {
		t.Fatal(err)
	}

	_, se, code = run(t, dir, "import", broken, "--slug", "issues-broken")
	if code != 4 || !strings.Contains(se, "bad_value") || !strings.Contains(se, "--guard status") {
		t.Fatalf("import must re-validate declaration shape: %d %s", code, se)
	}
	lsOut, _, _ := run(t, dir, "ls", "--all")
	doc := mustJSON(t, lsOut)
	for _, row := range doc["ledgers"].([]any) {
		if row.(map[string]any)["slug"] == "issues-broken" {
			t.Fatalf("a rejected import must not mint a ledger: %s", lsOut)
		}
	}
}

// TestImportRejectsTruncatedExport: a malformed line partway through an
// export file must abort the whole import cleanly — no ref left behind under
// the target slug (there's no delete verb, and slugs are never reused, so a
// half-created ledger would be a permanent dead end). The same slug must
// remain importable afterward from a good file.
func TestImportRejectsTruncatedExport(t *testing.T) {
	dir := seed(t)
	good := filepath.Join(t.TempDir(), "good.jsonl")
	run(t, dir, "export", "demo", "--to", good)

	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	last := lines[len(lines)-1]
	lines[len(lines)-1] = last[:len(last)/2] // truncate mid-JSON

	bad := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(bad, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, se, code := run(t, dir, "import", bad, "--slug", "partial")
	if code != 4 || !strings.Contains(se, "bad_export") {
		t.Fatalf("truncated export must fail cleanly: %d %s", code, se)
	}
	_, se2, code2 := run(t, dir, "status", "--ledger", "partial")
	if code2 == 0 || !strings.Contains(se2, "unknown_ledger") {
		t.Fatalf("a rejected import must leave no ref behind: %d %s", code2, se2)
	}

	// the slug must still be usable from a good file
	so, _, code3 := run(t, dir, "import", good, "--slug", "partial")
	if code3 != 0 {
		t.Fatalf("slug must remain importable after a rejected import: %s", so)
	}
}

// TestImportRemapsRollupChildren: F3 — import used to keep a rollup's
// Children verbatim while every event is re-SHAed, so an imported rollup
// pointed at ids that no longer existed and curated history silently
// un-collapsed (the "encapsulated" children would show back up as live
// roots, right alongside the rollup that claims to cover them). Export a
// ledger with a rollup, import into a fresh slug, and assert tail collapses
// identically on the copy: the rollup root is present, its children are
// absent from roots, and `tail --in <new-id>` still opens exactly them.
func TestImportRemapsRollupChildren(t *testing.T) {
	dir := seed(t) // demo: t1, t2 already carry sets/notes
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")
	if _, se, code := run(t, dir, "rollup", a, b, "-m", "k-thread finished", "--as", "curator"); code != 0 {
		t.Fatal(se)
	}

	f := filepath.Join(t.TempDir(), "demo.jsonl")
	if _, _, code := run(t, dir, "export", "demo", "--to", f); code != 0 {
		t.Fatal("export")
	}
	so, _, code := run(t, dir, "import", f, "--slug", "demo-rollup-copy")
	if code != 0 {
		t.Fatal(so)
	}

	origTail, _, _ := run(t, dir, "tail", "-n", "50", "--ledger", "demo")
	copyTail, _, _ := run(t, dir, "tail", "-n", "50", "--ledger", "demo-rollup-copy")
	origRoots := mustJSON(t, origTail)["events"].([]any)
	copyRoots := mustJSON(t, copyTail)["events"].([]any)
	if len(origRoots) != len(copyRoots) {
		t.Fatalf("root count differs after import (children un-collapsed?): orig %d copy %d\norig: %s\ncopy: %s",
			len(origRoots), len(copyRoots), origTail, copyTail)
	}

	var copyRid string
	rollupRoots := 0
	for _, e := range copyRoots {
		m := e.(map[string]any)
		if m["type"] == "rollup" {
			rollupRoots++
			copyRid = m["id"].(string)
		}
	}
	if rollupRoots != 1 {
		t.Fatalf("imported copy must have exactly one rollup root, got %d: %s", rollupRoots, copyTail)
	}

	in, se, code := run(t, dir, "tail", "--in", copyRid, "--ledger", "demo-rollup-copy")
	if code != 0 {
		t.Fatalf("--in on imported rollup failed: %s", se)
	}
	inDoc := mustJSON(t, in)
	if inDoc["summary"] != "k-thread finished" {
		t.Fatalf("imported rollup summary drifted: %v", inDoc)
	}
	inEvents, _ := inDoc["events"].([]any)
	if len(inEvents) != 2 {
		t.Fatalf("--in on imported rollup must open exactly its 2 remapped children: %v", inDoc)
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
