package cmd

import (
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/out"
)

// ---- envelope shapes (rev 16: "ready: the board, answered") ----

// waitingOn is one blocked-by edge's resolution state. terminal wins over
// human, human names only a non-terminal human-owned blocker, and
// in-progress-stale folds staleness into the state so waiting_on stays one
// discriminator instead of a bare stale flag plus six states.
type waitingOn struct {
	Key   string `json:"key"`
	State string `json:"state"` // terminal | open | in-progress | in-progress-stale | human | statusless
}

// readyEntry is one pickable-now key: status=open, not human-labeled, every
// edge terminal. id is the claim ticket (the status field's latest event).
type readyEntry struct {
	Key                      string   `json:"key"`
	Title                    string   `json:"title,omitempty"`
	Note                     string   `json:"note"`
	TS                       string   `json:"ts"`
	By                       string   `json:"by"`
	ID                       string   `json:"id"`
	UnblockedWithoutEvidence []string `json:"unblocked_without_evidence,omitempty"`
}

// heldEntry is spoken-for: a claim (kind "claim") or a human-owned
// non-terminal key (kind "human", label dominating status for placement). A
// human-labeled key that's also actively claimed renders kind "human" but
// carries the claim fields (by the claimant, age, claim id, stale).
type heldEntry struct {
	Key       string      `json:"key"`
	Title     string      `json:"title,omitempty"`
	Kind      string      `json:"kind"`             // "claim" | "human"
	Status    string      `json:"status,omitempty"` // human kind: current status value
	By        string      `json:"by"`
	Age       string      `json:"age,omitempty"`
	TS        string      `json:"ts,omitempty"` // human kind, unclaimed: the status event's ts
	ID        string      `json:"id"`
	Stale     bool        `json:"stale,omitempty"`
	WaitingOn []waitingOn `json:"waiting_on,omitempty"`
}

// blockedEntry is one open, unlabeled key with at least one unresolved edge.
// id is the status field's latest event — "blocked is not locked": claiming
// a blocked key by name with a valid --expect is legal.
type blockedEntry struct {
	Key       string      `json:"key"`
	Title     string      `json:"title,omitempty"`
	Note      string      `json:"note"`
	TS        string      `json:"ts"`
	By        string      `json:"by"`
	ID        string      `json:"id"`
	WaitingOn []waitingOn `json:"waiting_on"`
}

// attentionEntry is one triage item: a stale claim, a statusless key
// (half-seed or orphan), or a cycle (named by its member keys — a cycle
// entry has no single title, so it carries none).
type attentionEntry struct {
	Reason string   `json:"reason"` // stale-claim | statusless | cycle
	Key    string   `json:"key,omitempty"`
	Title  string   `json:"title,omitempty"`
	By     string   `json:"by,omitempty"`
	Age    string   `json:"age,omitempty"`
	ID     string   `json:"id,omitempty"`
	Keys   []string `json:"keys,omitempty"`
}

func init() { register(newReadyCmd) }

func newReadyCmd(c *Ctx) *cobra.Command {
	var ledgerFlag string
	var whereFlags []string
	var limit int
	cmd := &cobra.Command{Use: "ready",
		Short: "the board, answered: what to pick, what to respect, what to wait on, what needs a person",
		Args:  noPositionals("ready"),
		RunE: func(_ *cobra.Command, _ []string) error {
			return runReady(c, ledgerFlag, whereFlags, limit)
		}}
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	cmd.Flags().StringArrayVar(&whereFlags, "where", nil,
		"FIELD=VALUE or FIELD~=TOKEN; repeatable, AND together, composes uniformly with every list's own rule")
	cmd.Flags().IntVar(&limit, "limit", 50, "max entries per list (0 = unlimited); never bounds the frontier verdict")
	return cmd
}

func runReady(c *Ctx, ledgerFlag string, whereFlags []string, limit int) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	if !led.IsReadyCapable() {
		return out.Errf("bad_usage",
			"recreate this board with the ready-capable shape: --terminal status=<closed-values> --guard status --multi-field labels (see `ledger create --help`)",
			4, "'%s' is not a ready-capable board — `ready` only works on a board that declared --terminal on status", led.Slug)
	}
	clauses, err := parseWhereSpecs(whereFlags)
	if err != nil {
		return err
	}
	if err := validateWhere(led, clauses); err != nil {
		return err
	}

	full := allKeys(led)
	now := time.Now()
	allStates := make(map[string]keyState, len(full))
	for _, k := range full {
		allStates[k] = classifyKey(led, k, now)
	}
	keys := full
	if len(clauses) > 0 {
		match := matchingKeys(led, clauses)
		filtered := make([]string, 0, len(full))
		for _, k := range full {
			if match[k] {
				filtered = append(filtered, k)
			}
		}
		keys = filtered
	}
	filteredStates := make(map[string]keyState, len(keys))
	for _, k := range keys {
		filteredStates[k] = allStates[k]
	}

	titles := firstTitles(led)
	pos := chainPositions(led)
	cycles := detectCycles(led) // structural: the full graph, independent of --where

	var readyFull []readyEntry
	var heldFull []heldEntry
	var blockedFull []blockedEntry
	var attentionFull []attentionEntry

	for _, k := range keys {
		ks := allStates[k]
		if !ks.HasStatus {
			attentionFull = append(attentionFull, attentionEntry{Reason: "statusless", Key: k})
			continue
		}
		if ks.Terminal {
			continue // resolved: no longer on the board's active lists
		}
		waiting := waitingOnList(allStates, ks.Blockers)
		unresolved := hasUnresolved(waiting)

		if ks.Human {
			he := heldEntry{Key: k, Title: titles[k], Kind: "human", Status: ks.Status,
				By: ks.StatusEv.Author, TS: ks.StatusEv.TS, ID: ks.StatusEv.ID}
			if ks.Claimed {
				he.By, he.Age, he.ID, he.Stale = ks.StatusEv.Author, out.Age(ks.StatusEv.TS), ks.StatusEv.ID, ks.Stale
				he.TS = ""
			}
			if unresolved {
				he.WaitingOn = waiting
			}
			heldFull = append(heldFull, he)
			if ks.Claimed && ks.Stale {
				attentionFull = append(attentionFull, attentionEntry{Reason: "stale-claim", Key: k, Title: titles[k],
					By: ks.StatusEv.Author, Age: out.Age(ks.StatusEv.TS), ID: ks.StatusEv.ID})
			}
			continue
		}

		if ks.Claimed {
			he := heldEntry{Key: k, Title: titles[k], Kind: "claim",
				By: ks.StatusEv.Author, Age: out.Age(ks.StatusEv.TS), ID: ks.StatusEv.ID, Stale: ks.Stale}
			if unresolved {
				he.WaitingOn = waiting
			}
			heldFull = append(heldFull, he)
			if ks.Stale {
				attentionFull = append(attentionFull, attentionEntry{Reason: "stale-claim", Key: k, Title: titles[k],
					By: ks.StatusEv.Author, Age: out.Age(ks.StatusEv.TS), ID: ks.StatusEv.ID})
			}
			continue
		}

		// The ready-capable vocab is exactly {open, in-progress} plus
		// --terminal values, all handled above — this is status=open.
		if !unresolved {
			readyFull = append(readyFull, readyEntry{Key: k, Title: titles[k], Note: ks.StatusEv.Text, TS: ks.StatusEv.TS,
				By: ks.StatusEv.Author, ID: ks.StatusEv.ID, UnblockedWithoutEvidence: noEvidenceBlockers(led, ks.Blockers)})
		} else {
			blockedFull = append(blockedFull, blockedEntry{Key: k, Title: titles[k], Note: ks.StatusEv.Text,
				TS: ks.StatusEv.TS, By: ks.StatusEv.Author, ID: ks.StatusEv.ID, WaitingOn: waiting})
		}
	}

	var whereMatch map[string]bool
	if len(clauses) > 0 {
		whereMatch = matchingKeys(led, clauses)
	}
	for _, cyc := range cycles {
		if whereMatch != nil && !anyMatch(cyc, whereMatch) {
			continue
		}
		attentionFull = append(attentionFull, attentionEntry{Reason: "cycle", Keys: cyc})
	}

	sort.Slice(readyFull, func(i, j int) bool {
		if readyFull[i].TS != readyFull[j].TS {
			return readyFull[i].TS < readyFull[j].TS
		}
		return pos[readyFull[i].ID] < pos[readyFull[j].ID]
	})
	sort.Slice(heldFull, func(i, j int) bool { return heldFull[i].Key < heldFull[j].Key })
	sort.Slice(blockedFull, func(i, j int) bool { return blockedFull[i].Key < blockedFull[j].Key })
	sort.Slice(attentionFull, func(i, j int) bool {
		return attentionSortKey(attentionFull[i]) < attentionSortKey(attentionFull[j])
	})

	frontier := computeFrontier(readyFull, filteredStates, attentionFull)
	totals := map[string]int{
		"ready": len(readyFull), "held": len(heldFull), "blocked": len(blockedFull), "attention": len(attentionFull),
	}

	readyOut, heldOut, blockedOut, attentionOut :=
		capList(readyFull, limit), capList(heldFull, limit), capList(blockedFull, limit), capList(attentionFull, limit)

	payload := map[string]any{"ledger": led.Slug, "frontier": frontier,
		"ready": readyOut, "held": heldOut, "blocked": blockedOut, "attention": attentionOut, "totals": totals}

	lines := make([]string, 0, len(readyOut)+len(heldOut)+len(blockedOut)+len(attentionOut)+1)
	lines = append(lines, "frontier: "+frontier)
	for _, r := range readyOut {
		lines = append(lines, "ready: "+out.EscapeControls(r.Key)+" — "+out.EscapeControls(r.Note))
	}
	for _, h := range heldOut {
		tag := ""
		if h.Stale {
			tag = " [stale]"
		}
		lines = append(lines, "held ("+h.Kind+"): "+out.EscapeControls(h.Key)+" by "+out.EscapeControls(h.By)+tag)
	}
	for _, b := range blockedOut {
		lines = append(lines, "blocked: "+out.EscapeControls(b.Key)+" ⇠ waiting on "+waitingOnLine(b.WaitingOn))
	}
	for _, a := range attentionOut {
		lines = append(lines, "attention ("+a.Reason+"): "+attentionLabel(a))
	}
	outEmit(c, payload, lines)
	return nil
}

// waitingOnList renders every blocker token's resolution state, terminal
// ones included — waiting_on is informational (the frontier verdict already
// did the walking), and "terminal wins" is only meaningful if a resolved
// blocker can still show up here.
func waitingOnList(states map[string]keyState, blockers []string) []waitingOn {
	if len(blockers) == 0 {
		return nil
	}
	list := make([]waitingOn, 0, len(blockers))
	for _, tok := range blockers {
		list = append(list, waitingOn{Key: tok, State: blockerState(states, tok)})
	}
	return list
}

// blockerState classifies one blocked-by token from the full (unfiltered)
// board state: terminal wins whenever the blocker's status is terminal,
// labeled or not; human names only a non-terminal human-owned blocker.
func blockerState(states map[string]keyState, tok string) string {
	b, ok := states[tok]
	if !ok || !b.HasStatus {
		return "statusless"
	}
	if b.Terminal {
		return "terminal"
	}
	if b.Human {
		return "human"
	}
	if b.Claimed {
		if b.Stale {
			return "in-progress-stale"
		}
		return "in-progress"
	}
	return "open"
}

func hasUnresolved(waiting []waitingOn) bool {
	for _, w := range waiting {
		if w.State != "terminal" {
			return true
		}
	}
	return false
}

// noEvidenceBlockers lists blockers, among key's own edge set, whose
// terminal event carries no evidence refs — a floor against omission on an
// otherwise-ready key.
func noEvidenceBlockers(led *fold.Ledger, blockers []string) []string {
	var missing []string
	for _, tok := range blockers {
		ev, ok := led.Spine[tok]["status"]
		if !ok {
			continue
		}
		if contains(led.Terminal["status"], ev.Fields["status"]) && len(ev.Evidence) == 0 {
			missing = append(missing, tok)
		}
	}
	sort.Strings(missing)
	return missing
}

// computeFrontier is the frontier verdict: work-available when anything is
// pickable now or reclaimable (a non-human stale claim counts; a
// human-labeled key's stale claim needs a person, so it never counts here);
// else attention-needed when the attention list is non-empty; else
// all-handled. Computed from the (possibly --where-narrowed) full lists,
// never the --limit-truncated display slices.
func computeFrontier(readyFull []readyEntry, states map[string]keyState, attentionFull []attentionEntry) string {
	if len(readyFull) > 0 {
		return "work-available"
	}
	for _, ks := range states {
		if ks.HasStatus && !ks.Terminal && !ks.Human && ks.Claimed && ks.Stale {
			return "work-available"
		}
	}
	if len(attentionFull) > 0 {
		return "attention-needed"
	}
	return "all-handled"
}

// firstTitles maps every key to the message of its first status event — a
// key's immutable title, computed from history rather than stored.
func firstTitles(led *fold.Ledger) map[string]string {
	titles := map[string]string{}
	for _, ev := range led.Events {
		if ev.Type != "set" {
			continue
		}
		if _, ok := ev.Fields["status"]; !ok {
			continue
		}
		if _, have := titles[ev.Key]; have {
			continue
		}
		titles[ev.Key] = ev.Text
	}
	return titles
}

// detectCycles finds every true cycle in the blocked-by graph via a
// path-stack (white/gray/black) DFS with memo: a gray node hit again is a
// back edge (a real cycle); a black node is already fully explored beneath
// it (a diamond — shared dependency, never a false cycle). Each cycle's key
// set is deduped and returned sorted.
func detectCycles(led *fold.Ledger) [][]string {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var stack []string
	var cycles [][]string
	seen := map[string]bool{}
	var visit func(key string)
	visit = func(key string) {
		color[key] = gray
		stack = append(stack, key)
		for _, tok := range blockedByTokens(led, key) {
			switch color[tok] {
			case white:
				visit(tok)
			case gray:
				idx := -1
				for i, s := range stack {
					if s == tok {
						idx = i
						break
					}
				}
				if idx >= 0 {
					cyc := append([]string{}, stack[idx:]...)
					sort.Strings(cyc)
					sig := strings.Join(cyc, ",")
					if !seen[sig] {
						seen[sig] = true
						cycles = append(cycles, cyc)
					}
				}
			case black:
				// fully explored beneath it already: never a cycle through here
			}
		}
		stack = stack[:len(stack)-1]
		color[key] = black
	}
	for _, k := range allKeys(led) {
		if color[k] == white {
			visit(k)
		}
	}
	return cycles
}

func anyMatch(keys []string, match map[string]bool) bool {
	for _, k := range keys {
		if match[k] {
			return true
		}
	}
	return false
}

func attentionSortKey(a attentionEntry) string {
	if a.Key != "" {
		return a.Key
	}
	if len(a.Keys) > 0 {
		return a.Keys[0]
	}
	return ""
}

func attentionLabel(a attentionEntry) string {
	if a.Key != "" {
		return a.Key
	}
	return strings.Join(a.Keys, ",")
}

func waitingOnLine(waiting []waitingOn) string {
	parts := make([]string, len(waiting))
	for i, w := range waiting {
		parts[i] = w.Key + ":" + w.State
	}
	return strings.Join(parts, ", ")
}

// chainPositions maps every event id to its index in the chain — ready's
// oldest-first tie-break (alphabetical tie-breaks systematically favor
// early-alphabet keys, so this breaks ties by chain position instead).
func chainPositions(led *fold.Ledger) map[string]int {
	pos := make(map[string]int, len(led.Events))
	for i, ev := range led.Events {
		pos[ev.ID] = i
	}
	return pos
}

// blockedByTokens is key's current blocked-by value, split into its
// non-empty comma tokens. A key with no blocked-by value has none — it's
// trivially unblocked.
func blockedByTokens(led *fold.Ledger, key string) []string {
	ev, ok := led.Spine[key]["blocked-by"]
	if !ok {
		return nil
	}
	var toks []string
	for _, t := range strings.Split(ev.Fields["blocked-by"], ",") {
		if t != "" {
			toks = append(toks, t)
		}
	}
	return toks
}

// capList truncates a sorted list to limit entries; limit <= 0 means
// unlimited, matching tail/notes' existing --limit convention.
func capList[T any](items []T, limit int) []T {
	if items == nil {
		items = []T{}
	}
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}
