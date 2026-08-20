// Command chit-gh bridges a ledger issue board to a GitHub issue tracker.
//
// One verb, one-shot, idempotent, safe to re-run, safe to crash anywhere:
//
//	chit-gh sync --repo <owner/repo> --ledger <slug> [--store <path>]
//	              [--done <value>] [--not-planned <value>] [--list-limit <n>]
//
// Level 1 mirrors the board out to GitHub (keys become issues, terminal
// status changes close them with their message, renames retitle them, notes
// become comments). Level 2 takes GitHub changes back additively (new issues
// seed board keys, comments become notes, title edits become rename events,
// close/reopen become guarded board writes). Level 3 — label and assignee
// state sync — is deliberately out of scope.
//
// The board is canonical; GitHub is the intake and display window.
//
// Both sides are reached through CLIs only: `gh` for GitHub, `ledger` for
// the board. The bridge has NO identity of its own on GitHub — several
// logins may operate it, each also participating as a human, and nothing in
// the code path compares logins.
//
// OPERATING REQUIREMENT: single-instance. Two concurrent runs (a cron
// overlap on one store, or two replicas' operators at once) mint PERMANENT
// artifacts — duplicate issues, duplicate link notes, a doubly-imported or
// doubly-posted comment — and there is NO failure signal at run time: both
// runs exit 0. The first signal is the NEXT run's duplicate-link warnings,
// and comment-shaped overlaps leave no signal on any run, ever. Enforce one
// designated runner, a non-overlapping cron, or flock in the invocation; the
// bridge provides no lock in v1.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// Exit contract: 0 = report on stdout; 1 = error document on stderr;
// 2 = usage. The operator's lock/cron wrapper is written against this.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(argv []string, stdout, stderr *os.File) int {
	if len(argv) < 1 || argv[0] != "sync" {
		fmt.Fprintln(stderr, "usage: chit-gh sync --repo <owner/repo> --ledger <slug> [--store <path>] "+
			"[--done <value>] [--not-planned <value>] [--list-limit <n>]")
		return exitUsage
	}
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "GitHub repository, owner/repo (required)")
	slug := fs.String("ledger", "", "board slug (required)")
	store := fs.String("store", "", "ledger store path")
	ledgerBin := fs.String("ledger-bin", "ledger", "path to the ledger binary")
	ghBin := fs.String("gh-bin", "gh", "path to the gh binary")
	// The board's two MIRRORED TERMINALS, configured rather than assumed: a
	// legal ready-capable board can call these done/dropped. Startup refuses
	// a board whose terminal set does not match these two exactly.
	done := fs.String("done", "closed", "the board status value meaning done — mirrors to a completed GitHub close")
	notPlanned := fs.String("not-planned", "wontfix", "the board status value meaning not planned — mirrors to a 'not planned' GitHub close")
	listLimit := fs.Int("list-limit", 250, "issue-listing window; a run whose listing saturates it is refused")
	if err := fs.Parse(argv[1:]); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q — chit-gh sync takes flags only\n", fs.Arg(0))
		return exitUsage
	}
	// Flag shape runs BEFORE anything else, ahead of every check that reads
	// board or GitHub state.
	if *repo == "" || *slug == "" {
		fmt.Fprintln(stderr, "--repo and --ledger are both required")
		return exitUsage
	}
	if *listLimit < 1 {
		fmt.Fprintln(stderr, "--list-limit must be positive")
		return exitUsage
	}

	board := Board{Bin: *ledgerBin, Slug: *slug, Store: *store}
	// The pre-sync capability probe: an old `ledger` is refused BY NAME
	// rather than silently taking Law 3's refusal path on every guarded
	// intake write.
	if err := board.CheckCapable(); err != nil {
		return fail(stderr, err)
	}

	s := &Syncer{
		Board:      board,
		GH:         GH{Repo: *repo, Bin: *ghBin, ListLimit: *listLimit},
		Done:       *done,
		NotPlanned: *notPlanned,
	}
	report, err := s.Run()
	if err != nil {
		return fail(stderr, err)
	}
	blob, _ := json.MarshalIndent(report, "", " ")
	fmt.Fprintln(stdout, string(blob))
	return exitOK
}

func fail(stderr *os.File, err error) int {
	blob, _ := json.MarshalIndent(map[string]any{"ok": false, "error": err.Error()}, "", " ")
	fmt.Fprintln(stderr, string(blob))
	return exitError
}
