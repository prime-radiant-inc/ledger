# Ledger sync: activation and the partition contract (design)

2026-08-17, revision 2 — a rewrite after an adversarial round falsified
revision 1 (see Validation record). The decisive finding: the parent
tool spec (`2026-08-13-ledger-tool-design.md`, rev 13) already contains
a complete, seven-round-hardened sync design — tracking refs, the
sync/push verb pair, the same-root rule, remote-only adoption, sentinel
merges, range cursors, degraded modes, the breadcrumb, the privacy and
trust model — and revision 1 rewrote it from memory, worse, without
declaring a supersession. Revision 2 deletes that mistake:

**The parent spec's "Sync and push" section stands IN FULL and
unamended. It is the sync design.** This document adds only what the
issues layer (`2026-08-15-ledger-issues-design.md`, rev 17) and the
operator's determinism requirement need on top of it. These additions
are the tool's rev 15. The operator's three requirements, restated:
works offline (reduced guarantees accepted); simple to understand; any
host reading a synced ledger after the fact shows exactly the same
output.

What the parent already settles (cited, not restated — read it there):
tracking refs at `refs/ledger-remote/<remote>/<slug>` with refspec
repair; `ledger sync` (fetch, ff/no-op, one sentinel merge under CAS,
remote-only adoption; NEVER pushes) and `ledger push` (non-force,
selective by slug, per-slug outcomes, exit 3 partial); the same-root
rule with `root_mismatch`; sentinel `type:"sync"` merge events skipped
by every read; set-based cursor delivery over `cursor..head`
(topological, ts-tiebroken, SHA-tiebroken); appender-vs-syncer CAS;
degraded modes (`GIT_TERMINAL_PROMPT=0`, `credentials_needed`,
no-remote no-op); `.ledger.toml` with a remote NAME never a URL;
read-time freshness warnings outside the projection; the secrets
runbook; lifecycle-across-merges anomaly flags. Nothing here changes
any of it.

## Addition 1 — The total order, stated as an algorithm

The parent pins the order ("topological, timestamp-tiebroken,
SHA-tiebroken last") per delivery batch. This revision makes it THE
fold order, global, and states it as an algorithm because a comparator
phrasing invites `sort.Slice`, and a pairwise ancestry/timestamp
comparator is not transitive under clock skew (round-1 falsification):

**Kahn's topological sort over the event DAG; the ready set is a
min-heap keyed on (event timestamp, commit SHA).** Ancestry is
structurally guaranteed; skew can reorder only genuinely concurrent
events; ties cannot exist past the SHA key. Every read surface folds in
this order; `Events()` walks with `--topo-order` (its current
`git log --reverse` emits date order, verified wrong on merged DAGs)
and keeps the parent's two-subprocess batching. Merge growth, chain
shape, and convergence are the parent's mechanics unchanged —
convergence needs no deterministic merge minting (revision 1's
centerpiece, cut entirely: merge commits carry no events, so merge
SHAs never appear in any projection, and the remote ref is the
serialization point).

Fold-order consequences the implementation must honor, named:
- Every cursor the tool EMITS is an event id, never a ref tip
  (`watch`'s cold-start cursor currently publishes `HeadID()` — after
  a merge that is a sentinel; it must emit the fold head instead).
- The windowed precondition read (`EventsWindow`, issues rule 8) is
  sound only on linear history — its suffix invariant is
  by-git-log-order, not fold order. A ledger whose walk encounters any
  merge commit falls back to the whole-chain precondition read. Honest
  cost, stated: guarded writes on merged boards pay the full fold;
  the window optimization returns when history is linear again.

## Addition 2 — `contested`: the partition race, fold-derived

The parent already flags causally-unordered lifecycle collisions
(competing closes, competing successor links) by total-order-wins-plus-
anomaly-flag. `contested` extends exactly that pattern to guarded
fields, computed by the fold the way staleness and cycles are — sync
writes no markers; the DAG's own concurrency is the evidence.

- **Definition**: two or more writes to the same GUARDED field of the
  same key, pairwise concurrent (no one an ancestor of another), where
  no later write to that field has ALL of them as ancestors.
- **Entry shape, pinned** (its own shape, like `CycleBreak` — it is a
  self-service ticket, not a stale-claim variant):
  `{"reason": "contested", "key", "field", "title",
    "ids": [...fold order, winner LAST...], "by": [...parallel...],
    "expect": "<the winner's id — the corrective write's CAS ticket>",
    "human": <true iff the key carries the human label>}`
  It joins `attention`, drives `attention-needed` exactly like other
  attention items (subordinate to `work-available` — a contested key is
  in `held`, not `ready`, so a busy board surfaces it via
  `totals.attention`, the standing triage cue).
- **Clearing, ancestry-based**: the entry evaporates when (a) any write
  to that field has all competitors as ancestors — including a
  competitor's own later write — or (b) all competing writes wrote the
  SAME value (the race was real, the outcome identical; nothing to
  resolve). A deliberate dismissal is an ordinary corrective write and
  may legitimately require `--override` under rule 5 (re-asserting a
  terminal value trips `settled`; taking over a claim trips `claim`) —
  the override message is the resolution record, which is rule 5's
  whole point, and the skill says so.
- **Scope, honest**: this guarantee covers guarded fields — which
  exist only on boards that declared them. On plain ledgers and
  unguarded fields (labels included), cross-partition collisions
  resolve by fold order, last write wins, both writes in history,
  nothing flagged: the same stated trade those fields already accept
  on one store, extended. Rule 5's standing signals are likewise
  local-view gates: a partition can admit a state the gate would have
  refused (a key labeled `human` on one replica while claimed on
  another — different fields, not contested). Accepted, stated; the
  triage sweep's override grep and the anomaly flags are the net.
  Extending concurrency detection across field pairs is v2 if the
  field ever shows the need.

Recovery doctrine (one skill paragraph, break-on-sight style): a
contested entry carries its `expect`; the keeper is the fold-order
winner; the loser's work arrives as a `handoff` note per the Recovery
idiom. Trial-1's duplicate-work disease, detected by mechanism.

## Addition 3 — Determinism as a scoped, tested requirement

**Every single-ledger read verb's PROJECTION is a deterministic
function of (chain, evaluation time).** Same chain, same evaluation
time: same bytes, any host, always. Scope words, load-bearing:
projections — store-resolution breadcrumbs, freshness warnings (parent:
outside the projection by law), TTY chrome, and `ls`'s store-wide
listing are outside it.

- One root-level `--at <ts>` (pinned UTC layout) fixes the evaluation
  clock for every verb through `Ctx` — one flag, not N; `out.Age` and
  the envelope's staleness math read it. `--at` moves the CLOCK only;
  it does not time-travel the chain (revision 1's "re-render the board
  as it stood" claim is deleted — that requires chain truncation with
  its own merged-events semantics, deferred until wanted). Omitted
  means now, and now moves.
- **The standing determinism test is perturbed, not a clone-diff**: two
  replicas of the same DAG, converged via DIFFERENT merge orders, read
  under different `TZ`, `LC_ALL`, `HOME`, and user, stdout a pipe,
  fixed `--at` — every single-ledger read verb's projection byte-diffed.
  (Revision 1's clone-and-diff was verified vacuous: a plain clone
  carries no `refs/ledger/*` at all.)

## Implementation scope (tool rev 15)

Fold order → Kahn+(ts,SHA) in `Events()` (`--topo-order`); merged-
history fallback for `EventsWindow` preconditions; `watch` cold-start
cursor = fold head, never ref tip; `contested` in fold + envelope +
skill paragraph; root `--at` threaded through `Ctx` into `out.Age` and
`Envelope`; the perturbed determinism test; the sync/push doctrine
lines in the issues skill section (sync at sit-down, selective push at
checkpoints — the parent quickstart's existing rules, cross-referenced
not duplicated). Everything else in the parent's sync section is
already specified there and implemented per its own plan.

## Test plan (delta to the parent's)

1. Fold order: hand-built merged DAGs (skewed clocks, late-dated roots,
   criss-cross merges) fold identically on replicas built in different
   merge orders; ancestry never violated; the round-1 three-cycle
   counterexample (fast-clock ancestor) linearizes deterministically.
2. `contested`: same-field concurrent guarded writes → identical entry
   (ids fold-ordered, winner last, valid `expect`) on both replicas;
   cleared by (a) superseding write and (b) same-value auto-clear;
   dismissal requiring `--override` records it; subordinate to
   work-available; absent for sequential-with-sync writes; absent on
   unguarded fields.
3. Signals under partition: the human-label-vs-claim cross-field case
   lands unflagged (the stated accepted limit) — pinned as a test so
   the limit is a decision, not an accident.
4. `EventsWindow` fallback: a merged ledger's guarded write uses the
   whole-chain read (instrumented, per the existing byte-count tests);
   linear ledgers keep the window.
5. `watch`/`since` across a merge: cursors remain valid (range
   semantics, parent's law); cold-start cursor is an event id; merged-
   in events below a consumed cursor still deliver exactly once.
6. Determinism: the perturbed two-replica byte-diff, all single-ledger
   read verbs, fixed `--at`; `--at` with events newer than it renders
   sane non-negative ages (clock semantics only).
7. Parent-section regressions exercised through the new fold: sentinel
   merges invisible to reads; same-root refusal; remote-only adoption;
   appender-vs-syncer CAS; selective push; per-slug partial exit 3.

## Trial plan

Two working directories, one bare remote; fleet agents on both sides of
a staged partition claim, close, label, and break cycles offline; sync;
audit: exactly one keeper per contested key, contested entries
byte-identical across replicas, IDs stable, and the full read-verb
byte-diff under perturbed environments.

## Validation record

- Revision 1 was reviewed by two competing adversarial reviewers
  (both opus). Convergent Criticals, all probed against git 2.50.1:
  the fetch refspec (`refs/ledger/*:refs/ledger/*`) is rejected by git
  in exactly the divergence case sync exists for, and its forced form
  destroys local events; bare `--force-with-lease` rejects every push
  absent tracking refs, and is unnecessary (every push is a
  fast-forward by construction — the parent's non-force push IS the
  CAS); the "ancestry first; concurrent by ts" rule is a non-transitive
  comparator, cycling under ordinary skew; the "deterministic" merge
  SHA varies with the ambient timezone (probed: three TZs, three SHAs)
  and four further unpinned byte inputs. One reviewer additionally
  showed "empty tree delta" has two readings, one of which folds the
  merge as a duplicated event (probed), that wildcard push publishes
  never-named ledgers (probed), that a fresh clone could never adopt a
  remote ledger, and that the clone-diff determinism test was vacuous
  (probed). Both independently identified the deepest defect: revision
  1 silently reversed eight decisions of its own parent's hardened
  sync section. Revision 2 is the corrective: the parent stands; this
  document is additions only; the deterministic merge minting, the
  lease push, the per-verb `--at` flags, and the audit time-travel
  claim are all cut.
