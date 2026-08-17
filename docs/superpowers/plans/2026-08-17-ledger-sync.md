# Ledger Sync (Tool Rev 15) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement cross-replica sync for the ledger tool: the parent spec's Sync-and-push section plus sync spec rev 7's additions (Kahn fold, cursor contract with pager, contested, determinism, whole-chain preconditions).

**Architecture:** Git-native replication over phantom refs — `sync` fetches tracking refs and merges with sentinel commits, `push` publishes non-force. Every read folds one pinned order (Kahn over the sentinel-contracted event DAG). Partition races surface as `contested` attention entries computed by a width-bounded cover-set pass.

**Tech Stack:** Go (existing module `ledger/`), cobra, git plumbing via `internal/gitx`.

**Spec:** `docs/superpowers/specs/2026-08-17-ledger-sync-design.md` (rev 7) — THE binding document. Its header makes the parent spec's "Sync and push" section (`docs/superpowers/specs/2026-08-13-ledger-tool-design.md` ~lines 31–46) normative with exactly one amendment (watch batch bound deferred). The issues spec (`docs/superpowers/specs/2026-08-15-ledger-issues-design.md`) is normative for board semantics, amended in exactly the four places the sync spec's amendment inventory lists. **Every implementer reads the sync spec section for their task before coding. Where this plan and the spec disagree, the spec wins — flag the disagreement.**

**Reference spike:** branch `spike/sync-rev3` implements an OLDER revision of most of this. Retrieve any file with `git show spike/sync-rev3:ledger/<path>`. The spec's "Implementation scope" section lists exactly what the spike gets wrong (livelocking pager, global-restricted batch order, n² bitsets, windowed preconditions, per-slug push, `ok:true` failure envelopes, comma-joined `contested_resolved`, every-read-verb `--at`, no multi-root refusal, clock env var). Port structure and tests freely; port none of the listed deltas.

## Global Constraints

- **Reads batch**: folds go through one `git log` + one `cat-file --batch` pipeline, never per-event subprocesses (parent spec, measured 70ms vs 48s).
- **Sync never pushes. Push is non-force.** Tracking refs live at `refs/ledger-remote/<remote>/<slug>`, never `refs/remotes/`.
- **Sentinel sync merges** carry `event.json` `type:"sync"` and are skipped by every read, fold, count, and idempotency scan.
- **No clock env var in the release binary.** The clock funnel is `model.Now()`; the test seam is an internal package variable, never environment.
- **Determinism**: covered verbs (`show`,`status`,`tail`,`notes`,`ready`,`render`,`since`) render byte-identically for the same chain (+cursor for `since`, +`--at` where it exists) under any `TZ`/`LC_ALL`/`HOME`/user, both sinks.
- **Error contract** (parent): success = one JSON doc with `ok`; errors = `{error, message, hint}`; exit 4 usage/value errors, exit 3 partial outcomes.
- **Test output must be pristine**; foreground test runs only, with timeouts; full suite green at every task boundary (`go test ./... -count=1`, 10-min timeout).
- Go tests live beside their package; run from `ledger/`.

## File Structure

- `internal/dag/` (new): Kahn fold order + sentinel contraction + cover-set pass. No bitsets.
- `internal/store/store.go`: `Events` adopts the dag order; whole-chain preconditions (window deleted).
- `internal/cmd/cursor.go`: cursor contract (validity, range delivery, pager).
- `internal/cmd/sync.go`, `push.go`, `remote.go` (new): the verbs.
- `internal/cmd/freshness.go` (new): freshness warning shared by `ready`/`show`/`status`.
- `internal/board/contested.go` (new): write-heads via cover-set; attention integration in `frontier.go`.
- `internal/cmd/read.go`, `note.go`: `show --id`, `notes --id` fixes.
- `internal/model/model.go`: `Now()` funnel; `internal/out/`: age clamp.
- `ledger/docs/quickstart.md`, `skills/using-ledger/SKILL.md`: doctrine.

---

### Task 1: `dag` package — pinned fold order with sentinel contraction

**Files:**
- Create: `internal/dag/dag.go`, `internal/dag/dag_test.go`

**Interfaces:**
- Produces: `type Node struct { SHA string; Parents []string; TS string; IsSentinel bool }`;
  `type Result struct { Order []string; Children map[string][]string; Roots []string }` where `Order` is the pinned fold order over NON-sentinel nodes, `Children` is the child adjacency of the SENTINEL-CONTRACTED DAG (non-sentinel nodes only), and `Roots` are the contracted DAG's parentless nodes (multi-root detection consumes this);
  `func Sort(nodes []Node) Result`.

Spec: Addition 1. Kahn's topological sort; ready set is a min-heap keyed (parsed timestamp, full 40-char SHA); legacy and millisecond ts layouts compare AS TIMES (`model.ParseTS`); missing/unparseable ts sorts after all timestamped peers, by SHA; sentinels contracted out (parents spliced to children) BEFORE the sort so a syncing host's clock can never reorder other hosts' writes.

Port base: `git show spike/sync-rev3:ledger/internal/dag/dag.go`. Deltas from the spike: delete `Bitset`, the `wantAncestors` parameter, and `Result.Anc` entirely; add `Children` and `Roots` to `Result` (the spike computes contracted parents internally in `effectiveParents` — invert that map for `Children`; roots = contracted nodes with no in-set parent).

- [ ] **Step 1: Port the spike's `dag_test.go`** (`git show spike/sync-rev3:ledger/internal/dag/dag_test.go`), dropping every test that references `Anc`/`Bitset`/`wantAncestors`. Keep: skewed-clocks-vs-ancestry, ts-then-full-SHA ordering, mixed layouts compared as times, undated-sorts-last, sentinel contraction (incl. chained sentinels), swapped merge-parent order folds identically, criss-cross merges, torn commits contracted not dropped. Add two new tests:

```go
func TestChildrenIsContractedAdjacency(t *testing.T) {
    // A <- S(sentinel) <- B : contracted children of A must be [B]; S absent everywhere.
    r := Sort([]Node{
        {SHA: sha("a"), TS: "2026-08-17T01:00:00.000"},
        {SHA: sha("s"), Parents: []string{sha("a")}, TS: "2026-08-17T02:00:00.000", IsSentinel: true},
        {SHA: sha("b"), Parents: []string{sha("s")}, TS: "2026-08-17T03:00:00.000"},
    })
    if len(r.Children[sha("a")]) != 1 || r.Children[sha("a")][0] != sha("b") { t.Fatalf("contracted child: %v", r.Children) }
    if _, ok := r.Children[sha("s")]; ok { t.Fatal("sentinel present in adjacency") }
}
func TestRootsMultiRootDetected(t *testing.T) {
    // two parentless non-sentinel nodes joined by a sentinel merge -> 2 roots
    r := Sort([]Node{
        {SHA: sha("a"), TS: "2026-08-17T01:00:00.000"},
        {SHA: sha("x"), TS: "2026-08-17T01:30:00.000"},
        {SHA: sha("m"), Parents: []string{sha("a"), sha("x")}, TS: "2026-08-17T02:00:00.000", IsSentinel: true},
    })
    if len(r.Roots) != 2 { t.Fatalf("roots = %v", r.Roots) }
}
```

(`sha(seed)` = deterministic fake 40-char hex helper — port from the spike test file.)
- [ ] **Step 2: Run** `go test ./internal/dag/ -count=1` — FAIL (package absent).
- [ ] **Step 3: Implement** `dag.go` per the port+deltas above.
- [ ] **Step 4: Run** the package tests, then `go vet ./...`. PASS, clean.
- [ ] **Step 5: Commit** `feat(dag): pinned Kahn fold order with sentinel contraction`.

### Task 2: `store.Events` folds the pinned order

**Files:**
- Modify: `internal/store/store.go:185-253` (`Events`), reusing `catBatch`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `dag.Sort`, `dag.Node`.
- Produces: `Events(slug)` unchanged signature `([]model.Event, model.Meta, error)`, now returning events in fold order with sentinels excluded (they already are — keep it that way) and meta read independently of event presence; NEW `func (s Store) EventsDAG(slug string) ([]model.Event, model.Meta, dag.Result, error)` returning the same plus the contracted `dag.Result` (Tasks 4/5/7 consume it; `Events` becomes a thin wrapper discarding the Result).

Current defect being replaced: `Events` walks `git log --reverse --format=%H` — DATE order, wrong on merged DAGs. New read shape (Addition 1): one `git log --format=%H%x09%P` (traversal order irrelevant) + the existing one `cat-file --batch`; build `dag.Node`s (`IsSentinel` = event `type=="sync"` or missing event.json), `dag.Sort`, then order events by `Result.Order`.

- [ ] **Step 1: Write failing tests** in `store_test.go`: build a real divergent chain with `BuildCommit` + `git update-ref` (two branches off one root, sentinel merge commit carrying `{"type":"sync"}` event.json), assert (a) `Events` returns both branches' events ordered by ts across branches (skew fixture: branch-B event ts EARLIER than branch-A's ⇒ B's event first despite commit order), (b) sentinel absent, (c) meta present, (d) `EventsDAG` returns `Roots` len 1 and a `Children` map containing the cross-sentinel edge.
- [ ] **Step 2: Run** — FAIL (date order / missing func).
- [ ] **Step 3: Implement.** Keep the one-log+one-catBatch constraint (Global Constraints).
- [ ] **Step 4: Run the FULL suite** `go test ./... -count=1` (10-min timeout). Every existing test must stay green — linear chains fold identically under Kahn (single-child chains have singleton ready sets), so failures mean a real regression.
- [ ] **Step 5: Commit** `feat(store): Events folds the pinned Kahn order`.

### Task 3: Whole-chain precondition reads (window deleted)

**Files:**
- Modify: `internal/store/store.go` (`AppendChecked`/`casLoop`/`runPrecondition`; DELETE `EventsWindow` and `windowProbeSize` if no other consumer — verify with grep), `internal/cmd/set.go` (its windowed-read call sites)
- Test: `internal/cmd/expect_test.go`, `internal/store/store_test.go`, `internal/cmd/scale_test.go`

Spec: Addition 5 — "every guarded write uses whole-chain precondition reads, unconditionally"; the precondition still re-reads fresh INSIDE the CAS retry loop per attempt (issues rule 7 discipline). Amendment inventory item 1 retracts issues test 16's "not a full re-fold per retry" clause: find that test (grep `re-fold` / `windowProbeSize` in `internal/cmd/` and `internal/store/`), and rewrite it to assert the whole-chain read happens per attempt instead.

- [ ] **Step 1: Write/adjust failing tests**: a guarded `--expect` write against a key whose referenced event sits at the ROOT of a 200-event chain succeeds (window would have missed it — this may already pass via the fallback; the assertion that matters is behavioral equivalence with the window gone); the retracted test-16 assertion replaced.
- [ ] **Step 2: Run** targeted tests.
- [ ] **Step 3: Delete the window machinery**; precondition reads use `Events` (fold order) per attempt.
- [ ] **Step 4: Full suite green.**
- [ ] **Step 5: Commit** `feat(store): whole-chain precondition reads, window deleted (spec rev 7 Addition 5)`.

### Task 4: The cursor contract — `since`/`watch` range semantics + pager

**Files:**
- Modify: `internal/cmd/cursor.go` (replace `indexOf` positional logic wholesale)
- Test: `internal/cmd/cursor_test.go` (extend), new fixtures

**Interfaces:**
- Consumes: `store.EventsDAG`, `gitx` for `merge-base --is-ancestor` and `rev-list`.
- Produces: `deliverRange(c *Ctx, led, cursor string, limit int) (events []model.Event, next string, err error)` used by both `since` and `watch`.

Spec: Addition 2, verbatim law. Implement exactly:
1. **Validity**: cursor is ancestor-or-equal of tip (`git merge-base --is-ancestor <cursor> <tip>`; equal counts) else `reset_required` (existing error shape — keep it).
2. **Delivery**: non-sentinel commits in `cursor..tip`.
3. **Batch order**: `dag.Sort` restricted to the range's nodes (build Nodes only for range commits; contracted sub-DAG). One algorithm — do NOT write a second comparator.
4. **Unpaged**: emitted cursor = tip (sentinel included).
5. **Paged** (`--limit N`): deliver in batch order; stop at the first point where ≥N delivered AND some delivered C (i) descends from the incoming cursor and (ii) has every delivered event as ancestor-or-equal — judged on the contracted DAG, where (ii) reduces to "the delivered set's maximal elements = {C}" via delivered-child bookkeeping over `Result.Children`. Condition (i): `merge-base --is-ancestor <cursor> <C>` (one subprocess at each candidate stop-point, not per event). Emit C. If the range exhausts first, emit the TIP (sentinel included). `--limit` is a floor, not a ceiling.
6. **`watch`**: no `--limit`; every batch emits the tip (unchanged from current behavior after the range conversion).

- [ ] **Step 1: Write failing tests** — the spec's three pinned fixtures, built as real chains via store helpers:

```go
// (a) rev-4 livelock DAG: root -> {A1..A3} and {B1..B3} diverged, sentinel merge tip,
//     cursor = root. Page with --limit 2 until cursor repeats or all delivered.
//     Assert: union(pages) == unpaged drain, no event twice, terminates, final cursor == tip.
// (b) sentinel tip: same chain, cursor = root, limit 2: the final page must emit the TIP
//     (the sentinel SHA), not a branch head.
// (c) branch-local cursor: cursor = A-branch head. One page, --limit 1: delivers only
//     B-branch events (never re-delivers A events), exhausts, emits tip.
// (d) linear chain, limit 2: pages of exactly 2, last-delivered cursors, union == drain.
// (e) fold-head cursor validity: a mid-chain event id is VALID and re-delivers the other
//     branch (documented behavior); a foreign SHA => reset_required.
// (f) batch order: range where global-restricted order differs from range-local
//     (out-of-range ancestor with later ts) — assert the range-local (Kahn-on-range) order.
```
- [ ] **Step 2: Run** — FAIL (positional implementation).
- [ ] **Step 3: Implement** `deliverRange` per the law; wire `since` and `watch` (watch keeps its filter/timeout logic; only cursor mechanics change; cold-start post-merge watch delivers nothing until a new event — keep the existing resolveStartCursor tip behavior).
- [ ] **Step 4: Full suite green.**
- [ ] **Step 5: Commit** `feat(cursor): range semantics + the rev-7 pager law`.

### Task 5: `ledger sync`

**Files:**
- Create: `internal/cmd/sync.go`, `internal/cmd/remote.go`
- Modify: `internal/cmd/initcmd.go` (install refspec; breadcrumb text)
- Test: `internal/cmd/sync_test.go`

**Interfaces:**
- Consumes: `store.EventsDAG` (multi-root check via `Result.Roots`), `model.ValidateDeclarations`, `store` CAS.
- Produces: `resolveRemote(c *Ctx, flag string) (string, error)` (Task 6 reuses); outcome envelope helpers `emitOutcomes(verb string, outcomes []SlugOutcome) error` with `type SlugOutcome struct { Slug, Result, Detail, ID string }` (Task 6 reuses).

Spec: parent Sync-and-push section + rev 7 Pins. Port base: `git show spike/sync-rev3:ledger/internal/cmd/sync.go` and `remote.go`. Behavior:
- Refspec repair every invocation: install `+refs/ledger/*:refs/ledger-remote/<remote>/*` for the named remote; rewrite/remove refspecs targeting a different remote's `refs/ledger-remote/` namespace; prune tracking refs of removed remotes.
- Per slug: tracking⊆local ⇒ `no-op`; local⊆tracking ⇒ `fast-forward`; divergence ⇒ ONE sentinel merge under ref CAS (re-parent-and-retry on race); no local ref ⇒ CAS-create at tracking head (adoption) with `ValidateDeclarations` re-run — broken shape refused with the defect named.
- **Multi-root refusal (NEW vs spike)**: before moving/creating the local ref, run the candidate chain through `EventsDAG`-equivalent on the tracking ref; `len(Roots) > 1` ⇒ refuse, error naming both roots' SHAs, their creators (from each root's meta.json if present, else the commit author), and the tracking ref path `refs/ledger-remote/<remote>/<slug>`. Same-root rule (different single roots) keeps the parent's two-creator error + export/import exit; multi-root refusal's hint is remote-side repair, NOT export/import.
- Remote resolution: `--remote` > breadcrumb remote > `origin` > sole remote > (zero ⇒ clean no-op exit 0; ≥2 unselected ⇒ `ambiguous_remote` exit 4, hint = candidate list + `--remote <name>`; `--remote <unknown>` stays `bad_value`).
- Degraded: `GIT_TERMINAL_PROMPT=0` + blanked askpass ⇒ `credentials_needed`.
- Outcome envelope (rev 7 pin): `ok:true` iff every slug succeeded; any failure ⇒ `ok:false`, `error:"partial_failure"`, hint pointing at the outcomes array; exit 3. Payload always written before exit.
- `init`: install the refspec; breadcrumb says `ledger init && ledger sync`.

- [ ] **Step 1: Port + extend the spike's `sync_test.go`**, adding: multi-root refusal (build a graft with `git commit-tree -p <root1-desc> -p <root2>` pushed to a bare remote; assert refusal names both roots and the tracking ref; assert the un-grafted slug still syncs), `ambiguous_remote` (two remotes no origin), zero-remote no-op, `ok:false`+`error:"partial_failure"` on a rejected outcome, adoption re-validation refusal, refspec repair across `git remote rename`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Full suite green.**
- [ ] **Step 5: Commit** `feat(sync): tracking refs, sentinel merges, adoption, multi-root refusal`.

### Task 6: `ledger push`

**Files:**
- Create: `internal/cmd/push.go`
- Test: `internal/cmd/push_test.go`

**Interfaces:** Consumes `resolveRemote`, `emitOutcomes`, `SlugOutcome` from Task 5.

Spec: parent + Pins. **Batched (delta from spike)**: ONE `git push --porcelain --atomic=false <remote> refs/ledger/a:refs/ledger/a ...` for all selected slugs, parse per-ref porcelain lines into outcomes (`pushed`/`rejected`/`failed`). Non-force always. Selective by args. On non-fast-forward rejection: fetch tracking refs (so root mismatches diagnose with the two-creator error) and detail says ``run `ledger sync`, then retry `ledger push` `` — suppress git's own hint (`advice.pushNonFastForward=false`, never echo git stderr). Same outcome envelope/exit rules as sync. Zero remotes ⇒ clean no-op; ambiguity ⇒ `ambiguous_remote`.

- [ ] **Step 1: Write failing tests**: multi-slug batch push is ONE subprocess (count via `GIT_TRACE=1`-free approach: assert via a gitx call-recorder if the test harness has one, else assert all-slugs outcome correctness + rejection detail text), rejected slug ⇒ `ok:false`, `error:"partial_failure"`, exit 3, remote ref NOT moved (non-force assertion); selective push pushes only the named slug.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Full suite green.**
- [ ] **Step 5: Commit** `feat(push): batched non-force publish with per-slug outcomes`.

### Task 7: `contested`

**Files:**
- Create: `internal/board/contested.go`, `internal/board/contested_test.go`
- Modify: `internal/board/frontier.go` (AttentionEntry gets `Contest *Contest`; entries get `Contested bool`; the total sort), `internal/cmd/set.go` (write-path recording + response echo), `internal/cmd/ready.go`, `internal/out/` TTY render (resolution marker), `internal/cmd/scale_test.go` (bounds)
- Test: package tests + `internal/cmd/ready_test.go`

**Interfaces:**
- Produces: `type Contest struct { Field string; IDs []string; Authors []string; Expect string; Human bool }`;
  `func WriteHeads(events []model.Event, d dag.Result, key, field string) []int` (indices, fold order, winner last) — the SINGLE-PAIR form, used by the write path;
  `func AllContests(meta model.Meta, events []model.Event, d dag.Result) []Contest` — the board-wide cover-set pass, used by `ready`'s fold.

Spec: Addition 3, all bullets. The cover-set pass (board-wide): one reverse-topological walk over the contracted DAG accumulating per-node sets of guarded (key,field) pairs written by descendants-or-self; a write is a head iff no child's set contains its pair; free a node's set once its parents consumed it (peak residency = DAG width × pairs). The single-pair form: same walk, one boolean per node. Rules: `|heads|>1` ⇒ one entry per (key,field); winner = fold-order-last head; `expect` = winner id; NO same-value auto-clear; entry shape exactly `{"reason":"contested","key",title omitted when statusless,"contest":{"field","ids","authors","expect","human"}}`; membership unchanged + `"contested":true` on ready/held/blocked entries; attention sort = `(sort_key, reason, field)` with `sort_key` = key, or sorted member list joined by `,` for cycle entries, `field` = `""` where absent, implemented with a PLAIN (unstable-safe) sort. Write path: `contested_resolved` = losing ids as a JSON ARRAY on the collapsing event, computed per CAS attempt from that attempt's fresh read, unconditional reset per attempt (the override-carryover lesson); the `set` response ECHOES `contested_resolved`; the TTY event render shows a resolution marker (same class as `override:` labeling). Bounds: merged-5k `ready` with contests ≤350ms median-of-3; linear 140ms bound untouched.

Reference: `git show spike/sync-rev3:ledger/internal/board/contested.go` for the write-heads DEFINITION and test fixtures only — its ancestry substrate (bitsets) and comma-joined string are the named deltas. Port `contested_test.go` fixtures; rewrite assertions for the array shape.

- [ ] **Step 1: Write failing tests**: two concurrent claims flag once with valid expect; claim-then-close per side flags ONCE per field; same-value concurrent closes STILL flag; collapsing write records array `contested_resolved` (touch-base included) and clears; cover-set heads == a brute-force pairwise-reachability reference on the same fixtures; linear boards produce zero contests and skip the pass; entries byte-identical across two replicas built in different merge orders; the sort total-order fixture (two cycles + contested + stale-claim in one envelope, deterministic under `sort.Slice`); `set` response echoes; `ready` bounds.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Full suite green + bounds measured.**
- [ ] **Step 5: Commit** `feat(board): contested via cover-set pass, durable contested_resolved`.

### Task 8: Freshness warnings

**Files:**
- Create: `internal/cmd/freshness.go`
- Modify: `internal/cmd/ready.go`, `internal/cmd/read.go` (show/status)
- Test: `internal/cmd/sync_test.go` (extend)

Spec: Addition 3's freshness bullet + R4 placement pin. N = count of non-sentinel commits in `local..tracking` (the last-FETCHED tracking ref; no network on reads). TTY sink: one stderr line `[ledger] N unmerged remote events — run 'ledger sync'`. JSON sink: single top-level `freshness` sibling key `{"unmerged_remote_events": N, "hint": "run `ledger sync`"}`; the PROJECTION members are byte-unchanged by its presence. Root-mismatch tracking state reports the export/import guidance instead. Applies to `ready`, `show`, `status`.

- [ ] **Step 1: Write failing tests**: fetched-but-unmerged replica warns on all three verbs, both sinks; projection members byte-identical with and without the warning (marshal the doc, delete `freshness`, compare against a fresh replica's doc); synced replica silent.
- [ ] **Step 2–4: Run/implement/full suite.**
- [ ] **Step 5: Commit** `feat(freshness): unmerged-remote warning on ready/show/status, outside the projection`.

### Task 9: `--at`, the age clamp, and the clock funnel

**Files:**
- Modify: `internal/model/model.go` (add `func Now() time.Time` returning `nowFn()`; package var `nowFn = time.Now` — the internal test seam; route `NewEvent` ts through it), `internal/cmd/set.go:309` (Signals clock), `internal/cmd/ready.go`, `internal/cmd/ls.go`, note/notes path, `internal/board/board.go` (`StaleAge` clamps at zero), `internal/out/` (age render clamps)
- Test: `internal/cmd/at_test.go` (new), `internal/board/board_test.go`

Spec: Addition 4. `--at <ts>` (millisecond UTC layout; legacy accepted) EXISTS on `ready` and `notes` only; every other verb — `show`/`status`/`tail`/`since`/`watch`/`render`/`ls` and all writes — rejects it `bad_usage` via flag absence. `--at` moves the evaluation clock only; event newer than `--at` renders age `0s`. Age clamp is GENERAL: every age/staleness render clamps at zero under both clocks (peer-ahead case). Skew asymmetry behavior (Addition 1): a claim from a host N ahead goes stale exactly N late at every horizon — test via the `nowFn` seam, not env.

- [ ] **Step 1: Write failing tests**: `--at` accepted on ready/notes with `0s` future-age pinned; rejected `bad_usage` on show/status/tail/since/render/ls/set/close; general clamp (event ts 3h in the future renders `0s` in ready and ls with NO `--at`); ahead-writer staleness arrives exactly skew-late at horizons 1h and 5h (seam-driven); rule-5 staleness stays on the real clock (a write verb has no fake clock path at all).
- [ ] **Step 2–4: Run/implement/full suite.** Port `at_test.go` shapes from the spike, minus the env-var cases.
- [ ] **Step 5: Commit** `feat(clock): --at on ready/notes, general age clamp, internal Now seam`.

### Task 10: Id reads — `show --id` and `notes --id`

**Files:**
- Modify: `internal/cmd/read.go` (show), `internal/cmd/note.go` (notes)
- Test: `internal/cmd/read_test.go`

Spec: Addition 3's id-reads bullet, verbatim: `show --id <sha>` renders ONE event in full — type, key, fields, message, evidence, author with provenance marker, timestamp — including note bodies under the parent's per-line quoting and control-character escaping; unknown id ⇒ `bad_value` naming it. `notes --id` on any id that is NOT a note event of this ledger (non-note event and unknown id alike) ⇒ `bad_value` naming the id, hint pointing at `show --id`; never a silent empty list.

- [ ] **Step 1: Write failing tests**: `show --id` on a set-event id renders the event (assert type/key/author/provenance present); on a note id renders the quoted body; unknown ⇒ `bad_value`; `notes --id` on a set-event id ⇒ `bad_value` with `show --id` in the hint; on unknown ⇒ same; on a real note unchanged.
- [ ] **Step 2–4: Run/implement/full suite.**
- [ ] **Step 5: Commit** `feat(read): show --id full event render; notes --id never silently empty`.

### Task 11: The standing determinism test

**Files:**
- Create: `internal/cmd/determinism_test.go`
- Test: itself

Spec: Addition 4's standing test, verbatim: hand-built replicas of ONE event set differing in merge structure and merge-parent order (build directly with `BuildCommit` + hand `commit-tree` merges carrying sentinel event.json — the tool's own sync mostly precludes divergent merges); read under perturbed `TZ=Asia/Katmandu`/`LC_ALL=fr_FR.UTF-8`/`HOME=<temp>`/`USER=other`, BOTH sinks (pipe-JSON and forced-TTY via the existing test pty/`--tty`-equivalent used by render tests — check how `render.go` forces TTY styling and reuse), every covered verb byte-diffed: `show`, `status`, `tail`, `notes`, `ready` (fixed `--at`), `render`'s written FILE, and `since` at a fixed cursor across replicas holding the same chain; plus a fresh-clone re-fold of the same refs. Freshness keys and cross-slug presence lines verified OUTSIDE the diffed projection (delete the `freshness` key before diffing; that deletion asserted non-empty on exactly one side of a staged fetch).

- [ ] **Step 1: Write the test** (it IS the deliverable): two replica stores, same chain different merge shapes, subprocess-run the built binary (`go build` to t.TempDir()) under the perturbed env for both sinks, byte-compare per verb.
- [ ] **Step 2: Run** — must PASS against Tasks 1–10's implementation; any diff is a real bug to fix HERE.
- [ ] **Step 3: Full suite green.**
- [ ] **Step 4: Commit** `test(determinism): perturbed both-sinks byte-diff across replica shapes`.

### Task 12: Bootstrap surfaces — `ls` and the breadcrumb

**Files:**
- Modify: `internal/cmd/ls.go`, `internal/cmd/initcmd.go`
- Test: `internal/cmd/ls_test.go`, `internal/cmd/init_test.go`

Spec (parent): a fresh clone's `ledger ls`, finding a breadcrumb but no installed refspec, prints the bootstrap command (`ledger init && ledger sync`) instead of an empty listing; `ls` shows unsynced tracking-only slugs (a tracking ref with no local ref) marked as such; `.ledger.toml` carries the remote as a NAME never a URL, and `init` prints "commit this file so clones discover the ledger" and never commits.

- [ ] **Step 1: Write failing tests**: clone-with-breadcrumb-no-refspec `ls` prints the bootstrap hint; tracking-only slug appears in `ls` marked `(unsynced — run ledger sync)`; breadcrumb round-trip keeps the remote name.
- [ ] **Step 2–4: Run/implement/full suite.**
- [ ] **Step 5: Commit** `feat(ls): bootstrap hint and tracking-only listing`.

### Task 13: Doctrine — quickstart, skill, docs walkthrough

**Files:**
- Modify: `ledger/docs/quickstart.md` (budget 110 → 120 lines), `skills/using-ledger/SKILL.md`, `internal/docs/docs_test.go` (verb coverage picks up `sync`/`push` automatically — verify), executed walkthrough doc if present (grep `docs_test` for the walkthrough source)
- Test: `internal/docs/docs_test.go`, `internal/cmd/quickstart_test.go`

Content (spec: skill lines + quickstart rewording, exact requirements):
- Quickstart: sync habit ("start of a session: `ledger sync`; end: `ledger push`"); selective push privacy line; skew doctrine ONE line, behind-direction only: "board horizons must exceed expected inter-host clock skew, so claims are not born stale"; contested recovery: read both heads (`show --id`) before collapsing — a seed collision can hide two tasks under one key; collapse with the ticket's `expect`, `--override` where the settled gate trips, message says why; **the `needs_override` rewording** — replace the "a human labeled this, walk away" line with text naming all three signal sources: human label ⇒ walk away; `settled`/`claim` ⇒ the revise-a-settled-outcome idiom (`--expect <id> --override` with a message that says why). Never write secrets (existing line stays).
- SKILL.md `## Sync` section: same doctrine in skill register + the wedged-slug/multi-root note (admin repairs remote-side).
- Budget: quickstart ≤120 lines; `TestQuickstartMentionsEveryVerb` must pass with `sync`/`push` present.

- [ ] **Step 1: Run** `go test ./internal/docs/ ./internal/cmd/ -run 'Quickstart|Doc' -count=1` — observe the verb-coverage failure (new verbs undocumented).
- [ ] **Step 2: Write the doctrine** per the content list.
- [ ] **Step 3: Tests green, line budget verified** (`wc -l ledger/docs/quickstart.md` ≤ 120).
- [ ] **Step 4: Full suite green.**
- [ ] **Step 5: Commit** `docs: sync doctrine, needs_override rewording, skill section`.

---

## Task ordering

1 → 2 → {3, 4} → 5 → 6, and 7 (needs 2), 8 (needs 5), 9, 10 independent after 2; 11 after 1–10; 12 after 5; 13 after 5/6. Sequential execution in plan order is safe.

## Acceptance

The sync spec's Test plan items 1–15 map: item 1→Task 1/2, 2→4, 3→7, 4→7, 5→5, 6→3/7, 7→9, 8→11, 9→8, 10→9, 11→5, 12→5/6, 13→5/6, 14→10, 15→4. The trial plan (two replicas, partition fleet, audit) re-runs post-build as a conductor exercise, not a plan task.
