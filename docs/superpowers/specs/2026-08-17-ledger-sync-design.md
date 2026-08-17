# Ledger sync: activation and the partition contract (design)

2026-08-17, revision 5 — revision 4's own adversarial round (two
reviewers probing the spike binary directly) falsified four of its
pins: the paged-cursor rule LIVELOCKS on merged chains, the
batch-order pin silently amended the parent, the title pin
contradicted the issues spec's immutable-title law, and the
first-adoption root-trust sentence was factually wrong (the same-root
check is an intersection test — a grafted multi-root chain passes
every sync). Rev 5 is the corrective; the architecture is still rev
3's, which survived the spike and trial intact. History: Validation
record, bottom.

Rev 4 history: revision 3 SURVIVED its spike and trial — a full
throwaway implementation (branch `spike/sync-rev3`, kept as reference
for the production build) passed the smoke, the existing suite, and
the two-replica partition trial end to end. Rev 4 pinned the
decisions the spike had to make and folded in the trial's usability
findings.

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
genuinely concurrent events. Three skew consequences are accepted and
named: (a) cross-host last-write-wins between concurrent unguarded
writes follows the writers' clocks; (b) a peer whose clock runs far
behind writes claims that are born stale, which the Reclaim idiom then
treats as sanctioned; (c) the mirror case (rev 5, probed): a peer
whose clock runs AHEAD writes claims the slower replica renders as
age-clamped `0s` — indistinguishable from fresh — so that replica
cannot see them go stale, and cannot reclaim, until its own clock
passes the claim's timestamp; the clamp keeps the number honest but
makes this anomaly silent. **Board horizons MUST exceed expected
inter-host skew, in both directions** (skill line), the trial stages a
skewed-clock host, and born-stale/born-future anomaly flagging is v2.

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
  semantics — ancestry validity, `rev-list cursor..tip` delivery.
- **The cursor-emission law, one rule** (rev 5, replacing rev 4's
  contradictory pair): an UNPAGED drain emits the tip it drained
  against — always, sentinel included. A `--limit`-truncated drain
  emits the last delivered event, WHICH IS ONLY SOUND ON LINEAR
  HISTORY (there the delivered prefix is exactly the cursor's
  ancestry). **On a chain containing any merge, `--limit` is refused
  with `bad_usage`** ("paging is unsupported on merged history — drain
  without --limit"), using Addition 5's existing load-time merge
  check. Probed rationale (rev 4's "last delivered, duplicates
  possible, loss never" rule is falsified, not merely imprecise): on a
  merged chain the emitted cursor is a DAG node, the next range
  re-admits every delivered non-ancestor, and the cursor moves
  BACKWARD — the pager oscillates between two cursors forever and the
  chain's tail is never delivered. Loss never was false; the truth was
  livelock. Frontier-shaped cursors that page merged history are v2.
  `watch` has no `--limit` in v1 and always emits the tip; the parent
  spec's watch batch-bound sentence is v2 scope with it.
- **Batch order is the parent's law, restated** (rev 5, retracting rev
  4's pin): the range's events are ordered within the batch
  topologically, timestamp-tiebroken, SHA-tiebroken last — computed on
  the range, exactly as the parent states. Rev 4 pinned
  global-fold-restricted order instead; probed, that silently amends
  the parent (the global heap lets an out-of-range ancestor's
  timestamp reorder in-range concurrent events) and contradicted the
  retained rev 3 bullet above. The spike implements the retracted
  form; the production build implements the parent's — a named delta,
  and the "since agrees with tail on ranges" property is explicitly
  NOT promised (the divergence consequence stated above stands).

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
- **Two-root key collisions** (trial finding; rev 5 retracts rev 4's
  title pin): when contested heads descend from different seed events,
  the key may hold two genuinely different tasks (the trial's
  colliding `task-signup` seeds). The entry's `title` is the KEY's
  title under the issues spec's unamended law — the first status
  event's message in fold order, immutable, identical in every
  projection (rev 4 pinned "the fold-winner head's title"; probed,
  heads are status writes carrying no title, so the pin either
  demanded seed-walking machinery no spec defines or made one key
  render two titles across an envelope — both wrong; what the trial
  observed was the immutable-title law working as designed). The
  hazard is covered by doctrine plus one mechanism: the skill's
  contested-recovery line says read both heads before collapsing — a
  seed collision can hide two distinct tasks under one key, and
  collapsing adjudicates only the field value, never the identity;
  renaming/splitting is a human call — and the work order adds
  **`show --id <sha>`** (render one event with provenance), because
  the contested and break tickets hand agents bare ids and v1
  otherwise has no read path for an id (rev 5, probed gap).
- **Contested machinery is conditioned on merges** (rev 5): `|heads| >
  1` requires two writes neither an ancestor of the other, which a
  linear chain cannot contain — so boards whose chain has no merge
  (Addition 5's load-time check) skip ancestor bitsets entirely.
  Probed cost of not conditioning: bitsets are n²/8 bytes — 3MB at
  the parent's 5k scale but ~50MB at 20k and ~312MB at the parent's
  named heavy-year 50k, paid on every `ready` of every linear board
  for contests that cannot exist there.
- **Scope, honest**: ready-capable boards only — a plain board's
  `--guard` buys CAS, and its cross-replica races resolve by fold
  order, last write wins, unflagged (it has no envelope to carry the
  flag). Unguarded fields everywhere: same. Rule 5's signals are
  local-view gates; a partition can admit cross-field states the gate
  would have refused (human-labeled on one replica, claimed on the
  other) — accepted, stated, v2 if the field shows the need.
  Idempotency dedupe is also local-view: the same idempotency key
  landed on two replicas survives sync as two events — accepted,
  stated. The first defense for all of it is the SYNC HABIT (skill
  line), stated honestly (rev 5): the freshness warning compares the
  local ref against the last-FETCHED tracking ref, which moves only
  when sync or push contacts the remote — reads never touch the
  network, by design, so a partitioned replica renders a clean board
  with no warning (probed). The warning is the second net — it
  catches fetched-but-unmerged state — and it EXTENDS TO `ready`
  (stderr + a `--json` field outside the envelope), since `ready` is
  the read the picking loop actually uses.
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
  finding): future-timestamped events reach a reader two ways — `--at`
  fixed in the past (the spike's `-58099h1m24s` came from this case;
  rev 4 misattributed the number, rev 5 corrects the record) and a
  peer host whose clock runs ahead, which needs no `--at` at all and
  yields skew-sized negatives. Every age/staleness render clamps at
  zero, everywhere, under both clocks — and the clamp's silence on
  the clock-ahead reclaim case is stated in Addition 1(c).
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
retry re-loads). Cost, restated honestly (rev 5 — rev 4's framing was
probed and found misattributed): guarded-write wall time on a 5k
board measured IDENTICAL linear vs merged (~0.4s), because rule 5's
key-scoped label check already walks to the root for never-labeled
keys — the common case — even on linear boards; the ~35KB windowed
figure applies only to keys whose fields all resolve inside the
64-event window. What the merge fallback actually changes is the
GUARANTEE CLASS, and that amends the issues spec explicitly (rev 5,
flagged as an amendment): rule 8's "cost scales with the target key's
touched-history depth, never the whole board's event count" holds for
linear boards only; a merged board's guarded writes and `ready` are
full-fold class, with the acceptance number **merged 5k-event `ready`
with contested entries present ≤ 350ms median-of-3** (same
methodology as the issues spec's 140ms bound; the spike measured
~270ms, so this is a bound, not an aspiration). A fold-order-aware
suffix predicate that restores windowing on merged boards is a named
v2 optimization, not v1 scope.

## Pins from the spike (rev 4 — decisions the spike had to make)

- **Remote resolution order**: `--remote` > the breadcrumb's remote
  name > `origin` > the sole configured remote. The parent names the
  breadcrumb's optional remote but never ordered the fallbacks. Rev 5
  splits the terminal case (probed — the spike's literal chain made a
  two-remote/no-origin repo an exit-0 "no git remote configured"
  no-op at the checkpoint push, asserting the opposite of the truth):
  ZERO remotes ⇒ the parent's clean no-op with message; two or more
  remotes and nothing selects one ⇒ `bad_usage` naming the candidates,
  same shape as `--remote <unknown>`.
- **Push exit code and envelope**: exit 3 covers all-failed as well as
  partial — one code means "read the per-slug outcomes", and the
  outcomes payload is always written before exit. Rev 5 pins the
  envelope's discriminator (probed gap: the spike emitted `ok: true`
  with exit 3): `ok` is true iff every slug pushed; any failure ⇒
  `ok: false` with the per-slug outcomes — a consumer keying on `ok`
  must never read a failed push as success.
- **Multi-root chains are refused at the sync gate** (rev 5, replacing
  rev 4's factually-wrong pin): rev 4 claimed a hand-crafted
  multi-rooted remote chain "is caught by the same-root rule on every
  subsequent sync" — probed FALSE. The same-root check is an
  intersection test, so a grafted chain that retains the legitimate
  root passes every sync forever, folding in foreign events under a
  foreign creator and letting the foreign `meta.json` capture the
  ledger's identity (probed end to end: `commit-tree` graft on the
  bare remote, clean `merged` result, foreign scope rendered). The
  fix is one cheap check with the data already in hand: the fold's
  log parse knows the root set; **sync and adoption refuse to move or
  create the local ref when the candidate chain has more than one
  root**, naming both roots and their creators. Push access already
  grants event-writing; the refusal keeps it from granting identity
  capture. The runbook exit is the same-root rule's existing
  export/import path.
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
recovery incl. the read-both-heads seed-collision doctrine);
`show --id`; the quickstart `needs_override` rewording. The spike
branch `spike/sync-rev3` is reference material for the production
build, not a base to merge, and the delta list is longer than rev 4
claimed (rev 5, probed): beyond the rev 4 pins (array-shaped
`contested_resolved` + response echo and TTY marker, batched push,
single-fold `ready`, no clock env var), the spike also lacks every
skill line (`skills/using-ledger/SKILL.md` untouched — its quickstart
even points contested recovery at a skill section that doesn't
exist), the quickstart `needs_override` rewording, `show --id`, the
merged-history `--limit` refusal (it implements the livelocking rev 4
rule), the parent's range-local batch order (it implements the
retracted global-restricted order), the multi-root sync refusal, the
ambiguous-remote `bad_usage`, the `ok:false` push envelope, and the
merge-conditioned bitsets.

## Test plan (delta to the parent's)

1. Fold order: merged DAGs with skewed clocks, late-dated roots,
   criss-cross merges, mixed ts layouts, an unparseable ts; identical
   fold on replicas built in different merge orders; sentinels never
   affect real-event order (contraction test: same events, sentinel ts
   varied wildly, fold unchanged).
2. Cursors: post-merge cold-start watch delivers nothing until a new
   event; UNPAGED drain-emitted cursors are tips; merged-in events
   below a consumed cursor deliver exactly once; a fold-head cursor
   is VALID (it is an ancestor) and re-delivers the other branch —
   pinned as documented behavior so nobody mistakes it for loss;
   `reset_required` on non-ancestors; `--limit` pages a LINEAR chain
   to completion (union of pages = unpaged drain, last-delivered
   cursors) and is refused `bad_usage` on a merged chain — the
   refusal test's fixture is the probed livelock DAG, so removing the
   refusal fails loudly; batch order within a range is the parent's
   (topological, ts, SHA computed on the range — fixture where
   global-restricted order differs, asserting the parent's order).
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
   window; the load-time merge check adds no per-attempt subprocess;
   linear boards build NO ancestor bitsets (instrumented allocation);
   merged 5k `ready` with contested entries ≤ 350ms median-of-3.
7. `--at`: read verbs honor it (pinned `0s` future-age); write verbs
   reject `bad_usage`; `watch` timeout unaffected.
8. Determinism: the perturbed both-sinks byte-diff over the covered
   verbs; freshness warnings and cross-slug lines verified OUTSIDE the
   diffed projection.
9. `ready` freshness: a stale replica's `ready` warns on stderr and in
   the `--json` side-channel; the envelope bytes are unchanged by the
   warning.
10. Skew, both directions: a host 3h behind writes a claim on a
    2h-horizon board — born stale, reclaimable; a host 3h AHEAD
    writes a claim the true-clock replica renders clamped `0s` and
    cannot reclaim until real time passes the claim ts (documented
    hazards demonstrated, both-directions horizon doctrine line
    verified present in skill).
11. Multi-root refusal: a `commit-tree`-grafted remote chain
    retaining the legitimate root is refused by sync AND by first
    adoption, naming both roots and creators; the legitimate
    single-root chain still syncs.
12. Remote ambiguity: zero remotes ⇒ clean no-op with message; two
    remotes, no origin/breadcrumb/flag ⇒ `bad_usage` naming both;
    breadcrumb and `--remote` each resolve it.
13. Push envelope: all-failed and partial pushes carry `ok: false`
    with per-slug outcomes and exit 3; all-ok carries `ok: true`,
    exit 0.
14. `show --id`: renders exactly the named event with provenance for
    a contested ticket's loser id; unknown id errors cleanly.

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
  `expect` values — including task-signup's field collapse, while
  explicitly declining the rename/split identity call and flagging it
  for a human. Rev 4's changes all traced to spike/trial findings.
- Rev 4 adversarial round (two opus reviewers probing the spike
  binary, hand-built DAGs, and multi-replica worlds; 14 vs 8
  legitimate findings, zero disqualified). Four rev 4 pins
  falsified: the paged-cursor rule LIVELOCKS on merged chains (the
  cursor moves backward and the tail is never delivered — "loss
  never" was false; one reviewer probed it while the other blessed
  the rule as sound, the round's cautionary datum); the batch-order
  pin silently amended the parent's delivery law and contradicted
  the retained rev 3 bullet; the title pin contradicted the issues
  spec's immutable-title law and named a value status-write heads
  don't carry; the first-adoption root-trust sentence was backwards
  — the same-root intersection test passes a grafted multi-root
  chain on EVERY sync, foreign-meta identity capture included. Also
  probed: ambiguous multi-remote repos silently no-op'd at exit 0;
  the freshness warning cannot fire on a partitioned replica (it
  reads last-fetched tracking state); the merged-history cost story
  misattributed a cost the common guarded write already pays;
  issues rule 8 was voided for merged boards with no replacement
  number; the clamp hides the clock-ahead reclaim failure; the
  `-58099h` figure came from `--at`, not peer skew; the spike-delta
  list was understated; `ok: true` rode exit-3 pushes; bitsets were
  unconditioned (quadratic on linear boards); no read path existed
  for the ids the tickets hand out. Rev 5 is the corrective: the
  one-rule cursor law with linear-only paging, the parent's batch
  order restored, the key-title law restored + `show --id`, the
  multi-root sync/adoption refusal, split remote fallback terminal,
  `ok:false` push envelope, merge-conditioned bitsets, honest
  freshness/cost/skew wording, and the rule-8 amendment with a
  named 350ms bound.
