package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

// SlugOutcome is one slug's per-slug result. Both sync and push report a
// list of these, and both exit 3 when any of them failed (the spec's
// partial-failure code) — a fleet syncing twelve boards must never have one
// unreachable slug reported as total success or total failure.
type SlugOutcome struct {
	Slug   string `json:"slug"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
	ID     string `json:"id,omitempty"`
}

// failed reports the outcomes that make the whole invocation partial.
func (o SlugOutcome) failed() bool {
	return o.Result == "refused" || o.Result == "failed" || o.Result == "rejected"
}

func init() { register(newSyncCmd) }

func newSyncCmd(c *Ctx) *cobra.Command {
	var remote, asFlag string
	cmd := &cobra.Command{Use: "sync", Short: "fetch tracking refs and merge remote ledger history (never pushes)",
		Long: "Fetches refs/ledger/* from a remote into this repo's private tracking namespace\n" +
			"(refs/ledger-remote/<remote>/<slug>), then per slug: tracking already contained\n" +
			"in local is a no-op; local behind tracking fast-forwards; a true divergence gets\n" +
			"exactly one sentinel merge commit; a slug with no local ref is adopted at the\n" +
			"tracking head. Sync never pushes — `chit push` is a separate, deliberate act.",
		Args: noPositionals("sync"),
		RunE: func(_ *cobra.Command, _ []string) error { return runSync(c, remote, asFlag) }}
	cmd.Flags().StringVar(&remote, "remote", "", "git remote name (default: .ledger.toml's remote, else origin)")
	cmd.Flags().StringVar(&asFlag, "as", "", "author identity recorded on sentinel merge commits")
	return cmd
}

func runSync(c *Ctx, remoteFlag, asFlag string) error {
	remote, err := resolveRemote(c, remoteFlag)
	if err != nil {
		return err
	}
	if remote == "" {
		// Degraded mode, stated by the spec: a clean no-op with a message,
		// never an error. A repo with no remote is a legitimate way to run.
		outEmit(c, map[string]any{"synced": []SlugOutcome{}, "remote": nil,
			"note": "no git remote configured — nothing to sync"},
			[]string{"no git remote configured — nothing to sync (git remote add origin <url>)"})
		return nil
	}

	repairs := repairRefspecs(c.Store.Repo, remote)
	for _, r := range repairs {
		fmt.Fprintln(c.Stderr, "[chit] "+r)
	}
	if err := fetchTracking(c.Store.Repo, remote); err != nil {
		return err
	}

	author := model.ResolveAuthor(asFlag)
	outcomes := []SlugOutcome{} // never nil: an empty invocation must marshal as [], not JSON null
	for _, slug := range trackedSlugs(c.Store.Repo, remote) {
		outcomes = append(outcomes, c.syncOne(remote, slug, author))
	}
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].Slug < outcomes[j].Slug })

	return c.emitOutcomes("synced", remote, outcomes)
}

// syncOne is the parent spec's per-slug rule, in the order the cases have
// to be decided:
//
//	tracking ⊆ local          => no-op
//	tracking has >1 root      => refused (multi-root, before anything moves)
//	no local ref              => CAS-create at the tracking head (adoption)
//	different creation commit => refused, naming both creators
//	local ⊆ tracking          => fast-forward
//	true divergence           => exactly ONE sentinel merge, under ref CAS
func (c *Ctx) syncOne(remote, slug, author string) SlugOutcome {
	trackRef := store.TrackingRef(remote, slug)
	track, ok := c.Store.RevParse(trackRef)
	if !ok {
		return SlugOutcome{Slug: slug, Result: "failed", Detail: "tracking ref vanished mid-sync"}
	}
	local, haveLocal := c.Store.FullHead(slug)

	if haveLocal && c.Store.IsAncestor(track, local) {
		return SlugOutcome{Slug: slug, Result: "no-op"}
	}

	// Multi-root refusal (spec rev 5): before the local ref is moved or
	// created, check the TRACKING chain's own root set. A grafted remote
	// chain that retains the legitimate root would pass the same-root
	// intersection check below forever; this catches it at the source, with
	// the data the fold's own log parse already has in hand.
	_, _, trackResult, err := c.Store.EventsDAGAt(trackRef)
	if err != nil {
		return SlugOutcome{Slug: slug, Result: "failed", Detail: err.Error()}
	}
	if len(trackResult.Roots) > 1 {
		return c.multiRootRefusal(slug, trackRef, trackResult.Roots)
	}

	if !haveLocal {
		return c.adopt(slug, track, trackResult.Roots)
	}
	if bad := c.rootMismatch(slug, trackResult.Roots); bad != nil {
		return *bad
	}

	// Fast-forward or true divergence, both under CAS: re-classify against a
	// FRESH head at the top of every retry, never blindly re-parenting a
	// merge from stale state — the winner of a race may already have
	// brought `track` in (a plain fast-forward, or even a no-op), and
	// building a merge without re-checking first mints a redundant sentinel
	// for nothing (the exact chain-growth-per-sync failure the ff rule
	// exists to prevent). A CAS loss on EITHER branch loops back here rather
	// than failing outright — a concurrent local writer appending one event
	// during sync is normal, not exceptional.
	for attempt := 0; attempt < 5; attempt++ {
		if c.Store.IsAncestor(track, local) {
			return SlugOutcome{Slug: slug, Result: "no-op"}
		}
		if c.Store.IsAncestor(local, track) {
			if ok, _ := c.Store.CAS(store.Ref(slug), track, local); ok {
				return SlugOutcome{Slug: slug, Result: "fast-forward", ID: track[:10]}
			}
		} else {
			// True divergence: one sentinel merge, under CAS. The sentinel
			// carries a type:"sync" event.json, which every read, fold,
			// count and idempotency scan skips and the fold order contracts
			// out of the DAG entirely.
			ev := model.NewEvent("sync", author, c.Store.Repo)
			ev.Text = "sync " + remote + "/" + slug
			merge, err := c.Store.BuildMerge([]string{local, track}, ev)
			if err != nil {
				return SlugOutcome{Slug: slug, Result: "failed", Detail: err.Error()}
			}
			if ok, _ := c.Store.CAS(store.Ref(slug), merge, local); ok {
				c.Store.GCAuto()
				return SlugOutcome{Slug: slug, Result: "merged", ID: merge[:10]}
			}
		}
		cur, still := c.Store.FullHead(slug)
		if !still {
			return SlugOutcome{Slug: slug, Result: "failed", Detail: "local ref disappeared mid-sync"}
		}
		local = cur // a writer landed an event under us: re-classify and retry
	}
	return SlugOutcome{Slug: slug, Result: "failed", Detail: "ref kept moving — re-run chit sync"}
}

// rootMismatch enforces the same-root rule: sync merges only chains sharing
// a creation commit. Two clones that independently created one slug have
// nothing to merge — a merge would splice two unrelated ledgers into one
// chain — so this refuses, names BOTH creators from their meta.json, and
// points at the exit (export the local chain, import it under a new slug,
// and let sync adopt the remote one). Distinct from multi-root refusal: this
// compares two single-root chains that disagree, not one chain that already
// has more than one root of its own.
func (c *Ctx) rootMismatch(slug string, trackRoots []string) *SlugOutcome {
	_, _, localResult, err := c.Store.EventsDAG(slug)
	if err != nil {
		return &SlugOutcome{Slug: slug, Result: "failed", Detail: err.Error()}
	}
	if rootsIntersect(localResult.Roots, trackRoots) {
		return nil
	}
	return &SlugOutcome{Slug: slug, Result: "refused", Detail: rootMismatchDetail(c, slug, localResult.Roots, trackRoots)}
}

// rootsIntersect reports whether two root sets share a commit — the
// same-root rule's core test, shared by sync's refusal (rootMismatch above)
// and freshness's read-time check (freshness.go), which reaches the same
// root set more cheaply (store.Roots, not a whole-chain event read).
func rootsIntersect(a, b []string) bool {
	for _, r := range a {
		if model.Contains(b, r) {
			return true
		}
	}
	return false
}

// rootMismatchDetail builds the same-root rule's export/import guidance,
// naming both chains' creators — shared by sync's refusal above and
// freshness's read-time warning, which reaches the same conclusion without
// paying for sync's own whole-chain local read (the caller already has its
// root set in hand).
func rootMismatchDetail(c *Ctx, slug string, localRoots, trackRoots []string) string {
	if len(localRoots) == 0 || len(trackRoots) == 0 {
		return "different creation commits"
	}
	lby, lat, _ := creatorOf(c.Store, localRoots[0])
	rby, rat, _ := creatorOf(c.Store, trackRoots[0])
	return fmt.Sprintf("local chain created by %s at %s; remote chain created by %s at %s — "+
		"export the local chain and re-import it under a new slug (chit export %s --to %s.jsonl; "+
		"chit import %s.jsonl --slug %s-local), then sync adopts the remote chain",
		lby, lat, rby, rat, slug, slug, slug, slug)
}

// multiRootRefusal implements the rev 5 pin: a candidate chain with more
// than one root is refused outright, before the local ref is ever moved or
// created. Unlike the same-root rule's export/import exit, this one is
// remote-side: push is non-force and sync refuses, so nothing tool-side can
// repair or worsen a grafted remote ref — an admin must delete or
// force-replace it directly, and until they do, the slug stays wedged.
func (c *Ctx) multiRootRefusal(slug, trackRef string, roots []string) SlugOutcome {
	names := make([]string, len(roots))
	for i, r := range roots {
		names[i] = fmt.Sprintf("%s (created by %s)", r[:10], rootCreator(c.Store, r))
	}
	detail := fmt.Sprintf("the tracking ref %s has %d roots — %s — this chain was grafted, not synced from one "+
		"creation; an admin must delete or force-replace the remote ref (refs/ledger/%s), and until then every "+
		"host's sync refuses it",
		trackRef, len(roots), strings.Join(names, "; "), slug)
	return SlugOutcome{Slug: slug, Result: "refused", Detail: detail}
}

// rootCreator names a root commit's creator for the multi-root refusal:
// meta.json's created_by when present, else the commit's own (synthetic)
// git author name — a graft's foreign root often carries no meta.json at
// all, and the refusal must still name someone responsible for it.
func rootCreator(s store.Store, sha string) string {
	if meta, ok := s.MetaAt(sha); ok && meta.CreatedBy != "" {
		return meta.CreatedBy
	}
	if a := s.CommitAuthor(sha); a != "" {
		return a
	}
	return "unknown"
}

// adopt is the remote-only CAS-create — the cross-host resume path. It is a
// THIRD meta-minting path alongside create and import, so it enforces the
// same slug grammar create/import do (a remote publishing refs/ledger/
// <bad-slug> directly — never through this tool — must not silently mint an
// ungoverned local ref) and re-validates the arriving board's declarations
// exactly as import does: a ready-capable board whose declared shape is
// broken must be refused with the defect named, never minted. Adoption also
// announces the same provenance line adopt-by-hand would, so a planted slug
// arrives labeled through either door.
func (c *Ctx) adopt(slug, track string, roots []string) SlugOutcome {
	if !model.ValidSlug(slug) {
		return SlugOutcome{Slug: slug, Result: "refused",
			Detail: "bad_slug: '" + slug + "' is not a valid slug (slugs are lowercase-kebab: [a-z0-9][a-z0-9-]*, max 64 chars) — not adopted"}
	}
	if len(roots) == 0 {
		return SlugOutcome{Slug: slug, Result: "refused", Detail: "remote chain has no creation commit"}
	}
	meta, ok := c.Store.MetaAt(roots[0])
	if !ok {
		return SlugOutcome{Slug: slug, Result: "refused",
			Detail: "remote chain's creation commit carries no meta.json — not a ledger"}
	}
	if declErr := model.ValidateDeclarations(meta); declErr != nil {
		return SlugOutcome{Slug: slug, Result: "refused",
			Detail: "remote board's declarations are invalid and were NOT adopted: " + declErr.Msg + " — fix: " + declErr.Hint}
	}
	if casOK, stderr := c.Store.CAS(store.Ref(slug), track, ""); !casOK {
		if looksLikeCASRace(stderr) {
			return SlugOutcome{Slug: slug, Result: "failed", Detail: "a local ledger '" + slug + "' appeared mid-adoption"}
		}
		// Not a race: a real defect (D/F ref-name conflict, an illegal ref
		// name, macOS case-aliasing, lock contention) — name it truthfully
		// with git's own diagnosis rather than blame a race that never
		// happened.
		return SlugOutcome{Slug: slug, Result: "failed",
			Detail: "could not create the local ref for '" + slug + "': " + firstLine(stderr)}
	}
	by, created, scope := creatorOf(c.Store, roots[0])
	return SlugOutcome{Slug: slug, Result: "adopted", ID: track[:10],
		Detail: fmt.Sprintf("created by %s at %s, scope %s", by, created, scope)}
}

// looksLikeCASRace recognizes update-ref's own wording for a genuine
// compare-and-swap loss (the ref moved to something other than what "old"
// named) — the only case adoption's CAS-create failure should ever blame on
// a race. Everything else (a D/F ref-name conflict, an illegal ref name,
// lock contention) is a real defect the caller must name truthfully.
func looksLikeCASRace(stderr string) bool {
	return strings.Contains(stderr, "reference already exists") ||
		(strings.Contains(stderr, "is at ") && strings.Contains(stderr, "but expected"))
}

// emitOutcomes is the shared reporting tail of sync and push (Task 6 reuses
// it for push): one payload carrying every per-slug outcome and the remote
// synced/pushed against, a TTY line per outcome, and exit 3 whenever any
// slug failed (the spec's partial-failure code). ok is true iff every
// outcome succeeded; any failure folds the parent's error contract
// ({error,message,hint}) into THIS SAME document — ok:false,
// error:"partial_failure", hint pointing at the outcomes array — rather than
// a second one, so a consumer keying on either `ok` or `error` reads
// failure as failure. The payload is written BEFORE the non-zero exit is
// returned, exactly like watch's timeout.
func (c *Ctx) emitOutcomes(verb, remote string, outcomes []SlugOutcome) error {
	partial := false
	lines := make([]string, 0, len(outcomes)+1)
	for _, o := range outcomes {
		if o.failed() {
			partial = true
		}
		l := fmt.Sprintf("  %-24s %s", out.EscapeControls(o.Slug), o.Result)
		if o.ID != "" {
			l += " [" + o.ID + "]"
		}
		if o.Detail != "" {
			l += "  " + out.EscapeControls(o.Detail)
		}
		lines = append(lines, l)
	}
	payload := map[string]any{verb: outcomes, "remote": remote}
	if len(outcomes) == 0 {
		note := "nothing to " + strings.TrimSuffix(verb, "ed") + " on remote '" + remote + "'"
		lines = append(lines, note)
		payload["note"] = note // non-TTY (agent) mode gets the same note a TTY reader sees
	}
	if partial {
		payload["ok"] = false
		payload["error"] = "partial_failure"
		payload["message"] = "some slugs did not " + strings.TrimSuffix(verb, "ed")
		payload["hint"] = "see the per-slug outcomes in `" + verb + "`"
	}
	outEmit(c, payload, lines)
	if partial {
		// Payload already written; root.go returns this exit code without
		// printing a second document, as it does for watch's timeout.
		return &out.CLIError{Code: "partial_failure", ExitCode: 3}
	}
	return nil
}
