package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/out"
)

func init() { register(newRenderCmd) }

func newRenderCmd(c *Ctx) *cobra.Command {
	var to, ledgerFlag string
	cmd := &cobra.Command{Use: "render", Short: "write show's projection to a file, deterministically",
		Long: "Writes exactly show's TTY-style spine+notes render to a file, but with\n" +
			"absolute timestamps in place of relative ages, so two runs against the\n" +
			"same ledger state produce byte-identical output.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRender(c, to, ledgerFlag)
		}}
	cmd.Flags().StringVar(&to, "to", "", "output file path (required)")
	cmd.MarkFlagRequired("to")
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	return cmd
}

func runRender(c *Ctx, to, ledgerFlag string) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	content := strings.Join(renderLines(c, led), "\n") + "\n"
	if err := os.WriteFile(to, []byte(content), 0o644); err != nil {
		return out.Errf("write_failed", "check the path is writable", 1, "%s", err)
	}
	outEmit(c, map[string]any{"ledger": led.Slug, "path": to, "bytes": len(content)},
		[]string{fmt.Sprintf("rendered %s to %s (%d bytes)", led.Slug, to, len(content))})
	return nil
}

// renderLines is show's TTY projection, deterministically: the same
// redirect/header/spine/notes lines runShow builds, but with each note's
// identity line carrying its absolute ts (noteSummaryLineAt) instead of
// show's Age(ts) — a relative age would make two renders of the same
// ledger state differ byte-for-byte depending only on wall-clock time.
func renderLines(c *Ctx, led *fold.Ledger) []string {
	rows := spineRows(led, "")
	committers, _ := c.Store.Committers(led.Slug)

	allNotes := led.Notes()
	recent := allNotes
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}
	eventCount := len(nonSyncEvents(led.Events))

	var lines []string
	if led.SupersededBy != "" {
		lines = append(lines, redirectLine(c, led))
	}
	lines = append(lines, fmt.Sprintf("%s  scope=%s  base=%s  state=%s  events=%d  head=%s",
		led.Slug, led.Meta.Scope, led.Meta.Base, led.State, eventCount, led.Head()))
	for _, r := range rows {
		lines = append(lines, spineLine(r))
	}
	for _, n := range recent {
		lines = append(lines, noteSummaryLineAt(n.TS, n, committers))
	}
	return lines
}
