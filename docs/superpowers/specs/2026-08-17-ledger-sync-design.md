# Ledger sync: activation and the partition contract (design)

2026-08-17, revision 4 — revision 3 SURVIVED its spike and trial: a
full throwaway implementation (branch `spike/sync-rev3`, kept as
reference for the production build) passed the smoke, the existing
suite, and the two-replica partition trial end to end — every audit in
the Trial plan came back green on the first run. Rev 4 changes no
architecture; it pins the decisions the spike had to make that rev 3
left open, states two hazards the spike found that rev 3 missed, and
folds in the trial's usability findings. History: Validation record,
bottom.

Rev 3 history: revision 2 survived its structure and lost most of its
sentences — a second adversarial round (two reviewers, both probing
git and the shipped tree) falsified the cursor rule, the `--at` scope,
the contested clearing rules, three membership claims, and the window
fallback's story. Revision 3 kept rev 2's two sound decisions — the
parent's sync section stands; determinism is a scoped, tested
requirement — and rebuilt the additions on what the probes showed.

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
- **Paged delivery re-delivers, never loses** (spike finding, rev 4):
  a `--limit`-truncated drain must emit the LAST DELIVERED event as its
  cursor, not the tip — the tip would silently drop the remainder. On
  merged history that event's concurrent siblings are not its
  ancestors, so the next page may re-deliver them. Pinned as documented
  behavior: duplicates possible under paging on merged chains, loss
  never; consumers are idempotent by event id, which is already
  doctrine.
- **Batch order pinned** (rev 4): a range's events are ordered by the
  GLOBAL fold order restricted to the range, not by folding the range
  in isolation — rev 3 permitted either; the global-restricted form is
  simpler and makes `since` agree with `tail`.

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
  the override message doubles as the resolution note. Pinned by the
  spike and trial (rev 4): (a) the field is a JSON ARRAY of losing
  event ids (the spike's comma-joined string is a spike-ism); (b) the
  `set` response ECHOES `contested_resolved` — a writer must be able to
  see they just resolved a contest, especially the unwitting
  touch-base case; (c) the TTY render shows a resolution marker on the
  event, same mandatory-labeling class as `override:` — the spike's
  JSON-only visibility fails the reader the record exists for.
- **Same-value collapses go through the settled gate, stated** (trial
  finding): resolving a contested terminal write re-asserts a settled
  value, so the corrective write trips `needs_override` and the
  resolution path is `--expect <ticket's expect> --override` with the
  message doing double duty — the trial's recovery agent walked this
  correctly, but only after finding that the quickstart's
  `needs_override` doctrine line over-narrows ("a human labeled this,
  walk away" — false for `settled`/`claim` trips). The quickstart line
  is REWORDED in the production build: name all three signal sources
  and point the settled/claim cases at the revise-a-settled-outcome
  idiom rather than at walking away.
- **Two-root key collisions render honestly** (trial finding): when
  contested heads descend from different seed events, the key may hold
  two genuinely different tasks (the trial's colliding `task-signup`
  seeds). The entry's `title` is pinned as the fold-winner head's
  title, and the skill's contested-recovery line carries the doctrine:
  before collapsing, read both heads — a seed collision can hide two
  distinct tasks under one key, and collapsing adjudicates only the
  field value, not the identity; renaming/splitting is a human call.
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
  (pinned). There is no time-travel rendering in v1. Write-verb
  rejection may ride the flag's simple absence (unknown flag ⇒
  `bad_usage`), spike-validated; a pointed "the write clock cannot be
  moved" hint is optional polish, not required machinery.
- **Age clamping is a GENERAL rule, not an `--at` rule** (spike
  finding, reachable with no `--at` at all): a peer host whose clock
  runs AHEAD syncs in events with future timestamps, and unclamped
  rendering produced `age: "-58099h1m24s"`. Every age/staleness render
  clamps at zero, everywhere, under both clocks.
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

## Pins from the spike (rev 4 — decisions the spike had to make)

- **Remote resolution order**: `--remote` > the breadcrumb's remote
  name > `origin` > the sole configured remote > clean no-op. The
  parent names the breadcrumb's optional remote but never ordered the
  fallbacks.
- **Push exit code**: exit 3 covers all-failed as well as partial —
  one code means "read the per-slug outcomes", and the outcomes
  payload is always written before exit.
- **First-adoption root trust, stated gap**: adoption has no local
  chain to check the remote's root against; a hand-crafted
  multi-rooted remote chain is caught by the same-root rule on every
  SUBSEQUENT sync, never the first adoption. Stating this is cheaper
  than machinery; v2 if the field shows the need.
- **Production-build notes, named so they're decisions**: push is
  batched (the spike's per-slug subprocess is dozens of round trips at
  fleet scale); `ready` folds ONCE with ancestor bitsets threaded
  through resolution (the spike folds twice). The clock funnel
  (`model.Now()`) stays, but the spike's `LEDGER_TIME_OFFSET` env
  override is TRIAL INFRASTRUCTURE ONLY — a released binary must never
  let an env var move the clock that stamps immutable events; the
  production test seam is internal.
- **Quickstart budget 110 → 120 lines**: two new verbs plus the sync
  habit, selective push, skew-vs-horizon, and contested-recovery
  doctrine don't fit the round-6 budget; the verb-coverage guard test
  caught this, which is what it's for.

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
recovery incl. the read-both-heads seed-collision doctrine); the
quickstart `needs_override` rewording. The spike branch
`spike/sync-rev3` implements all of this except the rev 4 pins
(array-shaped `contested_resolved` + its response echo and TTY marker,
batched push, single-fold `ready`, no clock env var) and is reference
material for the production build, not a base to merge.

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
- Rev 3 spike + trial (branch `spike/sync-rev3`, opus builder; full
  scope built including the range conversion; existing suite green —
  the fold rewrite touches every read path, so that is the
  load-bearing regression signal). Two-replica trial: bare remote,
  six-agent mixed-model partition fleet, side B 3h behind, then a
  sonnet recovery agent. Every Trial-plan audit passed first run:
  six contested (key,field) entries each with a valid `expect`
  (same-value closes flagged, twin cycle-breaks flagged, two-root
  seed collision flagged, cross-field human-vs-claim correctly
  UNFLAGGED per the stated limit); exactly one keeper per key after
  recovery with `contested_resolved` durable in the chain; exactly
  one sentinel merge per divergence, zero growth on idle syncs;
  projections byte-identical across replicas under perturbed
  TZ/LC_ALL at fixed `--at`; adoption bootstrapped the skewed
  replica and re-validated declarations. Fleet behavior: the human
  label held (an agent hit `needs_override` and walked away), claim
  races resolved by CAS with clean `claim_lost` handling, both
  cycle-breakers followed the break ticket literally, and the
  recovery agent collapsed all six contests using the tickets'
  `expect` values. Rev 4's changes all trace to spike/trial findings:
  paged-cursor re-delivery, general age clamp, `contested_resolved`
  shape/echo/render, the settled-gate resolution path and quickstart
  rewording, seed-collision title doctrine, and the pins section.
