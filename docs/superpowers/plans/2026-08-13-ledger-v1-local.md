# Ledger v1 (local core) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `ledger` CLI's local core — phantom-ref storage, generalized enum fields, reads/watch, lifecycle, embedded docs, and the companion skill — everything in spec rev 10 except the sync/push layer (that is Plan 2).

**Architecture:** A Go binary shelling out to system git. Each ledger is `refs/ledger/<slug>`; each event is a commit whose tree holds `event.json` (creation commits add `meta.json`). Appends are CAS on `update-ref`; reads batch through `git log`/`cat-file`. State folds from events. Output: one JSON document per command (non-TTY) with an `ok`/`id`/`ledger` envelope; errors are `{error,message,hint}` on stderr. All behavior contracts come from the spec and were validated by three prototype/eval rounds; spike3 (`scratchpad/spike3/ledger.py` in the session scratchpad, also mirrored conceptually in this plan's code) is the reference for feel, NOT for copying (it lacks CAS-transactions, supersede, escaping, idempotency).

**Tech Stack:** Go ≥1.22, spf13/cobra (CLI + per-verb help), stdlib testing with real git repos in `t.TempDir()`. No daemon, no CGO, no other deps.

**Spec:** `docs/superpowers/specs/2026-08-13-ledger-tool-design.md` (rev 10). The plan argues from the spec; executors read both.

## Global Constraints

- Module lives at `ledger/` in this repo; module name `ledger`; binary `ledger` (`go build -o ledger .` inside `ledger/`).
- Shells out to **system git**; startup requires git ≥ 2.40 (checked once, cached; `git_too_old` error otherwise). All validation ran on 2.50.1.
- Slug grammar: `^[a-z0-9][a-z0-9-]{0,63}$` — enforced at create/import; slugs are never reused (even after close).
- Commit identity is synthetic, never the user's gitconfig: author = `<asserted author> <author@ledger.invalid>`, committer = `<harness marker> <marker@ledger.invalid>`; harness marker = `claude-code` when `$CLAUDECODE` is set, else `codex` when `$CODEX_THREAD_ID` is set, else `terminal`; `imported` for imports.
- Author resolution for writes: `--as` > `$LEDGER_AUTHOR` > harness marker (when a harness is detected) > `$USER`.
- Exit codes: 0 ok; 1 internal/git failure; 2 watch timeout (cursor still emitted); 4 contract errors. Error identifiers (exact strings): `unknown_ledger, no_open_ledger, ambiguous_ledger, slug_exists, bad_slug, unknown_field, vocab_unknown, evidence_required, bad_value, conflicting_body, empty_body, unknown_key, closed, reset_required, cas_exhausted, git_failed, git_too_old, unknown_verb, needs_successor, conflicting_source`.
- JSON when stdout is not a TTY; human text on a TTY. Every success is ONE JSON document containing `"ok": true` plus verb-specific fields; every write includes `"id"` (10-char commit SHA) and `"ledger"`; every cursor-consuming read includes `"cursor"`. Errors go to stderr as one JSON document `{"error","message","hint"}`.
- Renderer: absolute timestamps in projections; age strings only in `ls`/`--latest`/TTY convenience lines; C0 control chars (except `\n`) and ESC in note bodies render as visible escapes (`\r` → `^M`, `\x1b` → `^[`); note bodies in TTY renders are prefixed per line with `  | `.
- The tool never deletes anything under `refs/ledger/*`. No verb may have write side-effects except a well-formed write verb.
- After every successful write verb, run `git gc --auto` best-effort (ignore failures).
- Reads batch: one `git log` walk per ledger read; never per-event subprocesses for content (one `cat-file --batch` process fed all blob ids).

## File Structure

```
ledger/
  go.mod, main.go                     — module; cobra root, exit-code mapping
  internal/gitx/gitx.go(+_test)       — git exec layer, version check, identity env
  internal/model/model.go(+_test)     — Event, Meta, slug validation, origin capture
  internal/store/store.go(+_test)     — Store: resolution, Slugs, Events, Append (CAS), Transaction
  internal/fold/fold.go(+_test)       — Ledger fold: schema/require/spine/state/superseded_by
  internal/out/out.go(+_test)         — Emit/Fail envelope, TTY detection, age(), escapes
  internal/cmd/*.go(+_test)           — one file per verb family: create.go, set.go, note.go,
                                        vocab.go, close.go, read.go (status/show/notes/tail),
                                        cursor.go (since/watch), ls.go, port.go (export/import),
                                        initcmd.go, quickstart.go, resolve.go (pick-ledger)
  docs/quickstart.md, docs/quickstart-orchestrator.md   — embedded (go:embed)
  docs/admin.md                       — runbook printed/pointed to by init
  internal/docs/docs_test.go          — doc-examples harness + length budget
skills/using-ledger/SKILL.md          — companion skill (repo root, alongside ledger/)
```

Package dependency order: gitx ← model ← store ← fold ← cmd; out is leaf-usable everywhere. Tests at every layer use real git repos created by a shared helper.

---

### Task 1: Module scaffold + gitx exec layer

**Files:**
- Create: `ledger/go.mod`, `ledger/main.go`, `ledger/internal/gitx/gitx.go`
- Test: `ledger/internal/gitx/gitx_test.go`

**Interfaces:**
- Produces: `gitx.Repo{Dir string}` with method `Git(stdin string, args ...string) (stdout, stderr string, code int)` (runs `git -C r.Dir args...` when Dir != "", else in cwd; trims trailing newline from stdout/stderr). `gitx.CheckVersion() error` (nil if git ≥ 2.40; error text contains "git_too_old"). `gitx.IdentityArgs(author, committer string) []string` returning `["-c","user.name="+author, "-c","user.email=author@ledger.invalid", "-c","committer.name="+committer, "-c","committer.email=marker@ledger.invalid"]`.

- [ ] **Step 1: Scaffold module and write the failing test**

```bash
cd /Users/jesse/git/ledger-research && mkdir -p ledger/internal/gitx
cd ledger && go mod init ledger && go get github.com/spf13/cobra@latest
```

`ledger/internal/gitx/gitx_test.go`:
```go
package gitx

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestGitRunsInDir(t *testing.T) {
	dir := initRepo(t)
	r := Repo{Dir: dir}
	out, stderr, code := r.Git("", "rev-parse", "--is-inside-work-tree")
	if code != 0 || out != "true" {
		t.Fatalf("got %q %q %d", out, stderr, code)
	}
}

func TestGitStdinAndFailure(t *testing.T) {
	dir := initRepo(t)
	r := Repo{Dir: dir}
	blob, _, code := r.Git("hello", "hash-object", "-w", "--stdin")
	if code != 0 || len(blob) != 40 {
		t.Fatalf("hash-object: %q %d", blob, code)
	}
	_, stderr, code := r.Git("", "rev-parse", "-q", "--verify", "refs/nope")
	if code == 0 {
		t.Fatalf("expected nonzero, stderr=%q", stderr)
	}
}

func TestCheckVersion(t *testing.T) {
	if err := CheckVersion(); err != nil {
		t.Fatalf("system git should satisfy the floor: %v", err)
	}
}

func TestIdentityArgs(t *testing.T) {
	got := strings.Join(IdentityArgs("alice", "terminal"), " ")
	want := "-c user.name=alice -c user.email=author@ledger.invalid -c committer.name=terminal -c committer.email=marker@ledger.invalid"
	if got != want {
		t.Fatalf("got %q", got)
	}
	_ = os.Environ // keep imports honest if edited
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ledger && go test ./internal/gitx/`
Expected: FAIL — `Repo`, `CheckVersion`, `IdentityArgs` undefined.

- [ ] **Step 3: Implement gitx**

`ledger/internal/gitx/gitx.go`:
```go
// Package gitx is the only place the ledger touches git: a thin exec layer.
package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

type Repo struct{ Dir string }

func (r Repo) Git(stdin string, args ...string) (stdout, stderr string, code int) {
	full := args
	if r.Dir != "" {
		full = append([]string{"-C", r.Dir}, args...)
	}
	cmd := exec.Command("git", full...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code = 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return strings.TrimRight(so.String(), "\n"), strings.TrimRight(se.String(), "\n"), code
}

var versionOnce sync.Once
var versionErr error

// CheckVersion enforces the floor the spec delegates to implementation:
// update-ref --stdin transactions and per-refspec prune-fetch are load-bearing.
func CheckVersion() error {
	versionOnce.Do(func() {
		out, _, code := Repo{}.Git("", "--version")
		if code != 0 {
			versionErr = fmt.Errorf("git_too_old: git not found")
			return
		}
		f := strings.Fields(out) // "git version 2.50.1"
		if len(f) < 3 {
			return
		}
		parts := strings.Split(f[2], ".")
		if len(parts) < 2 {
			return
		}
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		if major < 2 || (major == 2 && minor < 40) {
			versionErr = fmt.Errorf("git_too_old: need git >= 2.40, found %s", f[2])
		}
	})
	return versionErr
}

func IdentityArgs(author, committer string) []string {
	return []string{
		"-c", "user.name=" + author, "-c", "user.email=author@ledger.invalid",
		"-c", "committer.name=" + committer, "-c", "committer.email=marker@ledger.invalid",
	}
}
```

`ledger/main.go` (placeholder wiring so the module builds; cobra arrives in Task 6):
```go
package main

import (
	"fmt"
	"os"

	"ledger/internal/gitx"
)

func main() {
	if err := gitx.CheckVersion(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run tests + build**

Run: `cd ledger && go test ./internal/gitx/ && go build ./...`
Expected: PASS, clean build.

- [ ] **Step 5: Commit**

```bash
git add ledger/go.mod ledger/go.sum ledger/main.go ledger/internal/gitx/
git commit -m "ledger: module scaffold and gitx exec layer"
```

---

### Task 2: Event/Meta model, slug validation, origin + author capture

**Files:**
- Create: `ledger/internal/model/model.go`
- Test: `ledger/internal/model/model_test.go`

**Interfaces:**
- Consumes: `gitx.Repo`.
- Produces:
```go
type Origin struct{ Host, CWD, Branch, Head, Session, SessionSource string; PID int }
type Event struct {
	TS string `json:"ts"`; Type string `json:"type"`
	Key string `json:"key,omitempty"`; Fields map[string]string `json:"fields,omitempty"`
	Kind string `json:"kind,omitempty"`; Text string `json:"text,omitempty"`
	Field string `json:"field,omitempty"`; Value string `json:"value,omitempty"` // vocab events
	Reason string `json:"reason,omitempty"`; LifecycleKind string `json:"lifecycle_kind,omitempty"` // close|superseded_by
	Successor string `json:"successor,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	Author string `json:"author"`; Origin Origin `json:"origin"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	ID string `json:"-"` // commit SHA prefix, set by store on read; JSON outputs add it explicitly
}
type Meta struct {
	Slug, Scope, Created, CreatedBy, Owner, Supersedes, Base string
	Fields map[string][]string `json:"fields"` // nil slice value = free field
	RequireEvidence map[string][]string `json:"require_evidence"`
}
func ValidSlug(s string) bool
func HarnessMarker() string                  // claude-code | codex | terminal
func ResolveAuthor(asFlag string) string     // --as > $LEDGER_AUTHOR > marker-if-harness > $USER
func CaptureOrigin(r gitx.Repo) Origin       // host, cwd, pid, branch (detached -> "(detached@<sha>)"), head, session env
func NewEvent(typ, author string, r gitx.Repo) Event  // ts=now UTC RFC3339-seconds, origin captured
```

- [ ] **Step 1: Write the failing test**

`ledger/internal/model/model_test.go`:
```go
package model

import (
	"os"
	"strings"
	"testing"

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
	if ev.Type != "set" || ev.Author != "alice" || len(ev.TS) != 19 { // 2026-08-13T21:00:00
		t.Fatalf("event: %+v", ev)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/model/` → FAIL (undefined symbols).

- [ ] **Step 3: Implement**

`ledger/internal/model/model.go`:
```go
// Package model defines the on-disk event/meta shapes and identity resolution.
package model

import (
	"os"
	"regexp"
	"strings"
	"time"

	"ledger/internal/gitx"
)

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func ValidSlug(s string) bool { return slugRE.MatchString(s) }

type Origin struct {
	Host string `json:"host"`; CWD string `json:"cwd"`; PID int `json:"pid"`
	Branch string `json:"branch"`; Head string `json:"head"`
	Session string `json:"session,omitempty"`; SessionSource string `json:"session_source,omitempty"`
}

type Event struct {
	TS string `json:"ts"`; Type string `json:"type"`
	Key string `json:"key,omitempty"`; Fields map[string]string `json:"fields,omitempty"`
	Kind string `json:"kind,omitempty"`; Text string `json:"text,omitempty"`
	Field string `json:"field,omitempty"`; Value string `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`; LifecycleKind string `json:"lifecycle_kind,omitempty"`
	Successor string `json:"successor,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	Author string `json:"author"`; Origin Origin `json:"origin"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	ID string `json:"-"`
}

type Meta struct {
	Slug string `json:"slug"`; Scope string `json:"scope"`
	Created string `json:"created"`; CreatedBy string `json:"created_by"`
	Owner string `json:"owner,omitempty"`; Supersedes string `json:"supersedes,omitempty"`
	Base string `json:"base,omitempty"`
	Fields map[string][]string `json:"fields"`
	RequireEvidence map[string][]string `json:"require_evidence,omitempty"`
}

func HarnessMarker() string {
	if os.Getenv("CLAUDECODE") != "" {
		return "claude-code"
	}
	if os.Getenv("CODEX_THREAD_ID") != "" {
		return "codex"
	}
	return "terminal"
}

func ResolveAuthor(asFlag string) string {
	if asFlag != "" {
		return asFlag
	}
	if a := os.Getenv("LEDGER_AUTHOR"); a != "" {
		return a
	}
	if m := HarnessMarker(); m != "terminal" {
		return m // an agent omitting --as must never sign as the human (spec, Identity)
	}
	return os.Getenv("USER")
}

func CaptureOrigin(r gitx.Repo) Origin {
	host, _ := os.Hostname()
	cwd, _ := os.Getwd()
	o := Origin{Host: host, CWD: cwd, PID: os.Getpid()}
	br, _, code := r.Git("", "symbolic-ref", "--short", "-q", "HEAD")
	head, _, _ := r.Git("", "rev-parse", "--short", "HEAD")
	o.Head = head
	if code == 0 && br != "" {
		o.Branch = br
	} else if head != "" {
		o.Branch = "(detached@" + head + ")"
	}
	for _, src := range []string{"CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID"} {
		if v := os.Getenv(src); v != "" {
			o.Session, o.SessionSource = v, src
			break
		}
	}
	return o
}

func NewEvent(typ, author string, r gitx.Repo) Event {
	return Event{TS: time.Now().UTC().Format("2006-01-02T15:04:05"), Type: typ,
		Author: author, Origin: CaptureOrigin(r)}
}

var _ = strings.TrimSpace // reserved for future normalizers
```

- [ ] **Step 4: Run** — `go test ./internal/model/` → PASS.

- [ ] **Step 5: Commit** — `git add ledger/internal/model/ && git commit -m "ledger: event/meta model, slug validation, identity capture"`

---

### Task 3: Store — resolution, append with CAS, batched reads, gc

**Files:**
- Create: `ledger/internal/store/store.go`
- Test: `ledger/internal/store/store_test.go`

**Interfaces:**
- Consumes: `gitx.Repo`, `model.Event/Meta`, `gitx.IdentityArgs`, `model.HarnessMarker`.
- Produces:
```go
type Expect int // ExpectPresent, ExpectAbsent
var ErrUnknownLedger, ErrSlugExists, ErrCASExhausted error  // sentinel errors, wrapped with slug
type Store struct{ Repo gitx.Repo }
func Resolve(storeFlag string) (Store, error)
	// --store flag > $LEDGER_DIR > nearest ancestor with .ledger.git or .git
	// (.ledger.git wins inside one dir); prints nothing itself — returns ChosenNote string too?  No:
	// returns (Store, note string, err) where note != "" when both kinds existed in the ancestry.
func (s Store) Slugs() ([]string, error)
func (s Store) Events(slug string) ([]model.Event, model.Meta, error)   // batched; Meta zero-value if absent
func (s Store) Append(slug string, ev model.Event, extra map[string]string, expect Expect) (id string, err error)
func (s Store) HeadID(slug string) (string, error)                       // 10-char sha
func (s Store) GCAuto()                                                  // best-effort, ignores errors
```
Actual signature for Resolve: `func Resolve(storeFlag string) (Store, string, error)`.

- [ ] **Step 1: Write the failing tests** (representative set; the helper mirrors gitx's)

`ledger/internal/store/store_test.go`:
```go
package store

import (
	"os/exec"
	"strings"
	"sync"
	"testing"

	"ledger/internal/gitx"
	"ledger/internal/model"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	return dir
}

func testStore(t *testing.T) Store { return Store{Repo: gitx.Repo{Dir: initRepo(t)}} }

func mkEvent(author string) model.Event {
	return model.Event{TS: "2026-08-13T00:00:00", Type: "set", Key: "k",
		Fields: map[string]string{"status": "done"}, Author: author}
}

func TestAppendCreateReadRoundtrip(t *testing.T) {
	s := testStore(t)
	meta := `{"slug":"demo","scope":"x","fields":{"status":["open","done"]}}`
	id, err := s.Append("demo", model.Event{TS: "t", Type: "create", Author: "a"},
		map[string]string{"meta.json": meta}, ExpectAbsent)
	if err != nil || len(id) != 10 {
		t.Fatalf("create: %v %q", err, id)
	}
	if _, err := s.Append("demo", model.Event{Type: "create", Author: "a"}, nil, ExpectAbsent); err == nil {
		t.Fatal("second create must fail ErrSlugExists")
	}
	if _, err := s.Append("nope", mkEvent("a"), nil, ExpectPresent); err == nil {
		t.Fatal("append to missing ledger must fail")
	}
	id2, err := s.Append("demo", mkEvent("alice"), nil, ExpectPresent)
	if err != nil {
		t.Fatal(err)
	}
	evs, meta2, err := s.Events("demo")
	if err != nil || len(evs) != 2 || meta2.Slug != "demo" {
		t.Fatalf("events: %v n=%d meta=%+v", err, len(evs), meta2)
	}
	if evs[1].ID != id2 || evs[1].Author != "alice" || evs[1].Fields["status"] != "done" {
		t.Fatalf("read back: %+v", evs[1])
	}
	head, _ := s.HeadID("demo")
	if head != id2 {
		t.Fatalf("head %q != %q", head, id2)
	}
}

func TestSyntheticCommitIdentity(t *testing.T) {
	s := testStore(t)
	s.Append("demo", model.Event{Type: "create", Author: "roleX"},
		map[string]string{"meta.json": "{}"}, ExpectAbsent)
	out, _, _ := s.Repo.Git("", "log", "-1", "--format=%an|%ae|%cn|%ce", "refs/ledger/demo")
	parts := strings.Split(out, "|")
	if parts[0] != "roleX" || parts[1] != "author@ledger.invalid" || parts[3] != "marker@ledger.invalid" {
		t.Fatalf("identity: %q", out) // committer name = harness marker; never gitconfig
	}
}

func TestConcurrentAppendCAS(t *testing.T) {
	s := testStore(t)
	s.Append("demo", model.Event{Type: "create", Author: "a"},
		map[string]string{"meta.json": "{}"}, ExpectAbsent)
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = s.Append("demo", mkEvent("w"), nil, ExpectPresent) }(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	evs, _, _ := s.Events("demo")
	if len(evs) != 11 { // create + 10; no losses, no duplicates
		t.Fatalf("got %d events", len(evs))
	}
}

func TestResolveOrder(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("LEDGER_DIR", "")
	st, note, err := Resolve(repo) // explicit flag wins
	if err != nil || st.Repo.Dir != repo || note != "" {
		t.Fatalf("%v %+v %q", err, st, note)
	}
	other := initRepo(t)
	t.Setenv("LEDGER_DIR", other)
	st, _, _ = Resolve("")
	if st.Repo.Dir != other {
		t.Fatalf("LEDGER_DIR should win over discovery: %q", st.Repo.Dir)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/store/` → FAIL.

- [ ] **Step 3: Implement**

`ledger/internal/store/store.go`:
```go
// Package store maps ledgers onto git phantom refs: refs/ledger/<slug>,
// one commit per event, CAS appends, batched reads.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ledger/internal/gitx"
	"ledger/internal/model"
)

type Expect int

const (
	ExpectPresent Expect = iota
	ExpectAbsent
)

var (
	ErrUnknownLedger = errors.New("unknown_ledger")
	ErrSlugExists    = errors.New("slug_exists")
	ErrCASExhausted  = errors.New("cas_exhausted")
)

type Store struct{ Repo gitx.Repo }

func ref(slug string) string { return "refs/ledger/" + slug }

// Resolve implements the spec's store-resolution order:
// --store flag > $LEDGER_DIR > nearest ancestor holding .ledger.git or .git
// (.ledger.git beats .git within one directory). note is non-empty when both
// kinds appear in the ancestry — callers print which store was chosen.
func Resolve(storeFlag string) (Store, string, error) {
	if storeFlag != "" {
		return storeFor(storeFlag), "", nil
	}
	if d := os.Getenv("LEDGER_DIR"); d != "" {
		return storeFor(d), "", nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Store{}, "", err
	}
	var sawOther bool
	for dir := cwd; ; dir = filepath.Dir(dir) {
		lg := filepath.Join(dir, ".ledger.git")
		gt := filepath.Join(dir, ".git")
		lgOK := exists(lg)
		gtOK := exists(gt)
		if lgOK && gtOK {
			return Store{Repo: gitx.Repo{Dir: lg}}, fmt.Sprintf("using store %s (a git repo is also here)", lg), nil
		}
		if lgOK {
			note := ""
			if sawOther {
				note = "using store " + lg
			}
			return Store{Repo: gitx.Repo{Dir: lg}}, note, nil
		}
		if gtOK {
			note := ""
			if sawOther {
				note = "using repo " + dir
			}
			return Store{Repo: gitx.Repo{Dir: dir}}, note, nil
		}
		sawOther = sawOther || lgOK || gtOK
		if dir == filepath.Dir(dir) {
			return Store{}, "", fmt.Errorf("no git repo or .ledger.git found from %s upward", cwd)
		}
	}
}

func storeFor(path string) Store {
	if strings.HasSuffix(path, ".ledger.git") || strings.HasSuffix(path, ".git") {
		return Store{Repo: gitx.Repo{Dir: path}}
	}
	if exists(filepath.Join(path, ".ledger.git")) {
		return Store{Repo: gitx.Repo{Dir: filepath.Join(path, ".ledger.git")}}
	}
	return Store{Repo: gitx.Repo{Dir: path}}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func (s Store) Slugs() ([]string, error) {
	out, stderr, code := s.Repo.Git("", "for-each-ref", "--format=%(refname)", "refs/ledger/")
	if code != 0 {
		return nil, fmt.Errorf("git_failed: %s", stderr)
	}
	var slugs []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		slugs = append(slugs, strings.TrimPrefix(line, "refs/ledger/"))
	}
	return slugs, nil
}

func (s Store) head(slug string) (string, bool) {
	out, _, code := s.Repo.Git("", "rev-parse", "-q", "--verify", ref(slug))
	return out, code == 0
}

func (s Store) HeadID(slug string) (string, error) {
	h, ok := s.head(slug)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	return h[:10], nil
}

// Events reads the whole chain with two subprocesses total:
// one `git log` for (commit, tree) pairs, one `cat-file --batch` for all blobs.
func (s Store) Events(slug string) ([]model.Event, model.Meta, error) {
	var meta model.Meta
	out, _, code := s.Repo.Git("", "log", "--reverse", "--format=%H %T", ref(slug))
	if code != 0 || out == "" {
		return nil, meta, fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
	}
	type pair struct{ commit, tree string }
	var pairs []pair
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		pairs = append(pairs, pair{f[0], f[1]})
	}
	// Resolve every tree's event.json (and creation meta.json) blob ids via ls-tree
	// on the batch of trees, then fetch blob contents in one cat-file --batch.
	var reqs []string
	blobOf := make([]map[string]string, len(pairs))
	for i, p := range pairs {
		lst, _, _ := s.Repo.Git("", "ls-tree", p.tree)
		m := map[string]string{}
		for _, l := range strings.Split(lst, "\n") {
			tab := strings.SplitN(l, "\t", 2)
			if len(tab) != 2 {
				continue
			}
			m[tab[1]] = strings.Fields(tab[0])[2]
		}
		blobOf[i] = m
		reqs = append(reqs, m["event.json"])
		if b, ok := m["meta.json"]; ok {
			reqs = append(reqs, b)
		}
	}
	contents := s.catBatch(reqs)
	evs := make([]model.Event, 0, len(pairs))
	for i, p := range pairs {
		var ev model.Event
		if err := json.Unmarshal([]byte(contents[blobOf[i]["event.json"]]), &ev); err != nil {
			continue // torn/foreign commit: skip, never crash a read
		}
		ev.ID = p.commit[:10]
		if b, ok := blobOf[i]["meta.json"]; ok {
			json.Unmarshal([]byte(contents[b]), &meta)
		}
		evs = append(evs, ev)
	}
	return evs, meta, nil
}

func (s Store) catBatch(ids []string) map[string]string {
	res := map[string]string{}
	if len(ids) == 0 {
		return res
	}
	out, _, _ := s.Repo.Git(strings.Join(ids, "\n"), "cat-file", "--batch")
	// format per object: "<sha> blob <size>\n<content>\n"
	rest := out
	for rest != "" {
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		hdr := strings.Fields(rest[:nl])
		rest = rest[nl+1:]
		if len(hdr) < 3 {
			continue
		}
		size := 0
		fmt.Sscanf(hdr[2], "%d", &size)
		if size > len(rest) {
			size = len(rest)
		}
		res[hdr[0]] = rest[:size]
		rest = strings.TrimPrefix(rest[size:], "\n")
	}
	return res
}

func (s Store) Append(slug string, ev model.Event, extra map[string]string, expect Expect) (string, error) {
	body, err := json.MarshalIndent(ev, "", " ")
	if err != nil {
		return "", err
	}
	for attempt := 0; attempt < 30; attempt++ {
		cur, ok := s.head(slug)
		if expect == ExpectAbsent && ok {
			return "", fmt.Errorf("%w: %s", ErrSlugExists, slug)
		}
		if expect == ExpectPresent && !ok {
			return "", fmt.Errorf("%w: %s", ErrUnknownLedger, slug)
		}
		entries := []string{}
		files := map[string]string{"event.json": string(body)}
		for k, v := range extra {
			files[k] = v
		}
		for name, content := range files {
			blob, se, code := s.Repo.Git(content, "hash-object", "-w", "--stdin")
			if code != 0 {
				return "", fmt.Errorf("git_failed: %s", se)
			}
			entries = append(entries, "100644 blob "+blob+"\t"+name)
		}
		tree, se, code := s.Repo.Git(strings.Join(entries, "\n")+"\n", "mktree")
		if code != 0 {
			return "", fmt.Errorf("git_failed: %s", se)
		}
		args := append(gitx.IdentityArgs(ev.Author, committerMarker(ev)),
			"commit-tree", tree, "-m", ev.Type+":"+ev.Key)
		if ok {
			args = append(args, "-p", cur)
		}
		csha, se, code := s.Repo.Git("", args...)
		if code != 0 {
			return "", fmt.Errorf("git_failed: %s", se)
		}
		old := cur // "" when creating
		if _, _, code := s.Repo.Git("", "update-ref", ref(slug), csha, old); code == 0 {
			s.GCAuto()
			return csha[:10], nil
		}
		time.Sleep(time.Duration(attempt) * 10 * time.Millisecond)
	}
	return "", ErrCASExhausted
}

func committerMarker(ev model.Event) string {
	if ev.Type == "import" {
		return "imported"
	}
	return model.HarnessMarker()
}

// GCAuto keeps stores packed: plumbing never triggers git's own auto-gc
// (measured: 3 loose objects per event, unbounded). Best-effort by design.
func (s Store) GCAuto() { s.Repo.Git("", "gc", "--auto", "--quiet") }
```

- [ ] **Step 4: Run** — `go test ./internal/store/ -count=1` → PASS (CAS test included).

- [ ] **Step 5: Commit** — `git add ledger/internal/store/ && git commit -m "ledger: phantom-ref store with CAS appends and batched reads"`

---

### Task 4: Fold — schema, vocab growth, spine, state, successor links

**Files:**
- Create: `ledger/internal/fold/fold.go`
- Test: `ledger/internal/fold/fold_test.go`

**Interfaces:**
- Consumes: `model.Event`, `model.Meta`.
- Produces:
```go
type Ledger struct {
	Slug string; Meta model.Meta
	Schema map[string][]string       // field -> vocab (nil = free)
	Require map[string][]string      // field -> values needing evidence
	Spine map[string]map[string]model.Event // key -> field -> latest set event
	State string                     // "open" | "closed:<reason>"
	SupersededBy string              // first superseded_by link in order, "" if none
	ExtraLinks []string              // later competing links (flagged by renders)
	Events []model.Event
}
func Fold(slug string, evs []model.Event, meta model.Meta) *Ledger
func (l *Ledger) Head() string
func (l *Ledger) Notes() []model.Event
```

- [ ] **Step 1: Write the failing test**

`ledger/internal/fold/fold_test.go`:
```go
package fold

import (
	"testing"

	"ledger/internal/model"
)

func ev(id, typ string, mut func(*model.Event)) model.Event {
	e := model.Event{ID: id, TS: "2026-08-13T00:00:0" + id[:1], Type: typ, Author: "a"}
	if mut != nil {
		mut(&e)
	}
	return e
}

func TestFoldSchemaSpineStateLinks(t *testing.T) {
	meta := model.Meta{Slug: "demo", Fields: map[string][]string{"status": {"open", "done"}},
		RequireEvidence: map[string][]string{"status": {"done"}}}
	evs := []model.Event{
		ev("1aaaaaaaaa", "create", nil),
		ev("2aaaaaaaaa", "set", func(e *model.Event) { e.Key = "t1"; e.Fields = map[string]string{"status": "open"} }),
		ev("3aaaaaaaaa", "vocab", func(e *model.Event) { e.Field = "status"; e.Value = "blocked" }),
		ev("4aaaaaaaaa", "set", func(e *model.Event) { e.Key = "t1"; e.Fields = map[string]string{"status": "blocked"} }),
		ev("5aaaaaaaaa", "note", func(e *model.Event) { e.Kind = "handoff"; e.Text = "hi" }),
		ev("6aaaaaaaaa", "lifecycle", func(e *model.Event) { e.LifecycleKind = "close"; e.Reason = "superseded" }),
		ev("7aaaaaaaaa", "lifecycle", func(e *model.Event) { e.LifecycleKind = "superseded_by"; e.Successor = "demo-2" }),
		ev("8aaaaaaaaa", "lifecycle", func(e *model.Event) { e.LifecycleKind = "superseded_by"; e.Successor = "demo-3" }),
	}
	l := Fold("demo", evs, meta)
	if got := l.Schema["status"]; len(got) != 3 || got[2] != "blocked" {
		t.Fatalf("vocab growth: %v", got)
	}
	if l.Spine["t1"]["status"].Fields["status"] != "blocked" {
		t.Fatalf("spine latest: %+v", l.Spine["t1"]["status"])
	}
	if l.State != "closed:superseded" {
		t.Fatalf("state: %q", l.State)
	}
	if l.SupersededBy != "demo-2" || len(l.ExtraLinks) != 1 || l.ExtraLinks[0] != "demo-3" {
		t.Fatalf("links: %q %v (first in total order wins; later links flagged)", l.SupersededBy, l.ExtraLinks)
	}
	if len(l.Notes()) != 1 || l.Head() != "8aaaaaaaaa" {
		t.Fatalf("notes/head")
	}
}

func TestFoldFreeFieldAndDefaults(t *testing.T) {
	meta := model.Meta{Fields: map[string][]string{"tag": nil}}
	l := Fold("x", []model.Event{ev("1aaaaaaaaa", "create", nil)}, meta)
	if v, ok := l.Schema["tag"]; !ok || v != nil {
		t.Fatalf("free field must fold as nil vocab: %v %v", v, ok)
	}
	if l.State != "open" || len(l.Spine) != 0 {
		t.Fatalf("empty fold: %+v", l)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/fold/` → FAIL.

- [ ] **Step 3: Implement**

`ledger/internal/fold/fold.go`:
```go
// Package fold derives current ledger state from the event chain.
// Everything mutable folds from events; meta.json holds only creation facts.
package fold

import "ledger/internal/model"

type Ledger struct {
	Slug string; Meta model.Meta
	Schema map[string][]string
	Require map[string][]string
	Spine map[string]map[string]model.Event
	State string
	SupersededBy string
	ExtraLinks []string
	Events []model.Event
}

func Fold(slug string, evs []model.Event, meta model.Meta) *Ledger {
	l := &Ledger{Slug: slug, Meta: meta, State: "open",
		Schema: map[string][]string{}, Require: map[string][]string{},
		Spine: map[string]map[string]model.Event{}, Events: evs}
	for f, v := range meta.Fields {
		if v == nil {
			l.Schema[f] = nil
		} else {
			l.Schema[f] = append([]string{}, v...)
		}
	}
	for f, v := range meta.RequireEvidence {
		l.Require[f] = append([]string{}, v...)
	}
	for _, ev := range evs {
		switch ev.Type {
		case "vocab":
			if cur, ok := l.Schema[ev.Field]; ok && cur != nil && !contains(cur, ev.Value) {
				l.Schema[ev.Field] = append(cur, ev.Value)
			}
		case "set":
			for f := range ev.Fields {
				if l.Spine[ev.Key] == nil {
					l.Spine[ev.Key] = map[string]model.Event{}
				}
				l.Spine[ev.Key][f] = ev
			}
		case "lifecycle":
			switch ev.LifecycleKind {
			case "close":
				if l.State == "open" { // first close in total order wins (spec: dueling closes)
					l.State = "closed:" + ev.Reason
				}
			case "superseded_by":
				if l.SupersededBy == "" {
					l.SupersededBy = ev.Successor // first link wins the redirect
				} else {
					l.ExtraLinks = append(l.ExtraLinks, ev.Successor)
				}
			}
		}
	}
	return l
}

func (l *Ledger) Head() string {
	if len(l.Events) == 0 {
		return ""
	}
	return l.Events[len(l.Events)-1].ID
}

func (l *Ledger) Notes() []model.Event {
	var out []model.Event
	for _, e := range l.Events {
		if e.Type == "note" {
			out = append(out, e)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run** — `go test ./internal/fold/` → PASS.

- [ ] **Step 5: Commit** — `git add ledger/internal/fold/ && git commit -m "ledger: state fold with vocab growth and successor links"`

---

### Task 5: Output layer — envelope, hints, TTY, ages, escaping

**Files:**
- Create: `ledger/internal/out/out.go`
- Test: `ledger/internal/out/out_test.go`

**Interfaces:**
- Produces:
```go
type CLIError struct{ Code, Message, Hint string; ExitCode int } // implements error
func Errf(code, hint string, exit int, format string, a ...any) *CLIError
func Emit(w io.Writer, tty bool, payload map[string]any, lines []string)  // payload gains "ok": true
func WriteError(w io.Writer, tty bool, e *CLIError)
func IsTTY(f *os.File) bool
func Age(ts string) string                 // "2h ago"; input format 2006-01-02T15:04:05
func EscapeControls(s string) string       // \r -> ^M, ESC -> ^[, other C0 (not \n\t) -> ^X
```
Verb funcs return `error`; `main.go` maps `*CLIError` → stderr JSON + exit code, other errors → `git_failed`/1.

- [ ] **Step 1: Write the failing test**

`ledger/internal/out/out_test.go`:
```go
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

func TestEmitTTYLines(t *testing.T) {
	var b bytes.Buffer
	Emit(&b, true, map[string]any{"id": "abc"}, []string{"line1", "line2"})
	if b.String() != "line1\nline2\n" {
		t.Fatalf("tty: %q", b.String())
	}
}

func TestWriteError(t *testing.T) {
	var b bytes.Buffer
	e := Errf("vocab_unknown", "ledger vocab add demo status x -m \"why\"", 4, "%q is bad", "x")
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
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/out/` → FAIL.

- [ ] **Step 3: Implement**

`ledger/internal/out/out.go`:
```go
// Package out is the single output pathway: one JSON document per command
// (non-TTY), aligned text on a TTY, errors as {error,message,hint}.
package out

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type CLIError struct {
	Code, Message, Hint string
	ExitCode            int
}

func (e *CLIError) Error() string { return e.Code + ": " + e.Message }

func Errf(code, hint string, exit int, format string, a ...any) *CLIError {
	return &CLIError{Code: code, Message: fmt.Sprintf(format, a...), Hint: hint, ExitCode: exit}
}

func Emit(w io.Writer, tty bool, payload map[string]any, lines []string) {
	if tty {
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
		return
	}
	payload["ok"] = true
	doc, _ := json.MarshalIndent(payload, "", " ")
	fmt.Fprintln(w, string(doc))
}

func WriteError(w io.Writer, tty bool, e *CLIError) {
	if tty {
		fmt.Fprintf(w, "error: %s\n", e.Message)
		if e.Hint != "" {
			fmt.Fprintf(w, "  fix: %s\n", e.Hint)
		}
		return
	}
	doc, _ := json.Marshal(map[string]string{"error": e.Code, "message": e.Message, "hint": e.Hint})
	fmt.Fprintln(w, string(doc))
}

func IsTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

func Age(ts string) string {
	t, err := time.Parse("2006-01-02T15:04:05", ts)
	if err != nil {
		return ts
	}
	s := int(time.Since(t.UTC()).Seconds())
	switch {
	case s >= 86400:
		return fmt.Sprintf("%dd ago", s/86400)
	case s >= 3600:
		return fmt.Sprintf("%dh ago", s/3600)
	case s >= 60:
		return fmt.Sprintf("%dm ago", s/60)
	default:
		return fmt.Sprintf("%ds ago", s)
	}
}

// EscapeControls neutralizes C0 controls and ESC so a note body can never
// visually overwrite the render on a TTY (counterfeit-provenance attack).
func EscapeControls(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == 0x1b:
			b.WriteString("^[")
		case r < 0x20:
			b.WriteString("^" + string(rune('@'+r)))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run** — `go test ./internal/out/` → PASS.

- [ ] **Step 5: Commit** — `git add ledger/internal/out/ && git commit -m "ledger: output envelope with hints, ages, control escaping"`

---

### Task 6: Cobra root, verb registry, error mapping, ledger resolution

**Files:**
- Create: `ledger/internal/cmd/root.go`, `ledger/internal/cmd/resolve.go`
- Modify: `ledger/main.go`
- Test: `ledger/internal/cmd/root_test.go`

**Interfaces:**
- Produces:
```go
// cmd package
func Execute() int   // builds root, runs, maps *out.CLIError -> stderr+code
type Ctx struct { Store store.Store; TTY bool; Stdout, Stderr io.Writer }
func NewCtx(storeFlag string) (*Ctx, error)
func (c *Ctx) PickLedger(ledgerFlag string) (*fold.Ledger, error)
	// explicit --ledger > sole open > no_open_ledger/ambiguous_ledger errors,
	// ambiguous hint lists slug (scope, last write age), recency-sorted.
func (c *Ctx) Load(slug string) (*fold.Ledger, error)
// every verb file registers itself via: func init() { register(newXCmd) } with
// var registry []func(*Ctx) *cobra.Command
```
- `main.go` becomes `func main(){ os.Exit(cmd.Execute()) }` (after `gitx.CheckVersion`).
- Root command help lists all verbs; unknown verbs produce `unknown_verb` error (cobra's default suggestion text is kept in the message).

- [ ] **Step 1: Write the failing test**

`ledger/internal/cmd/root_test.go`:
```go
package cmd

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	return dir
}

// run executes the CLI in-process against dir; returns stdout, stderr, exit code.
func run(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	var so, se bytes.Buffer
	code := ExecuteArgs(append([]string{"--store", dir}, args...), &so, &se)
	return so.String(), se.String(), code
}

func TestHelpIsSafeAndListsVerbs(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "--help")
	if code != 0 || !strings.Contains(so, "create") || !strings.Contains(so, "watch") {
		t.Fatalf("help: %d %q", code, so)
	}
	// the round-1 disaster: probing help must never write
	slugs, _, _ := run(t, dir, "ls")
	var doc map[string]any
	json.Unmarshal([]byte(slugs), &doc)
	if l, _ := doc["ledgers"].([]any); len(l) != 0 {
		t.Fatalf("help had side effects: %v", doc)
	}
}

func TestUnknownVerbErrors(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "bogus-verb")
	if code == 0 || !strings.Contains(se, "unknown_verb") {
		t.Fatalf("unknown verb: %d %q", code, se)
	}
}

func TestNoOpenLedgerError(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "status")
	if code != 4 || !strings.Contains(se, "no_open_ledger") || !strings.Contains(se, "create") {
		t.Fatalf("no_open_ledger with create hint: %d %q", code, se)
	}
}
```

Note for the implementer: `ExecuteArgs(args []string, stdout, stderr io.Writer) int` is the testable entry; `Execute()` wraps it with real os streams and `os.Args[1:]`. Tests always pass `--store <dir>` — a persistent root flag — so nothing depends on the test process's cwd.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/cmd/` → FAIL.

- [ ] **Step 3: Implement**

`ledger/internal/cmd/root.go`:
```go
// Package cmd wires the verbs. One rule from the spec's CLI surface contract:
// no invocation may have write side-effects except a well-formed write verb.
package cmd

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/gitx"
	"ledger/internal/out"
	"ledger/internal/store"
)

var registry []func(*Ctx) *cobra.Command

func register(f func(*Ctx) *cobra.Command) { registry = append(registry, f) }

func Execute() int {
	return ExecuteArgs(os.Args[1:], os.Stdout, os.Stderr)
}

func ExecuteArgs(args []string, stdout, stderr io.Writer) int {
	if err := gitx.CheckVersion(); err != nil {
		out.WriteError(stderr, false, out.Errf("git_too_old", "install git >= 2.40", 1, "%s", err))
		return 1
	}
	ctx := &Ctx{Stdout: stdout, Stderr: stderr, TTY: false}
	if f, ok := stdout.(*os.File); ok {
		ctx.TTY = out.IsTTY(f)
	}
	root := &cobra.Command{Use: "ledger", Short: "Durable working-state for coding agents, stored in git phantom refs",
		SilenceUsage: true, SilenceErrors: true,
		Long: "Durable working-state for coding agents.\nEvery write prints its event id — that id is a cursor for since/watch.\nRun `ledger quickstart` for agent doctrine."}
	var storeFlag string
	root.PersistentFlags().StringVar(&storeFlag, "store", "", "store location (default: nearest .ledger.git or git repo)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Name() == "help" {
			return nil
		}
		st, note, err := store.Resolve(storeFlag)
		if err != nil {
			return out.Errf("unknown_ledger", "run inside a git repo, or `ledger init` in a plain directory", 4, "%s", err)
		}
		if note != "" && ctx.TTY {
			io.WriteString(stderr, note+"\n")
		}
		ctx.Store = st
		return nil
	}
	for _, f := range registry {
		root.AddCommand(f(ctx))
	}
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil {
		return 0
	}
	var ce *out.CLIError
	if errors.As(err, &ce) {
		out.WriteError(stderr, ctx.TTY, ce)
		return ce.ExitCode
	}
	msg := err.Error()
	if strings.Contains(msg, "unknown command") {
		out.WriteError(stderr, ctx.TTY, out.Errf("unknown_verb", "run `ledger --help` for the verb list", 4, "%s", msg))
		return 4
	}
	out.WriteError(stderr, ctx.TTY, out.Errf("git_failed", "", 1, "%s", msg))
	return 1
}
```

`ledger/internal/cmd/resolve.go`:
```go
package cmd

import (
	"fmt"
	"io"
	"sort"

	"ledger/internal/fold"
	"ledger/internal/out"
	"ledger/internal/store"
)

type Ctx struct {
	Store  store.Store
	TTY    bool
	Stdout io.Writer
	Stderr io.Writer
}

func (c *Ctx) Load(slug string) (*fold.Ledger, error) {
	evs, meta, err := c.Store.Events(slug)
	if err != nil {
		return nil, out.Errf("unknown_ledger", "ledger ls --all  (lists every ledger here)", 4, "no ledger '%s' here", slug)
	}
	return fold.Fold(slug, evs, meta), nil
}

func (c *Ctx) PickLedger(ledgerFlag string) (*fold.Ledger, error) {
	if ledgerFlag != "" {
		return c.Load(ledgerFlag)
	}
	slugs, err := c.Store.Slugs()
	if err != nil {
		return nil, out.Errf("git_failed", "", 1, "%s", err)
	}
	var opens []*fold.Ledger
	for _, s := range slugs {
		l, err := c.Load(s)
		if err == nil && l.State == "open" {
			opens = append(opens, l)
		}
	}
	switch len(opens) {
	case 1:
		return opens[0], nil
	case 0:
		return nil, out.Errf("no_open_ledger",
			"ledger create <slug> --scope <what-it-tracks>  starts one; ledger ls --all lists closed ones",
			4, "no open ledgers in this repo")
	}
	sort.Slice(opens, func(i, j int) bool { return opens[i].Events[len(opens[i].Events)-1].TS > opens[j].Events[len(opens[j].Events)-1].TS })
	list := ""
	for i, l := range opens {
		if i > 0 {
			list += "; "
		}
		list += fmt.Sprintf("%s (%s, last write %s)", l.Slug, l.Meta.Scope, out.Age(l.Events[len(l.Events)-1].TS))
	}
	return nil, out.Errf("ambiguous_ledger", "add --ledger <slug>. Open: "+list, 4,
		"%d ledgers are open — say which one", len(opens))
}
```

Modify `ledger/main.go`:
```go
package main

import (
	"os"

	"ledger/internal/cmd"
)

func main() { os.Exit(cmd.Execute()) }
```

Also create a stub `ls` verb in this task so the tests compile (full `ls` behavior lands in Task 11 — the stub already satisfies "announce empty results"):
`ledger/internal/cmd/ls.go`:
```go
package cmd

import (
	"github.com/spf13/cobra"
)

func init() { register(newLsCmd) }

func newLsCmd(c *Ctx) *cobra.Command {
	var all bool
	cmd := &cobra.Command{Use: "ls", Short: "list ledgers with freshness", RunE: func(_ *cobra.Command, _ []string) error {
		return runLs(c, all)
	}}
	cmd.Flags().BoolVar(&all, "all", false, "include closed ledgers")
	return cmd
}
```
with `runLs` in the same file returning the empty-announce behavior for now (Task 11 fleshes it out):
```go
func runLs(c *Ctx, all bool) error {
	slugs, err := c.Store.Slugs()
	if err != nil {
		return err
	}
	rows := []map[string]any{}
	_ = all // full filtering in the ls task
	for range slugs {
	}
	if len(slugs) == 0 {
		outEmit(c, map[string]any{"ledgers": rows}, []string{"no ledgers in this repo — ledger create <slug> --scope <ref> starts one"})
		return nil
	}
	outEmit(c, map[string]any{"ledgers": rows}, []string{"(ls arrives fully in a later task)"})
	return nil
}
```
and a shared helper in `resolve.go`:
```go
func outEmit(c *Ctx, payload map[string]any, lines []string) { out.Emit(c.Stdout, c.TTY, payload, lines) }
```
Add a `status` stub the same way (registers, resolves via PickLedger, emits nothing yet) so `TestNoOpenLedgerError` exercises resolution:
```go
// in resolve.go for now; moved to read.go in Task 9
func init() { register(newStatusStub) }
func newStatusStub(c *Ctx) *cobra.Command {
	var ledgerFlag string
	cmd := &cobra.Command{Use: "status [key]", Short: "the spine: latest value per item", RunE: func(_ *cobra.Command, _ []string) error {
		_, err := c.PickLedger(ledgerFlag)
		return err
	}}
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	return cmd
}
```

- [ ] **Step 4: Run** — `go test ./internal/cmd/ && go build ./...` → PASS.

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: cobra root, error mapping, ledger resolution, safe help"`

---

### Task 7: create, vocab add, close, supersede (two-ref transaction)

**Files:**
- Create: `ledger/internal/cmd/create.go`, `ledger/internal/cmd/vocab.go`, `ledger/internal/cmd/close.go`
- Modify: `ledger/internal/store/store.go` (add `Transaction`)
- Test: `ledger/internal/cmd/lifecycle_test.go`, extend `ledger/internal/store/store_test.go`

**Interfaces:**
- Consumes: everything prior.
- Produces:
  - `store.Transaction(steps []TxStep) error` where `type TxStep struct{ Ref, New, Old string }` — one atomic `git update-ref --stdin` transaction (`start/prepare/commit` protocol; any CAS mismatch aborts the whole transaction). Used by `create --supersedes`.
  - CLI: `ledger create <slug> --scope S [--field NAME=V1,V2]... [--require-evidence FIELD=V1,V2]... [--owner O] [--supersedes OLD] [--as R] [-m TEXT]`; `ledger vocab add <slug> <field> <value> [-m why] [--as R]`; `ledger close <slug> --as-state shipped|abandoned|superseded [--superseded-by SLUG] [-m TEXT] [--as R]`.
  - Success payloads: create → `{id, ledger, created:true, fields, require_evidence}` (+ TTY line shows the schema and "first cursor: <id>"); vocab → `{id, ledger, vocab:{field:value}}`; close → `{id, ledger, closed:<state>}`.
  - Behavior contracts: default fields `status=open,done,failed,blocked` when no `--field`; `--require-evidence` on undeclared field → `unknown_field`; bad slug → `bad_slug` with grammar hint; existing slug (open OR closed) → `slug_exists`; `close --as-state superseded` without `--superseded-by` → `needs_successor`; closed ledger: `vocab add` → `closed`; `create --supersedes old` appends `superseded_by` (+`close` if old still open) to old and creates new, atomically; retried create completes a crash-dangled supersede (tested by pre-writing the link then creating).

- [ ] **Step 1: Write the failing tests**

`ledger/internal/cmd/lifecycle_test.go`:
```go
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("bad json: %v\n%s", err, s)
	}
	return doc
}

func TestCreateDefaultsAndEcho(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "create", "demo", "--scope", "test", "--as", "me")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	if doc["id"] == nil || doc["ledger"] != "demo" {
		t.Fatalf("create envelope: %v", doc)
	}
	fields := doc["fields"].(map[string]any)
	if _, ok := fields["status"]; !ok {
		t.Fatalf("default vocab missing: %v", fields)
	}
}

func TestCreateRejections(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "create", "UPPER", "--scope", "x")
	if code != 4 || !strings.Contains(se, "bad_slug") {
		t.Fatalf("%d %s", code, se)
	}
	run(t, dir, "create", "demo", "--scope", "x")
	_, se, code = run(t, dir, "create", "demo", "--scope", "x")
	if code != 4 || !strings.Contains(se, "slug_exists") {
		t.Fatalf("existing open slug: %s", se)
	}
	run(t, dir, "close", "demo", "--as-state", "abandoned")
	_, se, code = run(t, dir, "create", "demo", "--scope", "x")
	if code != 4 || !strings.Contains(se, "slug_exists") {
		t.Fatalf("closed slugs are never reused: %s", se)
	}
	_, se, _ = run(t, dir, "create", "d2", "--scope", "x", "--require-evidence", "review=approved")
	if !strings.Contains(se, "unknown_field") {
		t.Fatalf("require-evidence undeclared field: %s", se)
	}
}

func TestVocabAddAndClosedRules(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "demo", "--scope", "x", "--field", "status=open,done")
	so, _, code := run(t, dir, "vocab", "add", "demo", "status", "blocked", "-m", "needed")
	if code != 0 {
		t.Fatal(so)
	}
	run(t, dir, "close", "demo", "--as-state", "shipped")
	_, se, code := run(t, dir, "vocab", "add", "demo", "status", "later")
	if code != 4 || !strings.Contains(se, "closed") {
		t.Fatalf("vocab on closed: %s", se)
	}
}

func TestCloseSupersededNeedsSuccessor(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	_, se, code := run(t, dir, "close", "old", "--as-state", "superseded")
	if code != 4 || !strings.Contains(se, "needs_successor") {
		t.Fatalf("%d %s", code, se)
	}
}

func TestCreateSupersedes(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	so, _, code := run(t, dir, "create", "new", "--scope", "x", "--supersedes", "old")
	if code != 0 {
		t.Fatal(so)
	}
	// old is closed:superseded with a link to new (verified via status in Task 9;
	// here, assert through a raw read helper)
	so, _, _ = run(t, dir, "ls", "--all")
	if !strings.Contains(so, "old") || !strings.Contains(so, "new") {
		t.Fatal(so)
	}
}

func TestCreateSupersedesAlreadyClosed(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	run(t, dir, "close", "old", "--as-state", "abandoned")
	_, _, code := run(t, dir, "create", "recovery", "--scope", "x", "--supersedes", "old")
	if code != 0 {
		t.Fatal("supersede against an already-closed predecessor is the wrongful-close recovery and must work")
	}
}
```

Store transaction test (append to `store_test.go`):
```go
func TestTransactionAtomicity(t *testing.T) {
	s := testStore(t)
	s.Append("a", model.Event{Type: "create", Author: "x"}, map[string]string{"meta.json": "{}"}, ExpectAbsent)
	headA, _ := s.HeadID("a") // 10 chars; Transaction wants full shas — use rev-parse
	full, _, _ := s.Repo.Git("", "rev-parse", "refs/ledger/a")
	_ = headA
	// build a commit for ref b without updating any ref
	blob, _, _ := s.Repo.Git("{}", "hash-object", "-w", "--stdin")
	tree, _, _ := s.Repo.Git("100644 blob "+blob+"\tevent.json\n", "mktree")
	c1, _, _ := s.Repo.Git("", append(gitx.IdentityArgs("t", "terminal"), "commit-tree", tree, "-m", "x")...)
	// stale Old for ref a => whole transaction must abort; ref b must NOT be created
	err := s.Transaction([]TxStep{
		{Ref: "refs/ledger/b", New: c1, Old: ""},
		{Ref: "refs/ledger/a", New: c1, Old: strings.Repeat("0", 40)},
	})
	if err == nil {
		t.Fatal("stale CAS must abort")
	}
	if _, ok := s.head("b"); ok {
		t.Fatal("aborted transaction leaked ref b")
	}
	// correct Old commits both
	if err := s.Transaction([]TxStep{
		{Ref: "refs/ledger/b", New: c1, Old: ""},
		{Ref: "refs/ledger/a", New: c1, Old: full},
	}); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/... ` → FAIL.

- [ ] **Step 3: Implement**

`store.Transaction` (append to store.go):
```go
type TxStep struct{ Ref, New, Old string }

// Transaction commits all steps or none, via git's ref-transaction protocol.
func (s Store) Transaction(steps []TxStep) error {
	var b strings.Builder
	b.WriteString("start\n")
	for _, st := range steps {
		b.WriteString(fmt.Sprintf("update %s %s %s\n", st.Ref, st.New, st.Old))
	}
	b.WriteString("prepare\ncommit\n")
	_, stderr, code := s.Repo.Git(b.String(), "update-ref", "--stdin")
	if code != 0 {
		return fmt.Errorf("transaction aborted: %s", stderr)
	}
	return nil
}
```

`ledger/internal/cmd/create.go` (core logic; the supersede path builds the predecessor's link commit with `commit-tree` exactly as `Append` does, then runs one `Transaction` covering both refs — extract the commit-building portion of `Append` into `(s Store) buildCommit(slug parent string, ev model.Event, extra map[string]string) (sha string, err error)` in store.go and reuse it from both `Append` and this path):
```go
package cmd

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

func init() { register(newCreateCmd) }

func newCreateCmd(c *Ctx) *cobra.Command {
	var scope, owner, supersedes, asFlag, mFlag string
	var fields, reqEv []string
	cmd := &cobra.Command{Use: "create <slug>", Short: "start a new ledger with declared fields",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runCreate(c, args[0], scope, owner, supersedes, asFlag, mFlag, fields, reqEv)
		}}
	cmd.Flags().StringVar(&scope, "scope", "", "what this ledger tracks")
	cmd.MarkFlagRequired("scope")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "NAME=V1,V2 (empty after '=' = free text); repeatable")
	cmd.Flags().StringArrayVar(&reqEv, "require-evidence", nil, "FIELD=V1,V2: these values hard-error without --evidence")
	cmd.Flags().StringVar(&owner, "owner", "", "recorded owner (not enforced in v1)")
	cmd.Flags().StringVar(&supersedes, "supersedes", "", "predecessor slug to close and link")
	cmd.Flags().StringVar(&asFlag, "as", "", "author identity")
	cmd.Flags().StringVarP(&mFlag, "m", "m", "", "short annotation")
	return cmd
}

func runCreate(c *Ctx, slug, scope, owner, supersedes, asFlag, mFlag string, fieldSpecs, reqSpecs []string) error {
	if !model.ValidSlug(slug) {
		return out.Errf("bad_slug", "slugs are lowercase-kebab: [a-z0-9][a-z0-9-]*, max 64 chars", 4,
			"'%s' is not a valid slug", slug)
	}
	fields := map[string][]string{}
	for _, spec := range fieldSpecs {
		name, vals, _ := strings.Cut(spec, "=")
		var vv []string
		for _, v := range strings.Split(vals, ",") {
			if v != "" {
				vv = append(vv, v)
			}
		}
		fields[name] = vv // nil = free
	}
	if len(fields) == 0 {
		fields = map[string][]string{"status": {"open", "done", "failed", "blocked"}}
	}
	require := map[string][]string{}
	for _, spec := range reqSpecs {
		f, vals, _ := strings.Cut(spec, "=")
		if _, ok := fields[f]; !ok {
			return out.Errf("unknown_field", "declared fields: "+keys(fields), 4,
				"--require-evidence names '%s', which is not a declared field", f)
		}
		require[f] = strings.Split(vals, ",")
	}
	author := model.ResolveAuthor(asFlag)
	ev := model.NewEvent("create", author, c.Store.Repo)
	ev.Text = mFlag
	base, _, _ := c.Store.Repo.Git("", "rev-parse", "--short", "HEAD")
	meta := model.Meta{Slug: slug, Scope: scope, Created: ev.TS, CreatedBy: author,
		Owner: owner, Supersedes: supersedes, Base: base, Fields: fields, RequireEvidence: require}
	mb, _ := json.MarshalIndent(meta, "", " ")

	var id string
	var err error
	if supersedes == "" {
		id, err = c.Store.Append(slug, ev, map[string]string{"meta.json": string(mb)}, store.ExpectAbsent)
	} else {
		id, err = c.createSuperseding(slug, supersedes, ev, string(mb), author)
	}
	if err != nil {
		return mapStoreErr(err, slug)
	}
	payload := map[string]any{"id": id, "ledger": slug, "created": true,
		"fields": fields, "require_evidence": require}
	lines := []string{"[" + id + "] created " + slug, "  first cursor: " + id}
	outEmit(c, payload, lines)
	return nil
}

func keys(m map[string][]string) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return strings.Join(ks, ", ")
}

func mapStoreErr(err error, slug string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "slug_exists"):
		return out.Errf("slug_exists", "ledger ls --all — then pick a new slug, e.g. "+slug+"-2", 4,
			"ledger '%s' already exists (slugs are never reused)", slug)
	case strings.Contains(msg, "unknown_ledger"):
		return out.Errf("unknown_ledger", "ledger ls --all", 4, "no ledger '%s' here", slug)
	}
	return err
}
```
`createSuperseding` (same file): load predecessor (`c.Load`); build its link commit — a `superseded_by` lifecycle event (plus a preceding `close` event with reason `superseded` folded into the same commit chain when the predecessor is still open: build close commit parented on old head, then link commit parented on the close commit); build the new ledger's creation commit (no parent, `meta.json` attached); run ONE `store.Transaction` updating `refs/ledger/<old>` (Old = its current full sha, New = link commit) and creating `refs/ledger/<slug>` (Old = "", New = creation commit). On transaction abort, retry once from fresh heads; a pre-existing dangling link (crash recovery) is detected by folding the predecessor — if its `SupersededBy` already names `slug`, skip the predecessor step and only create.

`vocab.go` and `close.go` follow the same shape (PickLedger not needed — positional slug; validate open state via `c.Load(slug).State`; close builds one event via `Append`; `--as-state superseded` requires `--superseded-by` and then appends BOTH the close event and the `superseded_by` link event in two appends — same commit chain, no transaction needed since it's one ref):
```go
// close.go core
if asState == "superseded" && supersededBy == "" {
	return out.Errf("needs_successor", "add --superseded-by <slug> (the redirect is the load-bearing pointer)",
		4, "closing as superseded requires the successor link")
}
```

- [ ] **Step 4: Run** — `go test ./internal/... -count=1` → PASS.

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: create/vocab/close with supersede transaction"`

---

### Task 8: set + note — validation, evidence, idempotency, body sources

**Files:**
- Create: `ledger/internal/cmd/set.go`, `ledger/internal/cmd/note.go`
- Test: `ledger/internal/cmd/write_test.go`

**Interfaces:**
- Consumes: PickLedger, Load, store.Append, fold.
- Produces:
  - `ledger set <key> <FIELD=VALUE | VALUE>... [--ledger S] [--as R] [-m TEXT] [--evidence T:R]... [--idempotency-key K]`
  - `ledger note [-k KIND] [--key KEY] [--from-file P] [--ledger S] [--as R] [-m TEXT] [--evidence T:R]...` (body: exactly one of -m / --from-file / stdin)
  - Contracts: bare value → first declared field **in meta declaration order** (store field order: `Meta.Fields` is a map — add `FieldOrder []string` to Meta, written at create, so "first declared" is deterministic; create.go records it); value starting with `-` → `bad_value`; undeclared field → `unknown_field` listing declared; enum miss → `vocab_unknown` with exact `ledger vocab add <slug> <field> <value> -m "why"` hint; required-evidence miss → `evidence_required`; closed ledger: set → `closed` (hint names successor when one exists), note → allowed; multi-field sets are one event; idempotency dedupe scope = (author, key) over full history, dedupe response `{id, deduped:true, by}`; note body conflict → `conflicting_body`; empty body → `empty_body`.

- [ ] **Step 1: Write the failing tests**

`ledger/internal/cmd/write_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func setup(t *testing.T) string {
	dir := initRepo(t)
	run(t, dir, "create", "demo", "--scope", "test",
		"--field", "status=open,done,failed", "--field", "review=pending,approved",
		"--require-evidence", "status=done")
	return dir
}

func TestSetBareAndMultiField(t *testing.T) {
	dir := setup(t)
	so, _, code := run(t, dir, "set", "t1", "open", "review=pending", "--as", "impl")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	f := doc["fields"].(map[string]any)
	if f["status"] != "open" || f["review"] != "pending" {
		t.Fatalf("bare value must hit first declared field: %v", f)
	}
}

func TestSetRejections(t *testing.T) {
	dir := setup(t)
	_, se, code := run(t, dir, "set", "t1", "done", "--as", "impl")
	if code != 4 || !strings.Contains(se, "evidence_required") {
		t.Fatalf("%s", se)
	}
	so, _, code := run(t, dir, "set", "t1", "done", "--evidence", "commit:abc123", "--as", "impl")
	if code != 0 {
		t.Fatal(so)
	}
	_, se, _ = run(t, dir, "set", "t1", "wat", "--as", "impl")
	if !strings.Contains(se, "vocab_unknown") || !strings.Contains(se, "ledger vocab add demo status wat") {
		t.Fatalf("hint must be the exact command: %s", se)
	}
	_, se, _ = run(t, dir, "set", "t1", "severity=high", "--as", "impl")
	if !strings.Contains(se, "unknown_field") || !strings.Contains(se, "status") {
		t.Fatalf("%s", se)
	}
	_, se, _ = run(t, dir, "set", "t1", "--as")
	_ = se // cobra arg error; just must not panic or write
}

func TestIdempotencyAuthorScoped(t *testing.T) {
	dir := setup(t)
	so1, _, _ := run(t, dir, "set", "t1", "open", "--as", "a", "--idempotency-key", "t1-open")
	so2, _, _ := run(t, dir, "set", "t1", "open", "--as", "a", "--idempotency-key", "t1-open")
	d1, d2 := mustJSON(t, so1), mustJSON(t, so2)
	if d2["deduped"] != true || d2["id"] != d1["id"] {
		t.Fatalf("same author+key must dedupe: %v", d2)
	}
	so3, _, _ := run(t, dir, "set", "t1", "failed", "--as", "b", "--idempotency-key", "t1-open")
	if mustJSON(t, so3)["deduped"] == true {
		t.Fatal("different author must NOT dedupe (spec: author-scoped)")
	}
}

func TestNoteBodySources(t *testing.T) {
	dir := setup(t)
	_, se, code := run(t, dir, "note", "-k", "handoff", "-m", "short", "--from-file", "/dev/null")
	if code != 4 || !strings.Contains(se, "conflicting_body") {
		t.Fatalf("%s", se)
	}
	so, _, code := run(t, dir, "note", "-k", "gotcha", "--key", "t1", "-m", "trap here", "--as", "x")
	if code != 0 || mustJSON(t, so)["kind"] != "gotcha" {
		t.Fatal(so)
	}
	_, se, code = run(t, dir, "note", "-k", "x", "-m", "  ")
	if code != 4 || !strings.Contains(se, "empty_body") {
		t.Fatalf("%s", se)
	}
}

func TestClosedLedgerRules(t *testing.T) {
	dir := setup(t)
	run(t, dir, "close", "demo", "--as-state", "abandoned")
	_, se, code := run(t, dir, "set", "t1", "open", "--ledger", "demo")
	if code != 4 || !strings.Contains(se, "closed") {
		t.Fatalf("%s", se)
	}
	_, _, code = run(t, dir, "note", "-k", "postmortem", "-m", "lessons", "--ledger", "demo")
	if code != 0 {
		t.Fatal("closed ledgers accept notes")
	}
}
```

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement** `set.go`/`note.go` per the contracts above. Core of set's validation:

```go
func runSet(c *Ctx, key string, assignments []string, o writeOpts) error {
	led, err := c.PickLedger(o.ledger)
	if err != nil {
		return err
	}
	if led.State != "open" {
		hint := "closed ledgers accept only notes; for new work: ledger create <new-slug> --scope <ref>"
		if led.SupersededBy != "" {
			hint = "this ledger is superseded by '" + led.SupersededBy + "' — write there"
		}
		return out.Errf("closed", hint, 4, "'%s' is %s and refuses new field values", led.Slug, led.State)
	}
	first := ""
	if len(led.Meta.FieldOrder) > 0 {
		first = led.Meta.FieldOrder[0]
	}
	fields := map[string]string{}
	for _, spec := range assignments {
		f, v, cut := strings.Cut(spec, "=")
		if !cut {
			f, v = first, spec
		}
		if strings.HasPrefix(v, "-") {
			return out.Errf("bad_value", "write it as field=value", 4, "'%s' looks like a flag, not a value", v)
		}
		vocab, declared := led.Schema[f]
		if !declared {
			return out.Errf("unknown_field", "declared: "+strings.Join(led.Meta.FieldOrder, ", "), 4,
				"'%s' is not a declared field on '%s'", f, led.Slug)
		}
		if vocab != nil && !contains(vocab, v) {
			return out.Errf("vocab_unknown",
				fmt.Sprintf("ledger vocab add %s %s %s -m \"why this value is needed\"  — then re-run this set", led.Slug, f, v),
				4, "%q is not in %s's vocabulary (valid: %s)", v, f, strings.Join(vocab, ", "))
		}
		if contains(led.Require[f], v) && len(o.evidence) == 0 {
			return out.Errf("evidence_required", "re-run with --evidence commit:<range> | run:<id> | file:<path>", 4,
				"%s=%s requires evidence on '%s'", f, v, led.Slug)
		}
		fields[f] = v
	}
	if o.idemKey != "" {
		author := model.ResolveAuthor(o.as)
		for _, ev := range led.Events {
			if ev.Type == "set" && ev.IdempotencyKey == o.idemKey && ev.Author == author {
				outEmit(c, map[string]any{"id": ev.ID, "ledger": led.Slug, "deduped": true, "by": ev.Author},
					[]string{"deduped against " + ev.ID})
				return nil
			}
		}
	}
	ev := model.NewEvent("set", model.ResolveAuthor(o.as), c.Store.Repo)
	ev.Key, ev.Fields, ev.Text, ev.Evidence, ev.IdempotencyKey = key, fields, o.m, o.evidence, o.idemKey
	id, err := c.Store.Append(led.Slug, ev, nil, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, led.Slug)
	}
	outEmit(c, map[string]any{"id": id, "ledger": led.Slug, "key": key, "fields": fields},
		[]string{"[" + id + "] " + led.Slug + ": " + key + " " + renderFields(fields)})
	return nil
}
```
(`writeOpts{ledger, as, m string; evidence []string; idemKey string}`, `contains`, `renderFields` are small helpers in the same file. `Meta.FieldOrder []string` json:"field_order" is added to model.Meta and populated by create.go in declaration order — update Task 2's struct and Task 7's create accordingly; this is the one cross-task edit and it happens in this task with its own test assertion via TestSetBareAndMultiField.)

note.go body resolution:
```go
switch {
case o.m != "" && fromFile != "":
	return out.Errf("conflicting_body", "use --from-file for the body (drop -m), or -m alone for a short body",
		4, "a note has one body source; you gave both -m and --from-file")
case o.m != "":
	body = o.m
case fromFile != "":
	b, err := os.ReadFile(fromFile)
	if err != nil { return out.Errf("git_failed", "", 1, "%s", err) }
	body = string(b)
default:
	b, _ := io.ReadAll(stdin) // cmd.InOrStdin()
	body = string(b)
}
if strings.TrimSpace(body) == "" {
	return out.Errf("empty_body", `provide it with -m "...", --from-file <path>, or on stdin`, 4, "the note body is empty")
}
```

- [ ] **Step 4: Run** — `go test ./internal/... -count=1` → PASS.

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: set and note with validation, evidence, idempotency"`

---

### Task 9: Reads — status (spine/drill-down/by-branch), show, notes, tail

**Files:**
- Create: `ledger/internal/cmd/read.go` (replaces the Task-6 status stub in resolve.go — delete the stub)
- Test: `ledger/internal/cmd/read_test.go`

**Interfaces:**
- Consumes: PickLedger, fold.Ledger.
- Produces:
  - `status [key] [--field F] [--by-branch] [--ledger S]` — no key: `{ledger, scope, state, rows:[{key,field,value,note,by,branch,ts,id,evidence}]}` (rows sorted key then field; TTY rows show `(no evidence)` marker and the `-m` note text). With key: `{ledger, key, values:{field:row}, notes:[...], history:[last 8 events for key]}`; unknown key → `unknown_key` with known-keys hint. `--by-branch`: rows keyed (key, field, branch), latest per branch.
  - `show [--ledger S]` — status payload + `schema`, `require_evidence`, `recent_notes` (last 5: id/kind/by/ts/first_line), `events` count, `head`; superseded ledgers **lead with the redirect** in TTY and include `superseded_by` + `extra_links` in JSON; TTY identity line includes author provenance `by <author> (via <committer>)` for notes of kind ruling/standing-rule (committer read via one extra `git log --format=%cn` batch per render — store exposes `Committers(slug) map[id]string`).
  - `notes [-k K] [--key KEY] [--id SHA] [--latest] [-n N] [--ledger S]` — `{notes:[{id,kind,key,by,ts,text}]}`; `--latest` TTY output leads with age+author; bodies control-escaped and `  | ` prefixed on TTY.
  - `tail [-n N] [--ledger S]` — `{events:[...], cursor:<head>}` (events include `id`).
- Store addition: `func (s Store) Committers(slug string) (map[string]string, error)` — one `git log --format=%H %cn` pass.

- [ ] **Step 1: Write the failing tests**

`ledger/internal/cmd/read_test.go`:
```go
package cmd

import (
	"os/exec"
	"strings"
	"testing"
)

func seed(t *testing.T) string {
	dir := setup(t) // from write_test.go: demo with status/review fields
	run(t, dir, "set", "t1", "open", "--as", "impl", "-m", "starting")
	run(t, dir, "set", "t1", "done", "--evidence", "commit:abc123", "--as", "impl", "-m", "finished")
	run(t, dir, "set", "t2", "review=pending", "--as", "reviewer")
	run(t, dir, "note", "-k", "ruling", "--key", "t2", "-m", "ship it", "--as", "jesse")
	run(t, dir, "note", "-k", "handoff", "-m", "resume at t2", "--as", "impl")
	return dir
}

func TestStatusSpine(t *testing.T) {
	dir := seed(t)
	so, _, code := run(t, dir, "status")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	rows := doc["rows"].([]any)
	if len(rows) != 2 { // t1/status latest=done, t2/review
		t.Fatalf("rows: %v", rows)
	}
	r0 := rows[0].(map[string]any)
	if r0["key"] != "t1" || r0["value"] != "done" || r0["note"] != "finished" {
		t.Fatalf("latest-per-(key,field) with -m annotation: %v", r0)
	}
}

func TestStatusDrilldownAndUnknownKey(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "status", "t2")
	doc := mustJSON(t, so)
	if doc["key"] != "t2" || len(doc["notes"].([]any)) != 1 {
		t.Fatalf("drill-down must include attached notes: %v", doc)
	}
	_, se, code := run(t, dir, "status", "nope")
	if code != 4 || !strings.Contains(se, "unknown_key") || !strings.Contains(se, "t1") {
		t.Fatalf("unknown key hint lists known keys: %s", se)
	}
}

func TestByBranchFold(t *testing.T) {
	dir := seed(t)
	wt := t.TempDir()
	exec.Command("git", "-C", dir, "worktree", "add", "-b", "feat", wt).Run()
	run(t, wt, "set", "t2", "review=approved", "--as", "wt-reviewer")
	so, _, _ := run(t, dir, "status", "--by-branch", "--field", "review")
	doc := mustJSON(t, so)
	rows := doc["rows"].([]any)
	if len(rows) != 2 { // pending on main, approved on feat — both visible
		t.Fatalf("by-branch rows: %v", rows)
	}
}

func TestShowSchemaAndRedirect(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "show")
	doc := mustJSON(t, so)
	if doc["schema"] == nil || doc["require_evidence"] == nil || doc["head"] == nil {
		t.Fatalf("show payload: %v", doc)
	}
	if int(doc["events"].(float64)) < 6 {
		t.Fatalf("events count: %v", doc["events"])
	}
	run(t, dir, "create", "demo2", "--scope", "next", "--supersedes", "demo")
	so, _, _ = run(t, dir, "show", "--ledger", "demo")
	if mustJSON(t, so)["superseded_by"] != "demo2" {
		t.Fatal("superseded read must carry the redirect")
	}
}

func TestNotesFiltersAndEscaping(t *testing.T) {
	dir := seed(t)
	run(t, dir, "note", "-k", "gotcha", "-m", "bad\rFORGED line", "--as", "x")
	so, _, _ := run(t, dir, "notes", "-k", "gotcha", "--latest")
	doc := mustJSON(t, so)
	notes := doc["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("latest: %v", notes)
	}
	// raw body is preserved in JSON; escaping is a TTY-render concern
	if !strings.Contains(notes[0].(map[string]any)["text"].(string), "\r") {
		t.Fatal("JSON must carry the raw body")
	}
}

func TestTailCursor(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "tail", "-n", "3")
	doc := mustJSON(t, so)
	if len(doc["events"].([]any)) != 3 || doc["cursor"] == nil {
		t.Fatalf("tail: %v", doc)
	}
}
```

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement** `read.go` per the contracts. Row assembly:
```go
type row struct {
	Key string `json:"key"`; Field string `json:"field"`; Value string `json:"value"`
	Note string `json:"note"`; By string `json:"by"`; Branch string `json:"branch"`
	TS string `json:"ts"`; ID string `json:"id"`; Evidence []string `json:"evidence"`
}
func rowOf(key, f string, ev model.Event) row {
	return row{Key: key, Field: f, Value: ev.Fields[f], Note: ev.Text, By: ev.Author,
		Branch: ev.Origin.Branch, TS: ev.TS, ID: ev.ID, Evidence: ev.Evidence}
}
```
TTY spine line (status & show):
```go
evd := strings.Join(r.Evidence, " ")
if evd == "" { evd = "(no evidence)" }
note := ""
if r.Note != "" { note = `  "` + out.EscapeControls(r.Note) + `"` }
fmt.Sprintf("  %-16s %s=%-12s %-12s %-16s %s%s", r.Key, r.Field, r.Value, r.Branch, r.By, evd, note)
```
by-branch fold iterates `led.Events` (set events only), keyed `(key, field, branch)`, keeping the latest. Drill-down history = last 8 events with `ev.Key == key` (sets and notes). Show's JSON payload adds `schema`, `require_evidence` (from fold), `recent_notes`, `events: len(led.Events)`, `head: led.Head()`, and when `led.SupersededBy != ""`: `superseded_by`, `extra_links`, with TTY line 1 = `superseded by '<slug>' — read/write there` (or `successor '<slug>' not present locally — run ledger sync` when `c.Load(successor)` fails).

- [ ] **Step 4: Run** — `go test ./internal/... -count=1` → PASS.

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: status/show/notes/tail reads with by-branch folds"`

---

### Task 10: since + watch — cursors, paging, drain/block, follow

**Files:**
- Create: `ledger/internal/cmd/cursor.go`
- Test: `ledger/internal/cmd/cursor_test.go`

**Interfaces:**
- Consumes: PickLedger, store.HeadID, fold.
- Produces:
  - `since [cursor] [--limit N] [--ledger S]` — `{events, cursor, count}`; cursor validity = the id appears in the chain (ancestor check is trivially "is in events" for the linear local case; the merge-aware `merge-base --is-ancestor` form arrives with sync in Plan 2 — leave a comment); invalid → `reset_required` with hint "ledger status refolds current state; ledger since (no cursor) re-drains from the start"; `--limit` pages and the emitted cursor is the last delivered event.
  - `watch [--since CURSOR] [--key K] [--value V1,V2] [--kind K] [--timeout SECS] [--follow] [--ledger S]` — drain matching non-sentinel `set` events after cursor; empty drain → poll ref head every 200ms; on first match deliver the whole current batch, `{events, cursor}` exit 0. Cursorless: start at head, emit `{"starting_cursor": ...}` first (JSON: field in the final doc; TTY: a line). `--timeout` default 60, `0` = forever; timeout → `{timeout:true, events:[], cursor}` exit 2. `--follow`: implies no timeout (explicit `--timeout` with `--follow` = `conflicting_source` error reusing the identifier? No — use `bad_value` with message "—follow has no timeout"); streams line-per-event JSON (each line carries `id`), never exits on its own.
  - Watch filters: `--key` exact, `--value` comma-list matches any field value, `--kind` filters note events too (matching kind notes are delivered alongside sets when `--kind` given).

- [ ] **Step 1: Write the failing tests**

`ledger/internal/cmd/cursor_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestSincePagingAndReset(t *testing.T) {
	dir := seed(t) // 7+ events
	so, _, _ := run(t, dir, "since", "--limit", "2")
	doc := mustJSON(t, so)
	if int(doc["count"].(float64)) != 2 {
		t.Fatalf("limit: %v", doc)
	}
	cur := doc["cursor"].(string)
	so, _, _ = run(t, dir, "since", cur)
	doc2 := mustJSON(t, so)
	if int(doc2["count"].(float64)) < 1 {
		t.Fatal("paging must resume after cursor")
	}
	_, se, code := run(t, dir, "since", "ffffffffff")
	if code != 4 || !strings.Contains(se, "reset_required") {
		t.Fatalf("%s", se)
	}
}

func TestWatchDrainAndTimeout(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "tail", "-n", "1")
	first := mustJSON(t, so)["events"].([]any)[0].(map[string]any)["id"].(string)
	_ = first
	// drain: watch from the very first event id
	so, _, _ = run(t, dir, "since", "--limit", "1")
	c0 := mustJSON(t, so)["cursor"].(string)
	so, _, code := run(t, dir, "watch", "--since", c0, "--timeout", "5")
	if code != 0 {
		t.Fatalf("drain should match existing sets: %s", so)
	}
	doc := mustJSON(t, so)
	if doc["cursor"] == nil || len(doc["events"].([]any)) == 0 {
		t.Fatalf("watch payload: %v", doc)
	}
	// timeout with cursor intact
	head := doc["cursor"].(string)
	start := time.Now()
	so, _, code = run(t, dir, "watch", "--since", head, "--timeout", "1")
	if code != 2 || time.Since(start) < time.Second {
		t.Fatalf("timeout contract: code=%d", code)
	}
	doc = mustJSON(t, so)
	if doc["timeout"] != true || doc["cursor"] != head {
		t.Fatalf("timeout payload: %v", doc)
	}
}

func TestWatchCursorlessEmitsStart(t *testing.T) {
	dir := seed(t)
	so, _, code := run(t, dir, "watch", "--timeout", "1")
	if code != 2 {
		t.Fatal("no events after head: timeout expected")
	}
	if mustJSON(t, so)["starting_cursor"] == nil {
		t.Fatal("cursorless watch must emit its starting cursor (cold-start rule)")
	}
}

func TestWatchValueFilter(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "since", "--limit", "1")
	c0 := mustJSON(t, so)["cursor"].(string)
	so, _, _ = run(t, dir, "watch", "--since", c0, "--value", "done,failed", "--timeout", "5")
	for _, e := range mustJSON(t, so)["events"].([]any) {
		vals := e.(map[string]any)["fields"].(map[string]any)
		found := false
		for _, v := range vals {
			if v == "done" || v == "failed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("filter leak: %v", e)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement** `cursor.go`. Watch loop core:
```go
cur := opts.since
emittedStart := map[string]any{}
if cur == "" {
	h, err := c.Store.HeadID(led.Slug)
	if err != nil { return mapStoreErr(err, led.Slug) }
	cur = h
	emittedStart["starting_cursor"] = h
}
deadline := time.Now().Add(time.Duration(opts.timeout * float64(time.Second)))
for {
	led, err = c.Load(led.Slug) // refold; cheap (batched) and correct
	if err != nil { return err }
	idx := indexOf(led.Events, cur)
	if idx < 0 {
		return out.Errf("reset_required", "restart with `ledger watch` (no --since) to watch from now", 4,
			"cursor '%s' is not on ledger '%s'", cur, led.Slug)
	}
	newEvs := led.Events[idx+1:]
	hits := filterHits(newEvs, opts)
	if len(hits) > 0 {
		payload := map[string]any{"ledger": led.Slug, "events": eventDocs(hits), "cursor": newEvs[len(newEvs)-1].ID}
		for k, v := range emittedStart { payload[k] = v }
		outEmit(c, payload, watchLines(hits, newEvs))
		return nil
	}
	if len(newEvs) > 0 { cur = newEvs[len(newEvs)-1].ID } // advance past filtered events
	if opts.timeout > 0 && time.Now().After(deadline) {
		payload := map[string]any{"ledger": led.Slug, "timeout": true, "events": []any{}, "cursor": cur}
		for k, v := range emittedStart { payload[k] = v }
		out.Emit(c.Stdout, c.TTY, payload, []string{"timeout — no matching events; cursor: " + cur})
		return &out.CLIError{Code: "watch_timeout", Message: "timeout", ExitCode: 2}
	}
	time.Sleep(200 * time.Millisecond)
}
```
Note: the exit-2 path must NOT print an error doc — `main`'s error mapping special-cases `Code == "watch_timeout"` (payload already emitted; write nothing, return 2). Add that branch to `ExecuteArgs`. `--follow`: loop forever, printing each new hit as a JSON line (`{"id","key","fields","by","ts"}`) as it arrives, flushing; ignore timeout (error if explicitly set).

- [ ] **Step 4: Run** — `go test ./internal/... -count=1` → PASS.

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: since paging and watch with drain/block/follow"`

---

### Task 11: ls — freshness, idle marking, empty announcements

**Files:**
- Modify: `ledger/internal/cmd/ls.go` (complete the Task-6 stub)
- Test: `ledger/internal/cmd/ls_test.go`

**Interfaces:**
- Produces: `ls [--all]` — `{ledgers:[{slug,scope,state,last,events,idle:bool}]}`; default: open + closed-within-30-days; recency-sorted; open ledgers idle >45d get `idle:true` (TTY: `open, idle 62d`); empty repo and empty-after-filter announcements per spec; TTY columns: slug, scope (truncated 44), state, `last 2h ago`, `(N events)`.

- [ ] **Step 1: Write the failing test**

`ledger/internal/cmd/ls_test.go`:
```go
package cmd

import (
	"strings"
	"testing"
)

func TestLsEmptyAnnounces(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "ls")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	if l := doc["ledgers"].([]any); len(l) != 0 {
		t.Fatalf("%v", l)
	}
}

func TestLsFiltersAndSort(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "one", "--scope", "a")
	run(t, dir, "create", "two", "--scope", "b")
	run(t, dir, "close", "two", "--as-state", "shipped")
	so, _, _ := run(t, dir, "ls")
	doc := mustJSON(t, so)
	l := doc["ledgers"].([]any)
	if len(l) != 2 { // closed-within-30d stays visible
		t.Fatalf("%v", l)
	}
	so, _, _ = run(t, dir, "ls", "--all")
	if len(mustJSON(t, so)["ledgers"].([]any)) != 2 {
		t.Fatal("--all")
	}
	states := []string{}
	for _, e := range l {
		states = append(states, e.(map[string]any)["state"].(string))
	}
	if !strings.HasPrefix(states[0], "open") && !strings.HasPrefix(states[1], "open") {
		t.Fatalf("open ledgers present: %v", states)
	}
}
```
(The 30-day/45-day windows can't be black-box tested without fake clocks; the fold exposes `LastTS` and `ls.go` keeps `cutoff`/`idleAfter` as package vars the test overrides: `lsClosedCutoff = time.Hour*24*30`. Add one test that sets `lsIdleAfter = 0` and asserts `idle:true` appears.)

- [ ] **Step 2–4: fail → implement → pass** (implementation is a straightforward loop over `Slugs()`+`Load`, filter, sort by last TS desc, emit).

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: ls with freshness and idle marking"`

---### Task 12: export / import

**Files:**
- Create: `ledger/internal/cmd/port.go`
- Test: `ledger/internal/cmd/port_test.go`

**Interfaces:**
- Produces: `export <slug> [--to PATH]` — self-contained JSONL: line 1 `{"ledger_export":1,"slug","scope","meta":{...}}`, then one line per event (full event JSON including `id` for reference — with the documented caveat ids don't survive import). Default stdout. `import <path> --slug <new-slug> [--as R]` — creates a new ledger (slug validated, must not exist), replays payloads in order as new commits (`Type` preserved; commit identity: committer = `imported`); import events carry `"imported_from": "<original id>"` in event.json for traceability. Payload equality round-trip guaranteed; identity non-crossing stated in output (`{imported: N, ledger, note: "event ids did not survive the boundary"}`).

- [ ] **Step 1: Write the failing test**

`ledger/internal/cmd/port_test.go`:
```go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
```
(`execGit` helper: run git -C dir, return trimmed stdout.)

- [ ] **Step 2–4: fail → implement → pass.** Import sets each replayed event's `Type` as stored but stamps commit committer via a store append variant: add optional `CommitterOverride string` field on `model.Event` tagged `json:"-"`; `store.committerMarker` returns it when set.

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: JSONL export/import with imported provenance"`

---

### Task 13: init — refspec-free local parts, bare stores, breadcrumb, hooks

**Files:**
- Create: `ledger/internal/cmd/initcmd.go`, `ledger/docs/admin.md`
- Test: `ledger/internal/cmd/init_test.go`

**Interfaces:**
- Produces: `init [--hooks]` —
  - In a git repo: sets `core.logAllRefUpdates=always` (repo-local config); writes `.ledger.toml` (marker comment + optional `remote = "origin"` line commented out; never commits; prints "commit this file so clones discover the ledger"); prints the CLAUDE.md/AGENTS.md stanza; points at `ledger quickstart` and the admin runbook. (The fetch-refspec install is Plan 2 — sync; init here notes "sync arrives in a later release" only if the remote-oriented flags are used... it doesn't have them yet, so nothing to note.)
  - In a non-git directory: creates `./.ledger.git` (bare, `core.logAllRefUpdates=always`), no `.ledger.toml`.
  - `--hooks`: appends the SessionStart snippet to `.claude/settings.json`-adjacent docs? NO — v1 scope per spec: `--hooks` **writes the snippet file** `.ledger-hooks.md` containing the recommended SessionStart hook config and prints where to paste it; it does not edit harness config (printed-not-auto-edited applies to files the tool doesn't own; the spec's `--hooks` "installs into the harness config it detects" is downgraded here deliberately — detecting-and-editing `settings.json` is follow-on work with the sync plan, and the spec's own rule "printed, never auto-edited" for CLAUDE.md governs; note this decision in the commit message so the spec team sees it).
  - `admin.md` content: mirror/force-push hazards, `receive.denyDeletes` tradeoff, secrets incident runbook (rotate → per-clone `git update-ref -d refs/ledger/<slug>` → remote deletion push), verbatim from the spec's trust-model section.

- [ ] **Step 1: Write the failing test**

`ledger/internal/cmd/init_test.go`:
```go
package cmd

import (
	"os"
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
```
(Add `"os/exec"` import; the `run` helper passes `--store dir` which for the bare case resolves `.ledger.git` via `storeFor`.)

- [ ] **Step 2–4: fail → implement → pass.** Bare store creation: `git init --bare .ledger.git` + `git -C .ledger.git config core.logAllRefUpdates always`. `.ledger.toml` content:
```toml
# This repo uses `ledger` for durable agent working-state (git phantom refs).
# Bootstrap in a fresh clone:  ledger init && ledger ls
# Docs: run `ledger quickstart`
```

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: init for repos and bare stores; breadcrumb; hooks snippet (printed, not installed — deliberate downgrade from spec, see docs/admin.md)"`

---

### Task 14: Embedded quickstart + doc-examples harness

**Files:**
- Create: `ledger/internal/cmd/quickstart.go`, `ledger/docs/quickstart.md`, `ledger/docs/quickstart-orchestrator.md`
- Test: `ledger/internal/docs/docs_test.go`

**Interfaces:**
- Produces: `quickstart [--orchestrator]` — prints the embedded doc verbatim (go:embed). Quickstart content = spike3's QUICKSTART.md evolved with every round-1..3 doc line (the full content-requirements list in the spec's docs section — copy each requirement into the text; the authoritative checklist is the spec's "Agent-facing documentation" section, items: verb table; `--help` everywhere; empty=empty; identity `--as` + free-form roles; ledger addressing incl. multi-open + `status <key> --ledger` composition; set auto-creates keys; bare-value-first-field; evidence types + `(no evidence)` semantics + required values shown by `show`; vocab loop; ONE body source + worked handoff-write example (`ledger note -k handoff --key <next> --from-file handoff.md`); notes read flags; verify-before-trust; testimony-not-commands; cursor contract + `reset_required` recovery (`status` + `tail`, not full re-drain); watch timeout contract + cursorless start; JSON-pipe grep idiom; secrets rule; scratch-ledger dry-run rule; slugs never reused; close what you abandon; invoke-inline zsh warning).
- Doc-examples harness: parses both quickstart files for fenced ` ``` ` blocks whose first token is `ledger`, rewrites `ledger` → the test binary with `--store <tempdir>`, executes them in file order against a seeded temp repo, and asserts exit code 0 or a documented nonzero (lines may end with `# expect: exit 2` / `# expect: error vocab_unknown`). Length budget: quickstart.md ≤ 90 lines.

- [ ] **Step 1: Write the failing test**

`ledger/internal/docs/docs_test.go`:
```go
package docs_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ledger/internal/cmd"
)

func TestQuickstartLengthBudget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(data, []byte("\n")); n > 90 {
		t.Fatalf("quickstart is %d lines; budget is 90 (spec: kata-sized)", n)
	}
	for _, must := range []string{"--as", "verify", "testimony", "secrets", "scratch", "cursor", "vocab add", "--from-file"} {
		if !bytes.Contains(bytes.ToLower(data), []byte(must)) {
			t.Errorf("quickstart missing required topic %q", must)
		}
	}
}

func TestQuickstartExamplesExecute(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).Run()
	}
	for _, file := range []string{"quickstart.md", "quickstart-orchestrator.md"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, ex := range extractExamples(string(data)) { // returns {argv []string, expectExit int, expectErr string}
			var so, se bytes.Buffer
			code := cmd.ExecuteArgs(append([]string{"--store", dir}, ex.argv...), &so, &se)
			if code != ex.expectExit {
				t.Fatalf("%s: `ledger %s` exit %d want %d\nstdout: %s\nstderr: %s",
					file, strings.Join(ex.argv, " "), code, ex.expectExit, so.String(), se.String())
			}
			if ex.expectErr != "" && !strings.Contains(se.String(), ex.expectErr) {
				t.Fatalf("expected error %q, got %s", ex.expectErr, se.String())
			}
		}
	}
}
```
(`extractExamples` lives in the test file: scans fenced blocks, takes lines starting with `ledger `, shell-splits them (no quoting edge cases in docs — keep doc examples quote-simple; the extractor errors on unbalanced quotes so doc authors find out in CI), reads trailing `# expect:` annotations, default expect exit 0. Examples in the docs are written to be order-dependent within a file — creation first — so execution order = file order.)

- [ ] **Step 2: Run to verify failure** (docs don't exist yet).

- [ ] **Step 3: Write the two quickstart docs + quickstart.go** (`go:embed docs/*.md` — note embed paths: put the embed directive in a `ledger/docs/docs.go` package file `package docs; //go:embed *.md; var FS embed.FS` and have cmd/quickstart.go read from it). Port spike3's QUICKSTART.md, add the required lines, keep ≤90 lines, make every `ledger ...` example real and ordered (create a `qs-demo` ledger early so later examples target it; deliberate-error examples annotated `# expect: exit 4` + `# expect: error vocab_unknown`).

- [ ] **Step 4: Run** — `go test ./internal/docs/ -count=1` → PASS (this is test-plan item 37 live).

- [ ] **Step 5: Commit** — `git add ledger/ && git commit -m "ledger: embedded quickstart with executable-examples harness"`

---

### Task 15: using-ledger skill

**Files:**
- Create: `skills/using-ledger/SKILL.md`
- Test: manual review step (skills are prose; the doc harness covers command accuracy since the skill defers mechanics to quickstart)

**Interfaces:**
- Consumes: the shipped CLI behavior (all prior tasks).
- Produces: a superpowers-format skill. Frontmatter:
```markdown
---
name: using-ledger
description: Use when work spans sessions or agents and needs durable, verifiable state — starting multi-session or multi-agent work, dispatching subagent fleets, resuming after context death, handing off, tracking an investigation, or deciding "should this be a ledger?". Teaches when and how to use the `ledger` CLI's patterns; command mechanics live in `ledger quickstart`.
---
```
Body sections (each pattern grounded in its eval-proven scenario, ~15 lines apiece, no command mechanics beyond one illustrative line + "run `ledger quickstart` for mechanics"):
1. **When to reach for a ledger** (and when not: single-session work with no successor).
2. **Execution spine** — plan-shaped work: create with plan scope; seed keys from plan tasks; evidence-required terminal values; set with commit-range evidence per task; handoff note at the end.
3. **Coordination scoreboard** — fleets: create + seed pending rows; dictate `--as`/`--ledger`/`--store` grammar in worker briefs; monitor with cursor-carried watch (seed `--since` from create's id or watch before spawning).
4. **Checkpoint at context death** — the what-only-lives-in-my-head audit; `note -k handoff --from-file`.
5. **Resume-and-verify** — `show` → `notes -k handoff --latest` → verify evidence refs against git before skipping work; `(no evidence)` = testimony.
6. **Investigation ledger** — claims as keys (`repro-*`, `hyp-*`, `task-*` prefixes), statuses as epistemic state, rulings/gotchas as `--key`-attached notes; never fabricate evidence refs — say "not retained, rerun to verify".
7. **Discipline that keeps ledgers trustworthy** — scratch-ledger dry-runs, close what you abandon, secrets rule, testimony rule.

- [ ] **Step 1: Write `skills/using-ledger/SKILL.md`** with the structure above (full prose, drawn from the spec's companion-skill section and the three eval reports).
- [ ] **Step 2: Verify frontmatter + length** — description under 500 chars, body under ~150 lines, every `ledger` command line in it also appears in quickstart (spot-check by grep).
- [ ] **Step 3: Run the whole suite** — `cd ledger && go test ./... -count=1 && go vet ./...` → PASS.
- [ ] **Step 4: Commit** — `git add skills/ && git commit -m "using-ledger skill: patterns defer mechanics to quickstart"`

---

### Task 16: Scale smoke + full-suite gate

**Files:**
- Create: `ledger/internal/store/scale_test.go`
- Test: itself (guarded by `testing.Short()` — runs in CI, skippable locally with `-short`)

**Interfaces:** none new.

- [ ] **Step 1: Write the test**

```go
package store

import (
	"fmt"
	"testing"
	"time"

	"ledger/internal/model"
)

func TestScaleSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("scale smoke")
	}
	s := testStore(t)
	for l := 0; l < 30; l++ { // 30 ledgers x 60 events = 1800 events (CI-friendly slice of the 300x-spec probe)
		slug := fmt.Sprintf("led-%02d", l)
		s.Append(slug, model.Event{Type: "create", Author: "t"}, map[string]string{"meta.json": "{}"}, ExpectAbsent)
		for e := 0; e < 60; e++ {
			s.Append(slug, model.Event{Type: "set", Key: "k", Fields: map[string]string{"s": "v"}, Author: "t"}, nil, ExpectPresent)
		}
	}
	start := time.Now()
	slugs, _ := s.Slugs()
	for _, slug := range slugs {
		if _, _, err := s.Events(slug); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("full fold of 30x61 events took %v — reads are not batched enough", d)
	}
	// gc kept the store packed: loose objects bounded
	out, _, _ := s.Repo.Git("", "count-objects", "-v")
	t.Logf("count-objects:\n%s", out)
}
```

- [ ] **Step 2: Run** — `go test ./internal/store/ -run Scale -count=1` → PASS within budget.
- [ ] **Step 3: Full gate** — `cd ledger && gofmt -l . && go vet ./... && go test ./... -count=1` → all clean.
- [ ] **Step 4: Commit** — `git add ledger/ && git commit -m "ledger: scale smoke and full-suite gate"`

---

## Deferred to Plan 2 (sync layer) — explicitly NOT in this plan

Tracking namespace + refspec repair; `sync` (ff/merge/adopt/sentinels); `push`; merge-aware cursor ancestry (`merge-base --is-ancestor`); read-time freshness vs tracking refs; same-root refusal + export/import exit ramp wiring; degraded modes (`credentials_needed`, prune-diff); `init`'s refspec install + default-remote breadcrumb line; sentinel-skip rules in reads (local chains are linear — the reads in this plan add a `Type == "sync"` skip guard anyway, cheap and forward-compatible: add `if ev.Type == "sync" { continue }` in fold's spine/notes loops and cursor filters, with one fold test asserting a synthetic sync event is invisible).

## Self-review notes (performed while writing)

- Spec coverage: all rev-10 sections map to tasks except the sync layer (deliberately Plan 2, listed above) and visibility/owner-enforcement/verify (spec v2 items). The `--hooks` downgrade in Task 13 is a flagged deviation for review, not silent.
- Type consistency: `model.Meta.FieldOrder` is introduced in Task 8 and used by set; Task 2's struct definition should include it from the start (`FieldOrder []string \`json:"field_order"\``) — implementers of Task 2: include it; Task 7's create populates it.
- The Task-6 status stub is deleted in Task 9 (read.go owns `status`).
- Every error identifier used in tasks appears in the Global Constraints list.
