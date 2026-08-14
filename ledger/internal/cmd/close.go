package cmd

import (
	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

func init() { register(newCloseCmd) }

func newCloseCmd(c *Ctx) *cobra.Command {
	var asState, supersededBy, asFlag, mFlag string
	cmd := &cobra.Command{Use: "close <slug>", Short: "terminally close a ledger",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runClose(c, args[0], asState, supersededBy, asFlag, mFlag)
		}}
	cmd.Flags().StringVar(&asState, "as-state", "", "shipped|abandoned|superseded")
	cmd.MarkFlagRequired("as-state")
	cmd.Flags().StringVar(&supersededBy, "superseded-by", "", "successor slug (required with --as-state superseded)")
	cmd.Flags().StringVar(&asFlag, "as", "", "author identity")
	cmd.Flags().StringVarP(&mFlag, "message", "m", "", "short annotation")
	return cmd
}

func runClose(c *Ctx, slug, asState, supersededBy, asFlag, mFlag string) error {
	if asState == "superseded" && supersededBy == "" {
		return out.Errf("needs_successor", "add --superseded-by <slug> (the redirect is the load-bearing pointer)",
			4, "closing as superseded requires the successor link")
	}
	led, err := c.Load(slug)
	if err != nil {
		return err
	}
	if led.State != "open" {
		return out.Errf("closed", "close is terminal — this ledger is already "+led.State, 4,
			"'%s' is already %s", led.Slug, led.State)
	}
	author := model.ResolveAuthor(asFlag)
	ev := model.NewEvent("lifecycle", author, c.Store.Repo)
	ev.LifecycleKind = "close"
	ev.Reason = asState
	ev.Text = mFlag
	if supersededBy != "" {
		ev.Successor = supersededBy
	}
	id, err := c.Store.Append(slug, ev, nil, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, slug)
	}
	if asState == "superseded" {
		linkEv := model.NewEvent("lifecycle", author, c.Store.Repo)
		linkEv.LifecycleKind = "superseded_by"
		linkEv.Successor = supersededBy
		id, err = c.Store.Append(slug, linkEv, nil, store.ExpectPresent)
		if err != nil {
			return mapStoreErr(err, slug)
		}
	}
	outEmit(c, map[string]any{"id": id, "ledger": slug, "closed": asState},
		[]string{"[" + id + "] closed " + slug + " as " + asState})
	return nil
}
