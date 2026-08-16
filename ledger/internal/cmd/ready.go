package cmd

import (
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/out"
)

// readyItem is one unblocked key in ready's envelope: the note/ts/by/id all
// come from the same event — the key's latest "status" set — so the ticket
// (id) never desyncs from the row it's shown alongside.
// UnblockedWithoutEvidence names blockers resolved by a terminal event that
// carried no evidence refs (derived, recomputable — not a fixed vocab check,
// so boards with duplicate-style terminal values keep the signal too).
type readyItem struct {
	Key                      string   `json:"key"`
	ID                       string   `json:"id"`
	Note                     string   `json:"note"`
	TS                       string   `json:"ts"`
	By                       string   `json:"by"`
	UnblockedWithoutEvidence []string `json:"unblocked_without_evidence,omitempty"`
}

// blockedItem is one key with at least one unresolved blocked-by edge.
type blockedItem struct {
	Key       string   `json:"key"`
	WaitingOn []string `json:"waiting_on"`
}

// inProgressItem is one key currently claimed (status=in-progress). ID is
// the claim event's id — the input a reclaim's --expect needs. Stale is
// only ever true when the board declared --stale-after and the claim is
// older than it.
type inProgressItem struct {
	Key   string `json:"key"`
	By    string `json:"by"`
	Age   string `json:"age"`
	ID    string `json:"id"`
	Stale bool   `json:"stale,omitempty"`
}

func init() { register(newReadyCmd) }

func newReadyCmd(c *Ctx) *cobra.Command {
	var ledgerFlag string
	var whereFlags []string
	var limit int
	cmd := &cobra.Command{Use: "ready",
		Short: "the work-picking view: unblocked keys, blocked keys with waiting_on, and in-progress claims",
		Args:  noPositionals("ready"),
		RunE: func(_ *cobra.Command, _ []string) error {
			return runReady(c, ledgerFlag, whereFlags, limit)
		}}
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	cmd.Flags().StringArrayVar(&whereFlags, "where", nil,
		"FIELD=VALUE or FIELD~=TOKEN; repeatable, AND together (default: status=open, unless overridden)")
	cmd.Flags().IntVar(&limit, "limit", 50, "max entries per list (0 = unlimited)")
	return cmd
}

func runReady(c *Ctx, ledgerFlag string, whereFlags []string, limit int) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	if len(led.Terminal["status"]) == 0 {
		return out.Errf("bad_usage", "create this board with --terminal status=<values> (or recreate)", 4,
			"'%s' declares no --terminal values for status — ready can't tell resolved blockers from open ones", led.Slug)
	}
	clauses, err := parseWhereSpecs(whereFlags)
	if err != nil {
		return err
	}
	hasStatus := false
	for _, cl := range clauses {
		if cl.Field != "status" {
			continue
		}
		if cl.Token || cl.Value != "open" {
			op := "="
			if cl.Token {
				op = "~="
			}
			return out.Errf("bad_usage", "drop the --where status clause — ready already implies status=open", 4,
				"--where status%s%s contradicts ready's availability filter (status=open)", op, cl.Value)
		}
		hasStatus = true
	}
	if !hasStatus {
		clauses = append([]whereClause{{Field: "status", Value: "open"}}, clauses...)
	}
	if err := validateWhere(led, clauses); err != nil {
		return err
	}

	pos := chainPositions(led)
	readyItems := []readyItem{}
	blockedItems := []blockedItem{}
	for key := range matchingKeys(led, clauses) {
		var waiting, noEvidence []string
		for _, tok := range blockedByTokens(led, key) {
			if !isTerminalStatus(led, tok) {
				waiting = append(waiting, tok)
				continue
			}
			if ev, ok := led.Spine[tok]["status"]; ok && len(ev.Evidence) == 0 {
				noEvidence = append(noEvidence, tok)
			}
		}
		if len(waiting) > 0 {
			blockedItems = append(blockedItems, blockedItem{Key: key, WaitingOn: waiting})
			continue
		}
		ev := led.Spine[key]["status"]
		item := readyItem{Key: key, ID: ev.ID, Note: ev.Text, TS: ev.TS, By: ev.Author}
		if len(noEvidence) > 0 {
			sort.Strings(noEvidence)
			item.UnblockedWithoutEvidence = noEvidence
		}
		readyItems = append(readyItems, item)
	}
	// Oldest first; timestamp ties break by chain position, not key name —
	// alphabetical tie-break systematically favors early-alphabet keys.
	sort.Slice(readyItems, func(i, j int) bool {
		if readyItems[i].TS != readyItems[j].TS {
			return readyItems[i].TS < readyItems[j].TS
		}
		return pos[readyItems[i].ID] < pos[readyItems[j].ID]
	})
	sort.Slice(blockedItems, func(i, j int) bool { return blockedItems[i].Key < blockedItems[j].Key })
	readyItems = capList(readyItems, limit)
	blockedItems = capList(blockedItems, limit)

	inProgItems := inProgressList(led, clauses, limit)

	payload := map[string]any{"ledger": led.Slug, "ready": readyItems, "blocked": blockedItems, "in_progress": inProgItems}
	lines := make([]string, 0, len(readyItems)+len(blockedItems)+len(inProgItems))
	for _, r := range readyItems {
		lines = append(lines, "ready: "+out.EscapeControls(r.Key)+" — "+out.EscapeControls(r.Note))
	}
	for _, b := range blockedItems {
		lines = append(lines, "blocked: "+out.EscapeControls(b.Key)+" ⇠ waiting on "+strings.Join(b.WaitingOn, ", "))
	}
	for _, p := range inProgItems {
		tag := ""
		if p.Stale {
			tag = " [stale]"
		}
		lines = append(lines, "in_progress: "+out.EscapeControls(p.Key)+" by "+out.EscapeControls(p.By)+
			" ("+p.Age+")"+tag)
	}
	outEmit(c, payload, lines)
	return nil
}

// inProgressList is ready's third list: keys with status=in-progress,
// composing every non-status --where clause the caller passed (it has its
// own availability filter, status=in-progress, not ready/blocked's
// status=open — so the status=open clause is swapped out, not added to).
func inProgressList(led *fold.Ledger, clauses []whereClause, limit int) []inProgressItem {
	other := make([]whereClause, 0, len(clauses))
	for _, cl := range clauses {
		if cl.Field != "status" {
			other = append(other, cl)
		}
	}
	inProgClauses := append(other, whereClause{Field: "status", Value: "in-progress"})

	var staleAfter time.Duration
	if led.StaleAfter != "" {
		staleAfter, _ = time.ParseDuration(led.StaleAfter) // validated at create time
	}
	items := []inProgressItem{}
	for key := range matchingKeys(led, inProgClauses) {
		ev := led.Spine[key]["status"]
		item := inProgressItem{Key: key, By: ev.Author, Age: out.Age(ev.TS), ID: ev.ID}
		if staleAfter > 0 {
			if t, tErr := time.Parse("2006-01-02T15:04:05", ev.TS); tErr == nil {
				item.Stale = time.Since(t.UTC()) > staleAfter
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return capList(items, limit)
}

// capList truncates a sorted list to limit entries; limit <= 0 means
// unlimited, matching tail/notes' existing --limit convention.
func capList[T any](items []T, limit int) []T {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

// chainPositions maps every event id to its index in the chain — ready's
// oldest-first tie-break (rev 4 correction over the spike's alphabetical
// tie-break, which systematically favored early-alphabet keys).
func chainPositions(led *fold.Ledger) map[string]int {
	pos := make(map[string]int, len(led.Events))
	for i, ev := range led.Events {
		pos[ev.ID] = i
	}
	return pos
}

// blockedByTokens is key's current blocked-by value, split into its
// non-empty comma tokens. A key with no blocked-by value has none — it's
// trivially ready.
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

// isTerminalStatus reports whether key's current status is one of the
// ledger's declared --terminal status values. A key with no status set is
// treated as unresolved (never terminal), the conservative default.
func isTerminalStatus(led *fold.Ledger, key string) bool {
	ev, ok := led.Spine[key]["status"]
	if !ok {
		return false
	}
	return contains(led.Terminal["status"], ev.Fields["status"])
}
