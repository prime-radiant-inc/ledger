package main

import "fmt"

// Divergence classes. The CLASS coordinate is load-bearing, not decoration:
// a suppression record and a refusal record are different events with
// OPPOSITE meanings, and keying both on the bare (issue, aspect,
// observed-state) triple let one run's suppression record silently consume
// the next run's human-reserved refusal — swallowing its handoff note AND
// its one GitHub comment, and leaving a stale suppression note asserting the
// opposite of what happened (probed).
const (
	// classRefusal: the bridge will not make a board write the doctrine
	// forbids it to force (a human-reserved key, a lost terminal CAS).
	classRefusal = "refusal"
	// classSuppression: intake stood down on an aspect because the board is
	// about to push a different value there, and the GitHub value being
	// discarded was a real person's edit.
	classSuppression = "suppression"
	// classLink: a key with more than one github-link note. Aspect-less.
	classLink = "link"
)

// ASPECT is the closed list for the aspect coordinate.
const (
	aspectStatus = "status"
	aspectTitle  = "title"
)

// Record is one standing divergence: the bridge saw it, will not resolve it
// on its own, and remembers it so the next run does not shout again.
//
// Law 3: recorded ONCE with the state that was observed, then silently
// skipped (and counted) while nothing changes on either side. The handoff
// note and the one GitHub comment are written once per distinct quadruple
// EVER, not per episode — Law 2 keys the note on exactly this quadruple, so
// a recurrence dedupes by design. What recurs afresh on a re-observation is
// the COUNT, the report line, and the re-persisted record.
type Record struct {
	Issue    int    `json:"issue"`
	Class    string `json:"class"`
	Aspect   string `json:"aspect,omitempty"` // absent on classLink
	Observed string `json:"observed"`
}

func (r Record) less(o Record) bool {
	if r.Issue != o.Issue {
		return r.Issue < o.Issue
	}
	if r.Class != o.Class {
		return r.Class < o.Class
	}
	if r.Aspect != o.Aspect {
		return r.Aspect < o.Aspect
	}
	return r.Observed < o.Observed
}

// idem is the Law 2 key for this record's handoff note. It carries the CLASS
// so a suppression can never dedupe against a refusal.
func (r Record) idem() string {
	return fmt.Sprintf("gh-divergence-%d-%s-%s-%s", r.Issue, r.Class, r.Aspect, slugify(r.Observed))
}

// State is the bridge's durable state, stored as ONE note ON THE CHAIN under
// the reserved NOTE key `github-bridge-state`: the repo it is bound to, the
// cursor it last drained to, and its standing divergence records.
//
// There is deliberately NO map of imported comment ids and no comment
// high-water marks. Law 2 makes intake idempotent by construction
// (idempotency keys on the board writes, event-id markers on the mirrored
// comments), which deletes the map — and with it the unbounded growth AND
// the last-write-wins merge hazard it had, being the one part of bridge
// state two replicas could each edit.
type State struct {
	Repo    string   `json:"repo"`
	Cursor  string   `json:"cursor"`
	Records []Record `json:"records,omitempty"`
}

func (s *State) has(r Record) bool {
	for _, have := range s.Records {
		if have == r {
			return true
		}
	}
	return false
}

// sameRecords compares record SETS, not lists. Walk order is an artifact of
// which issue was visited first this run, and a set that merely reordered
// would otherwise count as "changed" — persisting a new state note every
// run, forever, which is the exact fixed point Law 1's persistence rule
// exists to reach.
func sameRecords(a, b []Record) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[Record]int, len(a))
	for _, r := range a {
		seen[r]++
	}
	for _, r := range b {
		seen[r]--
		if seen[r] < 0 {
			return false
		}
	}
	return true
}

// Report is one run's audit trail — the actions taken, and the two counts
// the idempotence proof asserts are zero on a converged re-run.
type Report struct {
	OK          bool   `json:"ok"`
	Repo        string `json:"repo"`
	Ledger      string `json:"ledger"`
	GHMutations int    `json:"gh_mutations"`
	BoardWrites int    `json:"board_writes"`
	// Cursor is the PERSISTED cursor: a run that changed nothing persists
	// nothing and reports the STORED value, not the drain's tip.
	Cursor string `json:"cursor"`
	// Divergences counts standing records of all three classes — refusals,
	// suppressions and duplicate-link conflicts — new and repeating alike.
	Divergences int `json:"divergences"`
	// SuppressedAuthors counts outbound events skipped per `github:@*`
	// author. The namespace is unenforced (author enforcement rides the
	// owner-enforcement v2 item, not a one-prefix carve-out), so a poisoned
	// `--as github:@x` event is invisible otherwise.
	SuppressedAuthors map[string]int `json:"suppressed_authors,omitempty"`
	Actions           []string       `json:"actions"`
	Warnings          []string       `json:"warnings,omitempty"`
}

func (r *Report) gh(format string, a ...any) {
	r.GHMutations++
	r.Actions = append(r.Actions, "gh:    "+fmt.Sprintf(format, a...))
}

func (r *Report) board(format string, a ...any) {
	r.BoardWrites++
	r.Actions = append(r.Actions, "board: "+fmt.Sprintf(format, a...))
}

func (r *Report) warn(format string, a ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, a...))
}
