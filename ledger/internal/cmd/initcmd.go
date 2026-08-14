package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"ledger/internal/gitx"
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
	"# Bootstrap in a fresh clone:  ledger init && ledger ls\n" +
	"# Docs: run `ledger quickstart`\n" +
	"# remote = \"origin\"  # default sync remote (name only); uncomment once `ledger sync` ships\n"

var claudeStanzaLines = []string{
	"  ## Ledger",
	"  This repo tracks agent working-state with `ledger` (durable, git-backed).",
	"  Run `ledger ls` at the start of a session to see open work before starting new work.",
	"  Record status and handoffs with `ledger set` / `ledger note`; never write secrets into a ledger entry.",
}

const adminPointerLine = "docs: `ledger quickstart` for agent doctrine; " +
	"admin runbook (mirror/force-push hazards, secrets incidents) at ledger/docs/admin.md"

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

	var lines []string
	var payload map[string]any
	var err error
	if _, statErr := os.Stat(filepath.Join(target, ".git")); statErr == nil {
		lines, payload, err = initRepoCase(target)
	} else {
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

// initRepoCase handles `ledger init` inside an existing git repo: it sets
// the reflog recovery net every time (cheap, idempotent), but writes the
// breadcrumb only once — a second run must never clobber a committed or
// hand-edited .ledger.toml.
func initRepoCase(target string) ([]string, map[string]any, error) {
	repo := gitx.Repo{Dir: target}
	if _, stderr, code := repo.Git("", "config", "core.logAllRefUpdates", "always"); code != 0 {
		return nil, nil, fmt.Errorf("git config core.logAllRefUpdates: %s", stderr)
	}

	payload := map[string]any{"kind": "repo", "path": target}
	tomlPath := filepath.Join(target, ".ledger.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		payload["already_initialized"] = true
		return []string{
			"[ledger] already initialized (.ledger.toml exists) — refreshed core.logAllRefUpdates",
			adminPointerLine,
		}, payload, nil
	}

	if err := os.WriteFile(tomlPath, []byte(ledgerTomlContent), 0o644); err != nil {
		return nil, nil, fmt.Errorf("write .ledger.toml: %w", err)
	}
	payload["already_initialized"] = false

	lines := []string{"[ledger] wrote .ledger.toml — commit this file so clones discover the ledger", "",
		"Add to CLAUDE.md or AGENTS.md:"}
	lines = append(lines, claudeStanzaLines...)
	lines = append(lines, "", adminPointerLine)
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
	payload := map[string]any{"kind": "bare", "path": bareDir}
	lines := []string{"[ledger] created bare store ./.ledger.git", adminPointerLine}
	return lines, payload, nil
}
