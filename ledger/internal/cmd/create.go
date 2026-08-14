package cmd

import (
	"encoding/json"
	"fmt"
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

// createSuperseding runs classify-then-write, at most twice: check the
// successor slug and the predecessor's fold state fresh, then either land
// the atomic two-ref transaction or take the narrower crash-recovery path.
// A transaction abort is a pure CAS race (something moved a ref under us,
// with no genuine collision or recoverable half-state to explain it) — the
// second pass reclassifies from fresh heads; if that also aborts, the error
// is a classified CLIError, never git's raw transaction-abort text.
func (c *Ctx) createSuperseding(slug, supersedes string, ev model.Event, metaJSON, author string) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		id, retry, err := c.attemptSupersede(slug, supersedes, ev, metaJSON, author)
		if !retry {
			return id, err
		}
	}
	return "", out.Errf("cas_exhausted",
		"re-run the same `ledger create ... --supersedes` command", 1,
		"the supersede transaction for '%s' could not land after retrying (concurrent writers?)", slug)
}

// attemptSupersede is one classify-then-write pass. retry=true means the
// transaction aborted for reasons the classification step didn't already
// explain (not a real slug collision, not a completed crash half-state) —
// the caller should reclassify from fresh heads rather than give up.
func (c *Ctx) attemptSupersede(slug, supersedes string, ev model.Event, metaJSON, author string) (id string, retry bool, err error) {
	pred, err := c.Load(supersedes)
	if err != nil {
		return "", false, err
	}

	if _, sErr := c.Store.HeadID(slug); sErr == nil {
		// The successor ref already exists. Classify before touching
		// anything: is this our own crash-dangled supersede (the successor
		// landed but the predecessor's link didn't), or a genuine slug
		// collision that just happens to share this name?
		_, succMeta, evErr := c.Store.Events(slug)
		if evErr == nil && succMeta.Supersedes == supersedes && pred.SupersededBy != slug {
			linkEv := model.NewEvent("lifecycle", author, c.Store.Repo)
			linkEv.LifecycleKind, linkEv.Successor = "superseded_by", slug
			if _, aErr := c.Store.Append(supersedes, linkEv, nil, store.ExpectPresent); aErr != nil {
				return "", false, mapStoreErr(aErr, supersedes)
			}
			recoveredID, _ := c.Store.HeadID(slug)
			return recoveredID, false, nil
		}
		return "", false, mapStoreErr(fmt.Errorf("%w: %s", store.ErrSlugExists, slug), slug)
	}

	oldRef := "refs/ledger/" + supersedes
	oldFull, _, code := c.Store.Repo.Git("", "rev-parse", oldRef)
	if code != 0 {
		return "", false, out.Errf("unknown_ledger", "ledger ls --all", 4, "no ledger '%s' here", supersedes)
	}
	linkParent := oldFull
	if pred.State == "open" {
		closeEv := model.NewEvent("lifecycle", author, c.Store.Repo)
		closeEv.LifecycleKind, closeEv.Reason, closeEv.Successor = "close", "superseded", slug
		closeSha, bErr := c.Store.BuildCommit(supersedes, oldFull, closeEv, nil)
		if bErr != nil {
			return "", false, bErr
		}
		linkParent = closeSha
	}
	linkEv := model.NewEvent("lifecycle", author, c.Store.Repo)
	linkEv.LifecycleKind, linkEv.Successor = "superseded_by", slug
	linkSha, lErr := c.Store.BuildCommit(supersedes, linkParent, linkEv, nil)
	if lErr != nil {
		return "", false, lErr
	}
	newSha, nErr := c.Store.BuildCommit(slug, "", ev, map[string]string{"meta.json": metaJSON})
	if nErr != nil {
		return "", false, nErr
	}

	steps := []store.TxStep{
		{Ref: "refs/ledger/" + slug, New: newSha, Old: ""},
		{Ref: oldRef, New: linkSha, Old: oldFull},
	}
	if tErr := c.Store.Transaction(steps); tErr != nil {
		return "", true, nil // pure CAS race: reclassify and retry
	}
	c.Store.GCAuto()
	return newSha[:10], false, nil
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
