package cmd

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

func init() { register(newRollupCmd) }

func newRollupCmd(c *Ctx) *cobra.Command {
	var msg, as, ledgerFlag, idemKey string
	cmd := &cobra.Command{Use: "rollup [EVENT_ID ...]",
		Short: "encapsulate a finished thread into one summary line (bare: show roots + instructions)",
		RunE: func(_ *cobra.Command, args []string) error {
			return runRollup(c, args, msg, as, ledgerFlag, idemKey)
		}}
	cmd.Flags().StringVarP(&msg, "message", "m", "", "the one-line summary")
	cmd.Flags().StringVar(&as, "as", "", "author identity for this write")
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	cmd.Flags().StringVar(&idemKey, "idempotency-key", "", "dedupe key scoped to (author, key) — rollups have no item key")
	return cmd
}

func runRollup(c *Ctx, ids []string, msg, as, ledgerFlag, idemKey string) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return rollupRootsView(c, led)
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return out.Errf("empty_body", `add -m "what this thread was and how it ended"`, 4,
			"a rollup needs its one-line summary")
	}
	if strings.ContainsAny(msg, "\n\r") {
		return out.Errf("bad_value", "put longer prose in a note, then cite that note's id in the summary line", 4,
			"a rollup summary is exactly one line")
	}
	author := model.ResolveAuthor(as)
	if idemKey != "" {
		// Mirrors set/note's semantics, but scoped to (author, key) alone —
		// a rollup has no item key, so that pair is the whole scope.
		for _, ev := range led.Events {
			if ev.Type == "rollup" && ev.IdempotencyKey == idemKey && ev.Author == author {
				outEmit(c, map[string]any{"id": ev.ID, "ledger": led.Slug, "deduped": true, "by": ev.Author},
					[]string{"deduped against " + ev.ID})
				return nil
			}
		}
	}
	byID := map[string]model.Event{}
	for _, e := range led.Events {
		if e.Type != "sync" {
			byID[e.ID] = e
		}
	}
	seen := map[string]bool{}
	var children []string
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := byID[id]; !ok {
			return out.Errf("unknown_event", "ledger tail --raw -n 30  lists recent events with their ids", 4,
				"'%s' is not an event on '%s'", id, led.Slug)
		}
		if owner, taken := led.Parent[id]; taken {
			return out.Errf("child_taken",
				"records have one parent — include that rollup instead: ledger rollup "+owner+" ... -m \"...\"", 4,
				"'%s' is already inside rollup %s", id, owner)
		}
		children = append(children, id)
	}
	ev := model.NewEvent("rollup", author, c.Store.Repo)
	ev.Children = children
	ev.Text = msg
	ev.IdempotencyKey = idemKey
	id, err := c.Store.Append(led.Slug, ev, nil, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, led.Slug)
	}
	due := -1
	if after, err := c.Load(led.Slug); err == nil {
		due = after.Due()
	}
	outEmit(c, map[string]any{"id": id, "ledger": led.Slug, "children": len(children), "rollup_due": due},
		[]string{"[" + id + "] " + led.Slug + ": " + strconv.Itoa(len(children)) +
			" records rolled into one line (" + strconv.Itoa(due) + " still unrolled)"})
	return nil
}

func rollupRootsView(c *Ctx, led *fold.Ledger) error {
	roots := led.Roots()
	rows := make([]map[string]any, 0, len(roots))
	lines := []string{"# " + led.Slug + " — current roots (" + strconv.Itoa(led.Due()) + " records not yet inside any rollup)"}
	for _, e := range roots {
		line := rootLine(led, e)
		rows = append(rows, map[string]any{"id": e.ID, "type": e.Type, "line": line})
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "",
		"Roll a FINISHED thread (a resolved hypothesis, a done task arc, a settled",
		"decision trail) into one line:",
		`  ledger rollup <id> <id> ... -m "one line" --as <role>`,
		"The line is a signpost for a cold reader: say what happened and how it",
		"ended, and carry concrete anchors (key names, evidence kinds, counts) into",
		"it, keeping each anchor next to the claim it actually backs. Summarize —",
		"never invent, and never restate another agent's evidenced claim as fact;",
		"it stays their testimony. A bridge note that closes one thread and opens",
		"another belongs to the thread it opens. Children may themselves be rollups",
		"— that's also the fix for a bad summary: roll IT up under a better line.",
		"Recent live work stays unrolled.")
	outEmit(c, map[string]any{"ledger": led.Slug, "rollup_due": led.Due(), "roots": rows}, lines)
	return nil
}

// rootLine renders one root for the curated views. Extended by tail (Task 3).
func rootLine(led *fold.Ledger, e model.Event) string {
	if e.Type == "rollup" {
		return "[" + e.ID + "] rollup by " + out.EscapeControls(e.Author) +
			" (" + strconv.Itoa(len(e.Children)) + " records — --in " + e.ID + " opens it): " +
			out.EscapeControls(e.Text)
	}
	return eventLine(e)
}
