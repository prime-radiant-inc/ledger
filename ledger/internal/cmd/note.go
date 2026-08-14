package cmd

import (
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

func init() { register(newNoteCmd) }

func newNoteCmd(c *Ctx) *cobra.Command {
	var kind, key, fromFile string
	var o writeOpts
	cmd := &cobra.Command{Use: "note", Short: "attach a free-text note to a ledger or item",
		Args: cobra.NoArgs,
		RunE: func(cc *cobra.Command, _ []string) error {
			return runNote(c, cc.InOrStdin(), kind, key, fromFile, o)
		}}
	cmd.Flags().StringVarP(&kind, "kind", "k", "", "note kind, e.g. handoff, gotcha, postmortem")
	cmd.Flags().StringVar(&key, "key", "", "item key this note is about (optional)")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "read the body from a file")
	cmd.Flags().StringVar(&o.ledger, "ledger", "", "target ledger")
	cmd.Flags().StringVar(&o.as, "as", "", "author identity")
	cmd.Flags().StringVarP(&o.m, "message", "m", "", "note body (short form)")
	cmd.Flags().StringArrayVar(&o.evidence, "evidence", nil, "TYPE:REF (e.g. commit:abc123); repeatable")
	return cmd
}

func runNote(c *Ctx, stdin io.Reader, kind, key, fromFile string, o writeOpts) error {
	var body string
	switch {
	case o.m != "" && fromFile != "":
		return out.Errf("conflicting_body", "use --from-file for the body (drop -m), or -m alone for a short body",
			4, "a note has one body source; you gave both -m and --from-file")
	case o.m != "":
		body = o.m
	case fromFile != "":
		b, err := os.ReadFile(fromFile)
		if err != nil {
			return out.Errf("git_failed", "", 1, "%s", err)
		}
		body = string(b)
	default:
		b, _ := io.ReadAll(stdin)
		body = string(b)
	}
	if strings.TrimSpace(body) == "" {
		return out.Errf("empty_body", `provide it with -m "...", --from-file <path>, or on stdin`, 4, "the note body is empty")
	}

	led, err := c.PickLedger(o.ledger)
	if err != nil {
		return err
	}
	author := model.ResolveAuthor(o.as)
	ev := model.NewEvent("note", author, c.Store.Repo)
	ev.Kind, ev.Key, ev.Text, ev.Evidence = kind, key, body, o.evidence
	id, err := c.Store.Append(led.Slug, ev, nil, store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, led.Slug)
	}
	payload := map[string]any{"id": id, "ledger": led.Slug, "kind": kind}
	if key != "" {
		payload["key"] = key
	}
	outEmit(c, payload, []string{"[" + id + "] " + led.Slug + ": note(" + kind + ") " + out.EscapeControls(firstLine(body))})
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + "..."
	}
	return s
}
