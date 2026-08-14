package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

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
	return cmd
}

func runSet(c *Ctx, key string, assignments []string, o writeOpts) error {
	led, err := c.PickLedger(o.ledger)
	if err != nil {
		return err
	}
	if led.State != "open" {
		hint := "closed ledgers accept only notes; for new work: ledger create <new-slug> --scope <ref>"
		if led.SupersededBy != "" {
			hint = "this ledger is superseded by '" + led.SupersededBy + "' — write there"
		}
		return out.Errf("closed", hint, 4, "'%s' is %s and refuses new field values", led.Slug, led.State)
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
	if o.idemKey != "" {
		author := model.ResolveAuthor(o.as)
		for _, ev := range led.Events {
			if ev.Type == "set" && ev.IdempotencyKey == o.idemKey && ev.Author == author && ev.Key == key {
				outEmit(c, map[string]any{"id": ev.ID, "ledger": led.Slug, "deduped": true, "by": ev.Author},
					[]string{"deduped against " + ev.ID})
				return nil
			}
		}
	}
	ev := model.NewEvent("set", model.ResolveAuthor(o.as), c.Store.Repo)
	ev.Key, ev.Fields, ev.Text, ev.Evidence, ev.IdempotencyKey = key, fields, o.m, o.evidence, o.idemKey
	id, err := c.Store.Append(led.Slug, ev, nil, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, led.Slug)
	}
	outEmit(c, map[string]any{"id": id, "ledger": led.Slug, "key": key, "fields": fields},
		[]string{"[" + id + "] " + led.Slug + ": " + key + " " + renderFields(fields)})
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
