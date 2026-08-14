package cmd

import (
	"fmt"

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
	switch asState {
	case "shipped", "abandoned", "superseded":
	default:
		return out.Errf("bad_value",
			fmt.Sprintf("valid --as-state values: shipped, abandoned, superseded — e.g. ledger close %s --as-state abandoned", slug),
			4, "'%s' is not a valid close state", asState)
	}
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

	if asState == "superseded" {
		// The close and the successor link must never be observable apart —
		// a reader between the two writes would see a closed ledger with no
		// redirect. Land both as one parent-chained pair under a single CAS.
		closeEv := model.NewEvent("lifecycle", author, c.Store.Repo)
		closeEv.LifecycleKind, closeEv.Reason, closeEv.Text, closeEv.Successor = "close", asState, mFlag, supersededBy
		linkEv := model.NewEvent("lifecycle", author, c.Store.Repo)
		linkEv.LifecycleKind, linkEv.Successor = "superseded_by", supersededBy

		ids, err := c.Store.AppendChain(slug, []model.Event{closeEv, linkEv}, nil, store.ExpectPresent)
		if err != nil {
			return mapStoreErr(err, slug)
		}
		outEmit(c, map[string]any{"close_id": ids[0], "id": ids[1], "ledger": slug, "closed": asState},
			[]string{"[" + ids[1] + "] closed " + slug + " as " + asState + " -> " + supersededBy})
		return nil
	}

	ev := model.NewEvent("lifecycle", author, c.Store.Repo)
	ev.LifecycleKind, ev.Reason, ev.Text = "close", asState, mFlag
	id, err := c.Store.Append(slug, ev, nil, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, slug)
	}
	outEmit(c, map[string]any{"id": id, "ledger": slug, "closed": asState},
		[]string{"[" + id + "] closed " + slug + " as " + asState})
	return nil
}
