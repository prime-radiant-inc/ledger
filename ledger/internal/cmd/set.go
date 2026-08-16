package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

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
	cmd.Flags().BoolVar(&override, "override", false, "override a standing rule-5 signal (requires -m; wired in Task 8)")
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
	declaredList := strings.Join(append(append([]string{}, led.Meta.FieldOrder...), led.Meta.MultiFields...), ", ")
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
			for _, tok := range splitTokens(v) {
				if !tokenRE.MatchString(tok) {
					return out.Errf("bad_value", "multi-field tokens match ^[a-z0-9][a-z0-9-]*$, comma-separated", 4,
						"'%s' is not a valid token for multi-field '%s'", tok, f)
				}
			}
		}
		if vocab != nil && !contains(vocab, v) {
			return out.Errf("vocab_unknown",
				fmt.Sprintf("ledger vocab add %s %s %s -m \"why this value is needed\"  — then re-run this set", led.Slug, f, v),
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
	target, usageErr := resolveExpectTarget(fields, led.Meta.Guard, led.Slug, expectSet)
	if usageErr != nil {
		return usageErr
	}

	if o.idemKey != "" {
		author := model.ResolveAuthor(o.as)
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
	ev := model.NewEvent("set", model.ResolveAuthor(o.as), c.Store.Repo)
	ev.Key, ev.Fields, ev.Text, ev.Evidence, ev.IdempotencyKey = key, fields, o.m, o.evidence, o.idemKey

	var pre store.Precondition
	if target != "" || ready {
		pre = setPrecondition(key, fields, target, expect, ready, led.Meta)
	}
	id, err := c.Store.AppendChecked(led.Slug, ev, pre, store.ExpectPresent)
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
func resolveExpectTarget(fields map[string]string, guard []string, slug string, expectSet bool) (target string, usageErr error) {
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

// setPrecondition builds the closure AppendChecked runs against a fresh
// event read on every CAS attempt (spec rule 7: never a pre-loop snapshot).
// Checks run in order: rule 3/4 CAS on the target field, then (ready-capable
// boards only) key grammar on first write, then blocked-by existence. Task 8
// inserts rule 5's signal checks after CAS.
func setPrecondition(key string, fields map[string]string, target, expect string, ready bool, meta model.Meta) store.Precondition {
	return func(events []model.Event) error {
		if target != "" {
			if err := checkCAS(events, key, target, expect, fields[target], ready, meta); err != nil {
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
		if v, touched := fields["blocked-by"]; touched {
			for _, tok := range splitTokens(v) {
				if _, exists := b.Keys[tok]; !exists {
					return out.Errf("unknown_key", "", 4, "blocked-by names '%s', which does not exist", tok)
				}
			}
		}
		return nil
	}
}

// checkCAS is spec rules 3-4, field-scoped: it looks only at the latest
// event that wrote `field` on `key` — other fields' events and notes carry
// no Fields[field] entry, so they never appear here.
func checkCAS(events []model.Event, key, field, expect, attemptedValue string, ready bool, meta model.Meta) error {
	latest := latestFieldEvent(events, key, field)
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

// splitTokens parses a comma-joined multi-field value; an empty value (the
// clear case) yields no tokens. Mirrors board.splitTokens (unexported there).
func splitTokens(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
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

func renderFields(fields map[string]string) string {
	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, f := range names {
		parts = append(parts, f+"="+fields[f])
	}
	return strings.Join(parts, " ")
}
