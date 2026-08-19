package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/board"
	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
)

// resolveAt parses --at (millisecond UTC layout; legacy accepted) into the
// evaluation clock for age/staleness rendering — the flag exists on ready
// and notes only (sync spec Addition 4). An empty flag means "the real
// clock, through the funnel"; an unparseable value is bad_value, never a
// silent revert to wall-clock now.
func resolveAt(at string) (time.Time, error) {
	if at == "" {
		return model.Now(), nil
	}
	t, err := model.ParseTS(at)
	if err != nil {
		return time.Time{}, out.Errf("bad_value",
			"millisecond UTC layout, e.g. 2030-01-01T00:00:00.000 (the legacy 2030-01-01T00:00:00 layout is also accepted)",
			4, "--at %q does not parse: %s", at, err)
	}
	return t, nil
}

// row is one spine cell: the latest value recorded for (key, field), with
// its provenance. Shared by status (spine + by-branch) and show. Title is
// populated only by show on a ready-capable board (spec "Titles"); omitempty
// makes it structurally absent everywhere else (plain boards, and statusless
// keys on a ready-capable board) rather than an empty string.
type row struct {
	Key      string   `json:"key"`
	Field    string   `json:"field"`
	Value    string   `json:"value"`
	Note     string   `json:"note"`
	By       string   `json:"by"`
	Branch   string   `json:"branch"`
	TS       string   `json:"ts"`
	ID       string   `json:"id"`
	Evidence []string `json:"evidence"`
	Title    string   `json:"title,omitempty"`
	// Renamed is present exactly when Title is a renamed title: a title
	// never renders unlabeled, so a reader can always see it is no longer
	// the seed's own message, and who changed it.
	Renamed *board.RenameInfo `json:"renamed,omitempty"`
}

func rowOf(key, f string, ev model.Event) row {
	return row{Key: key, Field: f, Value: ev.Fields[f], Note: ev.Text, By: ev.Author,
		Branch: ev.Origin.Branch, TS: ev.TS, ID: ev.ID, Evidence: ev.Evidence}
}

// spineRows dumps the fold's latest-per-(key,field) view — no branch
// awareness, the total-order-latest that Fold already computed.
func spineRows(led *fold.Ledger, field string) []row {
	rows := []row{}
	for key, fields := range led.Spine {
		for f, ev := range fields {
			if field != "" && f != field {
				continue
			}
			rows = append(rows, rowOf(key, f, ev))
		}
	}
	sortRows(rows)
	return rows
}

// byBranchRows folds latest-per-(key,field,branch) over the raw set events —
// the branch-qualified read that lets "review=approved on feat" coexist with
// "review=pending on main". Events are chronological, so a plain overwrite
// keeps the latest per bucket.
func byBranchRows(led *fold.Ledger, field string) []row {
	type bucket struct{ key, field, branch string }
	latest := map[bucket]model.Event{}
	for _, ev := range led.Events {
		if ev.Type != "set" {
			continue
		}
		for f := range ev.Fields {
			if field != "" && f != field {
				continue
			}
			latest[bucket{ev.Key, f, ev.Origin.Branch}] = ev
		}
	}
	rows := []row{}
	for b, ev := range latest {
		rows = append(rows, rowOf(b.key, b.field, ev))
	}
	sortRows(rows)
	return rows
}

func sortRows(rows []row) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Key != rows[j].Key {
			return rows[i].Key < rows[j].Key
		}
		if rows[i].Field != rows[j].Field {
			return rows[i].Field < rows[j].Field
		}
		return rows[i].Branch < rows[j].Branch
	})
}

// spineLine renders one row for a TTY. Evidence-less values are marked, and
// the annotation is control-escaped: a note body can otherwise counterfeit a
// provenance line on a raw terminal.
func spineLine(r row) string {
	evd := out.EscapeControls(strings.Join(r.Evidence, " "))
	if evd == "" {
		evd = "(no evidence)"
	}
	note := ""
	if r.Note != "" {
		note = `  "` + out.EscapeControls(r.Note) + `"`
	}
	return fmt.Sprintf("  %-16s %s=%-12s %-12s %-16s %s%s",
		out.EscapeControls(r.Key), out.EscapeControls(r.Field), out.EscapeControls(r.Value),
		out.EscapeControls(r.Branch), out.EscapeControls(r.By), evd, note)
}

// noteDoc is a note's JSON shape wherever one appears in a read verb's
// payload (notes, status drill-down). via is always populated from
// Committers, even when the kind doesn't mandate rendering it on a TTY.
type noteDoc struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Key  string `json:"key,omitempty"`
	By   string `json:"by"`
	TS   string `json:"ts"`
	Text string `json:"text"`
	Via  string `json:"via"`
}

func noteDocOf(n model.Event, committers map[string]string) noteDoc {
	return noteDoc{ID: n.ID, Kind: n.Kind, Key: n.Key, By: n.Author, TS: n.TS, Text: n.Text, Via: committers[n.ID]}
}

// mandatesVia is the provenance rule: ruling/standing-rule notes always
// render "(via committer)" — the doctrine text an agent must weigh by
// authorship — and so does any note rendered under --latest.
func mandatesVia(kind string, latest bool) bool {
	return latest || kind == "ruling" || kind == "standing-rule"
}

func provenance(n model.Event, committers map[string]string, latest bool) string {
	if mandatesVia(n.Kind, latest) {
		return "by " + out.EscapeControls(n.Author) + " (via " + out.EscapeControls(committers[n.ID]) + ")"
	}
	return "by " + out.EscapeControls(n.Author)
}

// quotedBody renders text as control-escaped lines, quote-prefixed per line
// so a forged control sequence in the body can't overwrite surrounding
// output — the one body-rendering path, shared by noteLines and show --id's
// full-event render (never a second escaping implementation).
func quotedBody(text string) []string {
	lines := make([]string, 0)
	for _, bl := range strings.Split(out.EscapeControls(text), "\n") {
		lines = append(lines, "  | "+bl)
	}
	return lines
}

// noteLines renders notes for a TTY: an identity line (age under --latest,
// absolute timestamp otherwise, per the spec's "age only in ls/--latest"
// rule) followed by the quoted body (quotedBody). now is the evaluation
// clock for the --latest age render (notes' --at, resolved by the caller;
// unused when latest is false).
func noteLines(notes []model.Event, committers map[string]string, latest bool, now time.Time) []string {
	lines := make([]string, 0, len(notes)*2)
	for _, n := range notes {
		when := n.TS
		if latest {
			when = out.AgeAt(n.TS, now)
		}
		head := when + " " + provenance(n, committers, latest) + "  [" + n.ID + "] " + out.EscapeControls(n.Kind)
		if n.Key != "" {
			head += " (" + n.Key + ")"
		}
		lines = append(lines, head)
		lines = append(lines, quotedBody(n.Text)...)
	}
	return lines
}

// eventJSON exposes model.Event's id (json:"-" on the struct itself, since
// Store.Append truncates it after the fact) for read-verb payloads that must
// carry it: tail's cursor stream and status's per-key history.
func eventJSON(ev model.Event) map[string]any {
	b, _ := json.Marshal(ev)
	var m map[string]any
	json.Unmarshal(b, &m)
	m["id"] = ev.ID
	return m
}

func eventsJSON(evs []model.Event) []map[string]any {
	docs := make([]map[string]any, 0, len(evs))
	for _, ev := range evs {
		docs = append(docs, eventJSON(ev))
	}
	return docs
}

// eventLine is the raw chain's per-event TTY render. A contest resolution
// is labeled here, never left JSON-only: the durable record exists to be
// read back off the chain.
func eventLine(ev model.Event) string {
	line := "[" + ev.ID + "] " + ev.TS + " " + ev.Type + " " + out.EscapeControls(ev.Author)
	if m := out.ContestedResolvedMarker(ev.ContestedResolved); m != "" {
		line += "  " + m
	}
	return line
}

// nonSyncEvents drops sync sentinels — invisible to fold's schema/spine/state
// already; read verbs that walk the raw event list (tail, show's count) keep
// that same invisibility rather than leaking merge plumbing into a render.
func nonSyncEvents(evs []model.Event) []model.Event {
	out := make([]model.Event, 0, len(evs))
	for _, ev := range evs {
		if ev.Type != "sync" {
			out = append(out, ev)
		}
	}
	return out
}

// truncateRunes caps s at n runes, appending "..." when trimmed — shared by
// show's recent-notes first line (90: a summary, not the body; full text
// stays a `notes --id` away) and ls's scope column (44).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// noteSummaryLineAt is show's (and render's) compact per-note render: an
// explicit leading identity timestamp, provenance, id, kind, and a
// truncated escaped first line — matching the JSON recent_notes shape
// field-for-field, never the full body noteLines prints. The timestamp is
// always the event's own absolute ts, never Age(n.TS): show has no --at
// (sync spec Addition 4 — it's clock-free, like every covered verb but
// ready/notes), and a wall-clock-relative age here would make two reads of
// the same unchanged chain, moments apart, render different bytes.
func noteSummaryLineAt(when string, n model.Event, committers map[string]string) string {
	line := when + " " + provenance(n, committers, false) + "  [" + n.ID + "] " + out.EscapeControls(n.Kind)
	if n.Key != "" {
		line += " (" + n.Key + ")"
	}
	return line + `  "` + truncateRunes(out.EscapeControls(firstLine(n.Text)), 90) + `"`
}

// dueAfter refolds and reports curation debt for a write envelope. Advisory:
// on any error it returns ok=false and the caller omits the field rather
// than failing a write that already landed.
func dueAfter(c *Ctx, slug string) (int, bool) {
	led, err := c.Load(slug)
	if err != nil {
		return 0, false
	}
	return led.Due(), true
}

func knownKeys(led *fold.Ledger) []string {
	ks := make([]string, 0, len(led.Spine))
	for k := range led.Spine {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// lastForKey returns the last n events (in chronological order) whose Key
// matches — status drill-down's "history" panel.
func lastForKey(evs []model.Event, key string, n int) []model.Event {
	matched := []model.Event{}
	for _, ev := range evs {
		if ev.Key == key {
			matched = append(matched, ev)
		}
	}
	if len(matched) > n {
		matched = matched[len(matched)-n:]
	}
	return matched
}

// ---- status ----

func init() { register(newStatusCmd) }

func newStatusCmd(c *Ctx) *cobra.Command {
	var field, ledgerFlag string
	var byBranch bool
	cmd := &cobra.Command{Use: "status [key]", Short: "the spine: latest value per item",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			key := ""
			if len(args) == 1 {
				key = args[0]
			}
			return runStatus(c, key, field, byBranch, ledgerFlag)
		}}
	cmd.Flags().StringVar(&field, "field", "", "limit to one declared field")
	cmd.Flags().BoolVar(&byBranch, "by-branch", false, "fold latest per (key, field, branch) instead of globally")
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	return cmd
}

func runStatus(c *Ctx, key, field string, byBranch bool, ledgerFlag string) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}

	if key == "" {
		var rows []row
		if byBranch {
			rows = byBranchRows(led, field)
		} else {
			rows = spineRows(led, field)
		}
		payload := map[string]any{"ledger": led.Slug, "scope": led.Meta.Scope, "state": led.State, "rows": rows}
		c.attachFreshness(led, payload)
		lines := addRedirect(c, led, payload)
		lines = append(lines, fmt.Sprintf("%s  scope=%s  state=%s", led.Slug, led.Meta.Scope, led.State))
		for _, r := range rows {
			lines = append(lines, spineLine(r))
		}
		outEmit(c, payload, lines)
		return nil
	}

	fields, ok := led.Spine[key]
	if !ok {
		hint := "known keys: " + strings.Join(knownKeys(led), ", ")
		return out.Errf("unknown_key", hint, 4, "no such key '%s' on '%s'", key, led.Slug)
	}
	values := map[string]row{}
	fieldNames := make([]string, 0, len(fields))
	for f, ev := range fields {
		if field != "" && f != field {
			continue
		}
		values[f] = rowOf(key, f, ev)
		fieldNames = append(fieldNames, f)
	}
	sort.Strings(fieldNames)

	committers, _ := c.Store.Committers(led.Slug)
	notes := []noteDoc{}
	var noteEvs []model.Event
	for _, n := range led.Notes() {
		if n.Key == key {
			notes = append(notes, noteDocOf(n, committers))
			noteEvs = append(noteEvs, n)
		}
	}
	history := lastForKey(led.Events, key, 8)

	payload := map[string]any{"ledger": led.Slug, "key": key, "values": values,
		"notes": notes, "history": eventsJSON(history)}
	c.attachFreshness(led, payload)

	lines := addRedirect(c, led, payload)
	lines = append(lines, key+" on "+led.Slug)
	for _, f := range fieldNames {
		lines = append(lines, spineLine(values[f]))
	}
	lines = append(lines, noteLines(noteEvs, committers, false, model.Now())...)
	for _, ev := range history {
		lines = append(lines, "  "+eventLine(ev))
	}
	outEmit(c, payload, lines)
	return nil
}

// ---- show ----

func init() { register(newShowCmd) }

func newShowCmd(c *Ctx) *cobra.Command {
	var ledgerFlag, idFlag string
	var whereRaw []string
	cmd := &cobra.Command{Use: "show", Short: "full render: schema, spine, notes", Args: noPositionals("show"),
		RunE: func(_ *cobra.Command, _ []string) error { return runShow(c, ledgerFlag, whereRaw, idFlag) }}
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	cmd.Flags().StringArrayVar(&whereRaw, "where", nil, "FIELD=VALUE (exact) or FIELD~=TOKEN (membership); repeatable, AND'd")
	cmd.Flags().StringVar(&idFlag, "id", "", "render exactly one event by id (prefix match) — a ticket's contest ids, or any event off `tail --raw`")
	return cmd
}

func runShow(c *Ctx, ledgerFlag string, whereRaw []string, idFlag string) error {
	if idFlag != "" {
		return runShowID(c, ledgerFlag, idFlag)
	}
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	clauses, err := parseWhere(whereRaw, led.Meta)
	if err != nil {
		return err
	}

	rows := spineRows(led, "")
	ready := model.ReadyCapable(led.Meta)
	// board.Build is only ever consulted below when ready (Title lookup) or
	// clauses is non-empty (matchWhere) — build it exactly then, never
	// unconditionally.
	var b *board.Board
	if ready || len(clauses) > 0 {
		b = board.Build(led.Meta, led.Events)
	}
	if ready {
		for i := range rows {
			if k, exists := b.Keys[rows[i].Key]; exists {
				rows[i].Title, rows[i].Renamed = k.Title, k.RenameInfo()
			}
		}
	}
	if len(clauses) > 0 {
		kept := []row{}
		for _, r := range rows {
			if matchWhere(b.Keys[r.Key], clauses) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	committers, _ := c.Store.Committers(led.Slug)

	allNotes := led.Notes()
	recent := allNotes
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}
	recentNotes := make([]map[string]any, 0, len(recent))
	for _, n := range recent {
		recentNotes = append(recentNotes, map[string]any{
			"id": n.ID, "kind": n.Kind, "by": n.Author, "ts": n.TS, "first_line": firstLine(n.Text),
		})
	}

	eventCount := len(nonSyncEvents(led.Events))
	payload := map[string]any{
		"ledger": led.Slug, "scope": led.Meta.Scope, "state": led.State, "rows": rows,
		"schema": led.Schema, "require_evidence": led.Require, "recent_notes": recentNotes,
		"events": eventCount, "head": led.Head(),
	}
	c.attachFreshness(led, payload)

	lines := addRedirect(c, led, payload)
	lines = append(lines, fmt.Sprintf("%s  scope=%s  base=%s  state=%s  events=%d  head=%s",
		led.Slug, led.Meta.Scope, led.Meta.Base, led.State, eventCount, led.Head()))
	for _, r := range rows {
		lines = append(lines, spineLine(r))
	}
	for _, n := range recent {
		lines = append(lines, noteSummaryLineAt(n.TS, n, committers))
	}
	outEmit(c, payload, lines)
	return nil
}

// findByID resolves a possibly-short --id argument (prefix match, mirroring
// notes --id's existing convention) against a set of events. Exactly one
// match is the only usable result: show --id's whole point is exactly one
// event, and notes --id's bad_value path (below) needs to tell "unknown"
// from "ambiguous" from "a real, different event" apart.
func findByID(evs []model.Event, id string) (ev model.Event, matches int) {
	for _, e := range evs {
		if strings.HasPrefix(e.ID, id) {
			ev = e
			matches++
		}
	}
	return ev, matches
}

// idReadErr is the bad_value show --id and notes --id share for an id that
// doesn't resolve to exactly one event: 0 matches is unknown, >1 is an
// ambiguous prefix — neither can hand back a specific event.
func idReadErr(slug, id string, matches int) error {
	if matches == 0 {
		return out.Errf("bad_value", "check the id — a ticket's contest ids, or any id off `ledger tail --raw`, are exact", 4,
			"'%s' does not match any event on '%s'", id, slug)
	}
	return out.Errf("bad_value", fmt.Sprintf("give more hex characters — the prefix matches %d events", matches), 4,
		"'%s' is ambiguous on '%s' (%d events match)", id, slug, matches)
}

// notesIDErr is notes --id's bad_value for an id that isn't a note event of
// this ledger: a real event of some other type (ev/matches==1 but not
// "note"), or unknown/ambiguous outright (matches != 1). Either way the
// hint sends the caller to show --id, which renders any event, note or not.
func notesIDErr(slug, id string, ev model.Event, matches int) error {
	hint := "use `show --id " + id + "` — it renders any event, note or not"
	if matches == 1 {
		return out.Errf("bad_value", hint, 4, "'%s' is a %s event on '%s', not a note", id, ev.Type, slug)
	}
	if matches == 0 {
		return out.Errf("bad_value", hint, 4, "'%s' does not match any event on '%s'", id, slug)
	}
	return out.Errf("bad_value", hint, 4, "'%s' is ambiguous on '%s' (%d events match, none are notes)", id, slug, matches)
}

// runShowID is show --id's full single-event render (sync spec Addition 3,
// "Id reads, both paths pinned"): a ticket hands out bare event ids — a
// contested entry's loser heads, chiefly — and this is how an agent reads
// one back in full; a ticket holder following a redirect must never land on
// less than the event.
func runShowID(c *Ctx, ledgerFlag, id string) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	ev, matches := findByID(led.Events, id)
	if matches != 1 {
		return idReadErr(led.Slug, id, matches)
	}
	committers, _ := c.Store.Committers(led.Slug)
	payload := eventJSON(ev)
	payload["ledger"] = led.Slug
	payload["via"] = committers[ev.ID]
	// show --id is the `show` verb (freshness's documented scope) and a
	// read verb (addRedirect's — every read verb carries the superseded
	// redirect, not just bare show), so both apply here exactly as they do
	// on the rest of show.
	c.attachFreshness(led, payload)
	lines := addRedirect(c, led, payload)
	lines = append(lines, showIDLines(ev, committers)...)
	outEmit(c, payload, lines)
	return nil
}

// showIDLines is show --id's TTY render: a header (id/ts/type/key/kind, plus
// the contested-resolved marker eventLine also carries), mandatory
// provenance (unlike an ordinary spine row, a bare event id has no key/field
// context for a reader to infer authorship from), fields, evidence, and —
// when the event carries a body — the message under quotedBody, the exact
// per-line-quoted, control-escaped treatment noteLines gives a note body.
// An event's Text is a note's body under a different Type; there is no
// second rendering for it here.
func showIDLines(ev model.Event, committers map[string]string) []string {
	head := "[" + ev.ID + "] " + ev.TS + " " + ev.Type
	if ev.Key != "" {
		head += " key=" + out.EscapeControls(ev.Key)
	}
	if ev.Kind != "" {
		head += " kind=" + out.EscapeControls(ev.Kind)
	}
	if m := out.ContestedResolvedMarker(ev.ContestedResolved); m != "" {
		head += "  " + m
	}
	lines := []string{head, "  " + provenance(ev, committers, true)}
	if len(ev.Fields) > 0 {
		names := fieldNames(ev.Fields)
		parts := make([]string, 0, len(names))
		for _, f := range names {
			parts = append(parts, out.EscapeControls(f)+"="+out.EscapeControls(ev.Fields[f]))
		}
		lines = append(lines, "  fields: "+strings.Join(parts, " "))
	}
	if len(ev.Evidence) > 0 {
		lines = append(lines, "  evidence: "+out.EscapeControls(strings.Join(ev.Evidence, " ")))
	}
	if ev.Text != "" {
		lines = append(lines, quotedBody(ev.Text)...)
	}
	return lines
}

// addRedirect is the superseded-read rule shared by every read verb: when a
// ledger carries a superseded_by link, its payload gains that link (plus any
// extra_links from dueling successors) and its TTY render leads with the
// redirect line — a reader must never have to guess it's looking at a
// forwarding address.
func addRedirect(c *Ctx, led *fold.Ledger, payload map[string]any) []string {
	if led.SupersededBy == "" {
		return nil
	}
	payload["superseded_by"] = led.SupersededBy
	if len(led.ExtraLinks) > 0 {
		payload["extra_links"] = led.ExtraLinks
	}
	return []string{redirectLine(c, led)}
}

// redirectLine is show's lead line on a superseded ledger: the redirect, or
// — when the successor hasn't arrived locally yet — the sync hint. Load
// failing (unknown_ledger) is exactly "not present locally".
func redirectLine(c *Ctx, led *fold.Ledger) string {
	if _, err := c.Load(led.SupersededBy); err != nil {
		return "successor '" + led.SupersededBy + "' not present locally — run ledger sync"
	}
	return "superseded by '" + led.SupersededBy + "' — read/write there"
}

// ---- notes ----

func init() { register(newNotesCmd) }

func newNotesCmd(c *Ctx) *cobra.Command {
	var kind, key, id, ledgerFlag, at string
	var latest bool
	var limit int
	cmd := &cobra.Command{Use: "notes", Short: "list notes", Args: noPositionals("notes"),
		RunE: func(_ *cobra.Command, _ []string) error {
			return runNotes(c, kind, key, id, latest, limit, ledgerFlag, at)
		}}
	cmd.Flags().StringVarP(&kind, "kind", "k", "", "filter by note kind")
	cmd.Flags().StringVar(&key, "key", "", "filter by item key")
	cmd.Flags().StringVar(&id, "id", "", "filter to one note by id (prefix match)")
	cmd.Flags().BoolVar(&latest, "latest", false, "only the single most recent matching note")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "how many notes (most recent)")
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	cmd.Flags().StringVar(&at, "at", "", "fix the evaluation clock for --latest's age render (millisecond UTC; legacy layout accepted)")
	return cmd
}

func runNotes(c *Ctx, kind, key, id string, latest bool, limit int, ledgerFlag, at string) error {
	now, err := resolveAt(at)
	if err != nil {
		return err
	}
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	matched := []model.Event{}
	for _, n := range led.Notes() {
		if kind != "" && n.Kind != kind {
			continue
		}
		if key != "" && n.Key != key {
			continue
		}
		if id != "" && !strings.HasPrefix(n.ID, id) {
			continue
		}
		matched = append(matched, n)
	}
	// notes --id's non-note branch (sync spec Addition 3): an id that isn't
	// a note event of THIS ledger — wrong type, or unknown outright — must
	// never fall through to a silent empty list (the parent's "empty
	// results announce themselves" rule). An id that IS a note but got
	// filtered out by --kind/--key is the ordinary, unchanged empty list.
	if id != "" && len(matched) == 0 {
		if ev, m := findByID(led.Events, id); m != 1 || ev.Type != "note" {
			return notesIDErr(led.Slug, id, ev, m)
		}
	}
	n := limit
	if latest {
		n = 1
	}
	if n > 0 && len(matched) > n {
		matched = matched[len(matched)-n:]
	}

	committers, _ := c.Store.Committers(led.Slug)
	docs := make([]noteDoc, 0, len(matched))
	for _, note := range matched {
		docs = append(docs, noteDocOf(note, committers))
	}
	payload := map[string]any{"ledger": led.Slug, "notes": docs}
	lines := addRedirect(c, led, payload)
	lines = append(lines, noteLines(matched, committers, latest, now)...)
	outEmit(c, payload, lines)
	return nil
}

// ---- tail ----

func init() { register(newTailCmd) }

func newTailCmd(c *Ctx) *cobra.Command {
	var limit int
	var ledgerFlag, inID string
	var raw bool
	cmd := &cobra.Command{Use: "tail", Short: "the curated history: roots, oldest first (rollups collapse their contents)",
		Args: noPositionals("tail"),
		RunE: func(_ *cobra.Command, _ []string) error { return runTail(c, limit, raw, inID, ledgerFlag) }}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "how many recent roots (or raw events)")
	cmd.Flags().BoolVar(&raw, "raw", false, "the true event chain, nothing collapsed")
	cmd.Flags().StringVar(&inID, "in", "", "open one rollup: list the records inside it")
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	return cmd
}

func runTail(c *Ctx, limit int, raw bool, inID, ledgerFlag string) error {
	if raw && inID != "" {
		return out.Errf("bad_value", "--raw shows the whole chain; --in opens one rollup — pick one", 4,
			"--raw and --in are mutually exclusive")
	}
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	if inID != "" {
		return runTailIn(c, led, inID)
	}
	if raw {
		evs := nonSyncEvents(led.Events)
		if limit > 0 && len(evs) > limit {
			evs = evs[len(evs)-limit:]
		}
		docs := eventsJSON(evs)
		for i, ev := range evs {
			if led.Losers[ev.ID] {
				docs[i]["duel_loser"] = true
			}
		}
		payload := map[string]any{"ledger": led.Slug, "raw": true, "events": docs, "cursor": led.Head()}
		lines := addRedirect(c, led, payload)
		for _, ev := range evs {
			l := eventLine(ev)
			if led.Losers[ev.ID] {
				l += " [duel-loser]"
			}
			lines = append(lines, l)
		}
		outEmit(c, payload, lines)
		return nil
	}
	evs := led.Roots()
	if limit > 0 && len(evs) > limit {
		evs = evs[len(evs)-limit:]
	}
	payload := map[string]any{"ledger": led.Slug, "events": eventsJSON(evs), "cursor": led.Head()}
	lines := addRedirect(c, led, payload)
	for _, ev := range evs {
		lines = append(lines, rootLine(led, ev))
	}
	outEmit(c, payload, lines)
	return nil
}

func runTailIn(c *Ctx, led *fold.Ledger, inID string) error {
	byID := map[string]model.Event{}
	for _, e := range led.Events {
		byID[e.ID] = e
	}
	r, ok := byID[inID]
	if !ok || r.Type != "rollup" {
		return out.Errf("unknown_event", "ledger tail shows the current roots; a rollup line's id works with --in <id>", 4,
			"'%s' is not a rollup on '%s'", inID, led.Slug)
	}
	var evs []model.Event
	for _, cid := range r.Children {
		if e, ok := byID[cid]; ok {
			evs = append(evs, e)
		}
	}
	payload := map[string]any{"ledger": led.Slug, "rollup": inID, "summary": r.Text, "events": eventsJSON(evs)}
	lines := addRedirect(c, led, payload)
	lines = append(lines, "inside ["+inID+"] \""+out.EscapeControls(r.Text)+"\":")
	for _, ev := range evs {
		lines = append(lines, "  "+rootLine(led, ev))
	}
	outEmit(c, payload, lines)
	return nil
}
