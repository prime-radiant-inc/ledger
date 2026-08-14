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
	cmd.Flags().StringVar(&o.idemKey, "idempotency-key", "", "dedupe key scoped to (author, kind, key)")
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
			return out.Errf("bad_value", "check the path", 4, "cannot read --from-file %s: %s", fromFile, err)
		}
		body = string(b)
	default:
		// A TTY stdin would block forever waiting for input that will never
		// arrive from a non-interactive source; treat it as no body given
		// rather than hang.
		if !out.IsTTY(os.Stdin) {
			b, _ := io.ReadAll(stdin)
			body = string(b)
		}
	}
	if strings.TrimSpace(body) == "" {
		return out.Errf("empty_body", `provide it with -m "...", --from-file <path>, or on stdin`, 4, "the note body is empty")
	}

	led, err := c.PickLedger(o.ledger)
	if err != nil {
		return err
	}
	author := model.ResolveAuthor(o.as)
	if o.idemKey != "" {
		// Mirrors set's semantics (author-scoped, item-key-scoped): a note
		// dedupes against a prior note by the same author, of the same kind,
		// about the same (possibly absent) item key, carrying the same K.
		for _, ev := range led.Events {
			if ev.Type == "note" && ev.IdempotencyKey == o.idemKey && ev.Author == author &&
				ev.Kind == kind && ev.Key == key {
				outEmit(c, map[string]any{"id": ev.ID, "ledger": led.Slug, "deduped": true, "by": ev.Author},
					[]string{"deduped against " + ev.ID})
				return nil
			}
		}
	}
	ev := model.NewEvent("note", author, c.Store.Repo)
	ev.Kind, ev.Key, ev.Text, ev.Evidence, ev.IdempotencyKey = kind, key, body, o.evidence, o.idemKey
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
