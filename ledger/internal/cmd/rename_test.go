package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- `title` is a reserved field name (bridge design rev 6) ----

// TestTitleIsReservedAtCreate: no board may declare, guard, or extend a
// field called "title". A legal board could otherwise declare and guard
// one, splitting the contested read path (which unions the rename stream
// and any guarded field) from the write path (renames only).
func TestTitleIsReservedAtCreate(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"--field", []string{"create", "b1", "--scope", "s", "--field", "title=a,b"}},
		{"--multi-field", []string{"create", "b2", "--scope", "s", "--field", "status=open,done", "--multi-field", "title"}},
		{"--guard", []string{"create", "b3", "--scope", "s", "--field", "status=open,done", "--guard", "title"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := initRepo(t)
			_, se, code := run(t, dir, c.args...)
			if code != 4 {
				t.Fatalf("%s title must be refused: exit %d, stderr %s", c.name, code, se)
			}
			doc := mustJSON(t, se)
			if doc["error"] != "bad_value" {
				t.Fatalf("%s title must be bad_value: %s", c.name, se)
			}
			if !strings.Contains(doc["message"].(string), "reserved") {
				t.Fatalf("%s title's message must say reserved: %s", c.name, se)
			}
		})
	}
}

// TestTitleIsReservedAtVocab: `vocab add <slug> title <value>` is refused
// as reserved too — the same ruling, on the one verb that can extend a
// declared field after create.
func TestTitleIsReservedAtVocab(t *testing.T) {
	dir := setupReady(t)
	_, se, code := run(t, dir, "vocab", "add", "issues", "title", "anything", "-m", "why")
	if code != 4 {
		t.Fatalf("vocab add title must be refused: exit %d, stderr %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_value" || !strings.Contains(doc["message"].(string), "reserved") {
		t.Fatalf("vocab add title must be a reserved bad_value: %s", se)
	}
}

// TestTitleIsReservedAtImport: import re-validates the exported meta, so a
// hand-edited export declaring "title" is refused at the boundary rather
// than recreated as a board no later create could ever have made.
func TestTitleIsReservedAtImport(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "a title", "--as", "a")

	path := filepath.Join(t.TempDir(), "export.jsonl")
	if _, se, code := run(t, dir, "export", "issues", "--to", path); code != 0 {
		t.Fatalf("export: %s", se)
	}
	data, err := os.ReadFile(path)
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
	meta["multi_fields"] = json.RawMessage(`["labels","blocked-by","title"]`)
	mb, _ := json.Marshal(meta)
	header["meta"] = mb
	hb, _ := json.Marshal(header)
	edited := filepath.Join(t.TempDir(), "titled.jsonl")
	if err := os.WriteFile(edited, []byte(string(hb)+"\n"+lines[1]), 0o644); err != nil {
		t.Fatal(err)
	}

	_, se, code := run(t, dir, "import", edited, "--slug", "copy")
	if code != 4 {
		t.Fatalf("importing a board declaring 'title' must be refused: exit %d, stderr %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_value" || !strings.Contains(doc["message"].(string), "reserved") {
		t.Fatalf("import must refuse 'title' as reserved: %s", se)
	}
}
