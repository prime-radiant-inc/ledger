package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

func init() { register(newVocabCmd) }

func newVocabCmd(c *Ctx) *cobra.Command {
	var asFlag, mFlag string
	cmd := &cobra.Command{Use: "vocab", Short: "manage a ledger's field vocabularies"}
	add := &cobra.Command{Use: "add <slug> <field> <value>", Short: "extend a declared field's vocabulary",
		Args: cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			return runVocabAdd(c, args[0], args[1], args[2], asFlag, mFlag)
		}}
	add.Flags().StringVar(&asFlag, "as", "", "author identity")
	add.Flags().StringVarP(&mFlag, "message", "m", "", "why this value is needed")
	cmd.AddCommand(add)
	return cmd
}

func runVocabAdd(c *Ctx, slug, field, value, asFlag, mFlag string) error {
	led, err := c.Load(slug)
	if err != nil {
		return err
	}
	if led.State != "open" {
		return out.Errf("closed", "closed ledgers refuse new vocabulary — ledger create <new-slug> --scope <ref> for further work", 4,
			"'%s' is %s and refuses vocab changes", led.Slug, led.State)
	}
	if field == model.TitleFieldName {
		// Reserved (bridge design rev 6): a title is derived, never declared
		// — and a board minted before the reservation could still carry a
		// declared 'title', so this refuses on the name, not on the schema.
		return out.Errf("bad_value",
			`titles come from the seed's -m and `+"`set <key> --rename \"<new title>\"`"+` — there is no vocabulary to extend`, 4,
			"'%s' is reserved: a key's title is derived, never a declared field", model.TitleFieldName)
	}
	vocab, declared := led.Schema[field]
	if !declared {
		return out.Errf("unknown_field", "declared fields: "+strings.Join(led.Meta.FieldOrder, ", "), 4,
			"'%s' is not a declared field on '%s'", field, led.Slug)
	}
	if vocab == nil {
		return out.Errf("unknown_field", "this field takes any value — there's nothing to add", 4,
			"'%s' is free-text and needs no vocabulary", field)
	}
	if field == "status" && model.ReadyCapable(led.Meta) {
		return out.Errf("bad_value", "declared status vocab: "+strings.Join(led.Meta.Fields["status"], ", "), 4,
			"a ready-capable board's status vocab is part of its immutable declaration — 'vocab add' cannot extend it")
	}
	author := model.ResolveAuthor(asFlag)
	ev := model.NewEvent("vocab", author, c.Store.Repo)
	ev.Field, ev.Value, ev.Text = field, value, mFlag
	id, err := c.Store.Append(slug, ev, nil, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, slug)
	}
	payload := map[string]any{"id": id, "ledger": slug, "vocab": map[string]string{field: value}}
	if due, ok := dueAfter(c, slug); ok {
		payload["rollup_due"] = due
	}
	outEmit(c, payload, []string{"[" + id + "] " + slug + ": vocab " + field + " += " + value})
	return nil
}
