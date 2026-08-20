package docs_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ledger/internal/cmd"
)

// quickstartLineBudget is the doctrine's kata-sized cap. Raised 120 -> 124
// by the GitHub-bridge design's quickstart amendment, which added the
// `set --rename` line: a cold agent must not keep the immutable-title
// belief.
const quickstartLineBudget = 124

func TestQuickstartLengthBudget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(data, []byte("\n")); n > quickstartLineBudget {
		t.Fatalf("quickstart is %d lines; budget is %d (spec: kata-sized)", n, quickstartLineBudget)
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
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	for _, file := range []string{"quickstart.md", "quickstart-orchestrator.md"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", file))
		if err != nil {
			t.Fatal(err)
		}
		examples, err := extractExamples(string(data))
		if err != nil {
			t.Fatalf("%s: %s", file, err)
		}
		if len(examples) == 0 {
			t.Fatalf("%s: no executable examples found", file)
		}
		for _, ex := range examples {
			var so, se bytes.Buffer
			code := cmd.ExecuteArgs(append([]string{"--store", dir}, ex.argv...), &so, &se)
			if code != ex.expectExit {
				t.Fatalf("%s: `chit %s` exit %d want %d\nstdout: %s\nstderr: %s",
					file, strings.Join(ex.argv, " "), code, ex.expectExit, so.String(), se.String())
			}
			if ex.expectErr != "" && !strings.Contains(se.String(), ex.expectErr) {
				t.Fatalf("%s: `chit %s` expected error %q, got %s",
					file, strings.Join(ex.argv, " "), ex.expectErr, se.String())
			}
		}
	}
}

// curatedOutOfQuickstart are verbs deliberately absent from the quickstart's
// doctrine: render/version/update/quickstart act on the binary or a file
// path rather than board/coordination doctrine, and completion/help are
// cobra machinery, not ledger verbs.
var curatedOutOfQuickstart = map[string]bool{
	"render": true, "version": true, "update": true, "quickstart": true,
	"completion": true, "help": true,
}

// TestQuickstartMentionsEveryVerb guards against the doctrine silently
// falling behind the verb set (the deferred-disclosure finding: `ready`
// shipped with the issue board and quickstart never learned it). It derives
// the live verb list from the cobra root itself — via `chit --help`,
// the same surface an agent actually reads — rather than hand-maintaining
// a second list that can drift the same way the doc did.
func TestQuickstartMentionsEveryVerb(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	var so, se bytes.Buffer
	if code := cmd.ExecuteArgs([]string{"--store", dir, "--help"}, &so, &se); code != 0 {
		t.Fatalf("chit --help: exit %d\n%s", code, se.String())
	}
	verbs := verbsFromHelp(so.String())
	if len(verbs) == 0 {
		t.Fatal("parsed zero verbs out of `chit --help` output — parser or cobra output format changed")
	}
	quickstart, err := os.ReadFile(filepath.Join("..", "..", "docs", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range verbs {
		if curatedOutOfQuickstart[v] {
			continue
		}
		if !bytes.Contains(quickstart, []byte(v)) {
			t.Errorf("quickstart.md never mentions verb %q (registered in cmd, not in curatedOutOfQuickstart)", v)
		}
	}
}

// verbsFromHelp pulls the first word of every line in cobra's "Available
// Commands:" block — the verb list a cold agent actually reads.
func verbsFromHelp(help string) []string {
	var verbs []string
	inSection := false
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(line, "Available Commands:") {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if fields := strings.Fields(line); len(fields) > 0 {
			verbs = append(verbs, fields[0])
		}
	}
	return verbs
}

// ---- docs/migrate-github.md: the bulk-migration recipe, executed ----

// migrationLoop returns the recipe's seeding loop, verbatim, out of
// docs/migrate-github.md — the single ```sh fenced block in the file. The
// test below runs THAT text, so a recipe that stops working fails here
// rather than in somebody's terminal halfway through a real backlog.
func migrationLoop(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "migrate-github.md"))
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	var cur []string
	inFence := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case !inFence && strings.TrimSpace(line) == "```sh":
			inFence = true
			cur = nil
		case inFence && strings.TrimSpace(line) == "```":
			inFence = false
			blocks = append(blocks, strings.Join(cur, "\n"))
		case inFence:
			cur = append(cur, line)
		}
	}
	if len(blocks) != 1 {
		t.Fatalf("migrate-github.md must carry exactly one ```sh block (the loop); found %d", len(blocks))
	}
	if !strings.Contains(blocks[0], "while IFS=") {
		t.Fatalf("the ```sh block is not the seeding loop:\n%s", blocks[0])
	}
	return blocks[0]
}

// ledgerBinDir builds the CLI once per test binary and returns the
// directory holding it — the recipe calls a bare `ledger`, so it has to be
// on PATH as a real executable, not an in-process ExecuteArgs call.
var ledgerBinDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "ledger-bin")
	if err != nil {
		return "", err
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "ledger"), ".")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}
	return dir, nil
})

// migrationBoard makes a repo with the ready-capable board the recipe's
// "Before you start" section tells you to create.
func migrationBoard(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	var so, se bytes.Buffer
	code := cmd.ExecuteArgs([]string{"--store", dir, "create", "issues", "--scope", "migration",
		"--field", "status=open,in-progress,closed,wontfix",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by",
		"--stale-after", "4h"}, &so, &se)
	if code != 0 {
		t.Fatalf("create board: %s", se.String())
	}
	return dir
}

// runMigrationLoop copies fixture in as ./issues.json (the filename step 1
// of the recipe writes) and runs the recipe's loop against dir's board.
func runMigrationLoop(t *testing.T, dir, fixture string) (stdout, stderr string) {
	t.Helper()
	binDir, err := ledgerBinDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "issues.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	sh := exec.Command("sh", "-c", migrationLoop(t))
	sh.Dir = dir
	sh.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "LEDGER_DIR=")
	var so, se bytes.Buffer
	sh.Stdout, sh.Stderr = &so, &se
	if err := sh.Run(); err != nil {
		// A broken row makes the loop `break`, which is not a failure of the
		// loop itself; only report a shell-level failure, with the output.
		t.Logf("loop exited %v\nstdout: %s\nstderr: %s", err, so.String(), se.String())
	}
	return so.String(), se.String()
}

// boardTitles reads the board back as {key: title}.
func boardTitles(t *testing.T, dir string) map[string]string {
	t.Helper()
	var so, se bytes.Buffer
	if code := cmd.ExecuteArgs([]string{"--store", dir, "show", "--ledger", "issues"}, &so, &se); code != 0 {
		t.Fatalf("show: %s", se.String())
	}
	var doc struct {
		Rows []struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(so.Bytes(), &doc); err != nil {
		t.Fatalf("show JSON: %v\n%s", err, so.String())
	}
	titles := map[string]string{}
	for _, r := range doc.Rows {
		titles[r.Key] = r.Title
	}
	return titles
}

// keyNotes returns the note bodies recorded against one key.
func keyNotes(t *testing.T, dir, key string) []string {
	t.Helper()
	var so, se bytes.Buffer
	if code := cmd.ExecuteArgs([]string{"--store", dir, "notes", "--key", key, "--ledger", "issues"}, &so, &se); code != 0 {
		t.Fatalf("notes: %s", se.String())
	}
	var doc struct {
		Notes []struct {
			Body string `json:"body"`
			Text string `json:"text"`
		} `json:"notes"`
	}
	if err := json.Unmarshal(so.Bytes(), &doc); err != nil {
		t.Fatalf("notes JSON: %v\n%s", err, so.String())
	}
	var bodies []string
	for _, n := range doc.Notes {
		bodies = append(bodies, n.Body+n.Text)
	}
	return bodies
}

// migrationKeys is the slug the recipe's own key expression produces for
// each fixture issue, including the 64-character truncation on issue 9.
var migrationKeys = map[int]string{
	1:  "fix-the-retry-storm-bug",
	2:  "add-stale-after-guidance-to-the-skill",
	7:  "gh-import-should-refuse-a-board",
	9:  "investigate-why-the-orchestrator-sometimes-reclaims-a-claim-that",
	11: "cache-warm-on-boot",
}

// TestMigrateGitHubRecipeSeedsEveryIssue: the recipe's loop, run verbatim
// against a checked-in `gh issue list --json number,title` fixture (never
// the live API), seeds one titled key per issue with a provenance note.
func TestMigrateGitHubRecipeSeedsEveryIssue(t *testing.T) {
	dir := migrationBoard(t)
	_, stderr := runMigrationLoop(t, dir, "gh-issues.json")
	if strings.Contains(stderr, "STOPPED at") {
		t.Fatalf("the clean fixture must migrate end to end: %s", stderr)
	}
	titles := boardTitles(t, dir)
	if len(titles) != len(migrationKeys) {
		t.Fatalf("want %d keys, got %d: %v", len(migrationKeys), len(titles), titles)
	}
	for n, key := range migrationKeys {
		if titles[key] == "" {
			t.Fatalf("issue %d seeded no key %q: %v", n, key, titles)
		}
		notes := keyNotes(t, dir, key)
		if len(notes) != 1 || !strings.Contains(notes[0], fmt.Sprintf("migrated from issues/%d", n)) {
			t.Fatalf("issue %d's key %q must carry exactly one provenance note: %v", n, key, notes)
		}
	}
	// The seed's -m is the title, verbatim off the fixture — the recipe's
	// load-bearing warning.
	if got := titles[migrationKeys[7]]; got != "gh: `import` should refuse a board" {
		t.Fatalf("the seed's -m must be the title verbatim: %q", got)
	}
}

// TestMigrateGitHubRecipeBreaksOnFailureThenResumes: the `|| break` half of
// the recipe. A poisoned row (issue 7 carrying issue 1's title, so its key
// collides and its `--expect none` loses) stops the loop right there —
// later rows never land — and the run says which issue stopped it. Fixing
// that row and re-running finishes the job without duplicating anything,
// because `--idempotency-key` makes the already-migrated rows no-ops.
func TestMigrateGitHubRecipeBreaksOnFailureThenResumes(t *testing.T) {
	dir := migrationBoard(t)
	_, stderr := runMigrationLoop(t, dir, "gh-issues-poisoned.json")
	if !strings.Contains(stderr, "STOPPED at issue #7") {
		t.Fatalf("a failed row must name itself and break: %q", stderr)
	}
	titles := boardTitles(t, dir)
	if len(titles) != 2 {
		t.Fatalf("only the two rows ahead of the poison may land: %v", titles)
	}
	for _, n := range []int{9, 11} {
		if _, seeded := titles[migrationKeys[n]]; seeded {
			t.Fatalf("issue %d is after the break and must never have landed: %v", n, titles)
		}
	}

	// Resume: the corrected fixture, same board.
	_, stderr = runMigrationLoop(t, dir, "gh-issues.json")
	if strings.Contains(stderr, "STOPPED at") {
		t.Fatalf("the resume run must finish: %s", stderr)
	}
	titles = boardTitles(t, dir)
	if len(titles) != len(migrationKeys) {
		t.Fatalf("want %d keys after the resume, got %d: %v", len(migrationKeys), len(titles), titles)
	}
	if notes := keyNotes(t, dir, migrationKeys[1]); len(notes) != 1 {
		t.Fatalf("re-running must not duplicate a note (idempotency-key): %v", notes)
	}
}

// example is one executable line pulled from a fenced ```-block: the
// verb+args to run (with the leading "ledger" token stripped) and the
// expectations parsed from a trailing "# expect: ..." annotation. Default
// expectation is a clean exit.
type example struct {
	argv       []string
	expectExit int
	expectErr  string
}

// extractExamples scans md for fenced ``` blocks and pulls out every line
// that starts with "ledger " (prose and inline `single-backtick` snippets
// outside a fence are never executed — only lines presented as a runnable
// transcript are). Each line is shell-split (quote-balanced only; doc
// examples are written quote-simple on purpose) and may end with a trailing
// "# expect: exit N" and/or "# expect: error <code>" annotation.
func extractExamples(md string) ([]example, error) {
	var exs []example
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if !inFence || !strings.HasPrefix(trimmed, "ledger ") {
			continue
		}
		cmdPart := trimmed
		ex := example{}
		if idx := strings.Index(trimmed, "# expect:"); idx >= 0 {
			cmdPart = strings.TrimSpace(trimmed[:idx])
			ann := strings.Fields(trimmed[idx+len("# expect:"):])
			for i := 0; i < len(ann); i++ {
				switch ann[i] {
				case "exit":
					i++
					if i >= len(ann) {
						return nil, fmt.Errorf("dangling 'exit' annotation: %q", line)
					}
					n, err := strconv.Atoi(ann[i])
					if err != nil {
						return nil, fmt.Errorf("bad exit annotation %q: %w", line, err)
					}
					ex.expectExit = n
				case "error":
					i++
					if i >= len(ann) {
						return nil, fmt.Errorf("dangling 'error' annotation: %q", line)
					}
					ex.expectErr = ann[i]
				default:
					return nil, fmt.Errorf("unrecognized expect annotation %q in %q", ann[i], line)
				}
			}
		}
		argv, err := shellSplit(cmdPart)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", line, err)
		}
		ex.argv = argv[1:] // drop the leading "ledger" token
		exs = append(exs, ex)
	}
	return exs, nil
}

// shellSplit does word-splitting good enough for quote-simple doc examples:
// single/double-quoted spans keep embedded spaces, everything else splits
// on whitespace. It errors on an unbalanced quote so a broken example fails
// loudly in CI rather than silently truncating an argument.
func shellSplit(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unbalanced quote")
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}
