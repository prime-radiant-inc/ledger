package cmd

import (
	"github.com/spf13/cobra"

	"ledger/docs"
)

func init() { register(newQuickstartCmd) }

// newQuickstartCmd prints one of the two embedded doctrine files verbatim.
// It never resolves a store (see root.go's PersistentPreRunE exemption
// list) — a cold agent must be able to run this before any ledger exists.
func newQuickstartCmd(c *Ctx) *cobra.Command {
	var orchestrator bool
	cmd := &cobra.Command{Use: "quickstart", Short: "print the embedded agent doctrine",
		Long: "Prints the cold-consumer quickstart doctrine. --orchestrator adds the\n" +
			"fleet-dispatch section for agents that spawn and coordinate others.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			name := "quickstart.md"
			if orchestrator {
				name = "quickstart-orchestrator.md"
			}
			data, err := docs.FS.ReadFile(name)
			if err != nil {
				return err
			}
			_, err = c.Stdout.Write(data)
			return err
		}}
	cmd.Flags().BoolVar(&orchestrator, "orchestrator", false, "also print the fleet-dispatch/orchestrator section")
	return cmd
}
