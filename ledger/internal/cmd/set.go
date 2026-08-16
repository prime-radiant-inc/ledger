package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

// multiTokenRE is a multi-field value token's grammar: kebab-case, no
// spaces or commas inside a token (rev 5, enforced at write time).
var multiTokenRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// writeOpts carries the flags shared by the write verbs (set, note).
type writeOpts struct {
	ledger, as, m string
	evidence      []string
	idemKey       string
	expect        string
	override      bool
}

func init() { register(newSetCmd) }

func newSetCmd(c *Ctx) *cobra.Command {
	var o writeOpts
	cmd := &cobra.Command{Use: "set <key> <FIELD=VALUE|VALUE>...", Short: "record field values for an item",
		Args: cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSet(c, args[0], args[1:], o)
		}}
	cmd.Flags().StringVar(&o.ledger, "ledger", "", "target ledger")
	cmd.Flags().StringVar(&o.as, "as", "", "author identity")
	cmd.Flags().StringVarP(&o.m, "message", "m", "", "short annotation")
	cmd.Flags().StringArrayVar(&o.evidence, "evidence", nil, "TYPE:REF (e.g. commit:abc123); repeatable")
	cmd.Flags().StringVar(&o.idemKey, "idempotency-key", "", "dedupe key scoped to (author, key)")
	cmd.Flags().StringVar(&o.expect, "expect", "",
		"<event-id>|none — required for a guarded field: conditional write, first-wins (short-SHA prefix ok)")
	cmd.Flags().BoolVar(&o.override, "override", false,
		"land a guarded write despite a standing rule-5 signal (claim/human/settled); requires -m")
	return cmd
}

func runSet(c *Ctx, key string, assignments []string, o writeOpts) error {
	if o.override && strings.TrimSpace(o.m) == "" {
		return out.Errf("bad_usage", `pass a reason: --override -m "<why>"`, 4,
			"--override requires a non-empty -m")
	}
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
	// Key grammar (ready-capable boards only): keys must be kebab-case so any
	// blocked-by edge can reference them — enforced at each key's first write,
	// since a legally-named key can't be retrofitted onto history that
	// already used a bad one.
	if led.IsReadyCapable() {
		if _, exists := led.Spine[key]; !exists && !multiTokenRE.MatchString(key) {
			return out.Errf("bad_value",
				"keys are kebab-case: [a-z0-9][a-z0-9-]*, so any blocked-by edge can reference them", 4,
				"'%s' is not a valid key on ready-capable board '%s'", key, led.Slug)
		}
	}
	first := ""
	if len(led.Meta.FieldOrder) > 0 {
		first = led.Meta.FieldOrder[0]
	}
	fields := map[string]string{}
	for _, spec := range assignments {
		f, v, cut := strings.Cut(spec, "=")
		if !cut {
			f, v = first, spec
		}
		if strings.HasPrefix(v, "-") {
			return out.Errf("bad_value", "write it as field=value", 4, "'%s' looks like a flag, not a value", v)
		}
		if led.IsMultiField(f) {
			// Multi-fields are vocab-free by declaration: the value is stored
			// as the literal comma-joined string, no vocab check. Every token
			// still has to satisfy the multi-field grammar (kebab-case, no
			// spaces/commas inside a token); on a ready-capable board,
			// blocked-by is additionally the one reserved multi-field name
			// with edge semantics — each token must already be a key in this
			// ledger's fold. A plain board's blocked-by is just a multi-field:
			// no existence check, no edge semantics.
			for _, tok := range strings.Split(v, ",") {
				if tok == "" {
					continue
				}
				if !multiTokenRE.MatchString(tok) {
					return out.Errf("bad_value", "tokens are kebab-case: [a-z0-9][a-z0-9-]*, comma-separated, no spaces", 4,
						"'%s' in %s=%s is not a valid token", tok, f, v)
				}
				if f == "blocked-by" && led.IsReadyCapable() {
					if _, ok := led.Spine[tok]; !ok {
						return out.Errf("unknown_key", "ledger show lists this board's keys", 4,
							"blocked-by names '%s', which is not a known key on '%s'", tok, led.Slug)
					}
				}
			}
			fields[f] = v
			continue
		}
		vocab, declared := led.Schema[f]
		if !declared {
			return out.Errf("unknown_field", "declared: "+strings.Join(led.Meta.FieldOrder, ", "), 4,
				"'%s' is not a declared field on '%s'", f, led.Slug)
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
	// Titles (ready-capable boards only): a key's title is the message of its
	// first status event, so that first write requires a non-empty trimmed -m.
	if led.IsReadyCapable() {
		if _, touchesStatus := fields["status"]; touchesStatus {
			if _, hasPriorStatus := led.Spine[key]["status"]; !hasPriorStatus && strings.TrimSpace(o.m) == "" {
				return out.Errf("empty_body",
					`pass -m "<title>" — the first status write's message becomes this key's title`, 4,
					"'%s' has no status history yet: the first status write requires a non-empty -m", key)
			}
		}
	}
	// The invariant (rev 5): a write touching a guarded field must carry
	// --expect, and a single --expect can speak for exactly one guarded
	// field (two guarded fields in one set would make it ambiguous which
	// field's history the id is a version stamp for).
	var guardedTouched []string
	for f := range fields {
		if led.IsGuarded(f) {
			guardedTouched = append(guardedTouched, f)
		}
	}
	condField := ""
	guardedWrite := false
	switch {
	case len(guardedTouched) > 1:
		sort.Strings(guardedTouched)
		return out.Errf("bad_usage", "split into separate `set` calls, one --expect per guarded field", 4,
			"this set touches %d guarded fields (%s) — a single --expect can guard only one",
			len(guardedTouched), strings.Join(guardedTouched, ", "))
	case len(guardedTouched) == 1:
		condField = guardedTouched[0]
		guardedWrite = true
		if o.expect == "" {
			return out.Errf("bad_usage", "ledger ready (or `ledger status <key>`) shows the field's current event id", 4,
				"field '%s' is guarded: pass --expect <event-id> (or --expect none for a first write)", condField)
		}
	case o.expect != "":
		// No guarded field touched, but the caller still wants a
		// conditional write (generally useful for any read-modify-write on
		// a key, not just guarded claims) — fine, as long as it's
		// unambiguous which field it guards.
		if len(fields) != 1 {
			return out.Errf("bad_usage", "write just the one field this --expect guards, or drop --expect", 4,
				"this --expect write touches %d fields — say which one it guards", len(fields))
		}
		for f := range fields {
			condField = f
		}
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
	var id string
	if o.expect != "" {
		var check store.SignalCheck
		if guardedWrite && led.IsReadyCapable() {
			check = rule5Check(led, key, condField, model.ResolveAuthor(o.as), o.override)
		}
		id, err = c.Store.AppendExpectChecked(led.Slug, ev, condField, o.expect, store.ExpectPresent, check)
	} else {
		id, err = c.Store.Append(led.Slug, ev, nil, store.ExpectPresent)
	}
	if err != nil {
		var lost *store.ClaimLostError
		if errors.As(err, &lost) {
			w := lost.Winner
			attemptedValue := fields[condField]
			if w.ID == "" {
				return out.Errf("claim_lost", claimLostHint(led, lost.Field, attemptedValue, o.expect), 4,
					"'%s' has no recorded event for field '%s' yet — --expect '%s' cannot match (use --expect none for a first write)",
					key, lost.Field, o.expect)
			}
			return out.Errf("claim_lost", claimLostHint(led, lost.Field, attemptedValue, o.expect), 4,
				"event %s by %s (%s=%s) beat you to '%s'", w.ID, w.Author, lost.Field, w.Fields[lost.Field], key)
		}
		var needsOv *needsOverrideError
		if errors.As(err, &needsOv) {
			return out.Errf("needs_override", `--override -m "<why>"`, 4, "%s", needsOverrideMessage(needsOv))
		}
		return mapStoreErr(err, led.Slug)
	}
	payload := map[string]any{"id": id, "ledger": led.Slug, "key": key, "fields": fields}
	if due, ok := dueAfter(c, led.Slug); ok {
		payload["rollup_due"] = due
	}
	outEmit(c, payload, []string{"[" + id + "] " + led.Slug + ": " + key + " " + renderFields(fields)})
	return nil
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
