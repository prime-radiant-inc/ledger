package main

import (
	"fmt"
	"os"

	"ledger/internal/gitx"
)

func main() {
	if err := gitx.CheckVersion(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
