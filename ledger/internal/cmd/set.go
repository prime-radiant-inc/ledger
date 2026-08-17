package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/board"
	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

// writeOpts carries the flags shared by the write verbs (set, note).
type writeOpts struct {
	ledger, as, m string
	evidence      []string
	idemKey       string
}

// tokenRE is the multi-field token grammar (spec "The board"): also reused
// for ready-capable boards' key-name check, since a key must be
// blocked-by-referenceable by the same rule.
var tokenRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func init() { register(newSetCmd) }

func newSetCmd(c *Ctx) *cobra.Command {
	var o writeOpts
	var expect string
	var override bool
	cmd := &cobra.Command{Use: "set <key> <FIELD=VALUE|VALUE>...", Short: "record field values for an item",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cc *cobra.Command, args []string) error {
			expectSet := cc.Flags().Changed("expect")
			return runSet(c, args[0], args[1:], o, expect, expectSet, override)
		}}
	cmd.Flags().StringVar(&o.ledger, "ledger", "", "target ledger")
	cmd.Flags().StringVar(&o.as, "as", "", "author identity")
	cmd.Flags().StringVarP(&o.m, "message", "m", "", "short annotation")
	cmd.Flags().StringArrayVar(&o.evidence, "evidence", nil, "TYPE:REF (e.g. commit:abc123); repeatable")
	cmd.Flags().StringVar(&o.idemKey, "idempotency-key", "", "dedupe key scoped to (author, key)")
	cmd.Flags().StringVar(&expect, "expect", "", "event-id-prefix|none — required on a guarded field (the invariant)")
	cmd.Flags().BoolVar(&override, "override", false, "override a standing rule-5 signal (requires -m)")
	return cmd
}

func runSet(c *Ctx, key string, assignments []string, o writeOpts, expect string, expectSet, override bool) error {
	led, err := c.PickLedger(o.ledger)
	if err != nil {
		return err
	}
	if led.State != "open" {
		hint := "closed ledgers accept only notes and rollups; for new work: ledger create <new-slug> --scope <ref>"
		if led.SupersededBy != "" {
			hint = "this ledger is superseded by '" + led.SupersededBy + "' — write there"
		}
		return out.Errf("closed", hint, 4, "'%s' is %s and refuses new field values", led.Slug, led.State)
	}
	ready := model.ReadyCapable(led.Meta)
	first := ""
	if len(led.Meta.FieldOrder) > 0 {
		first = led.Meta.FieldOrder[0]
	}
	declaredList := declaredFieldNames(led.Meta)
	fields := map[string]string{}
	for _, spec := range assignments {
		f, v, cut := strings.Cut(spec, "=")
		if !cut {
			f, v = first, spec
		}
		if strings.HasPrefix(v, "-") {
			return out.Errf("bad_value", "write it as field=value", 4, "'%s' looks like a flag, not a value", v)
		}
		vocab, declared := led.Schema[f]
		isMulti := contains(led.Meta.MultiFields, f)
		if !declared && !isMulti {
			return out.Errf("unknown_field", "declared: "+declaredList, 4,
				"'%s' is not a declared field on '%s'", f, led.Slug)
		}
		if isMulti {
			for _, tok := range board.SplitTokens(v) {
				if !tokenRE.MatchString(tok) {
					return out.Errf("bad_value", "multi-field tokens match ^[a-z0-9][a-z0-9-]*$, comma-separated", 4,
						"'%s' is not a valid token for multi-field '%s'", tok, f)
				}
			}
		}
		if vocab != nil && !contains(vocab, v) {
			hint := fmt.Sprintf("ledger vocab add %s %s %s -m \"why this value is needed\"  — then re-run this set", led.Slug, f, v)
			if f == "status" && ready {
				// A ready-capable board's status vocab is part of its
				// immutable declaration (vocab.go's runVocabAdd rejects
				// exactly this) — never hand out the command that would
				// silently desync board.Build's classification switch from
				// a live-extended Schema.
				hint = "status on a ready-capable board is fixed at create: " + strings.Join(vocab, ", ")
			}
			return out.Errf("vocab_unknown", hint,
				4, "%q is not in %s's vocabulary (valid: %s)", v, f, strings.Join(vocab, ", "))
		}
		if contains(led.Require[f], v) && len(o.evidence) == 0 {
			return out.Errf("evidence_required", "re-run with --evidence commit:<range> | run:<id> | file:<path>", 4,
				"%s=%s requires evidence on '%s'", f, v, led.Slug)
		}
		fields[f] = v
	}

	// Rules 1-2 (the invariant): flag validation, not a precondition — a
	// malformed guarded write never reaches AppendChecked at all.
	target, usageErr := resolveExpectTarget(fields, led.Meta.Guard, led.Slug, expect, expectSet)
	if usageErr != nil {
		return usageErr
	}
	if override && strings.TrimSpace(o.m) == "" {
		return out.Errf("bad_usage", `pass -m "<why>" alongside --override`, 4,
			"--override requires a non-empty -m explaining why")
	}

	author := model.ResolveAuthor(o.as)
	if o.idemKey != "" {
		for _, ev := range led.Events {
			if ev.Type == "set" && ev.IdempotencyKey == o.idemKey && ev.Author == author && ev.Key == key {
				payload := map[string]any{"id": ev.ID, "ledger": led.Slug, "deduped": true, "by": ev.Author}
				if due, ok := dueAfter(c, led.Slug); ok {
					payload["rollup_due"] = due
				}
				outEmit(c, payload, []string{"deduped against " + ev.ID})
				return nil
			}
		}
	}
	ev := model.NewEvent("set", author, c.Store.Repo)
	ev.Key, ev.Fields, ev.Text, ev.Evidence, ev.IdempotencyKey = key, fields, o.m, o.evidence, o.idemKey

	var pre store.Precondition
	if target != "" || ready {
		pre = setPrecondition(key, fields, target, expect, ready, led.Meta, o.m, author, override, &ev.Override)
	}
	id, err := c.Store.AppendChecked(led.Slug, &ev, pre, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, led.Slug)
	}
	payload := map[string]any{"id": id, "ledger": led.Slug, "key": key, "fields": fields}
	if due, ok := dueAfter(c, led.Slug); ok {
		payload["rollup_due"] = due
	}
	outEmit(c, payload, []string{"[" + id + "] " + led.Slug + ": " + key + " " + renderFields(fields)})
	return nil
}

// resolveExpectTarget enforces rules 1-2 and picks the single field --expect
// applies its CAS to ("" means no CAS at all): a guarded field always needs
// exactly one target (rule 1/2); an unguarded write may still carry --expect
// as real CAS, but only when it touches exactly one field — there is
// otherwise no way to say which field the id names (guarding makes --expect
// mandatory, never exclusive; it is never accepted-and-ignored).
//
// A flag value of "" (trimmed) is rejected outright, before any of that:
// checkCAS matches expect via strings.HasPrefix(latest.ID, expect), which is
// vacuously true for "" — an empty --expect would otherwise silently pass
// CAS unconditionally instead of failing closed. The realistic trigger is
// `--expect "$ID"` against an unset shell variable, so the message names
// that. Below git's own abbreviation floor (4 hex characters) is rejected
// too: a shorter prefix is unusably ambiguous as a CAS target.
func resolveExpectTarget(fields map[string]string, guard []string, slug, expect string, expectSet bool) (target string, usageErr error) {
	if expectSet {
		trimmed := strings.TrimSpace(expect)
		if trimmed == "" {
			return "", out.Errf("bad_usage", "pass --expect <event-id> or --expect none", 4,
				"--expect requires an event id or the literal 'none' (got empty — an unset shell variable?)")
		}
		if trimmed != "none" && len(trimmed) < 4 {
			return "", out.Errf("bad_usage", "pass at least 4 hex characters of the event id, or --expect none", 4,
				"--expect '%s' is shorter than git's own minimum abbreviation (4 hex characters)", trimmed)
		}
	}
	var guardedTouched []string
	for _, g := range guard {
		if _, ok := fields[g]; ok {
			guardedTouched = append(guardedTouched, g)
		}
	}
	switch {
	case len(guardedTouched) > 1:
		return "", out.Errf("bad_usage",
			"split this into separate set calls, one per guarded field", 4,
			"a set may touch at most one guarded field on '%s' (touched: %s)", slug, strings.Join(guardedTouched, ", "))
	case len(guardedTouched) == 1:
		if !expectSet {
			f := guardedTouched[0]
			return "", out.Errf("bad_usage", "add --expect <event-id> or --expect none", 4,
				"'%s' is guarded on '%s': every write must carry --expect <event-id> or --expect none", f, slug)
		}
		return guardedTouched[0], nil
	case expectSet:
		if len(fields) != 1 {
			return "", out.Errf("bad_usage",
				"--expect on an unguarded write only targets a single field — drop the ride-alongs or the flag", 4,
				"--expect on a write touching zero guarded fields is only legal for a single-field write (touched: %s)",
				strings.Join(fieldNames(fields), ", "))
		}
		return fieldNames(fields)[0], nil
	default:
		return "", nil
	}
}

// setPrecondition builds the closure AppendChecked runs against a fresh,
// backward-windowed event read on every CAS attempt (spec rule 7: never a
// pre-loop snapshot; spec rule 8: the read narrows to the target key,
// growing only as far back as this write's own checks require) — narrowing
// that covers only AppendChecked's own per-attempt read (see store.
// Precondition's doc comment). runSet's caller-side work — PickLedger/Load
// resolving the ledger, and the idempotency-key scan just above this
// function's call site — already folded the FULL event chain once before
// this closure is even built; this narrowing never touches that. Which
// (key, field) facts those checks need is entirely static — knowable from
// this write's own shape (target, fields, ready, meta.Guard, key) before any
// event is even read — so it's computed once, outside the returned closure:
// windowResolved below re-checks it against every attempt's actual window.
// Checks run in order: rule 3/4 CAS on the target field, then (ready-capable
// boards only) key grammar on first write, title enforcement on a first
// status write, blocked-by existence, then rule 5's standing-signal check
// on a guarded write. overrideOut is a pointer into the event actually
// being built (store.AppendChecked takes ev by pointer for exactly this):
// when a standing signal is overridden, the closure records the tool-
// computed signal names there for the winning attempt's commit.
func setPrecondition(key string, fields map[string]string, target, expect string, ready bool, meta model.Meta,
	text, author string, override bool, overrideOut *string) store.Precondition {
	_, touchesStatus := fields["status"]
	blockedByValue, touchesBlockedBy := fields["blocked-by"]
	var blockedByTokens []string
	if touchesBlockedBy {
		blockedByTokens = board.SplitTokens(blockedByValue)
	}
	// needKeyExists and needLabels mirror the decision logic's own gates
	// below exactly (grammar fallback only fires when key doesn't already
	// match tokenRE; the signal check only runs on a guarded write) — each
	// is a fact the read must resolve only when the decision logic actually
	// consults it.
	needKeyExists := ready && !tokenRE.MatchString(key)
	needLabels := ready && contains(meta.Guard, target)

	return func(events []model.Event, reachedRoot bool) error {
		// Reset unconditionally at the top of every attempt: overrideOut
		// points into the event AppendChecked actually builds from, and
		// that same event is reused across every CAS retry (never
		// recreated per attempt). Without this reset, a losing attempt
		// that recorded an override would leave it stuck on *overrideOut
		// for a later, winning attempt whose own fresh signal computation
		// found nothing to override — a stale attribution that never
		// existed on the state that actually landed.
		*overrideOut = ""

		// latestTarget is the one fact both windowResolved and checkCAS need
		// on the target field — computed once per attempt and shared, rather
		// than each scanning events for it separately.
		var latestTarget *model.Event
		if target != "" {
			latestTarget = latestFieldEvent(events, key, target)
		}

		if !reachedRoot && !windowResolved(events, key, target, latestTarget, touchesStatus, needKeyExists, needLabels, blockedByTokens) {
			// The window handed to this attempt hasn't reached far enough
			// back to provably answer every check below yet, and it isn't
			// the chain root either — a fact we need might exist further
			// back, unseen. Ask for more history rather than guess.
			return store.ErrNeedsMoreHistory
		}

		// From here on, events is guaranteed to hold everything the checks
		// below can need — found within the window, or proven absent by
		// reachedRoot — so the decision logic is exactly as correct here as
		// it is against a whole-chain read.
		if target != "" {
			if err := checkCAS(latestTarget, key, target, expect, fields[target], ready, meta); err != nil {
				return err
			}
		}
		if !ready {
			return nil
		}
		b := board.Build(meta, events)
		if _, exists := b.Keys[key]; !exists && !tokenRE.MatchString(key) {
			return out.Errf("bad_value", "rename the key to match ^[a-z0-9][a-z0-9-]*$ (lowercase kebab-case)", 4,
				"key '%s' can't be referenced by blocked-by edges; use kebab-case", key)
		}
		if touchesStatus {
			k := b.Keys[key]
			firstStatusWrite := k == nil || k.Status == nil
			if firstStatusWrite && strings.TrimSpace(text) == "" {
				return out.Errf("empty_body", "the first status write's -m becomes the key's title", 4,
					"'%s' has no title yet — the first status write on a ready-capable board requires a non-empty -m", key)
			}
		}
		if touchesBlockedBy {
			for _, tok := range blockedByTokens {
				if _, exists := b.Keys[tok]; !exists {
					return out.Errf("unknown_key", "", 4, "blocked-by names '%s', which does not exist", tok)
				}
			}
		}
		if contains(meta.Guard, target) {
			signals := b.Signals(b.Keys[key], touchesStatus, author, time.Now())
			if len(signals) > 0 {
				if !override {
					return out.Errf("needs_override", `--override -m "<why>"`, 4,
						"'%s' has standing signal(s) that guard this write: %s", key, formatSignals(signals))
				}
				*overrideOut = signalNames(signals)
			}
		}
		return nil
	}
}

// windowResolved reports whether events (a partial backward window, since
// reachedRoot is only checked by the caller) already contains every
// (key, field) fact setPrecondition's decision logic will consult for this
// particular write. Each fact is resolved the moment it's found: events is
// always a suffix of the full chain (the newest N commits), so if the true
// latest event for a (key, field) pair exists at all, and it's within this
// window, it's the one found here — there is no newer occurrence outside a
// backward window by construction. Finding nothing here is genuinely
// ambiguous (older than this window, or never written) until reachedRoot
// settles it, which is exactly why this function is never consulted when
// reachedRoot is true.
func windowResolved(events []model.Event, key, target string, latestTarget *model.Event, touchesStatus, needKeyExists, needLabels bool, blockedByTokens []string) bool {
	if target != "" && latestTarget == nil {
		return false
	}
	if needKeyExists && !keyTouched(events, key) {
		return false
	}
	if touchesStatus && latestFieldEvent(events, key, "status") == nil {
		return false
	}
	if needLabels && latestFieldEvent(events, key, "labels") == nil {
		return false
	}
	for _, tok := range blockedByTokens {
		if !keyTouched(events, tok) {
			return false
		}
	}
	return true
}

// keyTouched reports whether any event in events touches name at all, on
// any field — the resolution proof windowResolved needs for blocked-by
// token existence and the key-grammar fallback's own existence check: once
// any event names the key, its existence is settled regardless of which
// field carried it.
func keyTouched(events []model.Event, name string) bool {
	for _, ev := range events {
		if ev.Type == "set" && ev.Key == name {
			return true
		}
	}
	return false
}

// checkCAS is spec rules 3-4, field-scoped: latest is the latest event that
// wrote `field` on `key` (the caller's single scan, shared with
// windowResolved) — other fields' events and notes carry no Fields[field]
// entry, so they never appear here.
func checkCAS(latest *model.Event, key, field, expect, attemptedValue string, ready bool, meta model.Meta) error {
	if expect == "none" {
		if latest == nil {
			return nil
		}
		hint := claimLostHint(ready, field, attemptedValue, true, meta)
		return out.Errf("claim_lost", hint, 4,
			"event %s by %s (%s=%s) beat you to '%s'", latest.ID, latest.Author, field, latest.Fields[field], key)
	}
	if latest != nil && strings.HasPrefix(latest.ID, expect) {
		return nil
	}
	if latest == nil {
		// The field was never written at all, so there is no winner to name
		// — a caller pasting a stale or guessed id on a first-ever write.
		// Never fabricate a winner; name the actual state and the fix.
		return out.Errf("claim_lost", "a first write takes --expect none", 4,
			"'%s' has no prior event on '%s' — nothing matches --expect %s", field, key, expect)
	}
	hint := claimLostHint(ready, field, attemptedValue, false, meta)
	return out.Errf("claim_lost", hint, 4,
		"event %s by %s (%s=%s) beat you to '%s'", latest.ID, latest.Author, field, latest.Fields[field], key)
}

// claimLostHint selects the pinned hint text for a claim_lost error,
// dispatched by board capability first (a plain board must never be told
// ready-capable advice — running `ready` is bad_usage there) and field
// second (spec rule 3/4's hint matrix).
func claimLostHint(ready bool, field, attemptedValue string, none bool, meta model.Meta) string {
	if !ready {
		return fmt.Sprintf("re-read '%s' and try again", field)
	}
	switch field {
	case "status":
		if none {
			return "this key already exists — read it; if yours is a different issue, re-seed under a new key"
		}
		if board.Build(meta, nil).IsTerminal(attemptedValue) {
			return "you were reclaimed while working — leave a handoff note; never re-close blind"
		}
		return "re-run ledger ready and pick again"
	case "blocked-by":
		if none {
			return "this key already has edges — read it; if yours is a different issue, re-seed under a new key"
		}
		return "re-read the key's edges and merge"
	default:
		return fmt.Sprintf("re-read '%s' and try again", field)
	}
}

// latestFieldEvent returns the most recent "set" event that wrote field on
// key, or nil if none exists yet — the field-scoped CAS target. events is
// oldest-first (store.Events' order), so the last match wins.
func latestFieldEvent(events []model.Event, key, field string) *model.Event {
	var latest *model.Event
	for i := range events {
		ev := events[i]
		if ev.Type != "set" || ev.Key != key {
			continue
		}
		if _, ok := ev.Fields[field]; !ok {
			continue
		}
		e := ev
		latest = &e
	}
	return latest
}

// declaredFieldNames comma-joins meta's declared enum fields (in field
// order) and multi-fields, for an unknown_field error's "declared: ..."
// hint.
func declaredFieldNames(meta model.Meta) string {
	return strings.Join(append(append([]string{}, meta.FieldOrder...), meta.MultiFields...), ", ")
}

func fieldNames(fields map[string]string) []string {
	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	sort.Strings(names)
	return names
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// formatSignals renders standing signals in the needs_override message's
// pinned shape: "<name> (<facts>)[, <name> (<facts>)...]".
func formatSignals(signals []board.Signal) string {
	parts := make([]string, len(signals))
	for i, s := range signals {
		parts[i] = s.Name + " (" + s.Facts + ")"
	}
	return strings.Join(parts, ", ")
}

// signalNames comma-joins signal names for the event's override record —
// tool-computed, never caller-asserted (spec rule 5).
func signalNames(signals []board.Signal) string {
	names := make([]string, len(signals))
	for i, s := range signals {
		names[i] = s.Name
	}
	return strings.Join(names, ",")
}

func renderFields(fields map[string]string) string {
	names := fieldNames(fields)
	parts := make([]string, 0, len(names))
	for _, f := range names {
		parts = append(parts, f+"="+fields[f])
	}
	return strings.Join(parts, " ")
}
