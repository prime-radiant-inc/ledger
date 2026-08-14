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
		return out.Errf("unknown_ledger", "ledger ls --all", 4, "no ledger '%s' here", slug)
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
// importing harness's marker) and ImportedFrom, set to the original event id
// for traceability. Because ids are assigned by the store at commit time,
// identity does not cross the boundary: only payload equality is guaranteed.
func runImport(c *Ctx, path, newSlug string) error {
	if !model.ValidSlug(newSlug) {
		return out.Errf("bad_slug", "slugs are lowercase-kebab: [a-z0-9][a-z0-9-]*, max 64 chars", 4,
			"'%s' is not a valid slug", newSlug)
	}
	if _, err := c.Store.HeadID(newSlug); err == nil {
		return out.Errf("slug_exists", "ledger ls --all — then pick a new slug, e.g. "+newSlug+"-2", 4,
			"ledger '%s' already exists (slugs are never reused)", newSlug)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return out.Errf("bad_value", "check the path", 4, "cannot read '%s': %s", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 2 || lines[0] == "" {
		return out.Errf("bad_export", "re-export with `ledger export`", 4, "'%s' is not a ledger export", path)
	}
	var header exportHeader
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil || header.LedgerExport != 1 {
		return out.Errf("bad_export", "re-export with `ledger export`", 4, "'%s' is not a ledger export", path)
	}
	meta := header.Meta
	meta.Slug = newSlug
	metaJSON, _ := json.MarshalIndent(meta, "", " ")

	count := 0
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		var raw map[string]any
		var ev model.Event
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return out.Errf("bad_export", "re-export with `ledger export`", 4, "malformed event on line %d", count+2)
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return out.Errf("bad_export", "re-export with `ledger export`", 4, "malformed event on line %d", count+2)
		}
		ev.CommitterOverride = "imported"
		if id, ok := raw["id"].(string); ok {
			ev.ImportedFrom = id
		}

		var extra map[string]string
		expect := store.ExpectPresent
		if count == 0 {
			extra = map[string]string{"meta.json": string(metaJSON)}
			expect = store.ExpectAbsent
		}
		if _, err := c.Store.Append(newSlug, ev, extra, expect); err != nil {
			return mapStoreErr(err, newSlug)
		}
		count++
	}

	outEmit(c, map[string]any{"imported": count, "ledger": newSlug,
		"note": "event ids did not survive the boundary"},
		[]string{fmt.Sprintf("imported %d events into %s", count, newSlug)})
	return nil
}
