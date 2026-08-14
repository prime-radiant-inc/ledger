package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
)

// indexOf finds an event by id in the raw (sync-inclusive) chain. Cursor
// validity and cursor advance always check this chain, even though
// since/watch's delivery and filtering below only ever look at non-sync
// events — a cursor may legitimately land on a sync sentinel once merge
// lands (Plan 2). The linear local case makes membership the whole test;
// the merge-aware form is `git merge-base --is-ancestor`, which arrives
// with sync.
func indexOf(evs []model.Event, id string) int {
	for i, ev := range evs {
		if ev.ID == id {
			return i
		}
	}
	return -1
}

// ---- since ----

func init() { register(newSinceCmd) }

func newSinceCmd(c *Ctx) *cobra.Command {
	var limit int
	var ledgerFlag string
	cmd := &cobra.Command{Use: "since [cursor]", Short: "events after a cursor, oldest first",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cursor := ""
			if len(args) == 1 {
				cursor = args[0]
			}
			return runSince(c, cursor, limit, ledgerFlag)
		}}
	cmd.Flags().IntVar(&limit, "limit", 0, "max events to return (0 = unlimited)")
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	return cmd
}

func runSince(c *Ctx, cursor string, limit int, ledgerFlag string) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	idx := -1
	if cursor != "" {
		idx = indexOf(led.Events, cursor)
		if idx < 0 {
			return out.Errf("reset_required",
				"ledger status refolds current state; ledger tail -n 50 shows recent events", 4,
				"cursor '%s' is not on ledger '%s'", cursor, led.Slug)
		}
	}
	evs := nonSyncEvents(led.Events[idx+1:])
	if limit > 0 && len(evs) > limit {
		evs = evs[:limit]
	}
	next := cursor
	if len(evs) > 0 {
		next = evs[len(evs)-1].ID
	}
	lines := make([]string, 0, len(evs))
	for _, ev := range evs {
		lines = append(lines, eventLine(ev))
	}
	outEmit(c, map[string]any{"ledger": led.Slug, "events": eventsJSON(evs), "cursor": next, "count": len(evs)}, lines)
	return nil
}

// ---- watch ----

// watchOpts carries watch's flags. timeoutSet distinguishes an explicit
// --timeout from the flag's default, which matters only for the
// --follow/--timeout conflict check.
type watchOpts struct {
	ledger, since, key, kind string
	values                   []string
	timeout                  float64
	timeoutSet               bool
	follow                   bool
}

func init() { register(newWatchCmd) }

func newWatchCmd(c *Ctx) *cobra.Command {
	var o watchOpts
	cmd := &cobra.Command{Use: "watch", Short: "drain matching events, then block for more",
		Args: cobra.NoArgs,
		RunE: func(cc *cobra.Command, _ []string) error {
			o.timeoutSet = cc.Flags().Changed("timeout")
			return runWatch(c, o)
		}}
	cmd.Flags().StringVar(&o.since, "since", "", "cursor to resume from (default: current head)")
	cmd.Flags().StringVar(&o.key, "key", "", "filter to one item key")
	cmd.Flags().StringSliceVar(&o.values, "value", nil, "comma-separated field values to match")
	cmd.Flags().StringVar(&o.kind, "kind", "", "also deliver notes of this kind, alongside matching sets")
	cmd.Flags().Float64Var(&o.timeout, "timeout", 60, "seconds to wait for a match; 0 = forever")
	cmd.Flags().BoolVar(&o.follow, "follow", false, "stream matches forever, one JSON line per event (never exits on its own)")
	cmd.Flags().StringVar(&o.ledger, "ledger", "", "target ledger")
	return cmd
}

// resolveStartCursor is watch's cold-start rule: an explicit --since is
// used as-is (no announcement — the caller already knows its cursor);
// cursorless, it resolves the current head and announces it, since that's
// the only way a caller with no prior cursor can resume exactly where this
// run started watching from. The announcement has two forms: a TTY line
// (both follow and non-follow), and — only under --follow — a leading JSON
// line on stdout, because --follow's per-event stream has no enclosing
// envelope to carry `starting_cursor` in the way the non-follow path's
// final drain/timeout payload does (see the `start` merge in runWatch).
func resolveStartCursor(c *Ctx, led *fold.Ledger, since string, follow bool) (string, map[string]any, error) {
	if since != "" {
		return since, map[string]any{}, nil
	}
	h, err := c.Store.HeadID(led.Slug)
	if err != nil {
		return "", nil, mapStoreErr(err, led.Slug)
	}
	start := map[string]any{"starting_cursor": h}
	switch {
	case c.TTY:
		fmt.Fprintln(c.Stdout, "starting cursor: "+h)
	case follow:
		line, _ := json.Marshal(start)
		fmt.Fprintln(c.Stdout, string(line))
	}
	return h, start, nil
}

func runWatch(c *Ctx, o watchOpts) error {
	if o.follow && o.timeoutSet {
		return out.Errf("bad_value", "drop --timeout — --follow streams until killed", 4, "--follow has no timeout")
	}
	led, err := c.PickLedger(o.ledger)
	if err != nil {
		return err
	}

	cur, start, err := resolveStartCursor(c, led, o.since, o.follow)
	if err != nil {
		return err
	}

	hasDeadline := !o.follow && o.timeout > 0
	deadline := time.Now().Add(time.Duration(o.timeout * float64(time.Second)))

	for {
		led, err = c.Load(led.Slug) // refold; cheap (batched) and correct
		if err != nil {
			return err
		}
		idx := indexOf(led.Events, cur)
		if idx < 0 {
			return out.Errf("reset_required", "restart with `ledger watch` (no --since) to watch from now", 4,
				"cursor '%s' is not on ledger '%s'", cur, led.Slug)
		}
		newRaw := led.Events[idx+1:]
		hits := filterHits(nonSyncEvents(newRaw), o)

		// --follow never returns: each poll streams any new hits as one JSON
		// line apiece, then keeps waiting. Not covered by an automated test —
		// an infinite loop is untestable without process control (kill/timeout
		// from outside); verified by hand instead.
		if o.follow {
			for _, h := range hits {
				line, _ := json.Marshal(followDoc(h))
				fmt.Fprintln(c.Stdout, string(line))
			}
			if len(newRaw) > 0 {
				cur = newRaw[len(newRaw)-1].ID
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if len(hits) > 0 {
			payload := map[string]any{"ledger": led.Slug, "events": eventsJSON(hits), "cursor": newRaw[len(newRaw)-1].ID}
			for k, v := range start {
				payload[k] = v
			}
			outEmit(c, payload, watchLines(hits))
			return nil
		}
		if len(newRaw) > 0 {
			cur = newRaw[len(newRaw)-1].ID // advance past non-matching events so a re-poll doesn't re-scan them
		}
		if hasDeadline && time.Now().After(deadline) {
			payload := map[string]any{"ledger": led.Slug, "timeout": true, "events": []any{}, "cursor": cur}
			for k, v := range start {
				payload[k] = v
			}
			out.Emit(c.Stdout, c.TTY, payload, []string{"timeout — no matching events; cursor: " + cur})
			// main's error mapping special-cases Code == "watch_timeout": the
			// payload above is already written, so it prints nothing further
			// and just returns ExitCode — a second error document would be a
			// second write to the same stream.
			return &out.CLIError{Code: "watch_timeout", ExitCode: 2}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// filterHits is what watch actually delivers from a batch of non-sync
// events: `set` events (optionally scoped by --key/--value), plus `note`
// events of the requested --kind riding alongside them. Notes never match
// when --kind is unset — the default drain is set events only.
func filterHits(evs []model.Event, o watchOpts) []model.Event {
	var hits []model.Event
	for _, ev := range evs {
		switch ev.Type {
		case "set":
			if o.key != "" && ev.Key != o.key {
				continue
			}
			if len(o.values) > 0 && !fieldsMatchAny(ev.Fields, o.values) {
				continue
			}
			hits = append(hits, ev)
		case "note":
			if o.kind == "" || ev.Kind != o.kind {
				continue
			}
			if o.key != "" && ev.Key != o.key {
				continue
			}
			hits = append(hits, ev)
		}
	}
	return hits
}

func fieldsMatchAny(fields map[string]string, values []string) bool {
	for _, v := range fields {
		for _, want := range values {
			if v == want {
				return true
			}
		}
	}
	return false
}

func watchLines(hits []model.Event) []string {
	lines := make([]string, 0, len(hits))
	for _, ev := range hits {
		lines = append(lines, eventLine(ev))
	}
	return lines
}

// followDoc is --follow's per-line JSON shape: enough to act on an event as
// it lands, without the full envelope a batched watch/since payload carries.
// Note events additionally carry kind and a body preview (200 runes) — a
// bare {key,fields:null} tells a streaming consumer nothing about a note.
func followDoc(ev model.Event) map[string]any {
	doc := map[string]any{"id": ev.ID, "key": ev.Key, "fields": ev.Fields, "by": ev.Author, "ts": ev.TS}
	if ev.Type == "note" {
		doc["kind"] = ev.Kind
		doc["text"] = truncateRunes(ev.Text, 200)
	}
	return doc
}
