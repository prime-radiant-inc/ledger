# Ledger Issues Tracker (spec rev 16) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the issues-tracker layer (guarded conditional writes, standing signals, the `ready` envelope with frontier verdict, filtered reads, titles) in the core ledger tool per spec rev 16.

**Architecture:** All changes land in the existing Go binary. A new `internal/board` package derives per-key state (status/labels/edges/titles/staleness) from folded events; `set` gains `--expect`/`--override` with all precondition checks re-run on fresh state inside the store's CAS retry loop; a new `ready` verb computes the envelope and frontier verdict; `show` gains `--where`. Doctrine ships as a section in the existing `skills/using-ledger/SKILL.md`.

**Tech Stack:** Go (stdlib + spf13/cobra, already vendored — NO new dependencies), git-backed store in `internal/store`, test conventions from `internal/cmd/*_test.go` (`run(t, dir, args...) (stdout, stderr, exitCode)`, `mustJSON`, `initRepo`).

**Spec:** `docs/superpowers/specs/2026-08-15-ledger-issues-design.md` (revision 16) — THE binding authority; where this plan and the spec disagree, the spec wins. It extends `docs/superpowers/specs/2026-08-13-ledger-tool-design.md` (rev 13).

**Reference implementation:** branch `wip/issues-spike-v4` (commit 0251934) implements most of this untested, and its shell harness passed 40/40. CONSULT it for algorithms when stuck; NEVER copy code wholesale without writing the tests first, and NEVER merge it. Field evidence: `research/ledger-issues-spike-trial{,2,3,4}.md`.

## Global Constraints

- TDD for every behavior: failing test first, then code. Test output must be pristine.
- No new dependencies. gofmt-clean. Match surrounding code style (compact, comment-light, error strings via `out.Errf(identifier, hint, exitCode, format, args...)`).
- Usage/validation errors exit 4 with the standard JSON error envelope. New error identifiers introduced by this work: `claim_lost`, `needs_override`. Pre-existing identifiers reused: `bad_usage`, `bad_value`, `empty_body`, `unknown_field`, `unknown_key`.
- Reads batch: never per-event or per-ref git subprocesses (parent spec's 70ms-vs-48s lesson).
- Event timestamps: layout `2006-01-02T15:04:05.000`, UTC, no zone suffix. Readers parse both old (second-resolution) and new layouts.
- Performance acceptance (spec, pinned): full `ready` envelope at 5k events within 140ms (2× the parent spec's measured 70ms batched fold, same hardware class); conditional-set precondition read narrowed to the target key, never a full re-fold per retry.
- Everything issue-tracker-specific (rule-5 signals, `blocked-by` special treatment, key grammar, titles, `ready`) activates ONLY on ready-capable boards (`--terminal` declared on a field named `status`). A plain board's `--guard` buys the CAS rules alone; its `claim_lost` hints are always the generic one.
- The skill's command lines carry the absolute binary path and must execute verbatim (spec test item 17).
- Spec test plan (18 items) is the acceptance checklist; each task below names the items it covers. All 18 must be covered by the end.

## File Structure

- `internal/model/model.go` — Meta gains declaration fields; Event gains `Override`; timestamp layout + dual parser.
- `internal/model/validate.go` (new) — `ValidateDeclarations` shared by create and import.
- `internal/board/board.go` (new) — fold events → per-key derived state (status, labels, edges, title, evidence, staleness); signal computation.
- `internal/board/frontier.go` (new) — envelope list building + frontier DFS.
- `internal/store/store.go` — `Append` gains a per-attempt precondition callback.
- `internal/cmd/create.go` — new flags + validation wiring.
- `internal/cmd/port.go` — import re-validation.
- `internal/cmd/set.go` — `--expect`/`--override`, grammar/existence checks, titles/empty_body.
- `internal/cmd/where.go` (new) — `--where` clause parsing/matching shared by `show` and `ready`.
- `internal/cmd/ready.go` (new) — the `ready` verb.
- `internal/cmd/read.go` — `show` gains `--where` and titles.
- `skills/using-ledger/SKILL.md` — new pattern section + frontmatter triggers.

---

### Task 1: Sub-second timestamps with dual-layout parsing

**Files:**
- Modify: `ledger/internal/model/model.go` (NewEvent, ~line 107)
- Test: `ledger/internal/model/model_test.go`

**Interfaces:**
- Produces: `model.TSLayout = "2006-01-02T15:04:05.000"` (exported const); `model.TSLayoutLegacy = "2006-01-02T15:04:05"`; `func model.ParseTS(s string) (time.Time, error)` — tries new layout, falls back to legacy; both parsed as UTC.

- [ ] **Step 1: Write the failing tests**

```go
func TestTimestampLayoutMilliseconds(t *testing.T) {
	ev := NewEvent("set", "a", gitx.Repo{})
	if _, err := time.Parse(TSLayout, ev.TS); err != nil {
		t.Fatalf("new events must use %s: got %q (%v)", TSLayout, ev.TS, err)
	}
	if strings.HasSuffix(ev.TS, "Z") || strings.Contains(ev.TS, "+") {
		t.Fatalf("no zone suffix allowed: %q", ev.TS)
	}
}

func TestParseTSBothLayouts(t *testing.T) {
	for _, s := range []string{"2026-08-16T18:23:31.013", "2026-08-15T11:02:09"} {
		ts, err := ParseTS(s)
		if err != nil {
			t.Fatalf("ParseTS(%q): %v", s, err)
		}
		if ts.Location() != time.UTC {
			t.Fatalf("must parse as UTC")
		}
	}
	if _, err := ParseTS("not-a-time"); err == nil {
		t.Fatal("garbage must error")
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/model/` → FAIL (TSLayout undefined).
- [ ] **Step 3: Implement** — add the two consts and `ParseTS`; change `NewEvent`'s format string to `TSLayout`.

```go
const (
	TSLayout       = "2006-01-02T15:04:05.000"
	TSLayoutLegacy = "2006-01-02T15:04:05"
)

func ParseTS(s string) (time.Time, error) {
	if t, err := time.Parse(TSLayout, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(TSLayoutLegacy, s)
	return t.UTC(), err
}
```

- [ ] **Step 4: Run full package tests** — `go test ./internal/model/ ./internal/cmd/` (existing tests must not regress; any test asserting the old layout gets updated to `ParseTS`).
- [ ] **Step 5: Commit** — `feat(model): millisecond timestamps with dual-layout parser (spec test 15)`

Covers spec test item 15 (layout half; staleness math lands with Task 4).

---

### Task 2: Declarations — Meta fields, create flags, and ready-capability validation

**Files:**
- Modify: `ledger/internal/model/model.go` (Meta struct)
- Create: `ledger/internal/model/validate.go`
- Modify: `ledger/internal/cmd/create.go`
- Test: `ledger/internal/model/validate_test.go`, `ledger/internal/cmd/create_test.go` (extend existing if present, else create)

**Interfaces:**
- Produces (Meta additions, exact JSON tags):
  ```go
  MultiFields []string            `json:"multi_fields,omitempty"`
  Terminal    map[string][]string `json:"terminal,omitempty"`
  Guard       []string            `json:"guard,omitempty"`
  StaleAfter  string              `json:"stale_after,omitempty"` // Go time.ParseDuration input, verbatim
  ```
- Produces: `func model.ReadyCapable(m Meta) bool` (true iff `m.Terminal["status"]` is non-empty AND "status" is a declared enum field).
- Produces: `func model.ValidateDeclarations(m Meta) *DeclErr` where `type DeclErr struct{ Ident, Hint, Msg string }` (nil = valid). Used by create (Task 2) and import (Task 3).
- Create flags: `--multi-field <name>` (repeatable), `--terminal <field>=<v1>,<v2>` (repeatable), `--guard <field>` (repeatable), `--stale-after <duration>`.

**Validation rules (each → `bad_value` naming the fix; all from spec "The board"):**
1. `--terminal`/`--require-evidence` values must be a subset of the field's declared vocab.
2. `--guard` must name a declared field (enum or multi) — "a typo'd guard silently disabled every protection on the board".
3. `--stale-after` must parse via `time.ParseDuration`.
4. Ready-capability all-or-nothing: if `Terminal["status"]` declared → REQUIRE `--guard status`; non-terminal status vocab exactly `{open, in-progress}` both present (no third non-terminal value); a `labels` multi-field declared; `--guard blocked-by` whenever a `blocked-by` multi-field is declared.
5. A multi-field name may not collide with an enum field name.

- [ ] **Step 1: Write the failing validator tests** (table-driven; every rule above, plus the happy path with the spec's canonical create shape):

```go
func TestValidateDeclarations(t *testing.T) {
	ok := Meta{
		Fields:      map[string][]string{"status": {"open", "in-progress", "closed", "wontfix"}},
		FieldOrder:  []string{"status"},
		MultiFields: []string{"labels", "blocked-by"},
		Terminal:    map[string][]string{"status": {"closed", "wontfix"}},
		Guard:       []string{"status", "blocked-by"},
		StaleAfter:  "2h",
	}
	if e := ValidateDeclarations(ok); e != nil {
		t.Fatalf("canonical shape must validate: %+v", e)
	}
	cases := []struct{ name string; mutate func(*Meta); wantMsg string }{
		{"terminal not subset", func(m *Meta) { m.Terminal["status"] = []string{"nope"} }, "subset"},
		{"guard undeclared", func(m *Meta) { m.Guard = append(m.Guard, "priority") }, "not a declared field"},
		{"bad stale-after", func(m *Meta) { m.StaleAfter = "2 hours" }, "ParseDuration"},
		{"ready without guard status", func(m *Meta) { m.Guard = []string{"blocked-by"} }, "--guard status"},
		{"missing in-progress", func(m *Meta) { m.Fields["status"] = []string{"open", "closed", "wontfix"} }, "in-progress"},
		{"third non-terminal", func(m *Meta) { m.Fields["status"] = append(m.Fields["status"], "parked") }, "exactly"},
		{"missing labels", func(m *Meta) { m.MultiFields = []string{"blocked-by"} }, "labels"},
		{"blocked-by unguarded", func(m *Meta) { m.Guard = []string{"status"} }, "--guard blocked-by"},
	}
	for _, c := range cases {
		m := deepCopyMeta(ok) // helper in the test file
		c.mutate(&m)
		e := ValidateDeclarations(m)
		if e == nil || e.Ident != "bad_value" || !strings.Contains(e.Msg+e.Hint, c.wantMsg) {
			t.Fatalf("%s: want bad_value mentioning %q, got %+v", c.name, c.wantMsg, e)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement Meta fields + `ValidateDeclarations` + `ReadyCapable`.** Each error message names the fix (e.g. `"ready-capable boards (--terminal on status) require --guard status"`).
- [ ] **Step 4: CLI tests** — extend create's tests: the canonical spec create command succeeds and `meta.json` round-trips all new fields; each broken shape via real CLI flags exits 4 with `bad_value`; `ready`-irrelevant plain boards (`--guard` without `--terminal`) still create fine.

```go
func TestCreateReadyCapableShape(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "create", "issues", "--scope", "s",
		"--field", "status=open,in-progress,closed,wontfix",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by",
		"--require-evidence", "status=closed", "--stale-after", "2h")
	if code != 0 {
		t.Fatal(se)
	}
	_, se, code = run(t, dir, "create", "broken", "--scope", "s",
		"--field", "status=open,in-progress,closed", "--terminal", "status=closed")
	if code != 4 || !strings.Contains(se, "bad_value") || !strings.Contains(se, "--guard status") {
		t.Fatalf("all-or-nothing shape must be enforced: %s", se)
	}
}
```

- [ ] **Step 5: Run, commit** — `feat(create): board declarations + ready-capability all-or-nothing validation (spec tests 8, 14)`

Covers spec test items 8 (create side) and 14 (subset/parse halves).

---

### Task 3: Import re-validates declarations

**Files:**
- Modify: `ledger/internal/cmd/port.go` (`runImport`)
- Test: `ledger/internal/cmd/port_test.go`

**Interfaces:**
- Consumes: `model.ValidateDeclarations` (Task 2).

Import is a second meta-minting path; the spec: "import never re-derives declarations, but it RE-VALIDATES them — the same ready-capability shape checks create runs, same `bad_value` errors."

- [ ] **Step 1: Failing test** — export a valid ready-capable board, hand-edit the export file's meta line to remove `"guard"` entirely, import → must exit 4 `bad_value` mentioning `--guard status`, and no ledger is minted (`ls` does not show it). Also: unmodified export imports fine under a new slug and `meta.json` matches byte-for-byte except `slug`.
- [ ] **Step 2: Run to verify failure** (today the broken import succeeds).
- [ ] **Step 3: Implement** — call `ValidateDeclarations(meta)` after the slug re-mint, before any write; convert `DeclErr` via `out.Errf(e.Ident, e.Hint, 4, "%s", e.Msg)`.
- [ ] **Step 4: Run, commit** — `feat(import): re-validate declaration shape on import (spec test 14)`

Covers spec test item 14 (import-validation clause; the `ready`-output identity clause finishes in Task 11).

---

### Task 4: `internal/board` — derived per-key state

**Files:**
- Create: `ledger/internal/board/board.go`
- Test: `ledger/internal/board/board_test.go`

**Interfaces (produced; later tasks consume exactly these):**

```go
type FieldState struct {
	Value, ID, Author, TS, Note string
	Evidence                    []string
}
type Key struct {
	Name, Title string
	Status      *FieldState // nil = statusless
	Labels      []string    // parsed tokens, empty ok
	LabelsID    string      // latest labels event id ("" if none)
	BlockedBy   []string
	BlockedByID string
}
type Board struct {
	Meta model.Meta
	Keys map[string]*Key
}
func Build(meta model.Meta, events []model.Event) *Board
func (b *Board) IsTerminal(value string) bool            // membership in Meta.Terminal["status"]
func (k *Key) HasHuman() bool                            // "human" ∈ Labels
func (b *Board) StaleAge(k *Key, now time.Time) (stale bool, age time.Duration)
func FormatAge(d time.Duration) string                   // time.Duration.String() truncated to seconds
```

Semantics: latest event per (key, field) wins; multi-field values split on ","; `field=` (empty) clears; `Title` = `Text` of the key's FIRST event that sets `status`; `StaleAge` is true only when `Status.Value == "in-progress"`, `Meta.StaleAfter != ""`, and `now − ParseTS(Status.TS) > StaleAfter`. Staleness with a claim's own TS — never the caller's view.

- [ ] **Step 1: Failing unit tests** — hand-built event slices (no git): title from first status event survives later sets; label clear via `labels=`; staleness math across BOTH timestamp layouts (legacy-second event ages correctly — spec test 15's mixed-precision clause); sub-second `--stale-after` (e.g. `500ms` with a 1s-old claim → stale); `HasHuman` on multi-token labels (`"urgent,human"`); statusless key has `Status == nil`.
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement `Build` + helpers.** Single pass over events in order; no git access in this package (pure function of meta+events).
- [ ] **Step 4: Run, commit** — `feat(board): derived per-key state — titles, labels, edges, staleness (spec test 15 math)`

---

### Task 5: Store — per-attempt precondition callback in the CAS loop

**Files:**
- Modify: `ledger/internal/store/store.go` (`Append`)
- Test: `ledger/internal/store/store_test.go`

**Interfaces:**
- Produces: `type Precondition func(events []model.Event) error`; new method
  `func (s *Store) AppendChecked(slug string, ev model.Event, pre Precondition, mode ExpectMode) (string, error)`.
  Contract (spec rule 7, verbatim requirement): `pre` runs against a FRESH read of the chain inside EVERY CAS retry attempt — never a pre-loop snapshot; if `pre` returns an error the append aborts with that error, nothing written. Existing `Append` keeps its signature (delegates with `pre == nil`).

- [ ] **Step 1: Failing test** — precondition sees writes that land between attempts: append event A; call `AppendChecked` with a `pre` that fails while a sentinel key is absent; concurrently append the sentinel from another goroutine mid-retry (force a retry by writing between the precondition's read and the commit — use the store's existing retry-inducing test technique from `store_test.go` if present, else: first `pre` invocation triggers a competing direct `Append` via a `sync.Once`, so the CAS attempt fails, and the SECOND `pre` invocation must observe the sentinel — assert `pre` ran ≥2 times and last saw the sentinel).
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement** — thread `pre` into the retry loop after each fresh read, before constructing the commit.
- [ ] **Step 4: Run store tests, commit** — `feat(store): per-attempt precondition hook inside CAS retry (spec rule 7)`

---

### Task 6: `set` conditional writes — rules 1–4, grammar, existence, hints

**Files:**
- Modify: `ledger/internal/cmd/set.go`
- Test: `ledger/internal/cmd/expect_test.go` (new)

**Interfaces:**
- Consumes: `board.Build`, `store.AppendChecked` (Tasks 4–5).
- New flags: `--expect <event-id-prefix|none>`, `--override` (bool; used in Task 8 but the flag registers here so rule-2 parsing is complete).
- Produces (consumed by Task 8): `set`'s precondition closure structure — build `board.Build(meta, freshEvents)`, locate target key, run checks in order: rule 3/4 CAS → (Task 8 inserts signals here) → nil.

**Behavior (spec "The invariant" rules 1–4 + "The board" write-time checks; ALL exact strings pinned):**
- Rule 1: a set touching a guarded field without `--expect` → `bad_usage`, msg `"'<field>' is guarded on '<slug>': every write must carry --expect <event-id> or --expect none"`.
- Rule 2: a set touching two guarded fields → `bad_usage`; unguarded ride-alongs legal; `--expect` on a single-field write to an UNGUARDED field performs real CAS on that field (never ignored).
- Rule 3: `--expect <id>` (SHA prefix ok) — succeeds iff the written field's latest event on this key is still that event at append time; field-scoped (other fields' and notes' events never invalidate). Mismatch → `claim_lost`, exit 4, message `event <winner-id> by <winner-author> (<field>=<winner-value>) beat you to '<key>'`.
- Rule 4: `--expect none` — succeeds iff the field has no prior event on this key.
- Hint matrix, dispatched by board capability FIRST, field second:
  - ready-capable + status + mismatch, attempted value non-terminal: `re-run ledger ready and pick again`
  - ready-capable + status + mismatch, attempted value terminal: `you were reclaimed while working — leave a handoff note; never re-close blind`
  - ready-capable + blocked-by + mismatch: `re-read the key's edges and merge`
  - ready-capable + status + none-collision: `this key already exists — read it; if yours is a different issue, re-seed under a new key`
  - ready-capable + blocked-by + none-collision: `this key already has edges — read it; if yours is a different issue, re-seed under a new key`
  - EVERYTHING else (any field on a plain board, any other guarded field): `re-read '<field>' and try again`
- Multi-field writes: comma-split tokens each match `^[a-z0-9][a-z0-9-]*$` → else `bad_value` naming the malformed token; `field=` clears.
- Ready-capable boards only: key names must match the same token grammar at the key's FIRST write → `bad_value` (`"key '<key>' can't be referenced by blocked-by edges; use kebab-case"`); `blocked-by` tokens are keys, each must exist (have ≥1 event) → `unknown_key` exit 4 naming the token, no near-miss suggestions. On plain boards: none of this.

- [ ] **Step 1: Failing tests** — one test function per rule cluster, exact-string asserts on identifiers, messages, and every hint variant above. Race-free deterministic versions here (true races in Task 12). Include: notes never invalidate `--expect` (write a note between read and set); label ride-along on a status write is legal; `--expect` on a pure labels write CASes labels.
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement** — parse/validate flags, then `AppendChecked` with the precondition closure doing CAS + grammar/existence checks against the fresh board. Keep the closure small; hint selection in its own function `claimLostHint(ready bool, field, attemptedValue string, none bool, meta model.Meta) string`.
- [ ] **Step 4: Run, commit** — `feat(set): guarded conditional writes with field-scoped CAS + pinned hint matrix (spec tests 1, 2, 6, 13)`

Covers spec test items 1, 2 (mechanics; deterministic halves), 6, 13, and 4's non-race clauses.

---

### Task 7: Titles and `empty_body`

**Files:**
- Modify: `ledger/internal/cmd/set.go` (first-status-write check), `ledger/internal/cmd/read.go` (`show` rows carry `title` on ready-capable boards)
- Test: `ledger/internal/cmd/title_test.go` (new)

**Interfaces:**
- Consumes: `board.Key.Title` (Task 4).

- [ ] **Step 1: Failing tests** — on a ready-capable board: first status write with missing or whitespace-only `-m` → `empty_body` exit 4, hint `the first status write's -m becomes the key's title`; with `-m "Fix the flaky retry"` the title appears in `show` rows and survives claim/close/revision; on a PLAIN board a bare status set still works with no `-m` (parent semantics intact).
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement** — check inside the same precondition closure (first status event = no prior status event for the key); `show` gains `title` per row via `board.Build`.
- [ ] **Step 4: Run, commit** — `feat(set,show): titles derived from first status event; empty_body enforcement (spec test 3)`

Covers spec test item 3 (list-entry title assertions finish in Task 10).

---

### Task 8: Rule-5 standing signals and `--override`

**Files:**
- Modify: `ledger/internal/board/board.go` (signal computation), `ledger/internal/cmd/set.go`, `ledger/internal/model/model.go` (Event.Override)
- Test: `ledger/internal/board/signals_test.go`, `ledger/internal/cmd/override_test.go` (new)

**Interfaces:**
- Produces: `model.Event.Override string \`json:"override,omitempty"\``.
- Produces: `func (b *Board) Signals(k *Key, touchesStatus bool, author string, now time.Time) []Signal` with `type Signal struct{ Name, Facts string }` — returns standing signals in fixed order claim, human, settled.

**Semantics (spec rule 5, exact):**
- Signals exist ONLY on ready-capable boards.
- claim (only when `touchesStatus`): status is `in-progress`, non-stale, by a DIFFERENT author. Facts: `<claimant>, <age>`.
- human (every guarded write): key carries `human` label. Facts: `labeled 'human'`.
- settled (only when `touchesStatus`): status value is terminal — applies to everyone including the close's own author. Facts: `<value>, evidence: <yes|no>`.
- Any standing signal without `--override` → `needs_override` exit 4; message: `'<key>' has standing signal(s) that guard this write: <name> (<facts>)[, <name> (<facts>)...]`; hint: `--override -m "<why>"`.
- With `--override` and non-empty trimmed `-m`: write lands, event records `override: <names comma-joined>` — computed by the tool. `--override` with empty/whitespace `-m` → `bad_usage`. `--override` with NO standing signals: legal no-op, event records nothing.

- [ ] **Step 1: Failing board-level tests** — each signal in isolation and composed (`override: claim,human`); own claim not a signal; stale claim not a signal; human gates blocked-by writes but claim/settled never do; plain board returns no signals ever.
- [ ] **Step 2: Failing CLI tests** — cross-author claim → `needs_override` naming claimant+age; reclaim of a stale claim lands WITHOUT override; own close on human key blocked until `--override`; wontfix-over-evidenced-close blocked (settled), lands with `--override` and chain shows `"override":"settled"`; `--override -m "  "` → `bad_usage`.
- [ ] **Step 3: Run to verify failure; implement** — `Signals` in board; wired into set's precondition closure AFTER the CAS check (CAS mismatch wins over signals).
- [ ] **Step 4: Run, commit** — `feat(set): standing signals claim/human/settled with --override recording (spec test 7)`

Covers spec test item 7 (deterministic clauses; its harness-round clauses land in Task 12).

---

### Task 9: `--where` filtered reads on `show`

**Files:**
- Create: `ledger/internal/cmd/where.go`
- Modify: `ledger/internal/cmd/read.go` (`show`)
- Test: `ledger/internal/cmd/where_test.go` (new)

**Interfaces:**
- Produces: `type WhereClause struct{ Field, Value string; Membership bool }`; `func parseWhere(raw []string, meta model.Meta) ([]WhereClause, error)`; `func matchWhere(k *board.Key, cs []WhereClause) bool`. Task 10 reuses both.

**Semantics (spec "Filtered reads"):** `--where f=v` exact (enum fields only — `=` on a multi-field is `bad_usage`: sets have no exact-string identity); `--where f~=token` membership (multi-fields only — `~=` on enum is `bad_usage`); repeatable, AND'd; two `=` on one field `bad_usage` (unsatisfiable); undeclared field `unknown_field` listing declared fields.

- [ ] **Step 1: Failing tests** — every rule above via `show --where`, plus AND composition (two clauses both must hold) and bare `show` unchanged.
- [ ] **Step 2–4: Fail → implement → pass → commit** — `feat(show): --where filtered reads (spec test 11)`

Covers spec test item 11 (show half).

---

### Task 10: `ready` — envelope lists

**Files:**
- Create: `ledger/internal/board/frontier.go` (list building; verdict itself is Task 11), `ledger/internal/cmd/ready.go`
- Test: `ledger/internal/board/envelope_test.go`, `ledger/internal/cmd/ready_test.go`

**Interfaces:**
- Produces: `func (b *Board) Envelope(now time.Time, limit int, where []cmdWhere) Envelope` — plan note: to keep `board` free of `cmd` imports, `Envelope` takes a `func(*Key) bool` filter; `cmd/ready.go` adapts `matchWhere`.
- Envelope JSON exactly per the spec's pinned example: top level `{"ledger", "ok", "frontier", "ready", "held", "blocked", "attention", "totals"}`.

**List semantics (spec "`ready`: the board, answered", exact):**
- `ready`: status=open, not human-labeled, every `blocked-by` edge terminal. Sorted oldest-first by status TS, ties by chain position. Entry: `{key, title, note, ts, by, id}` + `unblocked_without_evidence: [<blocker>...]` when a blocker's terminal event carries no evidence refs (fires regardless of which terminal value).
- `held`: `kind:"claim"` entries (in-progress keys: by, age, id, stale bool, waiting_on when edges unresolved) and `kind:"human"` entries (human-labeled non-terminal keys; when also claimed they ADDITIONALLY carry by/age/id/stale; carry waiting_on under the same unresolved-edges condition). Label dominates status for placement, never information.
- `blocked`: open, unlabeled, unresolved edges. Carries `id`. `waiting_on` = `{key, state}`, state ∈ `terminal|open|in-progress|in-progress-stale|human|statusless`; `terminal` wins whenever the blocker's status is terminal, labeled or not; `human` = non-terminal human-owned; a human+claimed blocker renders `state:"human"` (accepted flattening).
- `attention`: `{reason:"stale-claim", key, by, age, id}` (human-labeled stale claims INCLUDED), `{reason:"statusless", key}`, `{reason:"cycle", keys:[...]}`. Titles on stale-claim entries only.
- `--limit` (default 50, per list) truncates lists; `totals` carries true counts. Sorting for held/blocked/attention: key ascending.
- `ready` on a non-ready-capable board → `bad_usage` with the create-time fix in the hint.

- [ ] **Step 1: Failing envelope unit tests** (hand-built boards): each list's membership and exact entry shape against a literal expected JSON structure; the human+claimed composite; claimed-but-blocked waiting_on; annotation fires on evidence-free wontfix blocker and not on evidenced close; limit truncation with honest totals; where-filter may empty held (no error).
- [ ] **Step 2: Failing CLI test** — seed the trial-4 board shape via CLI, assert the envelope's members and titles; `--where status=closed` legitimately empties all lists (exit 0).
- [ ] **Step 3–4: Fail → implement → pass → commit** — `feat(ready): consumer-organized envelope with ids, annotations, limits (spec tests 3, 10, 11, 12)`

Covers spec test items 10, 12, and finishes 3 and 11.

---

### Task 11: `ready` — frontier verdict

**Files:**
- Modify: `ledger/internal/board/frontier.go`
- Test: `ledger/internal/board/frontier_test.go`, extend `ledger/internal/cmd/port_test.go`

**Semantics (spec, exact):** `frontier` ∈ `work-available | all-handled | attention-needed`, computed over the FULL board regardless of `--limit`:
- `work-available`: non-empty ready OR a stale claim on a key NOT labeled human.
- else `attention-needed`: attention list non-empty (a human-labeled stale claim drives this, never work-available).
- else `all-handled`: every dependency chain terminates in a live claim or a NON-terminal human-owned key — terminal status resolves an edge regardless of label. DFS with path-stack cycle detection and a visited memo: diamonds legal and never false-flagged; true cycles → attention; statusless references → attention; open targets recursed.

- [ ] **Step 1: Failing table tests** over hand-built graphs (each row: events → expected verdict): linear chain all-live → all-handled; diamond behind one open key → work-available; true 2-cycle → attention-needed with cycle entry listing both keys; statusless reference → attention-needed; stale non-human claim only → work-available; stale HUMAN claim only → attention-needed; closed human blocker resolves dependents (ready non-empty → work-available); verdict computed full-board when `--limit 1` truncates lists.
- [ ] **Step 2–3: Fail → implement → pass.**
- [ ] **Step 4: Export/import identity test** (finishes spec test 14) — build a board, capture `ready` JSON, export, import into a FRESH store under the original slug, `ready` again: byte-identical except every `id` value; same-store import under a new slug: additionally the `ledger` name differs.
- [ ] **Step 5: Commit** — `feat(ready): frontier verdict with correct DFS (spec tests 9, 14)`

Covers spec test items 9 and 14 (completion).

---

### Task 12: Concurrency tests — the harness rounds as Go tests

**Files:**
- Create: `ledger/internal/cmd/race_test.go`
- Test: itself.

Port the three shell-harness sections plus rule-5 rounds into deterministic-enough Go tests: each round spawns two concurrent `set` invocations (run the built binary via `os/exec` from `t.TempDir()` boards, exactly like `research/scripts/expect-race-harness.sh` does — in-process cobra re-invocation shares state and is NOT a real race).

- [ ] **Step 1: Failing tests** (they fail before Tasks 6/8 merge is complete — this task runs after both):
  - 10 rounds: same-`--expect` status claims → exactly one success, one `claim_lost` (spec test 5's sibling for status; harness parity).
  - 10 rounds: first-edge `--expect none` races → one winner (spec test 5).
  - 10 rounds: label writes racing status claims → zero `claim_lost` on the status side (field-scoping, spec test 4); label edits carrying `--expect` serialize — one `claim_lost` (spec test 4's Label-edit clause).
  - 5 rounds: reclaim race on a stale claim (both `--expect` the stale claim's id) → one winner (trial-3/4 field scenario, mechanized).
- [ ] **Step 2: Verify pass; make rounds fail-loud** (any round with two successes fails the test naming the round).
- [ ] **Step 3: Commit** — `test(race): harness rounds as Go tests — CAS core, field scoping, first-edge, reclaim (spec tests 4, 5, 7-races)`

Also: delete nothing — `research/scripts/expect-race-harness.sh` stays as the historical citation.

---

### Task 13: Performance — narrowed precondition read and measured bounds

**Files:**
- Modify: `ledger/internal/store/store.go` (windowed backward read), `ledger/internal/cmd/set.go`
- Test: `ledger/internal/store/scale_test.go` (extend)

**Semantics (spec rule 8 + `ready` performance, exact):** the precondition read narrows to the target key (all its fields), resolving status, the written field, labels, and staleness from the most recent history touching them — cost scales with the key's touched-history depth, never board size; per-event subprocesses stay banned (chunked `git log -n <N>` + one `cat-file --batch` per chunk, window growing backward — e.g. 64, 256, 1024 — is the sanctioned shape). `blocked-by` existence resolves from the same walk; proving a token NONEXISTENT reaches the chain root (stated degenerate case). `unknown_key` full-scan is acceptable; measure it.

- [ ] **Step 1: Failing scale test** — seed 5,000 events (mixed keys, touch-base churn per existing `scale_test.go` conventions); assert: (a) full `ready` envelope < 140ms; (b) a conditional `set` on a recently-touched key completes without reading the full chain — instrument by counting `cat-file --batch` invocations or bytes read via the store's git wrapper, asserting the window stopped early; (c) state (not assert) the degenerate timings in test log output: long-untouched key, nonexistent blocked-by token.
- [ ] **Step 2–3: Fail → implement the windowed read → pass.** If (a) passes trivially with the existing whole-chain fold on target hardware, the windowed read is STILL required for (b) — the contract is both the number and the scaling shape.
- [ ] **Step 4: Commit** — `perf(store,ready): windowed backward precondition read; 140ms/5k ready bound measured (spec test 16)`

Covers spec test item 16.

---

### Task 14: Skill section, triggers, and doctrine-verbatim tests

**Files:**
- Modify: `skills/using-ledger/SKILL.md`
- Create: `ledger/internal/cmd/doctrine_test.go`
- Test: itself.

- [ ] **Step 1: Write the skill's new pattern section** — source it from the spec's "The write idioms" and "Board doctrine" sections VERBATIM in content: the picking loop keyed on `frontier`; Seed (edges-first + collision recovery + human reservation), Claim, Touch-base, Close (handoff on claim_lost), Reclaim (human exception), Revise-settled, Break-squat (both command variants), Edge edit, Label edit (union, `--expect`), Recovery; the idiom-wide human-override note; triage moment (attention as the sweep, `totals.attention` as a verdict-independent cue, override review, wontfix-without-evidence doctrine, dup defense with `[[key]]` as a grep convention); watch doctrine (full status vocab as `--value` terms, spurious label-collision wakes are harmless, every watch timeout is a staleness cue). Every command line carries the placeholder-free absolute-path form `~/path-to/ledger` resolved as the installed binary path convention the skill already uses — match the skill's existing binary-path style exactly. Frontmatter `description` gains the triggers `running an issue board` and `picking unblocked work`.
- [ ] **Step 2: Failing doctrine test** (spec test 17) — extract every fenced command line from the skill section (parse SKILL.md in the test), substitute the test binary path and a scratch board, execute each in the documented order, assert every one exits 0 or with its documented error identifier — no drift between doctrine and tool allowed.
- [ ] **Step 3: Watch-reality test** (spec test 18) — `watch` with the full status vocab as `--value` terms wakes on a claim; a label token colliding with a status word (`labels=open`) produces the documented spurious wake; timeout exit is clean (the staleness-notice path).
- [ ] **Step 4: Run, commit** — `feat(skill): issue-board pattern section + doctrine-verbatim and watch-reality tests (spec tests 17, 18)`

---

### Task 15: Full-suite gate and spec cross-check

**Files:** none new.

- [ ] **Step 1:** `go test ./...` from `ledger/` — everything green, output pristine.
- [ ] **Step 2:** `gofmt -l .` → empty.
- [ ] **Step 3:** Walk the spec's 18-item test plan; for each item name the test function(s) covering it in a checklist appended to the SDD ledger (not the spec). Any uncovered clause → write the missing test now.
- [ ] **Step 4:** Run `research/scripts/expect-race-harness.sh` against the NEW binary (20 rounds) — the old citation must hold against the real implementation.
- [ ] **Step 5: Commit** — `chore: rev-16 acceptance sweep — full suite, fmt, 18-item cross-check, shell harness parity`

---

## Task dependency order

1 → 2 → 3; 2 → 4 → 5 → 6 → 7 → 8; {2,4} → 9 → 10 → 11; {6,8} → 12; {6,10} → 13; all → 14 → 15. Strict sequence 1–8 first; 9–11 next; 12–14 may follow in any order; 15 last.
