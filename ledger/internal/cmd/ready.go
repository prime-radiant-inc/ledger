package cmd

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
)

// readyItem is one unblocked key in ready's envelope: the note/ts/by come
// from the event that set the availability field (status, by convention).
// ID is the key's latest set event across every field — the version stamp
// a claimant hands straight to `set <key> ... --expect <id>`, no second
// query needed.
type readyItem struct {
	Key  string `json:"key"`
	ID   string `json:"id"`
	Note string `json:"note"`
	TS   string `json:"ts"`
	By   string `json:"by"`
}

// blockedItem is one key with at least one unresolved blocked-by edge.
type blockedItem struct {
	Key       string   `json:"key"`
	WaitingOn []string `json:"waiting_on"`
}

func init() { register(newReadyCmd) }

func newReadyCmd(c *Ctx) *cobra.Command {
	var ledgerFlag string
	var whereFlags []string
	cmd := &cobra.Command{Use: "ready", Short: "the work-picking view: unblocked keys, and blocked keys with waiting_on",
		Args: noPositionals("ready"),
		RunE: func(_ *cobra.Command, _ []string) error { return runReady(c, ledgerFlag, whereFlags) }}
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	cmd.Flags().StringArrayVar(&whereFlags, "where", nil,
		"FIELD=VALUE or FIELD~=TOKEN; repeatable, AND together (default: status=open, unless overridden)")
	return cmd
}

func runReady(c *Ctx, ledgerFlag string, whereFlags []string) error {
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
		if cl.Field == "status" {
			hasStatus = true
			break
		}
	}
	if !hasStatus {
		clauses = append([]whereClause{{Field: "status", Value: "open"}}, clauses...)
	}
	if err := validateWhere(led, clauses); err != nil {
		return err
	}

	readyItems := []readyItem{}
	blockedItems := []blockedItem{}
	for key := range matchingKeys(led, clauses) {
		var waiting []string
		for _, tok := range blockedByTokens(led, key) {
			if !isTerminalStatus(led, tok) {
				waiting = append(waiting, tok)
			}
		}
		if len(waiting) == 0 {
			ev := led.Spine[key]["status"]
			latest, _ := model.LatestSetEvent(led.Events, key) // always found: ev above is one such event
			readyItems = append(readyItems, readyItem{Key: key, ID: latest.ID, Note: ev.Text, TS: ev.TS, By: ev.Author})
		} else {
			blockedItems = append(blockedItems, blockedItem{Key: key, WaitingOn: waiting})
		}
	}
	sort.Slice(readyItems, func(i, j int) bool {
		if readyItems[i].TS != readyItems[j].TS {
			return readyItems[i].TS < readyItems[j].TS
		}
		return readyItems[i].Key < readyItems[j].Key
	})
	sort.Slice(blockedItems, func(i, j int) bool { return blockedItems[i].Key < blockedItems[j].Key })

	payload := map[string]any{"ledger": led.Slug, "ready": readyItems, "blocked": blockedItems}
	lines := make([]string, 0, len(readyItems)+len(blockedItems))
	for _, r := range readyItems {
		lines = append(lines, "ready: "+out.EscapeControls(r.Key)+" — "+out.EscapeControls(r.Note))
	}
	for _, b := range blockedItems {
		lines = append(lines, "blocked: "+out.EscapeControls(b.Key)+" ⇠ waiting on "+strings.Join(b.WaitingOn, ", "))
	}
	outEmit(c, payload, lines)
	return nil
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
