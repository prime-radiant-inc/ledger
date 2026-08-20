package out

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEmitJSONEnvelope(t *testing.T) {
	var b bytes.Buffer
	Emit(&b, false, map[string]any{"id": "abc", "ledger": "demo"}, []string{"[abc] demo"})
	var doc map[string]any
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["ok"] != true || doc["id"] != "abc" || doc["ledger"] != "demo" {
		t.Fatalf("envelope: %v", doc)
	}
}

// TestEmitRespectsPresetOK: the sync/push partial-failure envelope needs
// ok:false folded into the SAME document as the rest of the payload
// (spec's error contract merged with the outcomes array), not a second
// error document on a second write. A caller that pre-sets "ok" in the
// payload must have it survive untouched.
func TestEmitRespectsPresetOK(t *testing.T) {
	var b bytes.Buffer
	Emit(&b, false, map[string]any{"ok": false, "error": "partial_failure", "synced": []string{}}, nil)
	var doc map[string]any
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["ok"] != false || doc["error"] != "partial_failure" {
		t.Fatalf("preset ok/error must survive: %v", doc)
	}
}

func TestEmitTTYLines(t *testing.T) {
	var b bytes.Buffer
	Emit(&b, true, map[string]any{"id": "abc"}, []string{"line1", "line2"})
	if b.String() != "line1\nline2\n" {
		t.Fatalf("tty: %q", b.String())
	}
}

func TestWriteError(t *testing.T) {
	var b bytes.Buffer
	e := Errf("vocab_unknown", "chit vocab add demo status x -m \"why\"", 4, "%q is bad", "x")
	WriteError(&b, false, e)
	var doc map[string]string
	json.Unmarshal(b.Bytes(), &doc)
	if doc["error"] != "vocab_unknown" || !strings.Contains(doc["hint"], "vocab add") {
		t.Fatalf("err doc: %v", doc)
	}
	if e.ExitCode != 4 || e.Error() == "" {
		t.Fatal("CLIError contract")
	}
}

func TestAge(t *testing.T) {
	ts := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02T15:04:05")
	if got := Age(ts); got != "2h ago" {
		t.Fatalf("age: %q", got)
	}
}

// TestAgeMillisecondLayout: new events are written with the millisecond
// timestamp layout (model.TSLayout). Age must parse it same as the legacy
// layout above — not fall back to returning the raw string.
func TestAgeMillisecondLayout(t *testing.T) {
	ts := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02T15:04:05.000")
	if got := Age(ts); got != "2h ago" {
		t.Fatalf("age (millisecond layout): got %q, want \"2h ago\"", got)
	}
}

func TestEscapeControls(t *testing.T) {
	in := "safe\rFORGED\x1b[31mred\nnext\tline"
	got := EscapeControls(in)
	if strings.ContainsAny(got, "\r\x1b") || !strings.Contains(got, "^M") || !strings.Contains(got, "^[") {
		t.Fatalf("escape: %q", got) // a body must not be able to overwrite the render (spec, trust model)
	}
	if !strings.Contains(got, "\n") || !strings.Contains(got, "\t") {
		t.Fatal("newline/tab must survive")
	}
}
