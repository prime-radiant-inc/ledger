// Package docs embeds the agent-facing documentation shipped inside the
// ledger binary, so `chit quickstart` reads it without touching the
// filesystem and every install of the binary carries doctrine that matches
// its own behavior.
package docs

import "embed"

//go:embed *.md
var FS embed.FS
