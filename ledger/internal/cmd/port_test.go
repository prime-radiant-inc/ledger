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

// normalizeReadyIDs recursively replaces every "id" field's value in a
// decoded JSON document with a placeholder, so two `ready` envelopes minted
// from different ledgers (each with its own content-addressed ids, assigned
// fresh at commit time) can be compared for structural identity.
func normalizeReadyIDs(v any) any {
	switch t := v.(type) {
	case map[string]any:
		norm := make(map[string]any, len(t))
		for k, val := range t {
			if k == "id" {
				norm[k] = "<id>"
				continue
			}
			norm[k] = normalizeReadyIDs(val)
		}
		return norm
	case []any:
		norm := make([]any, len(t))
		for i, val := range t {
			norm[i] = normalizeReadyIDs(val)
		}
		return norm
	default:
		return v
	}
}

// seedReadyIdentityBoard builds a ready-capable board exercising ready,
// held/human, blocked, and statusless attention — everything but a live
// claim. Deliberately claim-free, unlike ready_test.go's seedReadyEnvelope:
// a claim's `age` is computed against wall-clock time.Now() at the moment
// `ready` runs, so two sequential `ready` invocations around a real
// export/import round-trip would legitimately disagree by however long that
// took, which isn't the kind of drift this test is checking for.
func seedReadyIdentityBoard(t *testing.T) string {
	dir := setupReady(t)
	run(t, dir, "set", "spike-probe", "status=wontfix", "--expect", "none", "-m", "not doing it", "--as", "a")

	run(t, dir, "set", "fix-retry", "blocked-by=spike-probe", "--expect", "none", "--as", "a")
	run(t, dir, "set", "fix-retry", "status=open", "--expect", "none", "-m", "fix the retry loop", "--as", "a")

	run(t, dir, "set", "sign-off", "labels=human", "--as", "a")
	run(t, dir, "set", "sign-off", "status=open", "--expect", "none", "--override", "-m", "needs a human sign-off", "--as", "a")

	run(t, dir, "set", "deploy", "blocked-by=sign-off", "--expect", "none", "--as", "a")
	run(t, dir, "set", "deploy", "status=open", "--expect", "none", "-m", "ship it", "--as", "a")

	run(t, dir, "set", "half-seeded", "labels=urgent", "--as", "a")
	return dir
}

// TestExportImportReadyIdentity: spec test-plan item 14's remaining half —
// TestExportImportRoundtrip already covers raw event-payload identity;
// `ready`'s derived envelope (folded from those same events) must survive
// the export/import boundary just as faithfully, since it's pure board
// state, not per-run randomness. Only `id` values may legitimately differ
// (ids are content-addressed at commit time, so a copy's events always mint
// new ones): a fresh store importing under the ORIGINAL slug must produce a
// byte-identical `ready` (ids aside), including the `ledger` name itself; a
// same-store import under a NEW slug must additionally carry that new name
// while everything else (ids aside) still matches.
func TestExportImportReadyIdentity(t *testing.T) {
	dir := seedReadyIdentityBoard(t) // slug "issues"
	origSO, se, code := run(t, dir, "ready")
	if code != 0 {
		t.Fatalf("ready: %s", se)
	}
	origDoc := mustJSON(t, origSO)

	f := filepath.Join(t.TempDir(), "issues.jsonl")
	if _, se, code := run(t, dir, "export", "issues", "--to", f); code != 0 {
		t.Fatal(se)
	}

	// Fresh store, original slug: everything but ids must match exactly,
	// including the ledger name.
	freshDir := initRepo(t)
	if _, se, code := run(t, freshDir, "import", f, "--slug", "issues"); code != 0 {
		t.Fatal(se)
	}
	freshSO, se, code := run(t, freshDir, "ready", "--ledger", "issues")
	if code != 0 {
		t.Fatalf("ready on the fresh-store copy: %s", se)
	}
	freshDoc := mustJSON(t, freshSO)

	if freshDoc["ledger"] != origDoc["ledger"] {
		t.Fatalf("same slug in a fresh store must keep the same ledger name: orig %v fresh %v",
			origDoc["ledger"], freshDoc["ledger"])
	}
	wantNorm, _ := json.Marshal(normalizeReadyIDs(origDoc))
	freshNorm, _ := json.Marshal(normalizeReadyIDs(freshDoc))
	if string(wantNorm) != string(freshNorm) {
		t.Fatalf("ready must be byte-identical across the export/import boundary except ids:\norig  %s\nfresh %s",
			wantNorm, freshNorm)
	}

	// Same store, new slug: the ledger name must now differ; everything
	// else (ids aside) must still match.
	if _, se, code := run(t, dir, "import", f, "--slug", "issues-copy"); code != 0 {
		t.Fatal(se)
	}
	copySO, se, code := run(t, dir, "ready", "--ledger", "issues-copy")
	if code != 0 {
		t.Fatalf("ready on the same-store copy: %s", se)
	}
	copyDoc := mustJSON(t, copySO)
	if copyDoc["ledger"] == origDoc["ledger"] {
		t.Fatalf("a same-store import under a new slug must carry the new ledger name, got %v", copyDoc["ledger"])
	}
	copyDoc["ledger"] = origDoc["ledger"] // the one field expected to differ; normalize it before comparing the rest
	copyNorm, _ := json.Marshal(normalizeReadyIDs(copyDoc))
	if string(wantNorm) != string(copyNorm) {
		t.Fatalf("ready must be byte-identical (ledger name and ids aside) across a same-store import under a new slug:\norig %s\ncopy %s",
			wantNorm, copyNorm)
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
