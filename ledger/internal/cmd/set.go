package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/board"
	"ledger/internal/dag"
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
	var expect, rename string
	var override bool
	cmd := &cobra.Command{Use: `set <key> <FIELD=VALUE|VALUE>...   |   set <key> --rename "<new title>"`,
		Short: "record field values for an item, or retitle it",
		// One positional (the key) is the floor: a rename carries no
		// assignments at all, and a bare `set <key>` is caught below with the
		// grammar rather than by cobra's own arity message.
		Args: cobra.MinimumNArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			expectSet := cc.Flags().Changed("expect")
			if cc.Flags().Changed("rename") {
				return runRename(c, args[0], args[1:], o, rename, expect, expectSet, override)
			}
			if len(args) < 2 {
				return out.Errf("bad_usage",
					`ledger set <key> <field>=<value>... — or ledger set <key> --rename "<new title>" to retitle`, 4,
					"set needs at least one field=value assignment")
			}
			return runSet(c, args[0], args[1:], o, expect, expectSet, override)
		}}
	cmd.Flags().StringVar(&rename, "rename", "", `retitle the key; the rename IS the event (no fields, no evidence, no -m)`)
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
		isMulti := model.Contains(led.Meta.MultiFields, f)
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
		if vocab != nil && !model.Contains(vocab, v) {
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
		if model.Contains(led.Require[f], v) && len(o.evidence) == 0 {
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
		// Scoped SYMMETRICALLY (bridge design rev 6): a field write dedupes
		// only against an earlier FIELD-CARRYING event sharing its (author,
		// key, idem) — never against a rename, which asserts something else
		// entirely (rename.go's own scan is the mirror image).
		for _, ev := range led.Events {
			if ev.Type == "set" && len(ev.Fields) > 0 && ev.IdempotencyKey == o.idemKey &&
				ev.Author == author && ev.Key == key {
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
		pre = setPrecondition(key, fields, target, expect, ready, led.Meta, o.m, author, override,
			&ev.Override, &ev.ContestedResolved)
	}
	id, err := c.Store.AppendChecked(led.Slug, &ev, pre, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, led.Slug)
	}
	payload := map[string]any{"id": id, "ledger": led.Slug, "key": key, "fields": fields}
	line := "[" + id + "] " + led.Slug + ": " + key + " " + renderFields(fields)
	// The response ECHOES the resolution: a writer must be able to see they
	// just collapsed a contest, especially the unwitting touch-base case
	// that resolved one without ever reading the attention entry.
	if len(ev.ContestedResolved) > 0 {
		payload["contested_resolved"] = ev.ContestedResolved
		line += "  " + out.ContestedResolvedMarker(ev.ContestedResolved)
	}
	// Same rule for the other tool-computed attribution: --override asks for
	// permission, the tool decides what was actually standing, and the writer
	// must be able to see what it recorded in their name without re-reading
	// the chain.
	if ev.Override != "" {
		payload["override"] = ev.Override
		line += "  " + out.OverrideMarker(ev.Override)
	}
	if due, ok := dueAfter(c, led.Slug); ok {
		payload["rollup_due"] = due
	}
	outEmit(c, payload, []string{line})
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
	if err := checkExpectSyntax(expect, expectSet); err != nil {
		return "", err
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

// checkExpectSyntax rejects an unusable --expect value before any CAS runs,
// on either stream (field-scoped here, rename-scoped in rename.go). A
// flag value of "" (trimmed) is rejected outright: both CAS checks match
// via strings.HasPrefix(latest.ID, expect), which is vacuously true for "",
// so an empty --expect would silently pass unconditionally instead of
// failing closed. The realistic trigger is `--expect "$ID"` against an
// unset shell variable, so the message names that. Below git's own
// abbreviation floor (4 hex characters) is rejected too: a shorter prefix
// is unusably ambiguous as a CAS target.
func checkExpectSyntax(expect string, expectSet bool) error {
	if !expectSet {
		return nil
	}
	trimmed := strings.TrimSpace(expect)
	if trimmed == "" {
		return out.Errf("bad_usage", "pass --expect <event-id> or --expect none", 4,
			"--expect requires an event id or the literal 'none' (got empty — an unset shell variable?)")
	}
	if trimmed != "none" && len(trimmed) < 4 {
		return out.Errf("bad_usage", "pass at least 4 hex characters of the event id, or --expect none", 4,
			"--expect '%s' is shorter than git's own minimum abbreviation (4 hex characters)", trimmed)
	}
	return nil
}

// setPrecondition builds the closure AppendChecked runs against a fresh,
// whole-chain event read on every CAS attempt (spec rule 7: never a
// pre-loop snapshot; sync spec rev 7 Addition 5: the read is always the
// ledger's full history, unconditionally — see store.Precondition's doc
// comment). runSet's caller-side work — PickLedger/Load resolving the
// ledger, and the idempotency-key scan just above this function's call
// site — already folded the FULL event chain once before this closure is
// even built; that up-front read is unrelated to this per-attempt one.
// Checks run in order: rule 3/4 CAS on the target field, then (ready-capable
// boards only) key grammar on first write, title enforcement on a first
// status write, blocked-by existence, then rule 5's standing-signal check
// on a guarded write, and — on a guarded field — the contested write-heads
// this write is about to collapse. overrideOut and resolvedOut are pointers
// into the event actually being built (store.AppendChecked takes ev by
// pointer for exactly this): the closure records the tool-computed override
// signal names in the first and the losing write-head ids in the second,
// for the winning attempt's commit.
func setPrecondition(key string, fields map[string]string, target, expect string, ready bool, meta model.Meta,
	text, author string, override bool, overrideOut *string, resolvedOut *[]string) store.Precondition {
	_, touchesStatus := fields["status"]
	blockedByValue, touchesBlockedBy := fields["blocked-by"]
	var blockedByTokens []string
	if touchesBlockedBy {
		blockedByTokens = board.SplitTokens(blockedByValue)
	}

	return func(events []model.Event, d dag.Result) error {
		// Both pointers point into the event AppendChecked actually builds
		// from, and that same event is reused across every CAS retry (never
		// recreated per attempt). So every tool-computed field must be
		// written on EVERY invocation, "nothing to record" included —
		// otherwise a losing attempt's value survives into a winning
		// attempt's commit as an attribution that never existed on the state
		// that actually landed.
		//
		// The two fields satisfy that differently, and it is worth being
		// precise. *overrideOut is assigned only inside the has-signals
		// branch below, so this reset is what covers the no-signals case and
		// is load-bearing on its own. *resolvedOut is assigned
		// unconditionally on the guarded path, which already covers the
		// nothing-to-resolve case — this reset is belt-and-braces there, and
		// deliberately kept so that making that assignment conditional (the
		// shape override's has) cannot silently reintroduce the carryover.
		*overrideOut = ""
		*resolvedOut = nil

		if target != "" {
			latestTarget := latestFieldEvent(events, key, target)
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
		if model.Contains(meta.Guard, target) {
			// Sync design, Addition 3: this write descends from every current
			// head of (key, target) — the ref tip it is parented on does — so
			// it collapses the antichain, and the heads it beats are recorded
			// on it, greppable forever, whether the writer knew of the contest
			// or not. Single-pair form: a conditional set touches exactly one
			// guarded field, computed off THIS attempt's fresh read.
			*resolvedOut = board.ResolvedHeads(events, d, key, target)

			// Rule 5's append-time staleness stays on the real clock, through
			// the funnel: set has no --at flag (Addition 4 — a write verb with
			// a fake clock could dissolve this signal and skip needs_override
			// unrecorded), so this is always model.Now()'s live wall time.
			signals := b.Signals(b.Keys[key], touchesStatus, author, model.Now())
			if err := applyOverrideGate(key, signals, override, overrideOut); err != nil {
				return err
			}
		}
		return nil
	}
}

// applyOverrideGate is rule 5's shared tail, one definition for every write
// verb the signals gate: no standing signal makes --override a legal no-op,
// a standing signal without it refuses, and an override records what it
// overrode.
func applyOverrideGate(key string, signals []board.Signal, override bool, overrideOut *string) error {
	if len(signals) == 0 {
		return nil
	}
	if !override {
		return out.Errf("needs_override", `--override -m "<why>"`, 4,
			"'%s' has standing signal(s) that guard this write: %s", key, formatSignals(signals)).
			WithSignals(signalNameList(signals))
	}
	*overrideOut = signalNames(signals)
	return nil
}

// checkCAS is spec rules 3-4, field-scoped: latest is the latest event that
// wrote `field` on `key` (the caller's single scan) — other fields' events
// and notes carry no Fields[field] entry, so they never appear here.
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
	return strings.Join(signalNameList(signals), ",")
}

// signalNameList is the same names as a list, for the needs_override error
// document's machine-readable "signals" field. A consumer deciding whether
// to auto-override (a claim dissolves on the clock) or to refuse (a human
// label does not) needs the NAMES, and reading them out of the English
// message would be a prose dependency in a machine contract.
func signalNameList(signals []board.Signal) []string {
	names := make([]string, len(signals))
	for i, s := range signals {
		names[i] = s.Name
	}
	return names
}

func renderFields(fields map[string]string) string {
	names := fieldNames(fields)
	parts := make([]string, 0, len(names))
	for _, f := range names {
		parts = append(parts, f+"="+fields[f])
	}
	return strings.Join(parts, " ")
}
