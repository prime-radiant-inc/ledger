package cmd

import (
	"github.com/spf13/cobra"
)

func init() { register(newLsCmd) }

func newLsCmd(c *Ctx) *cobra.Command {
	var all bool
	cmd := &cobra.Command{Use: "ls", Short: "list ledgers with freshness", RunE: func(_ *cobra.Command, _ []string) error {
		return runLs(c, all)
	}}
	cmd.Flags().BoolVar(&all, "all", false, "include closed ledgers")
	return cmd
}

func runLs(c *Ctx, all bool) error {
	slugs, err := c.Store.Slugs()
	if err != nil {
		return err
	}
	rows := []map[string]any{}
	_ = all // full filtering in the ls task
	for range slugs {
	}
	if len(slugs) == 0 {
		outEmit(c, map[string]any{"ledgers": rows}, []string{"no ledgers in this repo — ledger create <slug> --scope <ref> starts one"})
		return nil
	}
	outEmit(c, map[string]any{"ledgers": rows}, []string{"(ls arrives fully in a later task)"})
	return nil
}
