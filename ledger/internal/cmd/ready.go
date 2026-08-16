package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/board"
	"ledger/internal/model"
	"ledger/internal/out"
)

func init() { register(newReadyCmd) }

func newReadyCmd(c *Ctx) *cobra.Command {
	var ledgerFlag string
	var whereRaw []string
	var limit int
	cmd := &cobra.Command{Use: "ready", Short: "the board, answered: ready/held/blocked/attention + frontier verdict",
		Args: noPositionals("ready"),
		RunE: func(_ *cobra.Command, _ []string) error { return runReady(c, ledgerFlag, whereRaw, limit) }}
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	cmd.Flags().StringArrayVar(&whereRaw, "where", nil, "FIELD=VALUE (exact) or FIELD~=TOKEN (membership); repeatable, AND'd — applies to every list")
	cmd.Flags().IntVar(&limit, "limit", 50, "cap each list (ready/held/blocked/attention); totals always carry the true count")
	return cmd
}

func runReady(c *Ctx, ledgerFlag string, whereRaw []string, limit int) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	if !model.ReadyCapable(led.Meta) {
		return out.Errf("bad_usage",
			"declare the ready-capable shape at create time: --field status=<...,open,in-progress,...> "+
				"--terminal status=<terminal-values> --guard status --multi-field labels (see `ledger create --help`)",
			4, "'%s' is not ready-capable — ready needs a declared status field with --terminal and --guard status", led.Slug)
	}
	clauses, err := parseWhere(whereRaw, led.Meta)
	if err != nil {
		return err
	}

	b := board.Build(led.Meta, led.Events)
	filter := func(k *board.Key) bool { return matchWhere(k, clauses) }
	env := b.Envelope(time.Now(), limit, filter)

	payload := map[string]any{
		"ledger": led.Slug, "frontier": env.Frontier,
		"ready": env.Ready, "held": env.Held, "blocked": env.Blocked, "attention": env.Attention,
		"totals": env.Totals,
	}
	lines := addRedirect(c, led, payload)
	lines = append(lines, readyLines(led.Slug, env)...)
	outEmit(c, payload, lines)
	return nil
}

// readyLines renders the envelope for a TTY: one summary line, then one
// line per entry across the four lists in the same order the JSON carries
// them.
func readyLines(slug string, env board.Envelope) []string {
	lines := []string{fmt.Sprintf("%s  frontier=%s  ready=%d held=%d blocked=%d attention=%d",
		slug, env.Frontier, env.Totals.Ready, env.Totals.Held, env.Totals.Blocked, env.Totals.Attention)}
	for _, e := range env.Ready {
		l := fmt.Sprintf("  ready    %-20s by %-12s [%s]  %q", out.EscapeControls(e.Key), out.EscapeControls(e.By), e.ID, out.EscapeControls(e.Title))
		if len(e.UnblockedWithoutEvidence) > 0 {
			l += "  unblocked_without_evidence=" + strings.Join(e.UnblockedWithoutEvidence, ",")
		}
		lines = append(lines, l)
	}
	for _, e := range env.Held {
		l := fmt.Sprintf("  held     %-20s %-6s by %-12s [%s]  %q", out.EscapeControls(e.Key), e.Kind, out.EscapeControls(e.By), e.ID, out.EscapeControls(e.Title))
		if e.Stale != nil {
			l += fmt.Sprintf("  age=%s stale=%v", e.Age, *e.Stale)
		}
		if len(e.WaitingOn) > 0 {
			l += "  waiting_on=" + waitingOnLine(e.WaitingOn)
		}
		lines = append(lines, l)
	}
	for _, e := range env.Blocked {
		l := fmt.Sprintf("  blocked  %-20s by %-12s [%s]  waiting_on=%s", out.EscapeControls(e.Key), out.EscapeControls(e.By), e.ID, waitingOnLine(e.WaitingOn))
		lines = append(lines, l)
	}
	for _, e := range env.Attention {
		switch e.Reason {
		case "stale-claim":
			lines = append(lines, fmt.Sprintf("  attn     stale-claim  %-20s by %-12s age=%s [%s]",
				out.EscapeControls(e.Key), out.EscapeControls(e.By), e.Age, e.ID))
		case "statusless":
			lines = append(lines, "  attn     statusless   "+out.EscapeControls(e.Key))
		case "cycle":
			lines = append(lines, "  attn     cycle        "+strings.Join(e.Keys, ","))
		}
	}
	return lines
}

func waitingOnLine(wo []board.WaitingOn) string {
	parts := make([]string, 0, len(wo))
	for _, w := range wo {
		parts = append(parts, w.Key+":"+w.State)
	}
	return strings.Join(parts, ",")
}
