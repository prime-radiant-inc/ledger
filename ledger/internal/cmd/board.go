package cmd

import (
	"sort"
	"strings"
	"time"

	"ledger/internal/fold"
	"ledger/internal/model"
)

// keyState is one key's ready-capable-board classification, folded fresh
// from a Ledger's spine — shared by rule 5's signal computation (checked
// against one key at CAS time) and `ready`'s full-board envelope (checked
// against every key). Keeping this in one place is what makes the two
// features agree on what "claimed"/"terminal"/"human-labeled" mean.
type keyState struct {
	Key       string
	HasStatus bool
	Status    string      // current status value ("" when HasStatus is false)
	StatusEv  model.Event // the status field's latest event (zero value when HasStatus is false)
	Terminal  bool        // Status is one of the board's --terminal status values
	Human     bool        // labels field's current value carries the "human" token
	Claimed   bool        // Status == "in-progress"
	Stale     bool        // Claimed and older than --stale-after (always false otherwise)
	Blockers  []string    // this key's current blocked-by tokens, in declared order
}

// classifyKey folds one key's ready-capable state as of led (whatever events
// led was folded from — a full-board Ledger for `ready`, or a
// freshly-refolded one from a CAS retry's just-fetched events for rule 5).
func classifyKey(led *fold.Ledger, key string, now time.Time) keyState {
	ks := keyState{Key: key}
	if ev, ok := led.Spine[key]["status"]; ok {
		ks.HasStatus = true
		ks.Status = ev.Fields["status"]
		ks.StatusEv = ev
		ks.Terminal = contains(led.Terminal["status"], ks.Status)
		ks.Claimed = ks.Status == "in-progress"
		if ks.Claimed {
			ks.Stale = isStaleClaim(led, ev, now)
		}
	}
	if ev, ok := led.Spine[key]["labels"]; ok {
		ks.Human = contains(splitTokens(ev.Fields["labels"]), "human")
	}
	ks.Blockers = blockedByTokens(led, key)
	return ks
}

// isStaleClaim reports whether ev (a status=in-progress event) is older than
// led's --stale-after horizon. No horizon declared (StaleAfter == "") means
// no claim is ever stale (rule 6).
func isStaleClaim(led *fold.Ledger, ev model.Event, now time.Time) bool {
	if led.StaleAfter == "" {
		return false
	}
	d, err := time.ParseDuration(led.StaleAfter)
	if err != nil {
		return false
	}
	t, err := model.ParseTS(ev.TS)
	if err != nil {
		return false
	}
	return now.Sub(t.UTC()) > d
}

// splitTokens splits a comma-joined multi-field value into its non-empty
// tokens.
func splitTokens(v string) []string {
	var toks []string
	for _, t := range strings.Split(v, ",") {
		if t != "" {
			toks = append(toks, t)
		}
	}
	return toks
}

// allKeys returns every key with at least one field ever set on it, sorted —
// the universe `ready` and cycle detection walk.
func allKeys(led *fold.Ledger) []string {
	ks := make([]string, 0, len(led.Spine))
	for k := range led.Spine {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
