// Package cmd wires the verbs. One rule from the spec's CLI surface contract:
// no invocation may have write side-effects except a well-formed write verb.
package cmd

import (
	"errors"
	"fmt"
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
		Long: "Durable working-state for coding agents.\nStart with `ledger create <slug> --scope <what-it-tracks>`.\nEvery write prints its event id — that id is a cursor for since/watch.\nRun `ledger quickstart` for agent doctrine."}
	var storeFlag string
	root.PersistentFlags().StringVar(&storeFlag, "store", "", "store location (default: nearest .ledger.git or git repo)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		ctx.StoreFlag = storeFlag
		switch cmd.Name() {
		case "help", "init", "quickstart", "version", "update", "completion", "__complete", "__completeNoDesc":
			// init bootstraps a store rather than requiring one to already
			// resolve — it may be run in a plain directory with no git repo
			// and no .ledger.git anywhere in the ancestry yet. quickstart
			// prints embedded doctrine and needs no store at all — a cold
			// agent must be able to run it before any ledger exists. version
			// and update act on the binary, not a store, and the first thing
			// a fresh install runs must work in an empty directory. cobra's
			// completion machinery (script generation and the hidden
			// __complete probes) must also work anywhere a shell runs.
			return nil
		}
		res, err := store.Resolve(storeFlag)
		if err != nil {
			return out.Errf("unknown_ledger", "run inside a git repo, or `ledger init` in a plain directory", 4, "%s", err)
		}
		if res.Note != "" && ctx.TTY {
			io.WriteString(stderr, res.Note+"\n")
		}
		ctx.Store = res.Store
		ctx.Shadowed = res.Shadowed
		return nil
	}
	root.PersistentPostRun = func(cmd *cobra.Command, _ []string) {
		// only reached when the verb succeeded — the daily update notice
		// never piles onto an error
		passiveUpdateCheck(ctx, cmd.Name())
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
		if ce.Code == "watch_timeout" {
			// the verb already emitted its payload; a second error document
			// would be a second write to the same stream.
			return ce.ExitCode
		}
		out.WriteError(stderr, ctx.TTY, ce)
		return ce.ExitCode
	}
	msg := err.Error()
	if strings.Contains(msg, "unknown command") {
		out.WriteError(stderr, ctx.TTY, out.Errf("unknown_verb", "run `ledger --help` for the verb list", 4, "%s", msg))
		return 4
	}
	if isCobraUsageErr(msg) {
		out.WriteError(stderr, ctx.TTY, out.Errf("bad_usage", "ledger <verb> --help shows usage", 4, "%s", msg))
		return 4
	}
	out.WriteError(stderr, ctx.TTY, out.Errf("git_failed", "", 1, "%s", msg))
	return 1
}

// noPositionals rejects an unexpected positional on a verb that addresses
// its ledger by flag. Cobra's own NoArgs says `unknown command "csvstat" for
// "ledger show"`, which the mapping above reads as an unknown *verb* and
// hints at the verb list — but the caller typed a slug, carrying the
// positional habit over from set/close (two eval agents did this
// independently). The fix is the --ledger flag, so the hint is that command,
// ready to paste. suggest names the verb to put in it: itself for the read
// verbs, `show` for `ls`, which has no --ledger of its own.
func noPositionals(suggest string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return out.Errf("bad_usage",
			fmt.Sprintf("did you mean: ledger %s --ledger %s?", suggest, args[0]), 4,
			"ledger %s takes no positional arguments (got %q)", cmd.Name(), args[0])
	}
}

// isCobraUsageErr recognizes cobra's own flag/arg-parsing error text — these
// are never a git or store failure, so they must not fall into the generic
// git_failed bucket (exit 1, no hint). The substrings are cobra's own
// wording; there is no typed error to switch on here.
func isCobraUsageErr(msg string) bool {
	for _, pat := range []string{
		"unknown flag", "unknown shorthand flag", "invalid argument",
		"requires at least", "accepts at most", "required flag",
	} {
		if strings.Contains(msg, pat) {
			return true
		}
	}
	return false
}
