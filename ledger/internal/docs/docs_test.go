package docs_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ledger/internal/cmd"
)

func TestQuickstartLengthBudget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(data, []byte("\n")); n > 110 {
		t.Fatalf("quickstart is %d lines; budget is 110 (spec: kata-sized)", n)
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
				t.Fatalf("%s: `ledger %s` exit %d want %d\nstdout: %s\nstderr: %s",
					file, strings.Join(ex.argv, " "), code, ex.expectExit, so.String(), se.String())
			}
			if ex.expectErr != "" && !strings.Contains(se.String(), ex.expectErr) {
				t.Fatalf("%s: `ledger %s` expected error %q, got %s",
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
// the live verb list from the cobra root itself — via `ledger --help`,
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
		t.Fatalf("ledger --help: exit %d\n%s", code, se.String())
	}
	verbs := verbsFromHelp(so.String())
	if len(verbs) == 0 {
		t.Fatal("parsed zero verbs out of `ledger --help` output — parser or cobra output format changed")
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
