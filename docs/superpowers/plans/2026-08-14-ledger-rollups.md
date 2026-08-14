# Ledger Roll-up Records Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement spec rev 12's roll-up records — first-class `rollup` events that encapsulate explicit child-event lists, a two-step `rollup` verb, and `tail` as the curated roots view — in the Go `ledger` CLI.

**Architecture:** A `rollup` event carries `children` (event SHAs) and a one-line summary in `text`. The fold gains a parent map (single parent, all-or-nothing duel resolution, first-in-total-order) plus `Roots()`/`Due()`; the state fold ignores rollups exactly as it ignores sync sentinels. `tail` renders roots by default in causal order (earliest transitive base event), with `--raw` and `--in <rollup-id>`. Write envelopes carry an advisory `rollup_due` count.

**Tech Stack:** Go 1.26, cobra (only dependency), shells out to system git ≥ 2.40. Module `ledger` at `ledger/`.

**Spec:** `docs/superpowers/specs/2026-08-13-ledger-tool-design.md` — rev 12, sections "Roll-up records (rev 12)", "Event schema", "Reads and coordination", tests 39–44. The spec is the binding authority. Spike reference (behavior, not code style): session scratchpad `spike4/ledger.py`. Eval evidence: `research/rollup-usability-test.md`.

## Global Constraints

- Only dependency is cobra; storage shells out to system git (≥ 2.40 floor already enforced in `internal/gitx`).
- Reads batch: folds go through the existing one-`log`-plus-one-`cat-file --batch` pipeline; never add per-event or per-ref subprocesses. `Roots()`/`Due()` are pure in-memory fold logic.
- Exit codes: 0 ok / 1 internal / 2 watch timeout / 4 contract errors. New error identifiers: `unknown_event`, `child_taken`; reuse `empty_body`, `bad_value`.
- Every error is `out.Errf(code, hint, exitCode, format, args...)` with a copy-pasteable hint.
- JSON is the default when stdout is not a TTY; TTY gets aligned text lines. All event-sourced strings rendered to TTY sinks go through `out.EscapeControls`.
- A never-rolled ledger's default `tail` output must be **byte-identical** to current behavior (spec: "byte-identical to rev 11") — no new fields in `tail`'s default payload.
- State fold (`show`/`status`) must be byte-identical before and after rollups land (spec test 43).
- `ledger/docs/quickstart.md` has a test-enforced 90-line budget, and every fenced `ledger ...` line in it executes verbatim in `internal/docs/docs_test.go` with `# expect:` annotations (verbatim argv — no variable substitution exists, so doc examples must be deterministic).
- Closed ledgers accept `rollup` (spec Lifecycle: closed refuses `set`/`vocab add`, accepts `note`, `rollup`, `superseded_by`).
- Match surrounding code style; the cmd package registers verbs via `register(newXCmd)` in `init()`.

---

### Task 1: Fold — children field, parent map, roots, due

**Files:**
- Modify: `ledger/internal/model/model.go` (Event struct, ~line 27)
- Modify: `ledger/internal/fold/fold.go`
- Test: `ledger/internal/fold/fold_test.go` (extend existing)

**Interfaces:**
- Consumes: existing `model.Event`, `fold.Fold(slug string, evs []model.Event, meta model.Meta) *Ledger`.
- Produces: `model.Event.Children []string` (JSON `children,omitempty`); `fold.Ledger.Parent map[string]string` (child id → winning rollup id); `fold.Ledger.Losers map[string]bool` (wholly-inert duel-losing rollup ids); `(*fold.Ledger).Roots() []model.Event` (causal order); `(*fold.Ledger).Due() int`. Tasks 2–5 rely on these exact names.

- [ ] **Step 1: Write the failing tests**

Append to `ledger/internal/fold/fold_test.go` (match its existing event-builder style — read the file first; it constructs `model.Event` literals and calls `fold.Fold`):

```go
func rollupEv(id, author string, children ...string) model.Event {
	return model.Event{TS: "2026-08-14T00:00:0" + id[len(id)-1:], Type: "rollup",
		Author: author, Text: "summary " + id, Children: children, ID: id}
}

func TestRollupFoldParentAndState(t *testing.T) {
	evs := []model.Event{
		{TS: "2026-08-14T00:00:01", Type: "create", Author: "a", ID: "e1"},
		{TS: "2026-08-14T00:00:02", Type: "set", Key: "k", Fields: map[string]string{"status": "done"}, Author: "a", ID: "e2"},
		rollupEv("r1", "curator", "e1", "e2"),
	}
	l := fold.Fold("s", evs, model.Meta{Fields: map[string][]string{"status": {"open", "done"}}})
	if l.Parent["e1"] != "r1" || l.Parent["e2"] != "r1" {
		t.Fatalf("parent map wrong: %v", l.Parent)
	}
	// state fold ignores rollups: spine still has k=done, state still open
	if l.Spine["k"]["status"].ID != "e2" || l.State != "open" {
		t.Fatalf("rollup leaked into state fold")
	}
}

func TestRollupDuelAllOrNothing(t *testing.T) {
	evs := []model.Event{
		{TS: "2026-08-14T00:00:01", Type: "create", Author: "a", ID: "e1"},
		{TS: "2026-08-14T00:00:02", Type: "note", Kind: "note", Text: "x", Author: "a", ID: "e2"},
		{TS: "2026-08-14T00:00:03", Type: "note", Kind: "note", Text: "y", Author: "a", ID: "e3"},
		rollupEv("r1", "a", "e2"),
		rollupEv("r2", "b", "e2", "e3"), // e2 already taken → r2 loses WHOLLY: e3 stays loose
	}
	l := fold.Fold("s", evs, model.Meta{})
	if !l.Losers["r2"] {
		t.Fatalf("r2 must be a duel loser")
	}
	if _, taken := l.Parent["e3"]; taken {
		t.Fatalf("all-or-nothing: losing rollup must not claim e3")
	}
	roots := l.Roots()
	for _, r := range roots {
		if r.ID == "r2" {
			t.Fatalf("loser rollup must not be a root")
		}
	}
	if l.Due() != 3 { // e1, e3 loose + ... e2 is inside r1; roots: e1, r1, e3 → non-rollup roots = e1, e3 = 2
		// (deliberate arithmetic check — fix the expected value to match the rule:
		// Due counts non-rollup roots; here that is e1 and e3)
		t.Fatalf("due = %d", l.Due())
	}
}

func TestRootsCausalOrder(t *testing.T) {
	// live event e4 lands BEFORE the rollup commit r1 that encapsulates the
	// earlier e2,e3 thread; causal order must put r1 (earliest base e2) first.
	evs := []model.Event{
		{TS: "2026-08-14T00:00:01", Type: "create", Author: "a", ID: "e1"},
		{TS: "2026-08-14T00:00:02", Type: "note", Kind: "note", Text: "t", Author: "a", ID: "e2"},
		{TS: "2026-08-14T00:00:03", Type: "note", Kind: "note", Text: "t", Author: "a", ID: "e3"},
		{TS: "2026-08-14T00:00:04", Type: "set", Key: "live", Fields: map[string]string{"status": "open"}, Author: "a", ID: "e4"},
		rollupEv("r1", "curator", "e2", "e3"),
	}
	l := fold.Fold("s", evs, model.Meta{Fields: map[string][]string{"status": {"open"}}})
	roots := l.Roots()
	ids := []string{}
	for _, r := range roots {
		ids = append(ids, r.ID)
	}
	want := []string{"e1", "r1", "e4"}
	if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("causal order wrong: got %v want %v", ids, want)
	}
}

func TestRecursiveRollupAndCoverage(t *testing.T) {
	// Coverage invariant (spec test 41): every non-sentinel event is either a
	// root or transitively inside exactly one visible root. Randomly roll a
	// 500-event chain and check.
	evs := []model.Event{{TS: "2026-08-14T00:00:00", Type: "create", Author: "a", ID: "e0"}}
	for i := 1; i < 500; i++ {
		evs = append(evs, model.Event{TS: "2026-08-14T00:00:01", Type: "note", Kind: "note",
			Text: "n", Author: "a", ID: fmt.Sprintf("e%d", i)})
	}
	rng := rand.New(rand.NewSource(41))
	loose := []string{}
	for _, e := range evs {
		loose = append(loose, e.ID)
	}
	for r := 0; r < 60; r++ { // 60 random rollups, each eating 2-9 loose records
		n := 2 + rng.Intn(8)
		if len(loose) < n+1 {
			break
		}
		var kids []string
		for j := 0; j < n; j++ {
			k := rng.Intn(len(loose))
			kids = append(kids, loose[k])
			loose = append(loose[:k], loose[k+1:]...)
		}
		id := fmt.Sprintf("r%d", r)
		evs = append(evs, rollupEv(id, "c", kids...))
		loose = append(loose, id)
	}
	l := fold.Fold("s", evs, model.Meta{})
	// walk down from every root; every event must be reached exactly once
	seen := map[string]int{}
	byID := map[string]model.Event{}
	for _, e := range l.Events {
		byID[e.ID] = e
	}
	var walk func(id string)
	walk = func(id string) {
		seen[id]++
		if e := byID[id]; e.Type == "rollup" {
			for _, c := range e.Children {
				walk(c)
			}
		}
	}
	for _, r := range l.Roots() {
		walk(r.ID)
	}
	for _, e := range l.Events {
		if seen[e.ID] != 1 {
			t.Fatalf("event %s reached %d times (coverage invariant)", e.ID, seen[e.ID])
		}
	}
}
```

Add imports `fmt`, `math/rand` to the test file as needed. Note the deliberate arithmetic comment in `TestRollupDuelAllOrNothing`: compute the correct `Due()` expectation from the rule (non-rollup roots; here `e1` and `e3` → 2) and write the literal `2`, not `3`.

- [ ] **Step 2: Run tests, verify failure**

Run: `cd ledger && go test ./internal/fold/ -run 'Rollup|Roots|Recursive' -v`
Expected: compile error — `model.Event` has no field `Children`, `l.Parent` undefined.

- [ ] **Step 3: Implement**

`ledger/internal/model/model.go` — add to the Event struct, after `Evidence`:

```go
	Children          []string          `json:"children,omitempty"`
```

`ledger/internal/fold/fold.go` — add `"sort"` to imports; add fields to `Ledger`:

```go
	// Parent maps a child event id to the rollup id that encapsulates it —
	// winners only. Losers holds rollup ids that lost a duel: a rollup with
	// ANY already-taken child loses wholly (its summary line is a claim
	// about its entire child set), stays in the raw chain, and is inert.
	Parent map[string]string
	Losers map[string]bool
```

Initialize both in `Fold`'s constructor literal (`Parent: map[string]string{}, Losers: map[string]bool{}`), and add a case to the event switch:

```go
		case "rollup":
			taken := false
			for _, c := range ev.Children {
				if _, ok := l.Parent[c]; ok {
					taken = true
					break
				}
			}
			if taken {
				l.Losers[ev.ID] = true
			} else {
				for _, c := range ev.Children {
					l.Parent[c] = ev.ID
				}
			}
```

Add after `Notes()`:

```go
// Roots returns every record not encapsulated by anything — the curated
// history — in causal order: each root sorts by the chain position of its
// earliest transitive base event, not the rollup commit's own position,
// which would sort curated threads after the live work they causally
// precede (spec rev 12; the rollup eval's one real defect).
func (l *Ledger) Roots() []model.Event {
	pos := map[string]int{}
	byID := map[string]model.Event{}
	for i, e := range l.Events {
		pos[e.ID] = i
		byID[e.ID] = e
	}
	memo := map[string]int{}
	var earliest func(id string) int
	earliest = func(id string) int {
		if v, ok := memo[id]; ok {
			return v
		}
		e, ok := byID[id]
		if !ok {
			return int(^uint(0) >> 1) // unknown child id: sort last, never crash
		}
		min := pos[id]
		if e.Type == "rollup" && len(e.Children) > 0 {
			min = int(^uint(0) >> 1)
			for _, c := range e.Children {
				if p := earliest(c); p < min {
					min = p
				}
			}
		}
		memo[id] = min
		return min
	}
	var roots []model.Event
	for _, e := range l.Events {
		if e.Type == "sync" || l.Losers[e.ID] {
			continue
		}
		if _, taken := l.Parent[e.ID]; taken {
			continue
		}
		roots = append(roots, e)
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return earliest(roots[i].ID) < earliest(roots[j].ID)
	})
	return roots
}

// Due is the advisory curation debt: unencapsulated non-rollup events.
// A root rollup is finished work, not debt (spec rev 12).
func (l *Ledger) Due() int {
	n := 0
	for _, e := range l.Roots() {
		if e.Type != "rollup" {
			n++
		}
	}
	return n
}
```

(Recursion terminates because a rollup's children are SHAs of commits that existed when it was written — they always precede it in the chain, and the spec's write-time validation plus git's content addressing make forward or cyclic references impossible.)

- [ ] **Step 4: Run tests, verify pass**

Run: `cd ledger && go test ./internal/fold/ -v`
Expected: all PASS, including pre-existing fold tests.

- [ ] **Step 5: Commit**

```bash
git add internal/model/model.go internal/fold/fold.go internal/fold/fold_test.go
git commit -m "fold: rollup events — parent map, all-or-nothing duels, causal roots, due count"
```

---

### Task 2: The `rollup` verb

**Files:**
- Create: `ledger/internal/cmd/rollup.go`
- Test: `ledger/internal/cmd/rollup_test.go` (new)

**Interfaces:**
- Consumes: Task 1's `led.Roots()`, `led.Due()`, `led.Parent`; existing `Ctx`, `c.PickLedger(ledgerFlag)`, `c.Load(slug)`, `model.NewEvent(typ, author, repo)`, `model.ResolveAuthor`, `store.Store.Append` (find the exact append call shape in `internal/cmd/set.go` and mirror it — same CAS, same identity plumbing, same `GCAuto` call if set.go does one), `out.Errf`, `outEmit`, `out.EscapeControls`.
- Produces: verb `ledger rollup [EVENT_ID ...] [-m TEXT] [--as ROLE] [--ledger SLUG]`. Bare → roots view + grammar, writes nothing, exit 0. With ids → appends a rollup event; envelope `{"ok":true,"id":<sha>,"ledger":<slug>,"children":<n>,"rollup_due":<n>}`. Errors: `unknown_event`, `child_taken`, `empty_body`, `bad_value` (all exit 4). Task 6's doc examples depend on bare `rollup` exiting 0 and `rollup deadbeef00 -m "x"` being `unknown_event`.

- [ ] **Step 1: Write the failing tests**

`ledger/internal/cmd/rollup_test.go` — read `internal/cmd/root_test.go` first for the harness (`setup(t)` builds a repo with ledger "demo"; `run(t, dir, args...)` returns stdout/stderr/exit; `mustJSON` parses). Then:

```go
package cmd

import (
	"strings"
	"testing"
)

// writeEv appends a set event and returns its id.
func writeEv(t *testing.T, dir, key string) string {
	t.Helper()
	so, se, code := run(t, dir, "set", key, "status=open", "-m", "work", "--as", "w")
	if code != 0 {
		t.Fatalf("set failed: %s %s", so, se)
	}
	return mustJSON(t, so)["id"].(string)
}

func TestRollupBareShowsRootsAndGrammar(t *testing.T) {
	dir := setup(t)
	writeEv(t, dir, "k1")
	so, se, code := run(t, dir, "rollup")
	if code != 0 {
		t.Fatalf("bare rollup must exit 0: %s", se)
	}
	doc := mustJSON(t, so)
	if doc["rollup_due"] == nil || doc["roots"] == nil {
		t.Fatalf("bare rollup payload missing roots/rollup_due: %v", doc)
	}
	if !strings.Contains(so, "rollup") { // grammar/instructions present in payload
		t.Fatalf("no instructions: %s", so)
	}
}

func TestRollupSubmitAndErrors(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")

	// happy path
	so, se, code := run(t, dir, "rollup", a, b, "-m", "thread done: k1+k2 landed", "--as", "curator")
	if code != 0 {
		t.Fatalf("submit failed: %s", se)
	}
	doc := mustJSON(t, so)
	rid := doc["id"].(string)
	if doc["children"].(float64) != 2 {
		t.Fatalf("children count: %v", doc)
	}
	if _, ok := doc["rollup_due"]; !ok {
		t.Fatalf("no rollup_due in envelope: %v", doc)
	}

	// child_taken names the owning rollup and hints inclusion
	_, se, code = run(t, dir, "rollup", a, "-m", "dup", "--as", "curator")
	if code != 4 || !strings.Contains(se, "child_taken") || !strings.Contains(se, rid) {
		t.Fatalf("child_taken must name owner %s: %d %s", rid, code, se)
	}

	// unknown_event
	_, se, code = run(t, dir, "rollup", "deadbeef00", "-m", "x", "--as", "curator")
	if code != 4 || !strings.Contains(se, "unknown_event") {
		t.Fatalf("unknown_event: %d %s", code, se)
	}

	// empty and multi-line summaries refused
	_, se, code = run(t, dir, "rollup", rid, "--as", "curator")
	if code != 4 || !strings.Contains(se, "empty_body") {
		t.Fatalf("empty_body: %d %s", code, se)
	}
	_, se, code = run(t, dir, "rollup", rid, "-m", "two\nlines", "--as", "curator")
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("multi-line must be bad_value: %d %s", code, se)
	}

	// recursion: rolling the rollup works (correction idiom)
	so, se, code = run(t, dir, "rollup", rid, "-m", "corrected line", "--as", "resumer")
	if code != 0 {
		t.Fatalf("recursive rollup failed: %s", se)
	}
}

func TestRollupOnClosedLedgerAllowed(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	if _, se, code := run(t, dir, "close", "demo", "--as-state", "shipped", "--as", "w"); code != 0 {
		t.Fatalf("close: %s", se)
	}
	_, se, code := run(t, dir, "rollup", a, "-m", "post-close curation", "--as", "curator", "--ledger", "demo")
	if code != 0 {
		t.Fatalf("rollup must be legal on a closed ledger (note precedent): %s", se)
	}
}
```

Adjust `setup(t)`/`run`/`mustJSON` usage to the file's actual helper names after reading `root_test.go` — the semantics above are the requirement; if `setup` seeds different field vocab, use a declared value in `writeEv`.

- [ ] **Step 2: Run tests, verify failure**

Run: `cd ledger && go test ./internal/cmd/ -run TestRollup -v`
Expected: FAIL — `unknown command "rollup"`.

- [ ] **Step 3: Implement `ledger/internal/cmd/rollup.go`**

```go
package cmd

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
)

func init() { register(newRollupCmd) }

func newRollupCmd(c *Ctx) *cobra.Command {
	var msg, as, ledgerFlag string
	cmd := &cobra.Command{Use: "rollup [EVENT_ID ...]",
		Short: "encapsulate a finished thread into one summary line (bare: show roots + instructions)",
		RunE: func(_ *cobra.Command, args []string) error {
			return runRollup(c, args, msg, as, ledgerFlag)
		}}
	cmd.Flags().StringVarP(&msg, "message", "m", "", "the one-line summary")
	cmd.Flags().StringVar(&as, "as", "", "author identity for this write")
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	return cmd
}

func runRollup(c *Ctx, ids []string, msg, as, ledgerFlag string) error {
	led, err := c.PickLedger(ledgerFlag)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return rollupRootsView(c, led)
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return out.Errf("empty_body", `add -m "what this thread was and how it ended"`, 4,
			"a rollup needs its one-line summary")
	}
	if strings.ContainsAny(msg, "\n\r") {
		return out.Errf("bad_value", "put longer prose in a note, then cite that note's id in the summary line", 4,
			"a rollup summary is exactly one line")
	}
	byID := map[string]model.Event{}
	for _, e := range led.Events {
		if e.Type != "sync" {
			byID[e.ID] = e
		}
	}
	seen := map[string]bool{}
	var children []string
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := byID[id]; !ok {
			return out.Errf("unknown_event", "ledger tail --raw -n 30  lists recent events with their ids", 4,
				"'%s' is not an event on '%s'", id, led.Slug)
		}
		if owner, taken := led.Parent[id]; taken {
			return out.Errf("child_taken",
				"records have one parent — include that rollup instead: ledger rollup "+owner+" ... -m \"...\"", 4,
				"'%s' is already inside rollup %s", id, owner)
		}
		children = append(children, id)
	}
	ev := model.NewEvent("rollup", model.ResolveAuthor(as), c.Store.Repo)
	ev.Children = children
	ev.Text = msg
	id, err := c.Store.Append(led.Slug, ev)
	if err != nil {
		return err
	}
	due := -1
	if after, err := c.Load(led.Slug); err == nil {
		due = after.Due()
	}
	outEmit(c, map[string]any{"id": id, "ledger": led.Slug, "children": len(children), "rollup_due": due},
		[]string{"[" + id + "] " + led.Slug + ": " + strconv.Itoa(len(children)) +
			" records rolled into one line (" + strconv.Itoa(due) + " still unrolled)"})
	return nil
}

func rollupRootsView(c *Ctx, led *fold.Ledger) error {
	roots := led.Roots()
	rows := make([]map[string]any, 0, len(roots))
	lines := []string{"# " + led.Slug + " — current roots (" + strconv.Itoa(led.Due()) + " records not yet inside any rollup)"}
	for _, e := range roots {
		line := rootLine(led, e)
		rows = append(rows, map[string]any{"id": e.ID, "type": e.Type, "line": line})
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "",
		"Roll a FINISHED thread (a resolved hypothesis, a done task arc, a settled",
		"decision trail) into one line:",
		`  ledger rollup <id> <id> ... -m "one line" --as <role>`,
		"The line is a signpost for a cold reader: say what happened and how it",
		"ended, and carry concrete anchors (key names, evidence kinds, counts) into",
		"it, keeping each anchor next to the claim it actually backs. Summarize —",
		"never invent, and never restate another agent's evidenced claim as fact;",
		"it stays their testimony. A bridge note that closes one thread and opens",
		"another belongs to the thread it opens. Children may themselves be rollups",
		"— that's also the fix for a bad summary: roll IT up under a better line.",
		"Recent live work stays unrolled.")
	outEmit(c, map[string]any{"ledger": led.Slug, "rollup_due": led.Due(), "roots": rows}, lines)
	return nil
}
```

Notes for the implementer: (a) check how `set.go` resolves the author — if it uses a helper other than `model.ResolveAuthor(as)` (e.g. reads `--as` via a shared flag helper or passes through `IdentityArgs`), mirror set.go exactly, including any `GCAuto` call after append; (b) `rootLine` is defined in Task 3 — for THIS task, add a minimal version at the bottom of `rollup.go` and Task 3 will move/extend it:

```go
// rootLine renders one root for the curated views. Extended by tail (Task 3).
func rootLine(led *fold.Ledger, e model.Event) string {
	if e.Type == "rollup" {
		return "[" + e.ID + "] rollup by " + out.EscapeControls(e.Author) +
			" (" + strconv.Itoa(len(e.Children)) + " records — --in " + e.ID + " opens it): " +
			out.EscapeControls(e.Text)
	}
	return eventLine(e)
}
```

(c) the closed-ledger test passes because `PickLedger`/`Load` don't gate on state — do NOT add a state check; closed accepts rollup by spec; (d) if `register`'s signature differs from `func(c *Ctx) *cobra.Command`, mirror the neighbors.

- [ ] **Step 4: Run tests, verify pass**

Run: `cd ledger && go test ./internal/cmd/ -run TestRollup -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/rollup.go internal/cmd/rollup_test.go
git commit -m "cmd: rollup verb — bare roots view, validated submit, closed-ledger curation"
```

---

### Task 3: `tail` — roots default, `--raw`, `--in`, duel-loser flag

**Files:**
- Modify: `ledger/internal/cmd/read.go` (`newTailCmd`/`runTail`, ~lines 480–506; `eventLine` area ~line 179)
- Modify: `ledger/internal/cmd/rollup.go` (only if you relocate `rootLine`; keeping it in rollup.go is fine)
- Test: `ledger/internal/cmd/rollup_test.go` (extend) and `ledger/internal/cmd/read_test.go`-style placement matching existing tail tests (find them with `grep -rn "runTail\|\"tail\"" internal/cmd/*_test.go` and put the byte-identical test beside them)

**Interfaces:**
- Consumes: Task 1's `led.Roots()`, `led.Losers`; Task 2's `rootLine(led, e)`; existing `eventsJSON`, `eventJSON`, `eventLine`, `nonSyncEvents`, `addRedirect`, `outEmit`.
- Produces: `ledger tail [-n N] [--raw] [--in ROLLUP_ID] [--ledger SLUG]`. Default payload shape UNCHANGED (`{"ledger","events","cursor"}` + ok) with `events` = roots (JSON per event, rollups carry their `children`). `--raw` payload adds `"raw":true` and full non-sync chain, with `"duel_loser":true` on loser rollup events and ` [duel-loser]` appended to their TTY lines. `--in` payload `{"ledger","rollup","summary","events"}`. `--raw` with `--in` → `bad_value`.

- [ ] **Step 1: Write the failing tests**

Extend `rollup_test.go`:

```go
func TestTailRootsAndDrill(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")

	// pre-rollup: default tail is byte-identical to the raw view minus the raw flag
	before, _, _ := run(t, dir, "tail", "-n", "50")
	beforeDoc := mustJSON(t, before)
	if _, has := beforeDoc["rollup_due"]; has {
		t.Fatalf("default tail must not grow new fields (byte-identical contract)")
	}

	so, se, code := run(t, dir, "rollup", a, b, "-m", "k-thread finished", "--as", "curator")
	if code != 0 {
		t.Fatalf("%s", se)
	}
	rid := mustJSON(t, so)["id"].(string)

	// default tail now collapses: no event with id a or b; one rollup root
	so, _, _ = run(t, dir, "tail", "-n", "50")
	if strings.Contains(so, `"id": "`+a+`"`) || strings.Contains(so, `"id": "`+b+`"`) {
		t.Fatalf("encapsulated events leaked into roots view: %s", so)
	}
	if !strings.Contains(so, rid) {
		t.Fatalf("rollup root missing: %s", so)
	}

	// --raw still shows everything
	so, _, _ = run(t, dir, "tail", "--raw", "-n", "50")
	if !strings.Contains(so, `"`+a+`"`) || !strings.Contains(so, `"raw": true`) {
		t.Fatalf("raw view must show the full chain: %s", so)
	}

	// --in opens the rollup
	so, se, code = run(t, dir, "tail", "--in", rid)
	if code != 0 {
		t.Fatalf("--in failed: %s", se)
	}
	doc := mustJSON(t, so)
	if doc["summary"] != "k-thread finished" || len(doc["events"].([]any)) != 2 {
		t.Fatalf("--in wrong: %v", doc)
	}

	// --in on a non-rollup id is unknown_event; --raw + --in is bad_value
	_, se, code = run(t, dir, "tail", "--in", a)
	if code != 4 || !strings.Contains(se, "unknown_event") {
		t.Fatalf("--in non-rollup: %d %s", code, se)
	}
	_, se, code = run(t, dir, "tail", "--raw", "--in", rid)
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("--raw --in: %d %s", code, se)
	}
}
```

The Task 1 fold test already proves loser exclusion from roots; the raw-view `duel_loser` flag gets its end-to-end assertion in Task 5's race test (a loser can only exist via a real race — `child_taken` blocks sequential creation). No separate flag test here.

- [ ] **Step 2: Run tests, verify failure**

Run: `cd ledger && go test ./internal/cmd/ -run TestTailRoots -v`
Expected: FAIL — unknown flags `--raw`/`--in`, and encapsulated events still present in default view.

- [ ] **Step 3: Implement**

In `read.go`, replace `newTailCmd`/`runTail`:

```go
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
		return out.Errf("unknown_event", "ledger tail  shows the current roots; rollup lines carry their id", 4,
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
```

Byte-identical caveat: the old default payload had no `"raw"` key and neither does the new default branch — only `--raw` adds it. If the existing `runTail` includes anything else in its payload (recheck lines 490–506 before editing), preserve it exactly in the default branch. `rootLine` stays in `rollup.go` (it needs `fold` + `out`); `eventLine` remains for base events, so a never-rolled ledger's default `tail` TTY lines and JSON are unchanged. Update any existing tail test that asserted the old `Short:` string.

- [ ] **Step 4: Run tests, verify pass**

Run: `cd ledger && go test ./internal/cmd/ -v -run 'Tail|Rollup'`
Expected: PASS, including pre-existing tail tests (fix any that pinned the old help text; do NOT weaken byte-shape assertions).

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/read.go internal/cmd/rollup.go internal/cmd/rollup_test.go
git commit -m "cmd: tail renders roots in causal order; --raw and --in views; duel-loser flag"
```

---

### Task 4: Advisory `rollup_due` in write envelopes; `since`/`watch` delivery; state-fold invariance

**Files:**
- Modify: `ledger/internal/cmd/set.go` (~line 96), `ledger/internal/cmd/note.go` (~line 87), `ledger/internal/cmd/vocab.go`, `ledger/internal/cmd/close.go`, `ledger/internal/cmd/create.go` (each write's success envelope)
- Modify: `ledger/internal/cmd/cursor.go` (`filterHits`, ~line 220)
- Test: `ledger/internal/cmd/rollup_test.go` (extend)

**Interfaces:**
- Consumes: Task 1's `Due()`; existing `c.Load`, `outEmit`, `filterHits`, `watchOpts`.
- Produces: every successful write envelope (set, note, vocab add, close, create — rollup already has it from Task 2) carries `"rollup_due": N`. `watch` delivers rollup events when NO `--key`/`--value`/`--kind` filter is active, skips them when any filter is set; `since` already delivers them (they are non-sync events — just assert it). Deduped responses (`"deduped":true`) are unchanged.

- [ ] **Step 1: Write the failing tests**

```go
func TestRollupDueInWriteEnvelopes(t *testing.T) {
	dir := setup(t)
	so, _, _ := run(t, dir, "set", "k1", "status=open", "--as", "w")
	if _, ok := mustJSON(t, so)["rollup_due"]; !ok {
		t.Fatalf("set envelope missing rollup_due: %s", so)
	}
	so, _, _ = run(t, dir, "note", "-k", "gotcha", "-m", "x", "--as", "w")
	if _, ok := mustJSON(t, so)["rollup_due"]; !ok {
		t.Fatalf("note envelope missing rollup_due: %s", so)
	}
}

func TestStateFoldUnchangedByRollups(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	before, _, _ := run(t, dir, "show")
	beforeStatus, _, _ := run(t, dir, "status")
	if _, se, code := run(t, dir, "rollup", a, "-m", "k1 done", "--as", "c"); code != 0 {
		t.Fatalf("%s", se)
	}
	after, _, _ := run(t, dir, "show")
	afterStatus, _, _ := run(t, dir, "status")
	// spec test 43: byte-identical modulo the event count/head lines, which
	// legitimately advance. Compare rows/spine JSON fields exactly.
	if mustJSON(t, before)["rows"] == nil ||
		!jsonEq(t, mustJSON(t, before)["rows"], mustJSON(t, after)["rows"]) ||
		!jsonEq(t, mustJSON(t, beforeStatus)["rows"], mustJSON(t, afterStatus)["rows"]) {
		t.Fatalf("state fold changed after rollup:\nbefore %s\nafter %s", before, after)
	}
}

func TestWatchDeliversRollupsUnfiltered(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	so, _, _ := run(t, dir, "set", "k2", "status=open", "--as", "w")
	cur := mustJSON(t, so)["id"].(string)
	ro, _, _ := run(t, dir, "rollup", a, "-m", "done", "--as", "c")
	rid := mustJSON(t, ro)["id"].(string)

	so, _, code := run(t, dir, "watch", "--since", cur, "--timeout", "1")
	if code != 0 {
		t.Fatalf("watch should drain the rollup event, got exit %d: %s", code, so)
	}
	if !strings.Contains(so, rid) {
		t.Fatalf("unfiltered watch must deliver the rollup: %s", so)
	}
	// filtered watch skips rollups but its cursor advances past them
	so, _, code = run(t, dir, "watch", "--since", cur, "--key", "nothing-matches", "--timeout", "1")
	if code != 2 {
		t.Fatalf("filtered watch: want timeout exit 2, got %d: %s", code, so)
	}
	if !strings.Contains(so, `"cursor"`) {
		t.Fatalf("timeout must still carry a cursor: %s", so)
	}
}
```

Add a tiny helper if none exists:

```go
func jsonEq(t *testing.T, a, b any) bool {
	t.Helper()
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
```

- [ ] **Step 2: Run tests, verify failure**

Run: `cd ledger && go test ./internal/cmd/ -run 'RollupDue|StateFold|WatchDelivers' -v`
Expected: FAIL — no `rollup_due` in set/note envelopes; watch drains nothing (exit 2) for the rollup.

- [ ] **Step 3: Implement**

Add to `read.go` (or a small shared spot in the cmd package):

```go
// dueAfter refolds and reports curation debt for a write envelope. Advisory:
// on any error it returns -1 and the caller omits the field rather than
// failing a write that already landed.
func dueAfter(c *Ctx, slug string) (int, bool) {
	led, err := c.Load(slug)
	if err != nil {
		return 0, false
	}
	return led.Due(), true
}
```

In each write verb's success emit (set.go:96 shape, note.go:87 shape, vocab.go, close.go, create.go), before `outEmit`, add:

```go
	if due, ok := dueAfter(c, led.Slug); ok {
		payload["rollup_due"] = due
	}
```

adapting to each file's payload variable (set.go builds the map inline — hoist it into a `payload :=` variable first; in create.go the slug is the new slug, in close.go it's the positional slug). Do NOT add it to the deduped early-return payload or to read verbs.

In `cursor.go`'s `filterHits`, add a case:

```go
		case "rollup":
			// delivered on unfiltered watches; --key/--value/--kind are
			// set/note filters and a filtered watcher shouldn't wake for
			// curation (cursor still advances past them — spec test 43)
			if o.key == "" && len(o.values) == 0 && o.kind == "" {
				hits = append(hits, ev)
			}
```

(Check the actual field names on `watchOpts` — `o.values` vs `o.value`, `o.kind` — and use them.)

- [ ] **Step 4: Run tests, verify pass**

Run: `cd ledger && go test ./internal/cmd/ -v`
Expected: PASS, including all pre-existing envelope tests (some pin exact payloads — extend those fixtures with `rollup_due` rather than deleting assertions).

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/set.go internal/cmd/note.go internal/cmd/vocab.go internal/cmd/close.go internal/cmd/create.go internal/cmd/cursor.go internal/cmd/read.go internal/cmd/rollup_test.go
git commit -m "cmd: advisory rollup_due on write envelopes; watch delivers rollups unfiltered"
```

---

### Task 5: Duel race — concurrent rollups claiming the same child (spec test 40)

**Files:**
- Test: `ledger/internal/cmd/rollup_test.go` (extend) — or beside the existing concurrency tests if they live in `internal/store` (check `grep -rn "goroutine\|sync.WaitGroup" internal/store/*_test.go internal/cmd/*_test.go` and follow the established pattern)

**Interfaces:**
- Consumes: Tasks 1–3 complete. `store.Store.Append`'s CAS loop; `fold.Fold`'s duel resolution.
- Produces: proof that racing rollups both land, exactly one wins, the loser is wholly inert and flagged.

- [ ] **Step 1: Write the failing-or-passing race test**

This test validates behavior that should already be correct; write it, run it, and only debug if it fails. Race two rollup writes that both claim child `a` (one also claims `b`), bypassing the CLI's write-time `child_taken` check by building both events from the same pre-write fold snapshot — mirror how the existing concurrent-set test constructs `store.Store` directly:

```go
func TestConcurrentRollupDuelAllOrNothing(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")

	st := storeFor(t, dir) // adapt: however cmd tests get a store.Store for dir; otherwise construct via store.Resolve after chdir
	var wg sync.WaitGroup
	for i, kids := range [][]string{{a}, {a, b}} {
		wg.Add(1)
		go func(n int, children []string) {
			defer wg.Done()
			ev := model.NewEvent("rollup", "racer-"+strconv.Itoa(n), st.Repo)
			ev.Children = children
			ev.Text = "race " + strconv.Itoa(n)
			if _, err := st.Append("demo", ev); err != nil {
				t.Errorf("append %d: %v", n, err)
			}
		}(i, kids)
	}
	wg.Wait()

	evs, meta, err := st.Events("demo")
	if err != nil {
		t.Fatal(err)
	}
	led := fold.Fold("demo", evs, meta)
	var rollups []model.Event
	for _, e := range evs {
		if e.Type == "rollup" {
			rollups = append(rollups, e)
		}
	}
	if len(rollups) != 2 {
		t.Fatalf("both racing appends must land (CAS serializes): got %d", len(rollups))
	}
	winner, loser := rollups[0], rollups[1] // total order: first in chain wins
	if !led.Losers[loser.ID] || led.Losers[winner.ID] {
		t.Fatalf("first-in-total-order must win: losers=%v", led.Losers)
	}
	if led.Parent[a] != winner.ID {
		t.Fatalf("child a owned by %s, want %s", led.Parent[a], winner.ID)
	}
	if _, taken := led.Parent[b]; taken && led.Losers[loser.ID] && contains2(loser.Children, b) {
		t.Fatalf("all-or-nothing: loser must not keep b")
	}

	// the loser is visible and flagged in tail --raw
	so, _, _ := run(t, dir, "tail", "--raw", "-n", "50")
	if !strings.Contains(so, `"duel_loser": true`) {
		t.Fatalf("raw view must flag the duel loser: %s", so)
	}
	// and absent from roots
	so, _, _ = run(t, dir, "tail", "-n", "50")
	if strings.Contains(so, loser.ID) {
		t.Fatalf("loser must not be a root: %s", so)
	}
}

func contains2(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
```

Adaptation notes: find how existing cmd tests obtain a `store.Store` for the test dir (there may be a helper; if the only path is `store.Resolve` + chdir, use `t.Chdir(dir)` first as other tests do). One subtlety: whichever racer's append lands *second* in the chain must be the loser — if goroutine scheduling makes the assertion flaky, determine winner/loser from chain order (as written: `rollups[0]` is first in the returned event order, which IS total order) rather than from goroutine index. If `b` ends up untaken because the `{a,b}` racer lost, the `Parent[b]` check passes vacuously — that's the all-or-nothing point; if the `{a,b}` racer *won*, then `b` is legitimately taken and the loser is `{a}` — the assertions as written handle both outcomes, keep them outcome-symmetric when adapting.

- [ ] **Step 2: Run the test**

Run: `cd ledger && go test ./internal/cmd/ -run TestConcurrentRollupDuel -v -count=3`
Expected: PASS three times (run with `-count=3` to shake out scheduling flakiness). If it fails, the defect is in Task 1's fold or Task 3's raw flag — fix there, not with test sleeps.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/rollup_test.go
git commit -m "test: concurrent rollup duel — CAS serializes, first wins wholly, loser flagged"
```

---

### Task 6: Docs — quickstart, orchestrator note, skill

**Files:**
- Modify: `ledger/docs/quickstart.md` (90-line budget, executable examples)
- Modify: `skills/using-ledger/SKILL.md`
- Test: `cd ledger && go test ./internal/docs/` (harness runs every fenced `ledger` line; length budget enforced)

**Interfaces:**
- Consumes: the shipped `rollup` verb and `tail` flags from Tasks 2–3 (examples execute against the real binary in a scratch repo).
- Produces: agent-facing doctrine for curation. No code.

- [ ] **Step 1: Read the harness and current docs**

Read `ledger/internal/docs/docs_test.go` fully (annotation grammar: `# expect: exit N` / `# expect: error <code>`; verbatim argv, no substitution) and `ledger/docs/quickstart.md`.

- [ ] **Step 2: Edit quickstart.md**

Make exactly these changes, then re-count lines (`wc -l` must be ≤ the budget the test enforces — currently 90):

(a) Verb table: add `rollup` (the table's bottom rows are the `--ledger`-taking verbs; `rollup` belongs there). Adjust the "All 15 verbs" line to "All 16 verbs".

(b) Rule 8 (content search) becomes: `8. **Content search** is a pipe: \`ledger tail --raw -n 200 | grep <term>\`.`

(c) Insert a new rule after rule 8 (renumber the rest, or use `8b.` to avoid renumbering — match the file's existing style decision and keep whichever costs fewer lines):

```
9. **Roll-ups keep history readable.** `tail` shows roots: each rollup is
   one summary line standing in for the records inside it (`--raw` = the
   full chain, `--in <id>` opens one). When a thread FINISHES, encapsulate
   it: bare `ledger rollup` shows roots + the submit grammar; `ledger
   rollup <id> <id> -m "one line"` records it, testimony like any write.
   Fix a bad summary by rolling IT up under a better line. Write replies
   carry `rollup_due`; roll at natural pauses and close, never mid-flow.
```

(d) Extend the fenced demo block (before the `watch` line, keeping the block's narrative order sensible) with deterministic examples:

```
ledger rollup
ledger rollup deadbeef00 -m "no such record"  # expect: exit 4 error unknown_event
ledger tail --raw -n 5
```

(e) Reclaim the lines the new rule costs by tightening these specific spots without dropping doctrine: the two-line intro sentence about `--orchestrator` can lose its second clause ("adds the fleet-dispatch section" → fold into the first line); rule 11's two sentences can merge; rule 13's example clause (`cmd="ledger set ..."; $cmd`) can compress to one line. If still over budget, the `status <key> --ledger <slug>` composition sentence in rule 2 is the next candidate. Never cut rules 9 (verify), 10 (secrets), or the vocab rule.

- [ ] **Step 3: Edit SKILL.md**

In `skills/using-ledger/SKILL.md`, add one paragraph to the "Discipline that keeps ledgers trustworthy" section (before the closing example), matching the file's voice:

```
Long-running ledgers earn curation: when a thread finishes — a hypothesis
resolves, a task arc completes — roll it into one summary line (`ledger
rollup`, bare form first for the grammar) so `tail` stays a screenful.
Summaries are second-order testimony: verify one against the records
inside it (`tail --in <id>`) before building on it, and fix a wrong one
by rolling it up under a corrected line — never expect to edit or delete.
Leave live work unrolled.
```

- [ ] **Step 4: Run the docs harness and full suite**

Run: `cd ledger && go test ./internal/docs/ -v` — every example must execute with its annotated outcome and the length budget must pass.
Then: `go test ./...` (full suite, ~155s; store package alone ~2min — wait for it).
Expected: all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/quickstart.md ../skills/using-ledger/SKILL.md
git commit -m "docs: rollup doctrine in quickstart (within budget) and using-ledger skill"
```

---

## Self-review notes (kept for the executor)

- Spec coverage: tests 39 (Task 2), 40 (Task 5), 41 (Task 1 coverage property + Task 3 causal order), 42 (Tasks 2+4), 43 (Task 4), 44's per-release eval re-run is a process item, not code. Rev-12 prose: in-stream events (T1/T2), structural rules incl. all-or-nothing (T1, T5), verb two-step + doctrine text (T2), advisory due (T4), reads/`tail` (T3), no create-time opt-in (nothing to do — absence of code IS the feature), closed-ledger rollup (T2 test).
- `noPositionals("tail")` conflicts with nothing: `tail` still takes no positionals; `--in` is a flag. `rollup` takes positionals by design and must NOT use `noPositionals`.
- Type consistency: `rootLine(led *fold.Ledger, e model.Event) string` (T2, used by T3); `dueAfter(c *Ctx, slug string) (int, bool)` (T4); `Roots() []model.Event`, `Due() int`, `Parent map[string]string`, `Losers map[string]bool` (T1, used everywhere).
- The plan's test code adapts to existing harness helper names (`setup`, `run`, `mustJSON`) — implementers must read `root_test.go` first; the assertions, error codes, and shapes are the requirements.
