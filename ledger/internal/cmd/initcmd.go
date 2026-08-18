package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/gitx"
	"ledger/internal/out"
)

func init() { register(newInitCmd) }

func newInitCmd(c *Ctx) *cobra.Command {
	var hooks bool
	cmd := &cobra.Command{Use: "init", Short: "bootstrap this directory for ledger: repo config, or a bare store",
		Long: "In a git repo: sets core.logAllRefUpdates=always and writes the .ledger.toml breadcrumb.\n" +
			"In a non-git directory: creates a bare store at ./.ledger.git.\n" +
			"Never commits anything and never auto-edits harness config.",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runInit(c, hooks)
		}}
	cmd.Flags().BoolVar(&hooks, "hooks", false,
		"write .ledger-hooks.md with the recommended SessionStart hook snippet (never edits harness config directly)")
	return cmd
}

// ledgerTomlContent is the committed breadcrumb: a marker comment plus the
// default sync remote as a commented-out git remote *name* (never a URL —
// a committed URL would let anyone who lands a commit redirect every
// clone's sync target).
const ledgerTomlContent = "# This repo uses `ledger` for durable agent working-state (git phantom refs).\n" +
	"# Bootstrap in a fresh clone:  ledger init && ledger sync\n" +
	"# Docs: run `ledger quickstart`\n" +
	"# remote = \"origin\"  # default sync remote (name only)\n"

var claudeStanzaLines = []string{
	"  ## Ledger",
	"  This repo tracks agent working-state with `ledger` (durable, git-backed).",
	"  Run `ledger ls` at the start of a session to see open work before starting new work.",
	"  Record status and handoffs with `ledger set` / `ledger note`; never write secrets into a ledger entry.",
}

func stanzaText() string { return strings.Join(claudeStanzaLines, "\n") }

// These three ride in every init's JSON payload (bootstrap_hint, stanza,
// admin_doc), not just the TTY lines — the agent-primary non-TTY mode needs
// the same doctrine pointers a human reads off the terminal.
const (
	bootstrapHint = "run `ledger quickstart` for agent doctrine"
	adminDocPath  = "ledger/docs/admin.md"
	commitHint    = "commit this file so clones discover the ledger"
)

var adminPointerLine = "docs: " + bootstrapHint + "; " +
	"admin runbook (mirror/force-push hazards, secrets incidents) at " + adminDocPath

// hooksSnippet is the content of the file --hooks writes. v1 scope stops at
// printing this file: detecting and editing a harness's settings.json is
// follow-on work with the sync plan, and the spec's own "printed, never
// auto-edited" rule for CLAUDE.md governs here too.
const hooksSnippet = "Paste this into `.claude/settings.json` yourself (merge with any existing " +
	"\"SessionStart\" hooks — ledger never edits harness config directly):\n\n" +
	"```json\n" +
	"{\n" +
	"  \"hooks\": {\n" +
	"    \"SessionStart\": [\n" +
	"      {\n" +
	"        \"hooks\": [\n" +
	"          {\n" +
	"            \"type\": \"command\",\n" +
	"            \"command\": \"ledger ls\"\n" +
	"          }\n" +
	"        ]\n" +
	"      }\n" +
	"    ]\n" +
	"  }\n" +
	"}\n" +
	"```\n"

func runInit(c *Ctx, hooks bool) error {
	target := c.StoreFlag
	if target == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		target = wd
	}

	// A target that's inside a git repo but isn't its root must never fall
	// through to the bare-store branch below: that would silently create a
	// shadow .ledger.git next to the real repo, and store resolution's
	// ancestor walk would then find the shadow before the real repo's own
	// (as-yet-uninstalled) store — a trap sprung on every future command run
	// from that subdirectory. Detect via git itself, comparing symlink-
	// resolved paths (macOS routes /tmp through /private/tmp, so a raw
	// string compare of an unresolved target against git's resolved
	// toplevel would false-positive on every plain temp-dir repo).
	toplevel, _, code := (gitx.Repo{Dir: target}).Git("", "rev-parse", "--show-toplevel")
	var lines []string
	var payload map[string]any
	var err error
	switch {
	case code == 0:
		var atRoot bool
		atRoot, err = sameDir(target, toplevel)
		if err != nil {
			return err
		}
		if !atRoot {
			return out.Errf("bad_value",
				fmt.Sprintf("run `ledger init` from the repo root (cd %s), or pass --store %s", toplevel, toplevel),
				4, "this is a subdirectory of git repo %s", toplevel)
		}
		lines, payload, err = initRepoCase(target)
	default:
		lines, payload, err = initBareCase(target)
	}
	if err != nil {
		return err
	}

	if hooks {
		hookPath := filepath.Join(target, ".ledger-hooks.md")
		if err := os.WriteFile(hookPath, []byte(hooksSnippet), 0o644); err != nil {
			return fmt.Errorf("write .ledger-hooks.md: %w", err)
		}
		lines = append(lines, "", "[ledger] wrote .ledger-hooks.md — paste its snippet into your harness config yourself")
		payload["hooks_snippet"] = hookPath
	}

	outEmit(c, payload, lines)
	return nil
}

// sameDir reports whether a and b name the same directory once symlinks are
// resolved out of both.
func sameDir(a, b string) (bool, error) {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false, fmt.Errorf("resolve %s: %w", a, err)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false, fmt.Errorf("resolve %s: %w", b, err)
	}
	return ra == rb, nil
}

// initRepoCase handles `ledger init` inside an existing git repo: it sets
// the reflog recovery net every time (cheap, idempotent), but writes the
// breadcrumb only once — a second run must never clobber a committed or
// hand-edited .ledger.toml.
func initRepoCase(target string) ([]string, map[string]any, error) {
	repo := gitx.Repo{Dir: target}
	if _, stderr, code := repo.Git("", "config", "core.logAllRefUpdates", "always"); code != 0 {
		return nil, nil, fmt.Errorf("git config core.logAllRefUpdates: %s", stderr)
	}

	// Best-effort refspec install: a freshly cloned repo already has its
	// "origin" remote, so this makes it sync-ready immediately rather than
	// waiting for the first `ledger sync` to repair it. Silently skipped
	// when the remote can't be resolved (zero or ambiguous remotes) — init
	// has no --remote flag, and sync's own repair runs regardless.
	refspecRepairs := installRefspecBestEffort(repo)

	payload := map[string]any{
		"kind": "repo", "path": target,
		"bootstrap_hint": bootstrapHint,
		"stanza":         stanzaText(),
		"admin_doc":      adminDocPath,
		"commit_hint":    commitHint,
	}
	if len(refspecRepairs) > 0 {
		payload["refspec_repairs"] = refspecRepairs
	}
	refspecLines := make([]string, len(refspecRepairs))
	for i, r := range refspecRepairs {
		refspecLines[i] = "[ledger] " + r
	}

	tomlPath := filepath.Join(target, ".ledger.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		payload["already_initialized"] = true
		lines := append([]string{
			"[ledger] already initialized (.ledger.toml exists) — refreshed core.logAllRefUpdates",
			adminPointerLine,
		}, refspecLines...)
		return lines, payload, nil
	}

	if err := os.WriteFile(tomlPath, []byte(ledgerTomlContent), 0o644); err != nil {
		return nil, nil, fmt.Errorf("write .ledger.toml: %w", err)
	}
	payload["already_initialized"] = false

	lines := []string{"[ledger] wrote .ledger.toml — " + commitHint, "",
		"Add to CLAUDE.md or AGENTS.md:"}
	lines = append(lines, claudeStanzaLines...)
	lines = append(lines, "", adminPointerLine)
	lines = append(lines, refspecLines...)
	return lines, payload, nil
}

// initBareCase handles `ledger init` in a directory with no git repo: it
// creates a self-describing bare store. Bare stores need no breadcrumb —
// their existence on disk is the marker — and bare git's reflogs default
// off, so the recovery-net config still needs setting explicitly.
func initBareCase(target string) ([]string, map[string]any, error) {
	bareDir := filepath.Join(target, ".ledger.git")
	if _, stderr, code := (gitx.Repo{}).Git("", "init", "--bare", bareDir); code != 0 {
		return nil, nil, fmt.Errorf("git init --bare: %s", stderr)
	}
	if _, stderr, code := (gitx.Repo{Dir: bareDir}).Git("", "config", "core.logAllRefUpdates", "always"); code != 0 {
		return nil, nil, fmt.Errorf("git config core.logAllRefUpdates: %s", stderr)
	}
	payload := map[string]any{
		"kind": "bare", "path": bareDir,
		"bootstrap_hint": bootstrapHint,
		"stanza":         stanzaText(),
		"admin_doc":      adminDocPath,
	}
	lines := []string{"[ledger] created bare store ./.ledger.git", adminPointerLine}
	return lines, payload, nil
}
