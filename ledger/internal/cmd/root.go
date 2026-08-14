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
		Long: "Durable working-state for coding agents.\nStart with `ledger create <slug> --scope <what-it-tracks>`.\nEvery write prints its event id — that id is a cursor for since/watch.\nRun `ledger quickstart` for agent doctrine."}
	var storeFlag string
	root.PersistentFlags().StringVar(&storeFlag, "store", "", "store location (default: nearest .ledger.git or git repo)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		ctx.StoreFlag = storeFlag
		if cmd.Name() == "help" || cmd.Name() == "init" {
			// init bootstraps a store rather than requiring one to already
			// resolve — it may be run in a plain directory with no git repo
			// and no .ledger.git anywhere in the ancestry yet.
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
	out.WriteError(stderr, ctx.TTY, out.Errf("git_failed", "", 1, "%s", msg))
	return 1
}
