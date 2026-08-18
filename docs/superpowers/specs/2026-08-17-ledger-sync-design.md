# Ledger sync: activation and the partition contract (design)

2026-08-17, revision 8 — one clause: the standing determinism test, on its first execution, caught a collision between the pager law (tip-at-drain, sentinel included) and since's byte-diff promise — the emitted resume cursor names replica-local sentinel structure and is excluded from the projection; the delivered events are the promise. Below, revision 7 — a second simplification round falsified rev
6's pager law twice (a sentinel tip has no single maximal delivered
element; a branch-local cursor is incomparable with the candidate) and
found the document's structure itself failing its "simple to
understand" requirement: rules stated in multiples, revision
narration displacing law, scope lists disagreeing. Rev 7 repairs the
pager with two clauses (cursor-descent condition; exhaust-and-emit-
tip), states the whole cursor contract in one place, completes the
amendment inventory (four issues-spec amendments, one parent
amendment — the watch bound — now all named), and restates every
multiply-stated rule once. History: Validation record, bottom.

Rev 6 history: a simplification round deleted three rev 5 mechanisms
by replacing them with less — the merged-history `--limit` refusal
(one-rule pager), the n²/8 ancestor bitsets and their
merge-conditioning (width-bounded cover-set pass), and the windowed
precondition read with its linear/merged bifurcation and merge
detector (whole-chain reads, one mode).

Rev 5 history: revision 4's adversarial round falsified four of its
pins — the paged-cursor livelock, the batch-order parent amendment,
the title pin, and the backwards root-trust sentence. The
architecture is still rev 3's, which survived its spike and
two-replica partition trial intact.

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
(`2026-08-13-ledger-tool-design.md`) stands IN FULL, with exactly one
amendment, named in Addition 2: the watch batch bound is deferred to
v2. It is the sync design — and it is SPECIFIED, NOT YET IMPLEMENTED:
no `sync`/`push` verb, tracking namespace, or refspec install exists
in the tree today. Tool rev 15 is one work order: the parent's sync
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
makes this anomaly silent. The doctrine is ASYMMETRIC, because skew
is an offset on the age and the horizon a threshold on it: a
behind-writer's claim ages `+skew` and is born stale whenever skew
exceeds the horizon, so raising the horizon fixes it; an
ahead-writer's claim ages `-skew` and goes stale exactly `skew` LATE
at every horizon setting, so no horizon fixes it (probed at two
horizons). Hence the skill line covers one direction: **board
horizons MUST exceed expected inter-host skew, so claims are not born
stale**; the ahead direction is an accepted v1 hazard whose only
defense is clock discipline. The trial stages a skewed-clock host,
and born-stale/born-future anomaly flagging is v2.

## Addition 2 — Cursors under merges

The whole cursor contract, in one place. (Every superseded formulation
and its falsification lives in the Validation record.)

- **Validity.** A cursor is a reachability token against the ref,
  never a fold position: valid iff it is an ancestor of (or equal to)
  the ref tip (`merge-base --is-ancestor`); otherwise
  `reset_required`.
- **Delivery.** A drain delivers the non-sentinel commits in
  `cursor..tip` — the parent's set-based law, which after a merge
  delivers merged-in events sitting fold-below a consumed cursor
  exactly once.
- **Batch order.** Addition 1's Kahn min-heap — same parsed-ts /
  full-SHA keys, same unparseable-ts rule — restricted to the range's
  sentinel-contracted sub-DAG. This is the parent's "topological,
  timestamp-tiebroken, SHA-tiebroken" order made unique by naming the
  algorithm; a second ordering routine would be a place for replicas
  to disagree. Stated consequence: a range fold can order two events
  concurrent with the cursor differently than the global fold renders
  them — "`since` agrees with `tail` on ranges" is explicitly NOT
  promised. Replaying consumers converge on the SET of events;
  state-rebuilders use `status`/`tail` (the parent's recovery
  doctrine), not a replayed stream.
- **Cursor emission.** An unpaged drain emits the tip it drained
  against — always, sentinel included (after a merge the sentinel is
  the only commit whose pending range is empty, and it is already a
  legal cursor). A `--limit` drain delivers in batch order and ends
  at the first point where BOTH hold: at least `--limit` events are
  delivered, and there is a delivered event C that (i) descends from
  the incoming cursor and (ii) has every delivered event as an
  ancestor-or-equal — maximality judged on the SENTINEL-CONTRACTED
  DAG, where it reduces to "no delivered child" (on the raw DAG the
  parent-edge test miscounts across sentinels). C is the emitted
  cursor; conditions (i)+(ii) make `cursor..C` exactly the delivered
  set, so exactly-once holds page over page and the cursor strictly
  advances. If the range exhausts before such a C exists, the page
  runs to exhaustion and emits the TIP, sentinel included — the same
  rule as unpaged. Two shapes hit that exhaustion clause, both
  probed: a fresh divergence (the tip is a sentinel, whose two branch
  heads are incomparable — no single C exists until real work lands
  above the merge) and an incremental consumer whose cursor sits on
  one branch (nothing in the other branch descends from it; without
  condition (i) the pager re-delivers the consumer's own branch and
  oscillates).
- **`--limit` is a floor, not a ceiling — stated plainly**: a page
  that crosses a divergence delivers the WHOLE concurrent region in
  one page, and the region's size is the partition's event count, not
  the replica count. Bounded paging resumes on the far side of the
  merge. A consumer that cannot afford one region-sized page uses the
  parent's `status` + `tail` recovery instead of the stream — the
  same doctrine as `reset_required`.
- **`watch`.** No `--limit` in v1; every batch emits the tip. This
  DEFERS the parent's "the same bound applies to watch's batch
  delivery" sentence (and its test-31 clause) to v2 — an explicit
  amendment to the parent, the only one this spec makes, named here
  and in the header. Consequence, stated: a cold-start or post-merge
  `watch` batch is unbounded; consumers past harness limits use
  `status` + `tail`. If `watch` gains `--limit`, it uses the pager
  law above.
- Implementation scope (the largest single item): convert
  `since`/`watch` from positional slice arithmetic (`indexOf` +
  `Events[idx+1:]`, silently lossy under merges) to this contract.

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
  **The attention sort, complete and total over every entry kind**
  (the issues spec never defined one, and cycle entries carry `keys`,
  not `key`): entries sort on `(sort_key, reason, field)`, where
  `sort_key` is `key` for keyed entries and the sorted member list
  joined by `,` for cycle entries, and `field` is the empty string
  where absent. Total by construction — the envelope's
  byte-determinism must not rest on an implementation's incidental
  sort stability.
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
  renaming/splitting is a human call. The tickets hand agents bare
  event ids; `show --id` (pinned below) is how they read one.
- **Id reads, both paths pinned**: `show --id <sha>` renders ONE
  event in full — type, key, fields, message, evidence, author with
  provenance marker, timestamp — including note bodies under the
  parent's per-line quoting and control-character escaping (a ticket
  holder following a redirect must never land on less than the
  event); unknown id ⇒ `bad_value` naming it. `notes --id` keeps its
  note-rendering contract; on any id that is NOT a note event of this
  ledger — non-note event and unknown id alike — it errors
  `bad_value` naming the id with a hint pointing at `show --id`,
  never a silent empty list (the parent's "empty results announce
  themselves" rule; probed, the silent empty told a contested-ticket
  holder the event didn't exist).
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
  catches fetched-but-unmerged state — and it EXTENDS TO `ready`,
  since `ready` is the read the picking loop actually uses. Placement,
  stated precisely (the parent's envelope is the WHOLE JSON document,
  so "outside the envelope" was not a place): on a TTY the warning is
  stderr; in JSON it is a single top-level `freshness` sibling key in
  the same document, and `ready`'s PROJECTION — the members the
  issues spec enumerates (`frontier`/`ready`/`held`/`blocked`/
  `attention`/`totals`) — is byte-unchanged by its presence or
  absence. Determinism language everywhere in this spec (test 9, the
  trial audit) binds the projection, not the document.
- **Machinery, honestly priced** (rev 6 — the rev 3–5 design carried
  full n²/8 ancestor bitsets, 3MB at 5k but ~312MB at the parent's
  named heavy-year 50k, plus a merge-conditioning special case that
  exempted only the linear boards where contests cannot exist; both
  are deleted): the write-heads question is descendant-existence, not
  general pairwise ancestry. One reverse-topological pass computes,
  per event, the set of guarded (key, field) pairs written by its
  descendants-or-self; a write is a head iff no child's set contains
  its pair; a node's set is freed once its parents have consumed it,
  so peak residency is DAG width × candidate pairs — and a
  sentinel-merge history's width is the replica count. Probed:
  identical heads to the bitset algorithm on shared fixtures, with
  kilobytes resident where the bitsets held hundreds of MB. `ready`
  runs the board-wide pass once, in its fold. The write path needs
  no board-wide pass: a conditional set touches exactly one guarded
  field (issues rule 2), so `contested_resolved` is the heads of
  that single (key, field) — one descendant-write-exists boolean per
  event, computed from the whole-chain precondition read already in
  hand, inside the CAS retry loop against each attempt's fresh read
  (the same freshness discipline as rule 7's checks; the cost is
  per-attempt and is priced and tested as such). The issues spec's
  `ready` bound is re-measured on a merged board with contested
  entries present — a named acceptance number, not an assumption;
  the 140ms bound still binds merge-free boards.

Adoption (the parent's remote-only CAS-create) is a third meta-minting
path and therefore RE-VALIDATES declarations exactly as import does —
a board arriving by sync with a broken ready-capable shape is refused
with the defect named, never minted.

## Addition 4 — Determinism, scoped and tested

**A covered verb's PROJECTION is a deterministic function of (chain,
evaluation time) — for `since`, of (chain, cursor, evaluation
time).** *Chain* means the set of non-sentinel events reachable from
the ref, independent of sentinel structure and merge-parent order —
the standing test's replicas are literally different commit graphs
holding the same chain, and the guarantee is exactly that they render
identically. One closed list. Covered: `show`, `status`, `tail`,
`notes`, `ready`, `render`, `since`. Excluded, each with its reason:
`watch` (arrival timing), `ls` (store-wide — two clones legitimately
hold different stores), `export` and bare `rollup` (dumps and
indexes, not projections; their determinism is untested in v1). The
projection also excludes: store-resolution breadcrumbs, freshness
warnings (parent law), TTY chrome, cross-slug presence lines
(`show`'s superseded-by resolution reads other slugs — probed; it
stays outside the guarantee), and **`since`'s emitted resume
`cursor` field** (rev 8 — the standing test caught the collision on
its first run: the emitted cursor is the ref tip by the pager law, a
SENTINEL SHA after any divergence, and sentinel structure is
replica-local by this section's own definition of chain; the cursor
is a replica-local resume token, and `since`'s promise is the
delivered events, byte-identical for the same chain and cursor).

- **`--at <ts>`** (millisecond UTC layout; the legacy layout
  accepted): fixes the evaluation clock for age/staleness rendering.
  The complete rule: the flag EXISTS on `ready` and `notes`
  (`--latest` ages) — the only verbs whose covered output depends on
  the clock — and on NO other verb; everywhere else, reads
  (`show`/`status`/`tail`/`since`/`watch`/`render`/`ls`) and writes
  alike, the flag's absence rejects as `bad_usage`. (`show`/`status`/
  `tail` render absolute timestamps per the parent — the flag was
  probed accepted-and-ignored there, the pattern the issues spec
  forbids; on write verbs a fake clock would dissolve rule 5's
  `claim` signal and skip `needs_override` unrecorded, and rule 6
  pins append-time staleness to the real clock.) `watch`'s timeout is
  a wall-clock duration, unaffected. `--at` moves the clock only,
  never the chain; an event newer than `--at` renders age `0s`
  (pinned). There is no time-travel rendering in v1.
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
  renderer is where locale/zone bugs live), every covered verb
  byte-diffed — with fixed `--at` on the verbs that take it, without
  it on the clock-free verbs, `render`'s written file included, and
  `since` at a fixed cursor; plus a fresh-clone re-fold of the same
  refs.

## Addition 5 — Guarded writes on merged history

**The windowed precondition read is DELETED; every guarded write uses
whole-chain precondition reads, unconditionally** (rev 6). Rev 3–5
kept a two-mode design — windowed on linear history, whole-chain
after the first merge, with a load-time merge detector switching
between them. The simplification round probed the window against the
spike's own scaling tests and found it a net LOSS on the common key:
a never-labeled key's rule-5 absence proof walks to the root anyway,
so the window added a wasted 64-event probe (13 subprocess calls vs
11) on top of the whole-chain read it fell back to; the window's
~35KB win existed only for keys whose status AND labels both resolve
inside the newest 64 events, and the issues spec makes labels rare
by design. Deleting it removes the window primitive and its
four-way truncation-correctness surface, the git-log-order-vs-
fold-order suffix subtlety, the linear/merged bifurcation, the
load-time merge detector (whose other consumers rev 6 also deleted),
and the fold-order-aware-window v2 item. Residual cost, stated: the
rare window-winning key's guarded write moves ~4MB instead of ~26KB
— roughly the parent's measured 70ms batched 5k fold on a write
path already costing ~0.4s.

**The complete issues-spec amendment inventory** (this spec amends
its sibling in exactly four places; nothing else in the issues spec
moves):

1. **Rule 8 and its test-16 clause.** Guarded-write precondition
   reads are full-fold class on every board (rule 8's depth-scaling
   claim was already conditional — the issues spec's own "honest
   worst case" names the full-scan degradation — and measured wall
   time at 5k is ~0.4s regardless). Issues test 16's "not a full
   re-fold per retry" clause is retracted with it: the precondition
   read IS the full read, per attempt.
2. **The `ready` bound splits.** The issues spec's 140ms bound still
   binds merge-free boards; a merged board with contested entries
   present is bound at **≤ 350ms median-of-3** (same methodology;
   the spike measured ~270ms, so this is a bound, not an
   aspiration).
3. **Clock scope.** The "boards assume same-host clock coherence;
   multi-host fleets sharing a store are a Plan 2 concern" sentence
   and the Deferred list's cross-machine item are superseded — this
   document is that Plan 2 answer (Addition 1's skew doctrine,
   Addition 4's age clamp).
4. **The attention sort** (Addition 3): the issues spec never
   defined an order for `attention`; the total order pinned there
   now binds it.

(The single amendment to the PARENT spec — the watch batch bound —
is named in Addition 2 and the header.)

## Pins from the spike (rev 4 — decisions the spike had to make)

- **Remote resolution order**: `--remote` > the breadcrumb's remote
  name > `origin` > the sole configured remote. The parent names the
  breadcrumb's optional remote but never ordered the fallbacks. Rev 5
  splits the terminal case (probed — the spike's literal chain made a
  two-remote/no-origin repo an exit-0 "no git remote configured"
  no-op at the checkpoint push, asserting the opposite of the truth):
  ZERO remotes ⇒ the parent's clean no-op with message; two or more
  remotes and nothing selects one ⇒ **`ambiguous_remote`**, exit 4,
  hint = the candidate list plus `--remote <name>` — the parent's
  `ambiguous_ledger` precedent, an identifier whose job is carrying
  candidates. Distinct from `bad_value` (`--remote <unknown>` — a
  name that doesn't exist), so an agent keying on identifiers can
  tell "you named a bad remote" from "you named none and there are
  several".
- **Push and sync outcome envelope** (rev 6 widens rev 5's push-only
  pin — the parent gives sync identical exit-3 semantics and the
  implementation shares the code path): exit 3 covers all-failed as
  well as partial — one code means "read the per-slug outcomes", and
  the outcomes payload is always written before exit. The
  discriminator: `ok` is true iff every slug succeeded; any failure ⇒
  `ok: false` AND `error: "partial_failure"` with a hint pointing at
  the per-slug outcomes array, so the document satisfies the parent's
  error contract (`{error, message, hint}`) instead of being a third
  shape neither success nor error — a consumer keying on either the
  parent's `ok` or its `error` surface reads failure as failure.
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
  capture. The runbook exit is REMOTE-SIDE (rev 6 — rev 5 pointed at
  the same-root rule's export/import path, which is backwards here:
  that exit re-slugs the LOCAL chain out of the way and invites sync
  to adopt the remote one, which for a graft is the poisoned chain —
  and whose final step the refusal itself now blocks): the grafted
  remote ref must be deleted or force-replaced by an admin, the same
  class of human-run ref surgery as the parent's secrets runbook,
  and until that happens the slug is wedged for the whole fleet —
  push is non-force and sync refuses, so no tool operation can
  repair or worsen it. Stated plainly in the refusal error: it names
  the tracking ref (`refs/ledger-remote/<remote>/<slug>`) so the
  operator can inspect the refused chain with plain git; tool-side
  reads of tracking-only refs beyond the parent's `ls` listing are
  v2.
- **Production-build notes, named so they're decisions**: push is
  batched (the spike's per-slug subprocess is dozens of round trips at
  fleet scale); `ready` folds ONCE, with the contested cover-set pass
  running in that fold (the spike folds twice and builds n² bitsets).
  The clock funnel (`model.Now()`) stays, but the spike's
  `LEDGER_TIME_OFFSET` env override is TRIAL INFRASTRUCTURE ONLY — a
  released binary must never let an env var move the clock that
  stamps immutable events; the production test seam is internal.
- **Quickstart budget 110 → 120 lines**: two new verbs plus the sync
  habit, selective push, skew-vs-horizon, and contested-recovery
  doctrine don't fit the round-6 budget; the verb-coverage guard test
  caught this, which is what it's for.

## Implementation scope (tool rev 15, one work order)

The parent's entire Sync-and-push section (verbs, tracking namespace,
refspec repair, same-root rule, adoption + its new declaration
re-validation, sentinel merges, degraded modes, breadcrumb, freshness
warnings — now including `ready`); `Events()` → parents + Kahn fold
with sentinel contraction; `since`/`watch` range-semantics conversion
with the single-maximal-element pager; whole-chain precondition reads
(deleting the window primitive); `contested` (the cover-set pass in
the fold, envelope entry + per-entry `contested` flags,
`contested_resolved` recording on the write path, the total-order
attention sort, skill paragraph); `--at` on `ready`/`notes` only;
the perturbed determinism test incl. `since` and `render`; skill
lines (sync habit, selective push, behind-direction skew doctrine,
contested recovery incl. the read-both-heads seed-collision
doctrine); `show --id` + the `notes --id` non-note announcement; the
quickstart `needs_override` rewording. The spike branch
`spike/sync-rev3` is reference material for the production build,
not a base to merge; its delta list is long — it implements the
rev 4 rules this spec has since replaced (livelocking pager,
global-restricted batch order, n² bitsets with merge conditioning,
windowed preconditions, `--at` on five read verbs where production
has two, `bad_usage`
ambiguity, `ok:true` failure envelopes, comma-joined
`contested_resolved` without echo or TTY marker, per-slug push,
double fold, clock env var) and lacks every skill line, the
quickstart rewording, `show --id`, and the multi-root refusal.

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
   `reset_required` on non-ancestors; `--limit` pages to completion
   (union of pages = unpaged drain, every event exactly once,
   termination) on ALL THREE probed fixtures — a linear chain, the
   rev-4 livelock DAG, and the two exhaustion shapes (sentinel tip
   with incomparable branch heads; incremental cursor on one branch
   of a merge, which must exhaust-and-emit-tip rather than
   re-deliver the consumer's own branch); a page crossing a
   concurrent region delivers the whole region (floor-not-ceiling
   pinned as documented behavior); batch order within a range is
   Addition 1's Kahn heap restricted to the contracted range
   sub-DAG (fixture where global-restricted order differs, asserting
   the range order).
3. `contested`: write-heads definition — two concurrent claims flag;
   claim-then-close per side flags ONCE per field with a valid
   `expect` (the fold-last head); same-value concurrent claims STILL
   flag; any collapsing write clears and records
   `contested_resolved` with the losing ids (touch-base included);
   entries byte-identical across replicas; `(key, reason, field)`
   sort with the cycle-entry member-list tiebreak (two coexisting
   cycles order deterministically under an UNSTABLE sort);
   statusless contested entries omit `title`; per-entry
   `contested: true` flags on ready/held/blocked; plain-board races
   unflagged (pinned as the stated trade); the cover-set pass
   produces identical heads to a reference pairwise-ancestry
   computation on merged fixtures.
4. Signals under partition: the human-label-vs-claim cross-field case
   lands unflagged (pinned accepted limit); duplicate idempotency keys
   across replicas both survive (pinned accepted limit).
5. Adoption re-validates: a synced-in board missing `--guard status`
   is refused with the defect named.
6. Precondition reads are whole-chain on every board (one mode — no
   window, no merge detector); merged 5k `ready` with contested
   entries ≤ 350ms median-of-3; a merged 5k guarded write's
   wall time and the cover-set pass's peak residency measured and
   within the stated class (width-bounded, not n²).
7. `--at`: `ready`/`notes` honor it (pinned `0s` future-age); every
   other verb — reads and writes — rejects it `bad_usage`; `watch`
   timeout unaffected.
8. Determinism: the perturbed both-sinks byte-diff over the covered
   verbs, `render`'s written file included, plus `since` at a fixed
   cursor byte-diffed across replicas holding the same chain;
   freshness warnings and cross-slug lines verified OUTSIDE the
   diffed projection.
9. `ready` freshness: a fetched-but-unmerged replica's `ready` warns
   on stderr (TTY) and via the top-level `freshness` key (JSON); the
   PROJECTION members are byte-unchanged by the warning's presence or
   absence.
10. Skew, both directions demonstrated, asymmetric doctrine
    verified: a host 3h behind writes a claim on a 2h-horizon board
    — born stale, reclaimable, and a horizon exceeding the skew
    prevents it; a host 3h AHEAD writes a claim that goes stale
    exactly 3h late at EVERY horizon setting (the unmitigated
    direction, pinned as documented); the behind-direction doctrine
    line verified present in skill, with no "both directions" claim.
11. Multi-root refusal: a `commit-tree`-grafted remote chain
    retaining the legitimate root is refused by sync AND by first
    adoption, naming both roots and creators and the tracking ref;
    the legitimate single-root chain still syncs.
12. Remote ambiguity: zero remotes ⇒ clean no-op with message; two
    remotes, no origin/breadcrumb/flag ⇒ `ambiguous_remote` whose
    hint names both; breadcrumb and `--remote` each resolve it;
    `--remote <unknown>` stays `bad_value` — the two identifiers are
    distinct.
13. Push and sync outcome envelopes: all-failed and partial carry
    `ok: false`, `error: "partial_failure"`, per-slug outcomes, exit
    3; all-ok carries `ok: true`, exit 0.
14. Id reads: `show --id` renders exactly the named event — a note
    event's body included, under the parent's quoting — for a
    contested ticket's loser id; unknown id ⇒ `bad_value`;
    `notes --id` errors `bad_value` with the `show --id` hint on
    BOTH non-note branches (non-note event, unknown id) and never
    returns a silent empty list.
15. Watch bound deferral: post-merge cold-start `watch` delivers the
    whole concurrent region in one batch (the deferred parent bound,
    pinned as documented v1 behavior, named as the parent amendment).

## Trial plan

Two working directories, one bare remote, one deliberately skewed
clock; fleet agents on both sides of a staged partition claim, close,
label, seed colliding keys, and break cycles offline; sync; audit:
every contested (key, field) carries exactly one entry with a valid
`expect`; exactly one keeper per contested key after recovery, with
`contested_resolved` in the chain; the projections of every covered
verb byte-identical across replicas under perturbed environments; IDs
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
- Rev 5 simplification-lens round (two opus reviewers, both probing
  the spike binary and building hand fixtures; 12 vs 10 legitimate
  findings, zero disqualified; six independent convergences). The
  round deleted machinery by replacing it with less. Convergent:
  the merged-history `--limit` refusal silently un-fixed the
  parent's bounded-recovery guarantee (round 7: unbounded drains
  silently break exactly-once) on every synced board — replaced by
  the single-maximal-element pager, no refusal at all; the n²/8
  bitsets were over-provisioned for a descendant-existence question
  and their merge-conditioning exempted only boards where contests
  can't exist — replaced by the width-bounded cover-set pass (one
  reviewer implemented it and proved identical heads, kilobytes vs
  312MB); the windowed precondition read was a probed net LOSS on
  the common never-labeled key — deleted outright, taking the
  linear/merged bifurcation and the load-time merge detector (left
  consumer-less) with it; the "both directions" horizon doctrine was
  wrong-signed for clock-ahead peers (probed at two horizons: the
  penalty is the skew, at every horizon); `since` was excluded from
  the determinism guarantee for a non-reason, leaving the cross-host
  resume verb with no cross-replica promise; `render` — the verb
  whose whole contract is determinism — was missing from the covered
  list. Also fixed: the multi-root runbook exit was self-defeating
  (export/import invites adopting the poisoned chain; the real exit
  is remote-side ref surgery, and the wedged-fleet state is now
  stated); the write path's contested cost was unpriced (rule 7
  puts it inside the CAS retry loop); the attention sort was not a
  total order for coexisting cycle entries; `--at` was
  accepted-and-ignored on absolute-timestamp verbs (now scoped to
  `ready`/`notes`/`ls`); the ambiguity error's `bad_usage`
  identifier couldn't carry the candidate list per the parent's hint
  contract (`bad_value`); `show --id`'s "no read path" premise was
  false — `notes --id` exists and silently lies on non-note ids
  (both paths now pinned); the outcome-envelope pin widened to sync;
  refusal errors name the tracking ref; and the issues-spec clock
  amendment is now flagged explicitly alongside rule 8's.
- Rev 6 clarity round (two opus reviewers, simplicity +
  ease-of-understanding lens; 12 vs 11 legitimate findings, five
  shared, zero disqualified). Rev 6's pager law falsified TWICE,
  probed: after every divergent sync the tip is a sentinel, so the
  delivered set ends with two incomparable maximal elements and the
  law names no cursor and never terminates ("the tip is always such
  an element" was false — sentinels are never delivered); and a
  single maximal element, when it exists, can be INCOMPARABLE with
  the incoming cursor (a consumer whose cursor sits on one branch),
  making `C..tip` re-admit the consumer's own branch — the rev-4
  oscillation reproduced under the rev-6 rule. Fix: condition (i)
  cursor-descent, condition (ii) dominates-delivered on the
  contracted DAG, exhaust-and-emit-tip otherwise. Also probed: the
  "excess is small" paging caveat confused DAG width with region
  event count (a divergence-crossing page delivers the whole
  region; `--limit` is a floor — now stated); maximality on the raw
  DAG miscounts across sentinels (contracted-DAG judgment pinned);
  `watch`'s missing bound was a silent parent amendment in a
  document claiming none (now the named parent amendment);
  "outside the envelope" was not a place — the parent defines the
  envelope as the whole document (projection vs document
  distinction pinned; the spike's top-level warning keys change
  envelope bytes); the determinism scope was stated three
  disagreeing ways with `ls` both in and out and "chain" undefined
  (one closed list; chain = the sentinel-independent event set);
  `--at`'s scope contradicted the standing test it serves (complete
  existence rule pinned; `ls` dropped — no promise consumed it);
  the attention sort was still not total across entry types (one
  tuple, all kinds); `show --id` was unspecified inside a bullet
  about seed collisions and `notes --id` still silently lied on
  unknown ids (both paths pinned, full-body render, `bad_value` on
  both bad branches); the write path was priced for a board-wide
  pass it doesn't need (narrowed to one pair's boolean); the
  amendment inventory was short by two (test-16 clause, 140ms
  linear bound — four issues amendments now enumerated); the
  ambiguity identifier moved to the parent's `ambiguous_*`
  precedent; the 70–78ms fold figure misquoted the parent (70ms);
  and Addition 2's rules were each stated twice in
  revision-narrative form (restated once; history moved here).
