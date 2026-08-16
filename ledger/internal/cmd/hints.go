package cmd

import "ledger/internal/fold"

// claimLostHint dispatches a claim_lost rejection's hint text (rule 3),
// by board capability first and field second — a plain board must never be
// told to run a verb that's bad_usage for it. expectSpec distinguishes an
// --expect none seed loser (rule 4's collision hints) from an --expect <id>
// mismatch (rule 3's reclaim/merge/generic hints). attemptedValue is the
// value THIS write tried to set on field — needed to special-case a failed
// terminal write (the Close idiom: a failed closer must never be told to
// abandon finished work).
func claimLostHint(led *fold.Ledger, field, attemptedValue, expectSpec string) string {
	if expectSpec == "none" {
		return seedCollisionHint(led, field)
	}
	return rule3Hint(led, field, attemptedValue)
}

// seedCollisionHint is rule 4's --expect none loser hint: a status seed
// collision points at "re-seed under a new key," an edge seed collision at
// the same but naming edges — never a merge suggestion, which on a name
// collision is exactly the contamination the Seed idiom's recovery text
// exists to undo. Any other field falls back to rule 3's generic hint.
func seedCollisionHint(led *fold.Ledger, field string) string {
	if led.IsReadyCapable() {
		switch field {
		case "status":
			return "this key already exists — read it; if yours is a different issue, re-seed under a new key"
		case "blocked-by":
			return "this key already has edges — read it; if yours is a different issue, re-seed under a new key"
		}
	}
	return rule3Hint(led, field, "")
}

// rule3Hint is the ordinary --expect <id> mismatch hint: on a ready-capable
// board, status points back at `ready` (or, when the write itself attempted
// a terminal value, at the Close idiom's handoff-note doctrine instead —
// that failed closer was reclaimed, not wrong, and must never re-close
// blind), blocked-by points at re-reading the edges; everything else,
// including every guarded field on a plain board whatever its name, gets the
// generic re-read hint.
func rule3Hint(led *fold.Ledger, field, attemptedValue string) string {
	if led.IsReadyCapable() {
		switch field {
		case "status":
			if contains(led.Terminal["status"], attemptedValue) {
				return "you were reclaimed while working — leave a handoff note; never re-close blind"
			}
			return "re-run ledger ready and pick again"
		case "blocked-by":
			return "re-read the key's edges and merge"
		}
	}
	return "re-read '" + field + "' and try again"
}
