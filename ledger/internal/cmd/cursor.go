package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/dag"
	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
)

// deliverRange is the whole cursor contract (sync spec rev 7, Addition 2),
// shared by `since` and `watch`. It returns the events to deliver, in batch
// order, and the cursor to emit.
//
//   - Validity: the cursor is a reachability token against the ref — valid
//     iff it is an ancestor of, or equal to, the tip. Anything else is
//     reset_required (resetHint is the caller's recovery advice; the two
//     verbs recover differently).
//   - Delivery: the non-sentinel commits of `cursor..tip`. Set-based, so
//     after a merge the merged-in events sitting fold-BELOW a consumed
//     cursor are delivered exactly once, which positional slice arithmetic
//     silently dropped.
//   - Batch order: Addition 1's Kahn fold, run on the RANGE's contracted
//     sub-DAG — the same algorithm as the global fold, never a second
//     comparator. Stated consequence: a range can order two events
//     concurrent with the cursor differently than `tail` renders them.
//   - Cursor emission: an unpaged drain emits the tip, sentinel included. A
//     --limit drain stops at the first delivered event C that dominates
//     everything delivered so far AND descends from the incoming cursor,
//     which makes `cursor..C` exactly the delivered set; if the range
//     exhausts before such a C exists, it emits the tip, same as unpaged.
//     --limit is therefore a floor, not a ceiling.
func deliverRange(c *Ctx, led *fold.Ledger, cursor string, limit int, resetHint string) ([]model.Event, string, error) {
	// The tip is read BEFORE the events, and that order is load-bearing: the
	// event read must cover every commit in the range, or a commit that
	// landed between the two reads would look like a sentinel (contracted
	// out, never delivered) while the cursor advanced past it. Reading the
	// tip first bounds the range by the older read; anything newer is simply
	// left for the next call.
	tip, err := c.Store.HeadSHA(led.Slug)
	if err != nil {
		return nil, "", mapStoreErr(err, led.Slug)
	}
	if cursor != "" && !c.Store.IsAncestor(cursor, tip) {
		return nil, "", out.Errf("reset_required", resetHint, 4,
			"cursor '%s' is not on ledger '%s'", cursor, led.Slug)
	}
	evs, _, err := c.Store.Events(led.Slug)
	if err != nil {
		return nil, "", mapStoreErr(err, led.Slug)
	}
	byID := make(map[string]model.Event, len(evs))
	for _, ev := range evs {
		byID[ev.ID] = ev
	}
	nodes, err := c.Store.RangeNodes(cursor, tip)
	if err != nil {
		return nil, "", out.Errf("git_failed", "", 1, "%s", err)
	}
	for i := range nodes {
		ev, ok := byID[nodes[i].SHA[:10]]
		if !ok {
			// no readable event here: a sync merge, or a torn/foreign commit.
			// Contracted out of the batch order, never delivered — the same
			// invisibility every other read gives them.
			nodes[i].IsSentinel = true
			continue
		}
		nodes[i].TS = ev.TS
	}
	res := dag.Sort(nodes)

	// Condition (ii) bookkeeping: the delivered set's maximal elements on the
	// range's contracted DAG. A node joins the frontier when delivered (its
	// children can only come later in topological order) and leaves it the
	// moment one of its children is delivered. A frontier of exactly one is
	// precisely "some delivered C has every delivered event as an
	// ancestor-or-equal" — and that C is always the event just delivered.
	parents := map[string][]string{}
	for p, kids := range res.Children {
		for _, k := range kids {
			parents[k] = append(parents[k], p)
		}
	}
	frontier := map[string]bool{}
	delivered := make([]model.Event, 0, len(res.Order))
	for _, sha := range res.Order {
		delivered = append(delivered, byID[sha[:10]])
		frontier[sha] = true
		for _, p := range parents[sha] {
			delete(frontier, p)
		}
		if limit <= 0 || len(delivered) < limit || len(frontier) != 1 {
			continue
		}
		// Condition (i), one subprocess per candidate stop-point (never per
		// event): C must descend from the incoming cursor, or `cursor..C`
		// would not be the delivered set and the pager would re-deliver the
		// consumer's own branch forever. A cursorless drain has nothing to
		// descend from, so (i) is vacuous.
		if cursor == "" || c.Store.IsAncestor(cursor, sha) {
			return delivered, sha[:10], nil
		}
	}
	return delivered, tip[:10], nil
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
	evs, next, err := deliverRange(c, led, cursor, limit,
		"chit status refolds current state; chit tail -n 50 shows recent events")
	if err != nil {
		return err
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
		Args: noPositionals("watch"),
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
		// Unbounded by design: watch has no --limit in v1, so every batch is
		// the whole `cur..tip` range and every batch emits the tip (spec
		// Addition 2, the one amendment this design makes to the parent's
		// "same bound applies to watch"). deliverRange re-reads the ref and
		// the chain per poll, which is what makes this loop see new events.
		evs, next, err := deliverRange(c, led, cur, 0,
			"restart with `chit watch` (no --since) to watch from now")
		if err != nil {
			return err
		}
		hits := filterHits(evs, o)

		// --follow never returns: each poll streams any new hits as one JSON
		// line apiece, then keeps waiting. Not covered by an automated test —
		// an infinite loop is untestable without process control (kill/timeout
		// from outside); verified by hand instead.
		if o.follow {
			for _, h := range hits {
				line, _ := json.Marshal(followDoc(h))
				fmt.Fprintln(c.Stdout, string(line))
			}
			cur = next
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if len(hits) > 0 {
			payload := map[string]any{"ledger": led.Slug, "events": eventsJSON(hits), "cursor": next}
			for k, v := range start {
				payload[k] = v
			}
			outEmit(c, payload, watchLines(hits))
			return nil
		}
		cur = next // advance past non-matching events so a re-poll doesn't re-scan them
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
		case "rollup":
			// delivered on unfiltered watches; --key/--value/--kind are
			// set/note filters and a filtered watcher shouldn't wake for
			// curation (cursor still advances past them — spec test 43)
			if o.key == "" && len(o.values) == 0 && o.kind == "" {
				hits = append(hits, ev)
			}
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
