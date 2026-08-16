package cmd

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
)

// lsClosedCutoff and lsIdleAfter are the spec's 30-day/45-day windows, kept
// as package vars (rather than inlined constants) so tests can override them
// instead of needing a fake clock.
var (
	lsClosedCutoff = 30 * 24 * time.Hour
	lsIdleAfter    = 45 * 24 * time.Hour
)

func init() { register(newLsCmd) }

func newLsCmd(c *Ctx) *cobra.Command {
	var all bool
	cmd := &cobra.Command{Use: "ls", Short: "list ledgers with freshness", Args: noPositionals("show"),
		RunE: func(_ *cobra.Command, _ []string) error {
			return runLs(c, all)
		}}
	cmd.Flags().BoolVar(&all, "all", false, "include ledgers closed more than 30 days ago")
	return cmd
}

func runLs(c *Ctx, all bool) error {
	slugs, err := c.Store.Slugs()
	if err != nil {
		return err
	}
	if len(slugs) == 0 {
		payload := map[string]any{"ledgers": []map[string]any{}}
		outEmit(c, payload, c.noteShadowedStore(payload,
			[]string{"no ledgers in this repo — ledger create <slug> --scope <ref> starts one"}))
		return nil
	}

	leds := make([]*fold.Ledger, 0, len(slugs))
	for _, s := range slugs {
		led, err := c.Load(s)
		if err != nil {
			continue // torn/foreign ref: skip, never crash a listing
		}
		leds = append(leds, led)
	}

	now := time.Now().UTC()
	kept := leds[:0]
	for _, led := range leds {
		if all || led.State == "open" || now.Sub(lastEventTime(led)) <= lsClosedCutoff {
			kept = append(kept, led)
		}
	}

	sort.Slice(kept, func(i, j int) bool { return lastEventTime(kept[i]).After(lastEventTime(kept[j])) })

	if len(kept) == 0 {
		payload := map[string]any{"ledgers": []map[string]any{}}
		outEmit(c, payload, c.noteShadowedStore(payload,
			[]string{"no ledgers match — ledger ls --all also shows ledgers closed more than 30 days ago"}))
		return nil
	}

	rows := make([]map[string]any, 0, len(kept))
	lines := make([]string, 0, len(kept))
	for _, led := range kept {
		last := lastEventTime(led)
		idle := led.State == "open" && now.Sub(last) > lsIdleAfter
		events := len(nonSyncEvents(led.Events))
		lastTS := led.Events[len(led.Events)-1].TS
		rows = append(rows, map[string]any{
			"slug": led.Slug, "scope": led.Meta.Scope, "state": led.State,
			"last": lastTS, "events": events, "idle": idle,
		})
		lines = append(lines, lsLine(led, lastTS, events, idle, now))
	}
	payload := map[string]any{"ledgers": rows}
	outEmit(c, payload, c.noteShadowedStore(payload, lines))
	return nil
}

// lastEventTime is the freshness clock for sorting, the 30-day closed
// cutoff, and the 45-day idle mark alike — the last event in the raw chain
// (sync events included: a merge is itself evidence the ledger is alive).
func lastEventTime(led *fold.Ledger) time.Time {
	if len(led.Events) == 0 {
		return time.Time{}
	}
	ts := led.Events[len(led.Events)-1].TS
	t, err := model.ParseTS(ts)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// lsLine renders one TTY row: slug, scope (truncated so a long scope can't
// blow out the column alignment), state — with the idle marker folded in,
// e.g. "open, idle 62d" — last-write age, and the non-sync event count.
func lsLine(led *fold.Ledger, lastTS string, events int, idle bool, now time.Time) string {
	state := led.State
	if idle {
		days := int(now.Sub(lastEventTime(led)).Hours() / 24)
		state = fmt.Sprintf("open, idle %dd", days)
	}
	return fmt.Sprintf("%-20s %-44s %-20s last %-10s (%d events)",
		out.EscapeControls(led.Slug), out.EscapeControls(truncateRunes(led.Meta.Scope, 44)),
		out.EscapeControls(state), out.Age(lastTS), events)
}
