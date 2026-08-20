package cmd

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
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

// lsBootstrapHint is what `ls` prints in place of an empty listing when the
// committed breadcrumb (.ledger.toml) is present but no ledger refspec has
// been installed in this clone (installedRefspec, remote.go) — a fresh
// clone of a repo that uses ledger, before its own `chit init && chit
// sync` has ever run here. Without this, the first `ls` in that clone reads
// as "no ledgers exist" when the truth is "nothing has been synced yet".
const lsBootstrapHint = "this repo uses ledger, but it hasn't been bootstrapped in this clone — run `" + bootstrapCmd + "`"

func runLs(c *Ctx, all bool) error {
	slugs, err := c.Store.Slugs()
	if err != nil {
		return err
	}

	local := make(map[string]bool, len(slugs))
	leds := make([]*fold.Ledger, 0, len(slugs))
	for _, s := range slugs {
		local[s] = true
		led, err := c.Load(s)
		if err != nil {
			continue // torn/foreign ref: skip, never crash a listing
		}
		leds = append(leds, led)
	}

	unsynced := map[string]bool{}
	for _, led := range trackingOnlyLedgers(c, local) {
		leds = append(leds, led)
		unsynced[led.Slug] = true
	}

	if len(leds) == 0 {
		return emitLsEmpty(c, "no ledgers in this repo — chit create <slug> --scope <ref> starts one")
	}

	now := model.Now().UTC()
	kept := leds[:0]
	for _, led := range leds {
		if all || led.State == "open" || now.Sub(lastEventTime(led)) <= lsClosedCutoff {
			kept = append(kept, led)
		}
	}

	sort.Slice(kept, func(i, j int) bool { return lastEventTime(kept[i]).After(lastEventTime(kept[j])) })

	if len(kept) == 0 {
		return emitLsEmpty(c, "no ledgers match — chit ls --all also shows ledgers closed more than 30 days ago")
	}

	rows := make([]map[string]any, 0, len(kept))
	lines := make([]string, 0, len(kept))
	for _, led := range kept {
		last := lastEventTime(led)
		idle := led.State == "open" && now.Sub(last) > lsIdleAfter
		events := len(nonSyncEvents(led.Events))
		lastTS := led.Events[len(led.Events)-1].TS
		un := unsynced[led.Slug]
		rows = append(rows, map[string]any{
			"slug": led.Slug, "scope": led.Meta.Scope, "state": led.State,
			"last": lastTS, "events": events, "idle": idle, "unsynced": un,
		})
		lines = append(lines, lsLine(led, lastTS, events, idle, now, un))
	}
	payload := map[string]any{"ledgers": rows}
	outEmit(c, payload, c.noteShadowedStore(payload, lines))
	return nil
}

// emitLsEmpty is ls's shared empty-listing path — no ledgers at all, or none
// surviving the closed-cutoff filter. It swaps in lsBootstrapHint instead of
// defaultMsg when the repo's breadcrumb is committed but no ledger refspec
// is installed here yet (see lsBootstrapHint).
func emitLsEmpty(c *Ctx, defaultMsg string) error {
	msg := defaultMsg
	payload := map[string]any{"ledgers": []map[string]any{}}
	if breadcrumbExists(c.Store.Repo.Dir) && !installedRefspec(c.Store.Repo) {
		msg = lsBootstrapHint
		payload["note"] = msg
	}
	outEmit(c, payload, c.noteShadowedStore(payload, []string{msg}))
	return nil
}

// trackingOnlyLedgers folds every slug a remote's tracking ref carries that
// this clone has no local refs/ledger/<slug> for yet — exactly the set
// `chit sync` would adopt. local is the already-known set of slugs with a
// local ref. A slug tracked by more than one remote is listed once.
func trackingOnlyLedgers(c *Ctx, local map[string]bool) []*fold.Ledger {
	var out []*fold.Ledger
	seen := map[string]bool{}
	for _, remote := range trackingNamespaces(c.Store.Repo) {
		for _, slug := range trackedSlugs(c.Store.Repo, remote) {
			if local[slug] || seen[slug] {
				continue
			}
			evs, meta, _, err := c.Store.EventsDAGAt(store.TrackingRef(remote, slug))
			if err != nil {
				continue // torn/foreign tracking ref: skip, never crash a listing
			}
			seen[slug] = true
			out = append(out, fold.Fold(slug, evs, meta))
		}
	}
	return out
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
	return t
}

// lsLine renders one TTY row: slug, scope (truncated so a long scope can't
// blow out the column alignment), state — with the idle marker folded in,
// e.g. "open, idle 62d" — last-write age, and the non-sync event count.
// unsynced appends the tracking-only marker for a slug ls found only via a
// remote's tracking ref, with no local ref of its own yet.
func lsLine(led *fold.Ledger, lastTS string, events int, idle bool, now time.Time, unsynced bool) string {
	state := led.State
	if idle {
		days := int(now.Sub(lastEventTime(led)).Hours() / 24)
		state = fmt.Sprintf("open, idle %dd", days)
	}
	if unsynced {
		state += " (unsynced — run chit sync)"
	}
	return fmt.Sprintf("%-20s %-44s %-20s last %-10s (%d events)",
		out.EscapeControls(led.Slug), out.EscapeControls(truncateRunes(led.Meta.Scope, 44)),
		out.EscapeControls(state), out.Age(lastTS), events)
}
