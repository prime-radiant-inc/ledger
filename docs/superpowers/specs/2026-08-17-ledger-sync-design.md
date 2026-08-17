# Ledger sync: activation and the partition contract (design)

2026-08-17, revision 3 — revision 2 survived its structure and lost
most of its sentences: a second adversarial round (two reviewers, both
probing git and the shipped tree) falsified the cursor rule, the `--at`
scope, the contested clearing rules, three membership claims, and the
window fallback's story. Revision 3 keeps rev 2's two sound decisions —
the parent's sync section stands; determinism is a scoped, tested
requirement — and rebuilds the additions on what the probes showed.
History: Validation record, bottom.

**The parent tool spec's "Sync and push" section
(`2026-08-13-ledger-tool-design.md`) stands IN FULL and unamended. It
is the sync design — and it is SPECIFIED, NOT YET IMPLEMENTED: no
`sync`/`push` verb, tracking namespace, or refspec install exists in
the tree today. Tool rev 15 is one work order: the parent's sync
section plus the additions below.** The issues layer
(`2026-08-15-ledger-issues-design.md`, rev 17) is normative for
everything board-shaped. Operator requirements: offline-first (reduced
guarantees accepted); simple to understand; identical output on any
host reading the same synced state.

## Addition 1 — The fold order

**Kahn's topological sort over the event DAG; the ready set is a
min-heap keyed on (event timestamp, commit SHA).** Pinned details, each
one a falsified-ambiguity repair:

- The read shape: one `git log --format=%H%x09%P` (traversal order
  irrelevant — `--topo-order` and `--date-order` are both
  merge-parent-dependent, probed) plus the existing one
  `cat-file --batch`; Kahn runs in-process on the parsed events. The
  parent's two-subprocess batching constraint holds.
- **Sentinel sync merges are contracted out of the DAG before the
  sort** (parents spliced to children). A sentinel therefore never
  delays or reorders real events — without this, the syncing host's
  clock decides last-write-wins outcomes between other hosts' writes
  (probed).
- Heap keys: the PARSED timestamp (legacy and millisecond layouts
  compare as times, never as strings); ties by FULL 40-char commit
  SHA. An event with a missing or unparseable `ts` sorts after all
  timestamped peers, by SHA.
- `Head()` (the fold head) is a display fact, not a cursor — see
  Addition 2.

Skew, stated: ancestry is structural and immune; skew reorders only
genuinely concurrent events. Two skew consequences are accepted and
named: (a) cross-host last-write-wins between concurrent unguarded
writes follows the writers' clocks; (b) a peer whose clock runs far
behind writes claims that are born stale, which the Reclaim idiom then
treats as sanctioned — **board horizons MUST exceed expected inter-host
skew** (skill line), the trial stages a skewed-clock host, and
born-stale anomaly flagging is v2.

## Addition 2 — Cursors under merges

Revision 2 had this exactly backwards; the probes settle it:

- **A cursor is a reachability token against the ref, never a fold
  position.** Validity: the cursor is an ancestor of (or equal to) the
  ref tip (`merge-base --is-ancestor`); otherwise `reset_required`.
  Delivery: the non-sentinel commits in `cursor..tip` — the parent's
  set-based law, which after a merge delivers merged-in events sitting
  fold-BELOW a consumed cursor exactly once.
- **"From now" is the ref tip, sentinel included** — the sentinel is
  the only commit whose pending range is empty after a merge, and it is
  already a legal cursor ("a cursor may legitimately land on a sync
  sentinel", shipped comment). The resume cursor a drain emits is the
  tip it drained against. Emitting the fold head instead (rev 2's rule)
  re-delivers the other branch forever, probed.
- **Batch order is local to the range**, per the parent (topological,
  ts, SHA within the batch). Stated consequence (probed divergence): a
  range fold can order two events concurrent with the cursor
  differently than the global fold renders them. Replaying consumers
  converge on the SET of events; state-rebuilders use `status`/`tail`
  (the parent's recovery doctrine), not a replayed stream.
- Implementation scope (the largest single item, previously unnamed):
  convert `since`/`watch` from positional slice arithmetic
  (`indexOf` + `Events[idx+1:]`, silently lossy under merges) to range
  semantics — ancestry validity, `rev-list cursor..tip` delivery — and
  make every emitted cursor the tip-at-drain.

## Addition 3 — `contested`: the partition race, fold-derived

One definition (both reviewers independently converged on it):

**For each (key, guarded field) on a ready-capable board, compute the
write-heads: the writes to that field with no descendant write to that
field. `|heads| > 1` is contested — one attention entry per
(key, field).** The winner is the fold-order-last head; `expect` is the
winner's id, which is the field's latest event by construction — a
valid CAS ticket always. No separate clearing rules: any write to the
field collapses the heads to one (the definition clears itself), and
there is NO same-value auto-clear — two concurrent claims or closes
carrying the same value are precisely the duplicate-work disease this
exists to flag (rev-2 rule cut, probed rationale).

- **Entry shape** (nested ticket, mirroring `break`; flat fields keep
  the existing envelope types):
  `{"reason": "contested", "key", "title" (omitted when the key is
  statusless), "contest": {"field", "ids": [fold order, winner last],
  "authors": [parallel], "expect", "human": <key carries the label>}}`.
  Attention sort gains the tiebreak `(key, reason, field)`.
- **Membership is unchanged and visible at the point of decision**: a
  contested key keeps its ordinary list placement (it can be in
  `ready` — e.g. concurrent seeds — or `held` or none), and any
  `ready`/`held`/`blocked` entry for a key with a live contest carries
  `"contested": true`. Doctrine: claiming or closing a flagged entry
  names the contest in the message, like `unblocked_without_evidence`.
  The attention entry and `totals.attention` ride in every envelope
  regardless of verdict.
- **Resolution leaves a durable record**: the write that collapses the
  heads gets `contested_resolved: <losing ids>` recorded on its event —
  tool-computed, like `override:`, greppable forever — whether the
  writer knew of the contest or not (a routine touch-base that resolves
  a contest still records it). Rule 5 is unchanged: no new standing
  signal; where the corrective write trips `claim`/`settled`/`human`,
  the override message doubles as the resolution note.
- **Scope, honest**: ready-capable boards only — a plain board's
  `--guard` buys CAS, and its cross-replica races resolve by fold
  order, last write wins, unflagged (it has no envelope to carry the
  flag). Unguarded fields everywhere: same. Rule 5's signals are
  local-view gates; a partition can admit cross-field states the gate
  would have refused (human-labeled on one replica, claimed on the
  other) — accepted, stated, v2 if the field shows the need.
  Idempotency dedupe is also local-view: the same idempotency key
  landed on two replicas survives sync as two events — accepted,
  stated. Freshness is the first defense for all of it: the parent's
  read-time freshness warning ("N unmerged remote events — run
  `ledger sync`") EXTENDS TO `ready` (stderr + a `--json` field outside
  the envelope), since `ready` is the read the picking loop actually
  uses.
- **Machinery, honestly priced**: contested needs pairwise ancestry.
  Parents flow through `Events()`; the fold accumulates ancestor
  bitsets during the Kahn pass (≈3MB at the parent's 5k scale);
  `board.Build` consumes them. The issues spec's 140ms `ready` bound
  is re-measured on a merged board with contested entries present —
  a named acceptance number, not an assumption.

Adoption (the parent's remote-only CAS-create) is a third meta-minting
path and therefore RE-VALIDATES declarations exactly as import does —
a board arriving by sync with a broken ready-capable shape is refused
with the defect named, never minted.

## Addition 4 — Determinism, scoped and tested

**Every single-ledger read verb's PROJECTION is a deterministic
function of (chain, evaluation time).** The projection excludes:
store-resolution breadcrumbs, freshness warnings (parent law: outside
the projection), TTY chrome, `ls`'s store-wide listing, and cross-slug
presence lines (`show`'s superseded-by resolution reads other slugs —
probed; it stays outside the guarantee). Verbs under the guarantee:
`show`, `status`, `tail`, `notes`, `ready`. `since`/`watch` are
excluded — their output is a function of the cursor argument and, for
`watch`, of arrival timing.

- **`--at <ts>`** (millisecond UTC layout; the legacy layout accepted):
  a READ-VERB flag fixing the evaluation clock for age/staleness
  rendering. It is `bad_usage` on every write verb — threading a fake
  clock into rule 5's append-time staleness would let a caller
  dissolve the `claim` signal and skip `needs_override` unrecorded,
  and rule 6 pins append-time staleness to the real clock. `watch`'s
  timeout is a wall-clock duration, unaffected. `--at` moves the clock
  only, never the chain; an event newer than `--at` renders age `0s`
  (pinned). There is no time-travel rendering in v1.
- **The standing determinism test**: hand-built replicas of one event
  set differing in merge structure and merge parent order (the tool's
  own sync mostly precludes divergent merges — probed — so the test
  builds them directly), read under perturbed `TZ`, `LC_ALL`, `HOME`,
  and user, on BOTH sinks (pipe-JSON and forced-TTY render — the TTY
  renderer is where locale/zone bugs live), fixed `--at`, every
  covered verb byte-diffed; plus a fresh-clone re-fold of the same
  refs.

## Addition 5 — Guarded writes on merged history

The windowed precondition read's suffix invariant is git-log-order,
not fold-order, and a window cannot see a merge below itself (probed).
The rule, honestly: **a ledger whose history contains any merge commit
uses whole-chain precondition reads, permanently.** Detection is one
`rev-list --merges --max-count=1` at ledger load — a permanent fact of
immutable history, safe to check pre-loop (a merge can only arrive via
sync's CAS'd ref move; a mid-write sync loses the CAS race and the
retry re-loads). Cost, stated with the tree's own numbers: a guarded
write on a merged 5k-event board pays the full-fold read (~3.9MB
measured) instead of ~35KB windowed; the issues rule-8 bound is
re-pinned for merged boards at the full-fold class. A fold-order-aware
suffix predicate that restores windowing on merged boards is a named
v2 optimization, not v1 scope.

## Implementation scope (tool rev 15, one work order)

The parent's entire Sync-and-push section (verbs, tracking namespace,
refspec repair, same-root rule, adoption + its new declaration
re-validation, sentinel merges, degraded modes, breadcrumb, freshness
warnings — now including `ready`); `Events()` → parents + Kahn fold
with sentinel contraction; `since`/`watch` range-semantics conversion;
`contested` (ancestor bitsets in the fold, envelope entry + per-entry
`contested` flags, `contested_resolved` recording, skill paragraph);
read-verb `--at` with write-verb rejection; the perturbed determinism
test; merged-history precondition fallback + re-pinned bounds; skill
lines (sync habit, selective push, skew-vs-horizon, contested
recovery).

## Test plan (delta to the parent's)

1. Fold order: merged DAGs with skewed clocks, late-dated roots,
   criss-cross merges, mixed ts layouts, an unparseable ts; identical
   fold on replicas built in different merge orders; sentinels never
   affect real-event order (contraction test: same events, sentinel ts
   varied wildly, fold unchanged).
2. Cursors: post-merge cold-start watch delivers nothing until a new
   event; drain-emitted cursors are tips; merged-in events below a
   consumed cursor deliver exactly once; a fold-head cursor is VALID
   (it is an ancestor) and re-delivers the other branch — pinned as
   documented behavior so nobody mistakes it for loss;
   `reset_required` on non-ancestors.
3. `contested`: write-heads definition — two concurrent claims flag;
   claim-then-close per side flags ONCE per field with a valid
   `expect` (the fold-last head); same-value concurrent claims STILL
   flag; any collapsing write clears and records
   `contested_resolved` with the losing ids (touch-base included);
   entries byte-identical across replicas; `(key, reason, field)`
   sort; statusless contested entries omit `title`; per-entry
   `contested: true` flags on ready/held/blocked; plain-board races
   unflagged (pinned as the stated trade).
4. Signals under partition: the human-label-vs-claim cross-field case
   lands unflagged (pinned accepted limit); duplicate idempotency keys
   across replicas both survive (pinned accepted limit).
5. Adoption re-validates: a synced-in board missing `--guard status`
   is refused with the defect named.
6. Merged-history fallback: first merge flips the board to whole-chain
   precondition reads (instrumented bytes); linear boards keep the
   window; the load-time merge check adds no per-attempt subprocess.
7. `--at`: read verbs honor it (pinned `0s` future-age); write verbs
   reject `bad_usage`; `watch` timeout unaffected.
8. Determinism: the perturbed both-sinks byte-diff over the covered
   verbs; freshness warnings and cross-slug lines verified OUTSIDE the
   diffed projection.
9. `ready` freshness: a stale replica's `ready` warns on stderr and in
   the `--json` side-channel; the envelope bytes are unchanged by the
   warning.
10. Skew: a host 3h behind writes a claim on a 2h-horizon board —
    born stale, reclaimable (documented hazard demonstrated, horizon
    doctrine line verified present in skill).

## Trial plan

Two working directories, one bare remote, one deliberately skewed
clock; fleet agents on both sides of a staged partition claim, close,
label, seed colliding keys, and break cycles offline; sync; audit:
every contested (key, field) carries exactly one entry with a valid
`expect`; exactly one keeper per contested key after recovery, with
`contested_resolved` in the chain; envelopes and `show`/`status`/`tail`
byte-identical across replicas under perturbed environments; IDs
stable throughout.

## Validation record

- Rev 1 (two opus reviewers, all Criticals probed): self-rejecting
  fetch refspec; lease-push 100% rejection; non-transitive order
  comparator; TZ-dependent "deterministic" merge SHA; eight silent
  reversals of the parent's hardened sync section. Corrective: the
  parent stands; additions only.
- Rev 2 (two opus reviewers, convergent, probed): the cursor rule was
  backwards (fold-head cursors re-deliver the other branch forever;
  the sentinel tip is the only empty-range cursor); root `--at` on
  write verbs would dissolve rule-5 signals unrecorded; the
  same-value auto-clear silenced the duplicate-claim disease itself;
  "a contested key is in `held`" was false in two of three cases;
  clearing rules let routine touch-bases erase contests without trace;
  `--topo-order` is merge-parent-dependent (probed both orders);
  the window fallback was undetectable below the window and
  "returns when linear again" was false (merges are permanent);
  `since`/`watch` are positional today and the conversion was
  unscheduled; contested needed an ancestry engine priced at five
  words; sentinels as heap participants let the syncer's clock
  reorder other hosts' writes; the parent's freshness warning never
  reached `ready`; adoption was an unvalidated minting path;
  "implemented per its own plan" was false (no sync verb exists).
  Both reviewers independently proposed the write-heads antichain
  definition adopted above. Rev 3 is the corrective; the durable
  `contested_resolved` record and sentinel contraction are its two
  genuinely new mechanisms.
