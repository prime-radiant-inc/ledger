# Migration Learnings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix everything the GitHub→ledger backlog migration surfaced — one response-echo gap, one init behavior, three doctrine gaps, and the missing bulk-migration path.

**Architecture:** Small, independent fixes to the shipped rev-15 tool plus doctrine additions to the `ledger-issues` skill. No new subsystems.

**Tech Stack:** Go (existing module `ledger/`), skill prose.

**Spec:** The migration transcript is the evidence base (2026-08-18 session; board `issues` on this repo, probe-key chain `533a4c0633..2c7ba2a95e`). Binding norms: the sync spec rev 8 response-echo pin for `contested_resolved` (the pattern Task 1 extends), the parent spec's init/breadcrumb section, and the `ledger-issues` skill's existing voice.

**Open decision for Jesse (not a task):** the board and GitHub issues #1–11 now coexist. Pick a canonicality story: (a) close the GH issues with a pointer to the board (board canonical, GH is intake), (b) keep GH canonical and treat the board as working state, or (c) mirror — which needs tooling nobody has asked for yet (YAGNI says a or b).

## Global Constraints

- Full suite green at every task boundary (`cd ledger && go test ./... -count=1`, foreground, 10-min timeout); `go vet` + `gofmt -l ./internal` clean.
- Quickstart stays ≤120 lines (`TestQuickstartLengthBudget`); skill edits go to `skills/ledger-issues/SKILL.md`, whose fenced commands are executed by `TestDoctrineVerbatimWalkthrough` — new fences must be executable in its fixture style.
- Error contract: every error is `{error, message, hint}`, exit nonzero; response-echo fields are tool-computed, never caller-supplied.

---

### Task 1: `set` response echoes `override` (symmetric with `contested_resolved`)

**Files:**
- Modify: `ledger/internal/cmd/set.go` (the response payload assembly — find where `contested_resolved` is echoed and add `override` beside it)
- Test: `ledger/internal/cmd/override_test.go`

**Evidence:** during the migration, a reserve-idiom seed recorded `override:"human"` in the chain but the `set` response showed `override: null`; verification required `show --id`. The rev-4 round pinned the echo for `contested_resolved` for exactly this reason ("a writer must be able to see" what the tool computed); `override` was never pinned and is the asymmetry. Already noted on GH issue #7.

- [ ] **Step 1:** Write the failing test: an override-recorded write's JSON response carries `"override": "human"` (extend an existing override test that already builds the fixture — assert on the command's stdout doc, not the chain).
- [ ] **Step 2:** Run it; FAIL (field absent/null).
- [ ] **Step 3:** Echo `ev.Override` into the response payload when non-empty, exactly as `contested_resolved` is echoed.
- [ ] **Step 4:** Targeted tests + full suite green.
- [ ] **Step 5:** Commit `fix(set): echo override in the response, symmetric with contested_resolved`.

### Task 2: `init` from a repo subdirectory

**Files:**
- Modify: `ledger/internal/cmd/initcmd.go`
- Test: `ledger/internal/cmd/init_test.go`

**Evidence:** `ledger init` run from `ledger/` (a subdirectory of the repo) produced no breadcrumb anywhere and its true output went unexamined (masked by a jq pipe — see Task 4's doctrine half). Re-run from the repo root it behaved perfectly. First: PROBE what init-from-subdir actually does today (error? silent partial?). Then make it do the obviously right thing: resolve to the repo root exactly as store resolution already does, write the breadcrumb THERE, and say so in the payload (`"path"` already names the root — verify it does when invoked from a subdir).

- [ ] **Step 1:** Write the failing test: `init` invoked with cwd `<repo>/subdir` writes `<repo>/.ledger.toml`, installs the refspec, and reports `path` = repo root. Capture today's actual behavior first and note it in the test comment.
- [ ] **Step 2:** Run; observe today's behavior (this is the probe — if it already passes, the migration failure was something else: chase it before proceeding).
- [ ] **Step 3:** Fix resolution to the repo root (reuse the store-resolution walk; no second implementation).
- [ ] **Step 4:** Targeted + full suite green.
- [ ] **Step 5:** Commit `fix(init): resolve to the repo root from any subdirectory`.

### Task 3: Skill doctrine — three gaps the migration hit

**Files:**
- Modify: `skills/ledger-issues/SKILL.md`
- Test: `ledger/internal/cmd/doctrine_test.go` (walkthrough picks up any new fences automatically — verify)

Three additions, in the file's existing voice:

1. **`--expect` scope line** (in the Issue board section near the claim idiom): `--expect` is REQUIRED on guarded fields; on unguarded fields it is optional but VALIDATED when passed — `--expect none` on a field with history fails ("beat you to"), which is CAS working, not a bug. Omit it entirely for plain unguarded writes.
2. **Retire-a-mistake idiom** (new bullet beside the seed-collision recovery): a junk or mistaken key cannot be deleted — retire it. If human-labeled, clear the label first (plain write, no `--expect`); seed a status if it has none; then `wontfix` with a message saying it's an artifact. Fence it in the walkthrough style (the migration's probe-key chain is the template).
3. **Horizon guidance** (one line where `--stale-after` first appears): pick the horizon from the board's tempo — hours for agent fleets (the claim-reclaim loop), a week-plus for human-paced backlogs; a horizon shorter than real work cadence turns every claim stale.

- [ ] **Step 1:** Run the doctrine tests; note the walkthrough's current fence count.
- [ ] **Step 2:** Write the three additions; the retire idiom's fences must execute in the walkthrough fixture.
- [ ] **Step 3:** Doctrine + quickstart tests green; full suite green.
- [ ] **Step 4:** Commit `docs(skill): --expect scope, retire-a-mistake idiom, horizon guidance`.

### Task 4: Bulk migration recipe

**Files:**
- Create: `ledger/docs/migrate-github.md` (short, executable recipe)
- Modify: `ledger/docs/admin.md` (one pointer line, if the file has a natural place)
- Test: `ledger/internal/docs/docs_test.go` — add the recipe's core loop to whatever executable-docs harness exists, or a dedicated test that runs it against a fixture JSON file (NOT the live GitHub API)

**Evidence:** 11 issues took ~33 hand-built commands. The right v1 answer is a documented loop, not new tool surface (YAGNI: `import` exists for whole-ledger JSONL, and extending it to board seeding is machinery nobody needs yet):

```sh
gh issue list --state open --json number,title,labels \
| jq -r '.[] | [.number, .title] | @tsv' \
| while IFS=$'\t' read -r n title; do
    key=$(echo "$title" | tr 'A-Z ' 'a-z-' | tr -cs 'a-z0-9-' '-' | cut -c1-64)
    ledger set "$key" status=open --expect none -m "$title" --as migrator --ledger issues \
      && ledger note --key "$key" -m "migrated from issues/$n" --as migrator --ledger issues \
      || break
  done
```

The recipe must carry the migration's two hard-won warnings verbatim: check exit codes per command (`|| break` — a jq pipe swallows failures silently; the migration's own init error was lost exactly this way), and the seed's `-m` IS the immutable title.

- [ ] **Step 1:** Write the doc with the loop above, tightened against real `gh` output shapes.
- [ ] **Step 2:** Add the executable test against a checked-in fixture JSON (5 fake issues → 5 seeded keys, exit-code break verified by a poisoned row).
- [ ] **Step 3:** Tests + full suite green.
- [ ] **Step 4:** Commit `docs: GitHub-to-board migration recipe, executable`.

### Task 5: Board bookkeeping

**Files:** none (board writes only)

- [ ] **Step 1:** On the `issues` board: seed `migration-learnings` status=open, `-m "implement the migration-learnings plan (docs/superpowers/plans/2026-08-18-migration-learnings.md)"`, note pointing at this plan; close it when Tasks 1–4 land.
- [ ] **Step 2:** Update GH issue #7's comment thread when Task 1 lands (the echo item is tracked there).
- [ ] **Step 3:** `ledger push`.
