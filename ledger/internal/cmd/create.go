package cmd

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

func init() { register(newCreateCmd) }

func newCreateCmd(c *Ctx) *cobra.Command {
	var scope, owner, supersedes, asFlag, mFlag string
	var fields, reqEv []string
	cmd := &cobra.Command{Use: "create <slug>", Short: "start a new ledger with declared fields",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runCreate(c, args[0], scope, owner, supersedes, asFlag, mFlag, fields, reqEv)
		}}
	cmd.Flags().StringVar(&scope, "scope", "", "what this ledger tracks")
	cmd.MarkFlagRequired("scope")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "NAME=V1,V2 (empty after '=' = free text); repeatable")
	cmd.Flags().StringArrayVar(&reqEv, "require-evidence", nil, "FIELD=V1,V2: these values hard-error without --evidence")
	cmd.Flags().StringVar(&owner, "owner", "", "recorded owner (not enforced in v1)")
	cmd.Flags().StringVar(&supersedes, "supersedes", "", "predecessor slug to close and link")
	cmd.Flags().StringVar(&asFlag, "as", "", "author identity")
	cmd.Flags().StringVarP(&mFlag, "message", "m", "", "short annotation")
	return cmd
}

func runCreate(c *Ctx, slug, scope, owner, supersedes, asFlag, mFlag string, fieldSpecs, reqSpecs []string) error {
	if !model.ValidSlug(slug) {
		return out.Errf("bad_slug", "slugs are lowercase-kebab: [a-z0-9][a-z0-9-]*, max 64 chars", 4,
			"'%s' is not a valid slug", slug)
	}
	fields := map[string][]string{}
	var fieldOrder []string
	for _, spec := range fieldSpecs {
		name, vals, _ := strings.Cut(spec, "=")
		var vv []string
		for _, v := range strings.Split(vals, ",") {
			if v != "" {
				vv = append(vv, v)
			}
		}
		if _, seen := fields[name]; !seen {
			fieldOrder = append(fieldOrder, name)
		}
		fields[name] = vv // nil = free
	}
	if len(fields) == 0 {
		fields = map[string][]string{"status": {"open", "done", "failed", "blocked"}}
		fieldOrder = []string{"status"}
	}
	require := map[string][]string{}
	for _, spec := range reqSpecs {
		f, vals, _ := strings.Cut(spec, "=")
		if _, ok := fields[f]; !ok {
			return out.Errf("unknown_field", "declared fields: "+keys(fields), 4,
				"--require-evidence names '%s', which is not a declared field", f)
		}
		require[f] = strings.Split(vals, ",")
	}
	author := model.ResolveAuthor(asFlag)
	ev := model.NewEvent("create", author, c.Store.Repo)
	ev.Text = mFlag
	base, _, _ := c.Store.Repo.Git("", "rev-parse", "--short", "HEAD")
	meta := model.Meta{Slug: slug, Scope: scope, Created: ev.TS, CreatedBy: author,
		Owner: owner, Supersedes: supersedes, Base: base, Fields: fields, RequireEvidence: require,
		FieldOrder: fieldOrder}
	mb, _ := json.MarshalIndent(meta, "", " ")

	var id string
	var err error
	if supersedes == "" {
		id, err = c.Store.Append(slug, ev, map[string]string{"meta.json": string(mb)}, store.ExpectAbsent)
		if err != nil {
			return mapStoreErr(err, slug)
		}
	} else {
		id, err = c.createSuperseding(slug, supersedes, ev, string(mb), author)
		if err != nil {
			return err // already a well-hinted error naming the right slug
		}
	}
	payload := map[string]any{"id": id, "ledger": slug, "created": true,
		"fields": fields, "require_evidence": require}
	lines := []string{"[" + id + "] created " + slug, "  first cursor: " + id}
	outEmit(c, payload, lines)
	return nil
}

// createSuperseding builds the predecessor's close+link commits (or just the
// link commit when the predecessor is already closed — the wrongful-close
// recovery path) and the new ledger's creation commit, then lands both refs
// in one atomic store.Transaction: refs/ledger/<supersedes> moves to the
// link commit, refs/ledger/<slug> is created fresh. A CAS abort (the
// predecessor moved under us) retries once from freshly reloaded heads.
// Crash recovery: if the predecessor's fold already shows SupersededBy ==
// slug, a prior attempt already landed that side of the transaction — this
// only needs to (re)create the new ledger, and if that's also already done,
// it's a no-op success.
func (c *Ctx) createSuperseding(slug, supersedes string, ev model.Event, metaJSON, author string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		pred, err := c.Load(supersedes)
		if err != nil {
			return "", err
		}

		if pred.SupersededBy == slug {
			if id, err := c.Store.HeadID(slug); err == nil {
				return id, nil // both sides already landed by a prior attempt
			}
			newSha, err := c.Store.BuildCommit(slug, "", ev, map[string]string{"meta.json": metaJSON})
			if err != nil {
				return "", err
			}
			if err := c.Store.Transaction([]store.TxStep{{Ref: "refs/ledger/" + slug, New: newSha, Old: ""}}); err != nil {
				lastErr = mapStoreErr(err, slug)
				continue
			}
			c.Store.GCAuto()
			return newSha[:10], nil
		}

		oldRef := "refs/ledger/" + supersedes
		oldFull, _, code := c.Store.Repo.Git("", "rev-parse", oldRef)
		if code != 0 {
			return "", out.Errf("unknown_ledger", "ledger ls --all", 4, "no ledger '%s' here", supersedes)
		}
		linkParent := oldFull
		if pred.State == "open" {
			closeEv := model.NewEvent("lifecycle", author, c.Store.Repo)
			closeEv.LifecycleKind, closeEv.Reason, closeEv.Successor = "close", "superseded", slug
			closeSha, err := c.Store.BuildCommit(supersedes, oldFull, closeEv, nil)
			if err != nil {
				return "", err
			}
			linkParent = closeSha
		}
		linkEv := model.NewEvent("lifecycle", author, c.Store.Repo)
		linkEv.LifecycleKind, linkEv.Successor = "superseded_by", slug
		linkSha, err := c.Store.BuildCommit(supersedes, linkParent, linkEv, nil)
		if err != nil {
			return "", err
		}
		newSha, err := c.Store.BuildCommit(slug, "", ev, map[string]string{"meta.json": metaJSON})
		if err != nil {
			return "", err
		}

		steps := []store.TxStep{
			{Ref: "refs/ledger/" + slug, New: newSha, Old: ""},
			{Ref: oldRef, New: linkSha, Old: oldFull},
		}
		if err := c.Store.Transaction(steps); err != nil {
			lastErr = mapStoreErr(err, slug)
			continue // retry once from fresh heads
		}
		c.Store.GCAuto()
		return newSha[:10], nil
	}
	return "", lastErr
}

func keys(m map[string][]string) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return strings.Join(ks, ", ")
}

func mapStoreErr(err error, slug string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "slug_exists"):
		return out.Errf("slug_exists", "ledger ls --all — then pick a new slug, e.g. "+slug+"-2", 4,
			"ledger '%s' already exists (slugs are never reused)", slug)
	case strings.Contains(msg, "unknown_ledger"):
		return out.Errf("unknown_ledger", "ledger ls --all", 4, "no ledger '%s' here", slug)
	}
	return err
}
