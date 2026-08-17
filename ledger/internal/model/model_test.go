package model

import (
	"os"
	"strings"
	"testing"
	"time"

	"ledger/internal/gitx"
)

func TestValidSlug(t *testing.T) {
	for slug, want := range map[string]bool{
		"a": true, "task-3": true, "a1-b2": true,
		"": false, "-a": false, "A": false, "a_b": false, "--help": false,
		strings.Repeat("a", 64): true, strings.Repeat("a", 65): false,
	} {
		if ValidSlug(slug) != want {
			t.Errorf("ValidSlug(%q) != %v", slug, want)
		}
	}
}

func TestResolveAuthor(t *testing.T) {
	t.Setenv("LEDGER_AUTHOR", "")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CODEX_THREAD_ID", "")
	if got := ResolveAuthor("boss"); got != "boss" {
		t.Fatalf("--as should win, got %q", got)
	}
	t.Setenv("LEDGER_AUTHOR", "envrole")
	if got := ResolveAuthor(""); got != "envrole" {
		t.Fatalf("LEDGER_AUTHOR should win, got %q", got)
	}
	t.Setenv("LEDGER_AUTHOR", "")
	t.Setenv("CLAUDECODE", "1")
	if got := ResolveAuthor(""); got != "claude-code" {
		t.Fatalf("harness marker must beat $USER, got %q", got) // spec: never sign as the human by accident
	}
	t.Setenv("CLAUDECODE", "")
	if got := ResolveAuthor(""); got != os.Getenv("USER") {
		t.Fatalf("bare $USER only with no harness, got %q", got)
	}
}

func TestCaptureOriginBranchAndDetached(t *testing.T) {
	dir := t.TempDir()
	r := gitx.Repo{Dir: dir}
	r.Git("", "init", "-b", "main")
	r.Git("", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "init")
	o := CaptureOrigin(r)
	if o.Branch != "main" || o.Head == "" || o.Host == "" || o.PID == 0 {
		t.Fatalf("origin: %+v", o)
	}
	head, _, _ := r.Git("", "rev-parse", "HEAD")
	r.Git("", "checkout", "-q", head)
	o = CaptureOrigin(r)
	if !strings.HasPrefix(o.Branch, "(detached@") {
		t.Fatalf("detached branch capture: %q", o.Branch)
	}
}

func TestNewEventShape(t *testing.T) {
	ev := NewEvent("set", "alice", gitx.Repo{})
	if ev.Type != "set" || ev.Author != "alice" || len(ev.TS) != 23 { // 2026-08-13T21:00:00.000
		t.Fatalf("event: %+v", ev)
	}
}

func TestTimestampLayoutMilliseconds(t *testing.T) {
	ev := NewEvent("set", "a", gitx.Repo{})
	if _, err := time.Parse(TSLayout, ev.TS); err != nil {
		t.Fatalf("new events must use %s: got %q (%v)", TSLayout, ev.TS, err)
	}
	if strings.HasSuffix(ev.TS, "Z") || strings.Contains(ev.TS, "+") {
		t.Fatalf("no zone suffix allowed: %q", ev.TS)
	}
}

func TestParseTSBothLayouts(t *testing.T) {
	for _, s := range []string{"2026-08-16T18:23:31.013", "2026-08-15T11:02:09"} {
		ts, err := ParseTS(s)
		if err != nil {
			t.Fatalf("ParseTS(%q): %v", s, err)
		}
		if ts.Location() != time.UTC {
			t.Fatalf("must parse as UTC")
		}
	}
	if _, err := ParseTS("not-a-time"); err == nil {
		t.Fatal("garbage must error")
	}
}
