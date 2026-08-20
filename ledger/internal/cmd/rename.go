package cmd

import (
	"strings"

	"ledger/internal/board"
	"ledger/internal/dag"
	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

// runRename is `set <key> --rename "<new title>"` — the rename event.
//
// Shape, deliberately: a flag on `set` rather than a new verb, carried by
// its own top-level field on an otherwise-empty set event. The rename IS
// the event: no field assignments and no evidence, because either would
// make one commit assert two different things and the fold would have to
// pick. -m is bad_usage on a bare rename (the title is --rename's own
// argument) and REQUIRED with --override, where it is the override
// justification and never a title.
//
// The gate: claim and settled do NOT gate a rename — a title is not an
// outcome — but `human` DOES, because retitling a person's reserved issue
// under them is exactly the friction that label exists to create.
// board.Signals with touchesStatus false already IS that rule: it returns
// the human signal and nothing else.
//
// --expect is never required, and is real CAS against the key's latest
// RENAME event when passed — a second, rename-scoped CAS stream alongside
// the field-scoped one.
func runRename(c *Ctx, key string, assignments []string, o writeOpts, rename, expect string, expectSet, override bool) error {
	switch {
	case len(assignments) > 0:
		return out.Errf("bad_usage", "split it: one `set` for the fields, one `set --rename` for the title", 4,
			"--rename is the whole event; it takes no field=value assignments (got: %s)", strings.Join(assignments, " "))
	case len(o.evidence) > 0:
		return out.Errf("bad_usage", "drop --evidence — a retitle is not an outcome", 4,
			"--rename takes no evidence")
	case override && strings.TrimSpace(o.m) == "":
		return out.Errf("bad_usage", `pass -m "<why>" alongside --override`, 4,
			"--override requires a non-empty -m explaining why")
	case o.m != "" && !override:
		return out.Errf("bad_usage", "drop -m — --rename carries the new title itself", 4,
			"--rename and -m can't ride the same event: the rename IS the event")
	}
	if err := checkExpectSyntax(expect, expectSet); err != nil {
		return err
	}
	title := strings.TrimSpace(rename)
	if title == "" {
		return out.Errf("empty_body", `pass the new title: --rename "<new title>"`, 4,
			"--rename needs a non-empty title")
	}
	if strings.ContainsAny(title, "\n\r") {
		return out.Errf("bad_value", "a title is exactly one line; put the detail in a note", 4,
			"--rename takes a single line")
	}

	led, err := c.PickLedger(o.ledger)
	if err != nil {
		return err
	}
	if led.State != "open" {
		hint := "closed ledgers accept only notes and rollups"
		if led.SupersededBy != "" {
			hint = "this ledger is superseded by '" + led.SupersededBy + "' — write there"
		}
		return out.Errf("closed", hint, 4, "'%s' is %s and refuses renames", led.Slug, led.State)
	}
	// Titles exist only on ready-capable boards, so a rename has nothing to
	// name on a plain board — and silently accepting one would mint an event
	// no read on that board could ever surface.
	if !model.ReadyCapable(led.Meta) {
		return out.Errf("bad_usage",
			"titles (and so renames) exist only on ready-capable boards — declare --terminal/--guard status at create", 4,
			"'%s' is not ready-capable: it has no titles to rename", led.Slug)
	}

	author := model.ResolveAuthor(o.as)
	// Idempotency is scoped SYMMETRICALLY: a rename dedupes only against an
	// earlier RENAME sharing its (author, key, idem) — never against a field
	// write, which asserts something else entirely (runSet's own scan is the
	// mirror image).
	if o.idemKey != "" {
		for _, ev := range led.Events {
			if ev.Type == "set" && ev.Rename != "" && ev.IdempotencyKey == o.idemKey &&
				ev.Author == author && ev.Key == key {
				payload := map[string]any{"id": ev.ID, "ledger": led.Slug, "deduped": true, "by": ev.Author}
				if due, ok := dueAfter(c, led.Slug); ok {
					payload["rollup_due"] = due
				}
				outEmit(c, payload, []string{"deduped against " + ev.ID})
				return nil
			}
		}
	}

	ev := model.NewEvent("set", author, c.Store.Repo)
	ev.Key, ev.Rename, ev.IdempotencyKey = key, title, o.idemKey
	if override {
		// The message rides the event as the override justification, which is
		// where every other override's -m lives. It is never a title: the
		// fold reads titles from Rename, never from Text.
		ev.Text = o.m
	}
	var prior string
	id, err := c.Store.AppendChecked(led.Slug, &ev,
		renamePrecondition(key, expect, expectSet, led.Meta, author, override,
			&ev.Override, &prior, &ev.ContestedResolved), store.ExpectPresent)
	if err != nil {
		return mapStoreErr(err, led.Slug)
	}
	payload := map[string]any{"id": id, "ledger": led.Slug, "key": key, "rename": title, "prior_title": prior}
	line := "[" + id + "] " + led.Slug + ": " + out.EscapeControls(key) +
		` renamed "` + out.EscapeControls(prior) + `" -> "` + out.EscapeControls(title) + `"`
	// The rename stream is a contested stream like any guarded field's, so a
	// rename that collapses an antichain says so — in the response and on the
	// event, the same labeling a guarded set gets.
	if len(ev.ContestedResolved) > 0 {
		payload["contested_resolved"] = ev.ContestedResolved
		line += "  " + out.ContestedResolvedMarker(ev.ContestedResolved)
	}
	// And the override the tool computed, for the same reason (runSet's echo
	// is the mirror image).
	if ev.Override != "" {
		payload["override"] = ev.Override
		line += "  " + out.OverrideMarker(ev.Override)
	}
	if due, ok := dueAfter(c, led.Slug); ok {
		payload["rollup_due"] = due
	}
	outEmit(c, payload, []string{line})
	return nil
}

// renamePrecondition runs against a fresh whole-chain read on every CAS
// attempt (the same contract setPrecondition documents): the rename-scoped
// CAS, then the one structural rule — a rename needs a locally existing
// TITLED key, so a rename against a key no seed or earlier rename ever
// titled is unknown_key rather than a title minted out of nothing. The gate
// costs the key's labels, which is the same whole-chain read class every
// other rule-5 check pays; renames are rare.
//
// priorOut, overrideOut and resolvedOut are reset and recomputed on every
// attempt: a losing attempt's value must never survive into the winning
// attempt's commit or response (setPrecondition spells out why at length).
func renamePrecondition(key, expect string, expectSet bool, meta model.Meta, author string,
	override bool, overrideOut, priorOut *string, resolvedOut *[]string) store.Precondition {
	return func(events []model.Event, d dag.Result) error {
		*priorOut, *overrideOut = "", ""
		// The rename stream contests as the pseudo-field "title": this write
		// descends from every current head of that stream (the ref tip it is
		// parented on does), so it collapses the antichain, and the heads it
		// beats are recorded on it, greppable forever — computed off THIS
		// attempt's fresh read, exactly as a guarded set's are.
		*resolvedOut = board.ResolvedHeads(events, d, key, board.TitleField)
		if expectSet {
			if err := checkRenameCAS(latestRenameEvent(events, key), key, expect); err != nil {
				return err
			}
		}
		b := board.Build(meta, events)
		k := b.Keys[key]
		if k == nil || k.Title == "" {
			return out.Errf("unknown_key",
				`seed it first: ledger set `+key+` status=open --expect none -m "<title>"`, 4,
				"'%s' has no title to rename — a key's title starts at its first status write", key)
		}
		*priorOut = k.Title
		// Rule 5, scoped: touchesStatus false, so claim and settled never fire
		// here and `human` always does. No standing signal makes --override a
		// legal no-op, exactly as on every other write verb — `human` is an
		// unguarded labels token any writer or sync merge can clear, so
		// refusing here would be a mid-CAS-loop TOCTOU.
		signals := b.Signals(k, false, author, model.Now())
		return applyOverrideGate(key, signals, override, overrideOut)
	}
}

// checkRenameCAS is rules 3-4 scoped to the rename stream: `--expect none`
// succeeds only on a never-renamed key, `--expect <id>` only while that id
// is still the key's latest rename.
func checkRenameCAS(latest *model.Event, key, expect string) error {
	if expect == "none" {
		if latest == nil {
			return nil
		}
		return renameClaimLost(latest, key)
	}
	if latest == nil {
		return out.Errf("claim_lost", "a first rename takes --expect none (or no --expect at all)", 4,
			"'%s' has never been renamed — nothing matches --expect %s", key, expect)
	}
	if strings.HasPrefix(latest.ID, expect) {
		return nil
	}
	return renameClaimLost(latest, key)
}

// renameClaimLost is the rename stream's rule-3 contract row: the pinned
// claim_lost naming the rename that beat this one, and the hint that sends
// the caller to read the current title first.
func renameClaimLost(latest *model.Event, key string) error {
	return out.Errf("claim_lost", "read the current title first — `ledger status "+key+"` shows it", 4,
		"event %s by %s already renamed '%s' to %q", latest.ID, latest.Author, key, latest.Rename)
}

// latestRenameEvent returns the most recent rename event on key, or nil.
// events is oldest-first (store.Events' order), so the last match wins —
// the same fold order Build's title derivation uses.
func latestRenameEvent(events []model.Event, key string) *model.Event {
	var latest *model.Event
	for i := range events {
		if events[i].Type != "set" || events[i].Key != key || events[i].Rename == "" {
			continue
		}
		e := events[i]
		latest = &e
	}
	return latest
}
