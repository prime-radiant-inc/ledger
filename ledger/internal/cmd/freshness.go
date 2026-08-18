package cmd

import (
	"fmt"

	"ledger/internal/fold"
	"ledger/internal/store"
)

// Read-time freshness (sync design Addition 3's freshness bullet): when the
// resolved remote's last-FETCHED tracking ref holds commits local lacks, a
// read warns rather than rendering a clean board that silently omits them.
// Reads never touch the network — this compares local against whatever
// `sync` or `push` last fetched, which moves only on their own contact with
// the remote. A partitioned replica with no fresh fetch renders clean, no
// warning: the SYNC HABIT is the first defense, this is the second net.
//
// Applies to `ready`, `show`, `status` — the reads the picking loop and a
// human actually use. Placement is pinned precisely (spec rev 5): on a TTY
// the warning is one stderr line; in JSON it is a single top-level
// `freshness` sibling key in the same document. Neither is ever a member of
// the payload the projection's byte-determinism covers — attachFreshness
// only ever ADDS the "freshness" key, never touches anything else the
// caller already put in payload.
func (c *Ctx) attachFreshness(led *fold.Ledger, payload map[string]any) {
	remote := freshnessRemote(c)
	if remote == "" {
		return
	}
	track, ok := c.Store.RevParse(store.TrackingRef(remote, led.Slug))
	if !ok {
		return // nothing fetched from this remote for this slug yet
	}
	local, ok := c.Store.FullHead(led.Slug)
	if !ok {
		return
	}
	if c.Store.IsAncestor(track, local) {
		return // everything fetched is already merged locally
	}

	// Root-mismatch tracking state (same-root rule, sync.go): the tracking
	// ref is not "unmerged events" of THIS chain at all — it's a different
	// ledger wearing the same slug — so the read warns with the export/
	// import guidance instead of a count. Roots are read cheaply
	// (store.Roots, not a whole-chain event read) since this runs on hot
	// paths; local's roots ride along on led.DAG, already read once by the
	// caller's own load.
	trackRoots := c.Store.Roots(track)
	if !rootsIntersect(led.DAG.Roots, trackRoots) {
		hint := rootMismatchDetail(c, led.Slug, led.DAG.Roots, trackRoots)
		payload["freshness"] = map[string]any{"hint": hint}
		fmt.Fprintln(c.Stderr, "[ledger] "+hint)
		return
	}

	n := c.Store.NonSentinelCount(local, track)
	if n == 0 {
		return // only sentinel commits ahead — nothing a reader is missing
	}
	payload["freshness"] = map[string]any{"unmerged_remote_events": n, "hint": "run `ledger sync`"}
	fmt.Fprintf(c.Stderr, "[ledger] %d unmerged remote events — run 'ledger sync'\n", n)
}

// freshnessRemote picks the remote freshness checks against: routed through
// resolveRemote itself (flag "" — a read verb takes no --remote flag) so
// the resolution order lives in exactly one place, never a second copy that
// can drift from sync's. Its error (ambiguous, or any other) is swallowed
// into silence here: a read verb must never fail because remotes are
// ambiguous; absent a clear answer it just has nothing to warn from.
func freshnessRemote(c *Ctx) string {
	remote, err := resolveRemote(c, "")
	if err != nil {
		return ""
	}
	return remote
}
