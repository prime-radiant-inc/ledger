package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

// exportHeader is JSONL line 1 of an export: a format tag plus enough of the
// original meta to recreate the ledger's schema on import.
type exportHeader struct {
	LedgerExport int        `json:"ledger_export"`
	Slug         string     `json:"slug"`
	Scope        string     `json:"scope"`
	Meta         model.Meta `json:"meta"`
}

// ---- export ----

func init() { register(newExportCmd) }

func newExportCmd(c *Ctx) *cobra.Command {
	var to string
	cmd := &cobra.Command{Use: "export <slug>", Short: "dump a ledger as self-contained JSONL",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runExport(c, args[0], to)
		}}
	cmd.Flags().StringVar(&to, "to", "", "write to this path instead of stdout")
	return cmd
}

// runExport writes one JSONL document: a header line (format tag, slug,
// scope, and the meta needed to recreate the schema), then one line per
// event verbatim — sync sentinels included, since they're part of history.
// Event ids are carried for reference only: import's caveat is that ids
// never survive the boundary.
func runExport(c *Ctx, slug, to string) error {
	evs, meta, err := c.Store.Events(slug)
	if err != nil {
		return out.Errf("unknown_ledger", "chit ls --all", 4, "no ledger '%s' here", slug)
	}

	var buf strings.Builder
	hb, _ := json.Marshal(exportHeader{LedgerExport: 1, Slug: slug, Scope: meta.Scope, Meta: meta})
	buf.Write(hb)
	buf.WriteByte('\n')
	for _, ev := range evs {
		eb, _ := json.Marshal(eventJSON(ev))
		buf.Write(eb)
		buf.WriteByte('\n')
	}

	if to == "" {
		fmt.Fprint(c.Stdout, buf.String())
		return nil
	}
	if err := os.WriteFile(to, []byte(buf.String()), 0o644); err != nil {
		return out.Errf("write_failed", "check the path is writable", 1, "%s", err)
	}
	outEmit(c, map[string]any{"ledger": slug, "exported": len(evs), "to": to},
		[]string{fmt.Sprintf("exported %d events from %s to %s", len(evs), slug, to)})
	return nil
}

// ---- import ----

func init() { register(newImportCmd) }

func newImportCmd(c *Ctx) *cobra.Command {
	var slug string
	cmd := &cobra.Command{Use: "import <path>", Short: "recreate a ledger from an export file",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runImport(c, args[0], slug)
		}}
	cmd.Flags().StringVar(&slug, "slug", "", "slug for the new ledger (must not already exist)")
	cmd.MarkFlagRequired("slug")
	return cmd
}

// runImport replays an export's events onto a brand-new slug, in order,
// preserving each event's Type and payload untouched — the only changes are
// the commit's own committer identity (stamped "imported", never the
// importing harness's marker), ImportedFrom (set to the original event id,
// for traceability), and — for rollup events only — Children, rewritten from
// old ids to new via remapRollupChildren. Because ids are assigned by the
// store at commit time, raw identity does not otherwise cross the boundary:
// a cursor or an --id reference from the source ledger is meaningless on the
// copy. Without the Children rewrite, though, an imported rollup would still
// point at old ids that resolve to nothing in the new chain — its curated
// children would silently un-collapse in `tail` — so that one id reference
// does have to survive the boundary, structurally if not literally.
//
// Every line is parsed and validated before any git object is written: a
// malformed line partway through the file must never leave a half-created
// ledger behind under a slug that (with no delete verb, slugs never reused)
// would then be permanently unusable. Once the whole file checks out, the
// entire chain lands under one CAS ref creation via AppendChain — either the
// full import lands or none of it does.
func runImport(c *Ctx, path, newSlug string) error {
	if !model.ValidSlug(newSlug) {
		return out.Errf("bad_slug", "slugs are lowercase-kebab: [a-z0-9][a-z0-9-]*, max 64 chars", 4,
			"'%s' is not a valid slug", newSlug)
	}
	if _, err := c.Store.HeadID(newSlug); err == nil {
		return out.Errf("slug_exists", "chit ls --all — then pick a new slug, e.g. "+newSlug+"-2", 4,
			"ledger '%s' already exists (slugs are never reused)", newSlug)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return out.Errf("bad_value", "check the path", 4, "cannot read '%s': %s", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 2 || lines[0] == "" {
		return out.Errf("bad_export", "re-export with `chit export`", 4, "'%s' is not a ledger export", path)
	}
	var header exportHeader
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil || header.LedgerExport != 1 {
		return out.Errf("bad_export", "re-export with `chit export`", 4, "'%s' is not a ledger export", path)
	}

	var evs []model.Event
	for i, line := range lines[1:] {
		if line == "" {
			continue
		}
		var raw map[string]any
		var ev model.Event
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return out.Errf("bad_export", "re-export with `chit export`", 4, "malformed event on line %d", i+2)
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return out.Errf("bad_export", "re-export with `chit export`", 4, "malformed event on line %d", i+2)
		}
		ev.CommitterOverride = "imported"
		if id, ok := raw["id"].(string); ok {
			ev.ImportedFrom = id
		}
		evs = append(evs, ev)
	}
	if len(evs) == 0 {
		return out.Errf("bad_export", "re-export with `chit export`", 4, "'%s' has no events to import", path)
	}

	meta := header.Meta
	meta.Slug = newSlug
	if declErr := model.ValidateDeclarations(meta); declErr != nil {
		return out.Errf(declErr.Ident, declErr.Hint, 4, "%s", declErr.Msg)
	}
	metaJSON, _ := json.MarshalIndent(meta, "", " ")

	if _, err := c.Store.AppendChain(newSlug, evs, map[string]string{"meta.json": string(metaJSON)},
		store.ExpectAbsent, remapRollupChildren); err != nil {
		return mapStoreErr(err, newSlug)
	}

	outEmit(c, map[string]any{"imported": len(evs), "ledger": newSlug,
		"note": "event ids did not survive the boundary"},
		[]string{fmt.Sprintf("imported %d events into %s", len(evs), newSlug)})
	return nil
}

// remapRollupChildren is AppendChain's remap hook for import: a rollup's
// Children list names old (pre-import) event ids, which resolve to nothing
// once ids are reassigned at commit time. priorIDs is keyed by each earlier
// event's ImportedFrom (its old id), populated as those events are built —
// complete for every child by the time its rollup's turn comes, since
// children always precede the rollup that encapsulates them in chain order.
// A child id absent from priorIDs (a hand-crafted export payload citing an
// id that was never itself an event in this file) is left verbatim.
func remapRollupChildren(ev *model.Event, priorIDs map[string]string) {
	if ev.Type != "rollup" || len(ev.Children) == 0 {
		return
	}
	remapped := make([]string, len(ev.Children))
	for i, cid := range ev.Children {
		if nid, ok := priorIDs[cid]; ok {
			remapped[i] = nid
		} else {
			remapped[i] = cid
		}
	}
	ev.Children = remapped
}
