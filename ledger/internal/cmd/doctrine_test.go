// Doctrine tests: the skill's "Issue board" pattern section
// (skills/using-ledger/SKILL.md, repo root) is executable doctrine, not
// aspirational prose. TestDoctrineVerbatimWalkthrough (spec test 17) parses
// that section straight out of the file, extracts every fenced command
// line, and replays them in document order against a real scratch board —
// if the doctrine and the tool ever drift, this test is the one that
// notices. TestWatch* (spec test 18) independently exercises the watch
// doctrine bullet's three claims against the real watch implementation.
package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---- locating and parsing the doctrine section ----

// skillMDPath finds skills/using-ledger/SKILL.md relative to this test
// file's own location (via runtime.Caller), rather than assuming a `go
// test` working directory — the module root (ledger/) sits one level
// below the repo root, and the skill lives in a sibling directory of that.
func skillMDPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// thisFile: <repo-root>/ledger/internal/cmd/doctrine_test.go
	// up three (cmd -> internal -> ledger) reaches <repo-root>.
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "skills", "using-ledger", "SKILL.md")
}

// doctrineBinary is the exact placeholder every command line in the
// section must open with — the skill's absolute-binary-path convention
// for this section (spec "The write idioms": doctrine lines that carry a
// bare `ledger` get typed as bare `ledger`, a trial-proven failure mode).
const doctrineBinary = "~/path-to/ledger"

// doctrineCdTokens is the exact three-token prefix every command line in
// the section must open with, ahead of doctrineBinary — rev 17's
// cwd-independence convention (spec: "a missing, empty, or broken-looking
// store is REPORTED, never repaired" — trial 5's worker destroyed the live
// store by re-running a setup script after a cwd mistake, so every line now
// carries its own working directory, like the binary path before it).
// "<board dir>" is a placeholder exactly like doctrineBinary itself:
// neither is substituted with a real value, since run()/execLedger already
// supply the scratch board via --store; both are just asserted-and-stripped
// here.
var doctrineCdTokens = []string{"cd", "<board dir>", "&&"}

// expectCommentRE parses a doctrine command line's optional trailing
// outcome annotation, mirroring quickstart.md's own walkthrough convention
// (`# expect: exit 4 error vocab_unknown`, or exit-code-only for cases
// with no single error field to name, e.g. a watch timeout).
var expectCommentRE = regexp.MustCompile(`^(.*?)\s+# expect: exit (\d+)(?: error (\S+))?\s*$`)

// doctrineCmd is one parsed, not-yet-substituted command line from the
// section: verb+args (binary placeholder already stripped) plus its
// documented outcome (default: exit 0, no error).
type doctrineCmd struct {
	raw      string
	tokens   []string
	wantExit int
	wantErr  string
}

// doctrineSection extracts the named "## <heading>" section's raw lines
// (everything up to the next top-level heading or EOF) from a SKILL.md
// already split into lines.
func doctrineSection(t *testing.T, lines []string, heading string) []string {
	t.Helper()
	start := -1
	end := len(lines)
	for i, l := range lines {
		if start == -1 && l == heading {
			start = i + 1
			continue
		}
		if start != -1 && strings.HasPrefix(l, "## ") {
			end = i
			break
		}
	}
	if start == -1 {
		t.Fatalf("SKILL.md has no %q section — doctrine not yet written", heading)
	}
	return lines[start:end]
}

// tokenizeCommand splits a command line on spaces, treating a
// double-quoted substring (as every `-m "..."` in the doctrine carries) as
// one token, and an angle-bracketed placeholder (as every `--expect
// <...>` in the doctrine carries — several of them are multi-word, e.g.
// "<your own edge event id>") as one token too, brackets kept intact so
// resolveExpectPlaceholders can recognize it.
func tokenizeCommand(line string) []string {
	var tokens []string
	var cur strings.Builder
	inQuotes := false
	inBracket := false
	for _, r := range line {
		switch {
		case r == '"' && !inBracket:
			inQuotes = !inQuotes
		case r == '<' && !inQuotes && !inBracket:
			inBracket = true
			cur.WriteRune(r)
		case r == '>' && inBracket:
			inBracket = false
			cur.WriteRune(r)
		case r == ' ' && !inQuotes && !inBracket:
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// parseDoctrineCommands reads SKILL.md, isolates the "## Issue board"
// section, and returns every fenced command line inside it, in document
// order, across every fenced block the section contains (multiple idioms
// each carry their own small fenced example; this walks them as one
// continuous scratch-board scenario).
func parseDoctrineCommands(t *testing.T) []doctrineCmd {
	t.Helper()
	path := skillMDPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	section := doctrineSection(t, strings.Split(string(data), "\n"), "## Issue board")

	var cmds []doctrineCmd
	inFence := false
	for _, l := range section {
		if strings.TrimSpace(l) == "```" {
			inFence = !inFence
			continue
		}
		if !inFence || strings.TrimSpace(l) == "" {
			continue
		}
		wantExit := 0
		wantErr := ""
		body := l
		if m := expectCommentRE.FindStringSubmatch(l); m != nil {
			body = m[1]
			wantExit, _ = strconv.Atoi(m[2])
			wantErr = m[3]
		}
		toks := tokenizeCommand(strings.TrimSpace(body))
		if len(toks) == 0 {
			continue
		}
		if len(toks) < len(doctrineCdTokens) || !equalTokens(toks[:len(doctrineCdTokens)], doctrineCdTokens) {
			t.Fatalf("doctrine command line must open with %q (drifted cwd-independence convention): %q",
				strings.Join(doctrineCdTokens, " "), l)
		}
		toks = toks[len(doctrineCdTokens):]
		if len(toks) == 0 || toks[0] != doctrineBinary {
			t.Fatalf("doctrine command line must open with %q (drifted binary-path convention): %q", doctrineBinary, l)
		}
		cmds = append(cmds, doctrineCmd{raw: l, tokens: toks[1:], wantExit: wantExit, wantErr: wantErr})
	}
	return cmds
}

// equalTokens reports whether a and b are the same tokens in the same
// order.
func equalTokens(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSkillFrontmatterIncludesBoardTriggers is spec test 17's second
// clause: the skill's frontmatter "description" (its trigger text — what a
// reader scans to decide whether this skill applies) must name the board
// scenarios this section teaches, not just the ledger's original
// investigation-log use cases. Without this, an agent picking a coordinating
// unblocked-work task would never be pointed at the Issue board section at
// all.
func TestSkillFrontmatterIncludesBoardTriggers(t *testing.T) {
	data, err := os.ReadFile(skillMDPath(t))
	if err != nil {
		t.Fatalf("reading SKILL.md: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 || lines[0] != "---" {
		t.Fatalf("SKILL.md must open with a --- frontmatter fence")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		t.Fatal("SKILL.md frontmatter has no closing ---")
	}
	var description string
	for _, l := range lines[1:end] {
		if strings.HasPrefix(l, "description:") {
			description = strings.TrimPrefix(l, "description:")
			break
		}
	}
	if description == "" {
		t.Fatal("SKILL.md frontmatter has no description field")
	}
	for _, want := range []string{"issue board", "picking unblocked work"} {
		if !strings.Contains(description, want) {
			t.Fatalf("frontmatter description must name the board scenarios (missing %q): %q", want, description)
		}
	}
}

// ---- id resolution across the walkthrough ----

// setField returns the field name a `set <key> <field=value> ...` command
// touches — always tokens[1] (the key) and tokens[2] (the assignment) in
// every doctrine command, since none of the idiom examples combine a
// bare-value assignment with a guarded write.
func setField(tokens []string) string {
	if len(tokens) < 3 {
		return ""
	}
	field, _, _ := strings.Cut(tokens[2], "=")
	return field
}

// resolveExpectPlaceholders substitutes a `--expect <...>` bracketed
// placeholder with a real event id, using this (key, field)'s write
// history so far. Ordinary placeholders ("the seed id", "own claim id",
// "the edge field's latest id", ...) always mean "whatever's current for
// this key+field right now" — the latest history entry. A command
// documented to fail with claim_lost is, by construction, the one
// exception: the whole point of that demonstration is a caller holding an
// id that's no longer current (something else became latest in between),
// so its placeholder resolves one generation back instead.
func resolveExpectPlaceholders(tokens []string, wantErr string, history map[string][]string) []string {
	if len(tokens) == 0 || tokens[0] != "set" {
		return tokens
	}
	hk := tokens[1] + "/" + setField(tokens)
	out := append([]string(nil), tokens...)
	for i, tok := range out {
		if tok != "--expect" || i+1 >= len(out) || !strings.HasPrefix(out[i+1], "<") {
			continue
		}
		h := history[hk]
		if len(h) == 0 {
			continue // left unresolved; the write below fails loudly and names the bad token
		}
		idx := len(h) - 1
		if wantErr == "claim_lost" && len(h) >= 2 {
			idx = len(h) - 2
		}
		out[i+1] = h[idx]
	}
	return out
}

// hasReclaimMessage reports whether tokens carries a `-m` value starting
// with the Reclaim idiom's load-bearing message prefix (spec: "reclaiming
// from <by>: stale <age>"). Used to pace the walkthrough: a reclaim must
// genuinely be stale, so the test sleeps past the scratch board's
// --stale-after horizon right before running one of these — the only two
// lines in the whole section where wall-clock time actually matters.
func hasReclaimMessage(tokens []string) bool {
	for i, tok := range tokens {
		if tok == "-m" && i+1 < len(tokens) && strings.HasPrefix(tokens[i+1], "reclaiming from ") {
			return true
		}
	}
	return false
}

// doctrineStaleAfter is the scratch board's staleness horizon: short
// enough that a real sleep in the test is cheap, generous enough that
// scheduling jitter across a handful of preceding in-process writes can't
// make an unrelated claim look stale by accident.
const doctrineStaleAfter = "300ms"

// doctrineBoard creates the scratch, ready-capable board the walkthrough
// runs against. Its declaration is deliberately NOT sourced from the
// doctrine section (board creation is "The board" spec section, out of
// this task's scope — the pattern section teaches idioms against an
// already-declared board, exactly like the other seven patterns never
// re-teach `ledger create` either).
func doctrineBoard(t *testing.T) string {
	t.Helper()
	dir := initRepo(t)
	_, se, code := run(t, dir, "create", "issues", "--scope", "issue board doctrine walkthrough",
		"--field", "status=open,in-progress,closed,wontfix",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by",
		"--require-evidence", "status=closed",
		"--stale-after", doctrineStaleAfter)
	if code != 0 {
		t.Fatalf("create scratch board: %s", se)
	}
	return dir
}

// TestDoctrineVerbatimWalkthrough is spec test 17: every fenced command
// line in SKILL.md's "## Issue board" section, executed in document order
// against a fresh scratch board, must exit 0 or with its documented error
// identifier. No drift between doctrine and tool allowed.
func TestDoctrineVerbatimWalkthrough(t *testing.T) {
	dir := doctrineBoard(t)
	cmds := parseDoctrineCommands(t)
	if len(cmds) == 0 {
		t.Fatal("no fenced commands found in the Issue board section")
	}

	history := map[string][]string{}
	for _, c := range cmds {
		if hasReclaimMessage(c.tokens) {
			time.Sleep(350 * time.Millisecond)
		}
		args := resolveExpectPlaceholders(c.tokens, c.wantErr, history)

		so, se, code := run(t, dir, args...)
		if code != c.wantExit {
			t.Fatalf("doctrine line %q: exit %d (want %d)\nstdout: %s\nstderr: %s", c.raw, code, c.wantExit, so, se)
		}
		if c.wantErr != "" {
			doc := mustJSON(t, se)
			if doc["error"] != c.wantErr {
				t.Fatalf("doctrine line %q: error %v (want %q)\nstderr: %s", c.raw, doc["error"], c.wantErr, se)
			}
		}
		if code == 0 && len(args) > 0 && args[0] == "set" {
			doc := mustJSON(t, so)
			id, _ := doc["id"].(string)
			if id == "" {
				t.Fatalf("doctrine line %q: set response carried no id: %s", c.raw, so)
			}
			hk := args[1] + "/" + setField(args)
			history[hk] = append(history[hk], id)
		}
	}
}

// ---- spec test 18: watch doctrine, driven independently of the doc text ----

// watchResult is one background `ledger watch` subprocess's outcome.
type watchResult struct {
	stdout, stderr string
	code           int
}

// startWatch launches the built race binary (see race_test.go's TestMain,
// which builds it once for the whole package) as a real watch subprocess
// against dir, returning a channel the caller reads its outcome from once
// the process exits (on a match, or on timeout). A real subprocess is
// used rather than an in-process call so it genuinely blocks and polls
// concurrently with the triggering write below, exactly like two
// independent agents would.
func startWatch(dir string, args ...string) <-chan watchResult {
	ch := make(chan watchResult, 1)
	go func() {
		cmd := exec.Command(binPath, append([]string{"--store", dir}, args...)...)
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		ch <- watchResult{so.String(), se.String(), exitCode(err)}
	}()
	return ch
}

// TestWatchFullStatusVocabWakesOnClaim: the doctrine's picking-loop watch
// idiom passes the full status vocabulary as --value terms; a claim (a
// status=in-progress write) must wake it.
func TestWatchFullStatusVocabWakesOnClaim(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)

	ch := startWatch(dir, "watch", "--ledger", "issues", "--value", "open,in-progress,closed,wontfix", "--timeout", "5")
	time.Sleep(300 * time.Millisecond) // let the watch subprocess start and resolve its starting cursor first
	_, se, code := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "b")
	if code != 0 {
		t.Fatalf("claim: %s", se)
	}

	select {
	case r := <-ch:
		if r.code != 0 {
			t.Fatalf("watch must wake with exit 0 on the claim: %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
		}
		events, _ := mustJSON(t, r.stdout)["events"].([]any)
		if len(events) == 0 {
			t.Fatalf("watch must deliver the claim event: %s", r.stdout)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("watch subprocess never returned")
	}
}

// TestWatchLabelCollisionSpuriousWake: watch matches any field's value,
// unscoped — a labels write using the token "open" is not a status claim
// at all, but it collides with the status vocabulary and wakes the
// watcher anyway. The doctrine calls this harmless; the fix is just to
// re-run `ready`, never to treat it as a real signal.
func TestWatchLabelCollisionSpuriousWake(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}

	ch := startWatch(dir, "watch", "--ledger", "issues", "--value", "open,in-progress,closed,wontfix", "--timeout", "5")
	time.Sleep(300 * time.Millisecond)
	_, se, code := run(t, dir, "set", "k1", "labels=open", "--expect", "none", "--as", "b")
	if code != 0 {
		t.Fatalf("label write: %s", se)
	}

	select {
	case r := <-ch:
		if r.code != 0 {
			t.Fatalf("a spurious wake still exits 0: %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
		}
		doc := mustJSON(t, r.stdout)
		events, _ := doc["events"].([]any)
		if len(events) != 1 {
			t.Fatalf("expected exactly the spurious labels=open event: %s", r.stdout)
		}
		ev, _ := events[0].(map[string]any)
		fields, _ := ev["fields"].(map[string]any)
		if _, isStatus := fields["status"]; isStatus {
			t.Fatalf("the spurious wake must be the labels event, not a real status claim: %v", ev)
		}
		if fields["labels"] != "open" {
			t.Fatalf("expected the labels=open collision to be what woke the watcher: %v", ev)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("watch subprocess never returned")
	}
}

// TestWatchTimeoutExitsClean: staleness fires no event, so a watch
// timeout — not an error condition, just the absence of a match — is how
// a picker notices it needs to re-check `ready`. The exit is clean: code
// 2, a timeout payload on stdout, nothing on stderr.
func TestWatchTimeoutExitsClean(t *testing.T) {
	dir := setupReady(t)
	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "title", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}

	stdout, stderr, code := execLedger(t, dir, "watch", "--ledger", "issues",
		"--value", "open,in-progress,closed,wontfix", "--timeout", "1")
	if code != 2 {
		t.Fatalf("timeout must exit 2: %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("a timeout is not an error — stderr must stay empty: %q", stderr)
	}
	doc := mustJSON(t, stdout)
	if doc["timeout"] != true {
		t.Fatalf("timeout payload must carry timeout:true: %s", stdout)
	}
	if events, _ := doc["events"].([]any); len(events) != 0 {
		t.Fatalf("a timeout must carry no events: %s", stdout)
	}
}
