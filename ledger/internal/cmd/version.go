package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"ledger/internal/out"
)

// Version is stamped at release-build time via
// -ldflags "-X ledger/internal/cmd.Version=<semver>". Source builds say dev,
// which also keeps every update-related nag and network call switched off.
var Version = "dev"

func init() { register(newVersionCmd) }

// newVersionCmd never resolves a store (see root.go's exemption list) — the
// first thing a fresh install runs must work in an empty directory.
func newVersionCmd(c *Ctx) *cobra.Command {
	return &cobra.Command{Use: "version", Short: "print the chit version",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			out.Emit(c.Stdout, c.TTY, map[string]any{
				"version": Version, "os": runtime.GOOS, "arch": runtime.GOARCH,
			}, []string{fmt.Sprintf("chit %s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)})
			return nil
		}}
}
