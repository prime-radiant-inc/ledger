// Command fakegh is the `gh` stand-in the bridge's tests build and put on
// the bridge's --gh-bin. It is a binary rather than an in-process fake
// because the bridge talks to GitHub through a subprocess and nothing else,
// and because a crash sweep has to be able to fail a real process.
package main

import (
	"os"

	"ledger/bridge/fakegh"
)

func main() { os.Exit(fakegh.Run(os.Args[1:], os.Stdout, os.Stderr)) }
