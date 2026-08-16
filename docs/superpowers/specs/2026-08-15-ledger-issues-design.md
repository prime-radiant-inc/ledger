# Ledger as issue tracker (design)

2026-08-15, revision 11. A sixth round (second blind round, two fresh cold
reviewers) found what ten predecessors missed, including the best single
find of the project: nothing required a ready-capable board to actually
`--guard status` — the entire invariant was opt-in, silently absent when
the one flag was omitted, title enforcement included. Also: the walk
enumerated five of six `waiting_on` states and forgot `open` (the most
common blocker shape); "a visited-set" specified the classically wrong
DFS (diamond dependencies false-flagged as cycles — a shape trial 3
literally built); the walk's "one ready call" broke past `--limit`; the
"don't poll, use watch" doctrine leaned on field-scoped watching the tool
verifiably doesn't have and a staleness event that never fires; rule 8's
"narrow the read" couldn't answer its own label check; and the identifier
provenance sentence was checkably wrong a second time. Rev 11 closes all
of it. The meta-lesson, in one reviewer's words: rounds converged on
polishing the rules that were written down and missed that the
precondition making them bind was never itself required.

Previously — revision 10, the cold-review editing pass. A fifth round ran
blind: two reviewers given the document with no knowledge of prior rounds
and no steering. Their findings converged on rev 9's newest layer and
caught two structural misses eight steered reviewers left standing: the
stop-walk misclassified statusless keys as resolved (and its citation for
rules 9–10 claimed harness coverage the harnesses don't contain — the
validation claims are now split honestly), and `--guard` itself was the
one declaration without create-time validation (a typo silently disabled
every protection on the board). Rev 10 applies both reviewers' full sets:
`waiting_on` entries become objects carrying resolution state (making the
walk envelope-local and correct for stale and statusless nodes), titles
are enforced not hoped for, the edges-first collision gets a deterministic
recovery (a successful `--expect none` proves the stranger had no prior
edges — recovery is "clear to empty"), rule 8's "reuse" is pinned to
within-attempt, "don't poll" is reconciled with `watch`, and a dozen
formats, orderings, and attributions are pinned. Verdicts both rounds:
editing pass, not redesign — the invariant's core held.

Previously — revision 9. A fourth adversarial round (two reviewers, warned
that six predecessors had stripped the cheap findings) still found
load-bearing holes, and rev 9 closes them. The heaviest: rule 9 gated the
same-value reclaim hijack but left the terminal-value eviction of a fresh
claim wide open (the mirror image of the hole rev 8 closed) — rev 9 unifies
all cross-author interference with a live claim under one visible gate,
`--override-claim`. Second: issue titles existed only in the seed event's
message and vanished from every live view at first claim, making dup-search
unreliable on any real board — titles are now first-class derived data on
every list. Also: dependent seeding is edges-first (closing a
pickable-before-dependencies window outright), claimed-but-blocked keys are
visible (`in_progress` carries `waiting_on`), `human_owned` entries carry
their `id`, rule 9+10 composition and the same-author-label case are
stated, board status vocab is create-validated, the `ready` envelope and
all new formats are pinned, the stop-condition walk is an algorithm with
cycle handling, touch-base has cadence doctrine and an economics note, the
forensic-record framing is honestly caveated (rejected writes append
nothing), and the test plan grows to cover all of it.

Previously — revision 8. Rev 7 consolidated six revisions into final-form
rules; a third adversarial round (two fresh reviewers, one probing the spec
text as a lifecycle whole, one re-verifying claims against the built spike)
found the consolidation's blind spots, and rev 8 closes them. The heavy
ones: reclaim had no staleness precondition (any live claim was legally
hijackable — "stale" was a commit-message string, not a checked condition);
the human_owned quarantine only filtered `ready` while doctrine's own first
read still surfaced the key and nothing gated the write (trial 3's incident
survived through a different door); and the write idioms omitted the single
most common write on any board — seeding a new issue, which on a guarded
board requires `--expect none` and, with dependencies, two writes. Also
fixed: key grammar vs. edge grammar mismatch, `human_owned`'s shape and a
list-membership truth table, pagination signals, a stated `ready` cost
requirement, honest reframing of two oversold protections, the lost-work
reconciliation path, triage's missing worked commands, `waiting_on` scope,
duration grammar, and a numbered test plan (the parent spec's precedent,
dropped by the consolidation). History: Validation record, bottom. Extends
tool spec rev 13; these additions are the tool's rev 14.

Design center, unchanged since rev 1: an issue tracker is the
investigation-ledger pattern plus three things ledgers lacked — multi-valued
fields (labels, dependency edges), filtered reads, and a race-safe way for
agents to pick and claim unblocked work. No new storage, no daemon. Sharing
across machines stays Plan 2.

## What already works today (no changes)

Issues as keys; status vocab via enum fields; evidence-required values;
discussion as keyed notes with kinds (`comment`, `repro`, `ruling`,
`handoff`); attribution and provenance per event; `watch`/`since` for live
triage; CAS-safe concurrent writers; rollups for closed-thread curation of
`tail`. Priorities are just another enum field for boards that want one.

## The board

```
ledger create issues --scope "issue tracker for <repo>" \
    --field status=open,in-progress,closed,wontfix \
    --terminal status=closed,wontfix \
    --multi-field labels --multi-field blocked-by \
    --guard status --guard blocked-by \
    --require-evidence status=closed \
    --stale-after 2h
```

**Declarations** (recorded in immutable meta; every one validated at
create):

- `--multi-field <name>`: multi-valued, vocab-free. Tokens match
  `^[a-z0-9][a-z0-9-]*$`, comma-separated; malformed token → `bad_value`
  naming it. Replace-wholesale per set; `name=` clears.
- `--terminal <field>=<v1>,<v2>`: values that stop blocking. MUST be a
  subset of the field's vocab (`bad_value`; an accepted typo permanently
  deadlocks dependents). `--require-evidence` values get the same check.
- `--guard <field>`: conditional writes only (the invariant). The field
  MUST be declared via `--field` or `--multi-field` (`bad_value` at create
  — rev 10, cold-review find: a typo'd guard name matched nothing and
  silently disabled every protection on the board; the one declaration
  without validation was the one whose failure was total).
- `--stale-after <duration>`: staleness horizon. Go `time.ParseDuration`
  grammar; `age` fields render the same way. Optional — but without it
  nothing is ever stale and cross-author reclaim is impossible (see
  invariant rule 9).
- Conventions, stated: availability field is `status`, edge field is
  `blocked-by` (`ready` errors helpfully when absent); the reserved label
  `human` marks keys that belong to people. **On a board that declares
  `blocked-by`, keys must match the multi-field token grammar, enforced at
  each key's first write** (`bad_value`: "key '<k>' is not
  edge-referenceable — issue keys use kebab tokens") — otherwise legally
  named keys can exist that no edge can ever point at (reviewer-verified:
  the token check fired before `unknown_key` and orphaned `User_Auth`
  forever).

**`blocked-by` tokens are keys**, each validated as existing at write time
(`unknown_key`, exit 4, naming the token; no near-miss suggestions — rev
13's YAGNI cut stands). Cycles are representable and surface visibly in
`blocked` as keys waiting on each other. A "blocked" status value is
deliberately absent: blocked is derived.

**Export/import round-trips meta byte-for-byte**; import never re-derives
declarations. Test: an exported board's `ready` output is identical after
import.

## The invariant: guarded fields take conditional writes only

`set` gains `--expect <event-id> | --expect none`. Five
separately-discovered races (claim-verify TOCTOU, zombie reopen,
unconditioned close, unconditioned triage, first-edge 0→N) were each one
more unguarded status write; the invariant ends the enumeration.

1. A set touching a guarded field MUST carry `--expect`; a plain set is
   `bad_usage` naming the rule and the fix.
2. A conditional set touches exactly one guarded field (else `bad_usage`);
   unguarded fields may ride along.
3. `--expect <event-id>` (SHA prefix accepted): succeeds only if the
   written guarded field's latest event on this key is still that event at
   append time. **Field-scoped**: other fields' events never invalidate it
   (key-scoping made ordinary triage kill claims); notes never invalidate
   it.
4. `--expect none`: succeeds only if the field has no prior event on this
   key. Racing first writes serialize: one winner. (Replaced a snapshot
   "has edges?" gate that reopened the race it guarded.)
5. **Terminal→terminal transitions are `bad_usage`** ("reopen first, then
   re-resolve"). What this honestly buys: the honest-but-stale agent hits
   an error and a hint at the natural point to reconsider (a trial's
   triager legally flipped an evidenced close to unevidenced wontfix
   through the key's *current* id — `--expect` guards stale reads, not
   stale decisions). Against a determined agent it buys two visible,
   attributable events instead of one — friction and auditability, not
   prevention. Triage doctrine includes the corresponding sweep (below).
6. **Failure contract**: `claim_lost`, exit 4; message names the winning
   event's id, author, and the exact value it wrote (a trial surfaced a
   malformed message on the reclaim path — the format is a tested
   requirement). Hints: status → "re-run ledger ready and pick again";
   blocked-by → "re-read the key's edges and merge".
7. **Atomicity contract**: every precondition in this section — id match,
   `none`, staleness (rule 9), the human gate (rule 10) — re-validates
   against a fresh read inside the store's CAS retry loop on every
   attempt; never a pre-loop snapshot. Validation, split honestly
   (rev 10, cold-review find — the previous citation claimed coverage the
   scripts don't contain): the field-scoped CAS mechanics (id match,
   `none`, field-scoping) are harness-validated
   (`research/scripts/expect-race-harness.sh` 20/20 + the spike's
   extended harness 30/30, both committed). The staleness gate, both
   overrides, and their composition (rules 9–10) are REASONED, NOT YET
   HARNESSED — they postdate every trial and harness; their harness
   rounds are mandatory rev-14 tests and must pass before the invariant's
   validation claim covers them.
8. **Performance requirement**: the precondition read must not re-fold the
   full chain per retry (the spike did: ~70ms per 5k events per attempt).
   Narrow the read to the **target key** (rev 11 — "key/field" was
   incoherent: a field-narrowed read cannot answer the human gate's
   label check; the unit is one key, all fields): walk the chain
   backward from head, early-stopping once the target key's guarded
   field, label state, and staleness inputs are all resolved. Honest
   worst case, stated: a key untouched for thousands of events degrades
   toward the full-chain scan — the bound test (19) measures the common
   case and states the degenerate one. "Reuse" means **within-attempt
   only**: one fresh read per CAS attempt, shared across that attempt's
   checks. Cross-attempt caching is exactly the pre-loop snapshot rule 7
   forbids.
9. **Live claims are protected from ALL cross-author interference**
   (rev 9; rev 8 gated only same-value re-claims, leaving the mirror-image
   hole: anyone could `wontfix` or `close` a fresh claim with just its
   current id). Any write to the availability field whose `--expect`
   target is an `in-progress` event **by a different author** — whatever
   value it writes — succeeds only if (a) that claim is stale at append
   time (fresh-read, per rule 7), or (b) the write carries
   `--override-claim` with a message (recorded on the event as
   `override: claim` — greppable). Otherwise `not_stale`, exit 4; message
   format, pinned like `claim_lost`'s: the claim's author, its age, the
   board's horizon, and the override hint. Same-author writes (touch-base,
   your own close) are unaffected. On a board without `--stale-after`
   nothing is ever stale, so cross-author interference always requires the
   override. This one gate covers reclaim, eviction, triage takeover, and
   squat-breaking: stale claims are freely reclaimable, live ones are
   touchable only through a visible, attributable, message-bearing act.
10. **The human gate** (rev 8; the quarantine alone only closed the
    `ready` door — doctrine's own `show --where status=open` still
    surfaced the key, and nothing stopped the write). A write to a guarded
    field on a key carrying the board's `human` label is `human_owned`,
    exit 4 — message pinned like its siblings' (rev 11): it names the
    key, the label, and the two-path hint ("remove the label, or pass
    --override-human -m '<why>'"). The gate lifts only with
    `--override-human` plus a non-empty (trimmed) message; either
    override flag without a message is `bad_usage` naming the rule
    (rev 11 — this was the one unspecified error path in the taxonomy).
    Honest limits, stated: identity is asserted, so this is friction and
    visibility, not authentication — and `labels` is unguarded by design,
    so removing the label first is possible; the gate's value is that
    either path (override flag or label removal) is a separate, visible,
    attributable act, the same two-event philosophy as rule 5. The
    override is recorded on the event as `override: human` (greppable,
    same shape as rule 9's). **Composition** (rev 9): the human gate is
    checked first — a write failing both rules reports `human_owned`, not
    `not_stale`; a write needing both passes carries both overrides.
    **No same-author carve-out**: a label added mid-claim freezes even the
    claimant's own close (deliberate — labeling a claimed key is how a
    human says "stop"); the claimant resolves by label removal or
    `--override-human`, both visible acts, and doctrine says so. The gate
    applies to a key's FIRST status write too: a key pre-labeled `human`
    before seeding — a legitimate way to reserve planned work for a
    person — seeds only with `--override-human` or after label removal.
    Worked (rev 11, since the single `-m` is BOTH the permanent title and
    the override justification, by design): `set <key> status=open
    --expect none --override-human -m "<title> — reserved for <who>:
    <why>"`.

`--expect` on a write touching zero guarded fields stays legal for any
single-field write (general read-modify-write protection).

## Filtered reads (`show --where`)

`show --where <field>=<value>` (exact) and `--where <field>~=<token>`
(membership, multi-fields only). Repeatable, AND'd. Errors: undeclared
field → `unknown_field` with the declared list; `~=` on a non-multi field
→ `bad_usage`; two `=` clauses on one field → `bad_usage`. Bare `show`
stays unfiltered.

## `ready`: pick unblocked work

`ledger ready [--where …] [--limit N]` — one envelope, four lists, each
bounded by `--limit` (default 50, per list), with totals. The envelope
shape is pinned (rev 9 — four consumers guessing independently will
disagree):

```json
{"ledger": "issues", "ok": true,
 "ready": [...], "blocked": [...], "in_progress": [...], "human_owned": [...],
 "totals": {"ready": 3, "blocked": 7, "in_progress": 2, "human_owned": 1}}
```

**Titles are first-class derived data and ENFORCED** (rev 10; rev 9
derived them from the first status event's `-m` but `-m` was optional, so
a title could be blank forever — reproducing by omission the exact
dup-search failure the feature exists to fix): on a board with a guarded
availability field, the first status write to a key REQUIRES a non-empty
`-m` after trimming whitespace (`empty_body`, exit 4, hint naming it as
the title). Every list
entry carries `title`; `show` rows on boards gain the same field.

List membership is a function of (status, human label, edges), exactly:

| status | `human` label | edges resolved | list |
|---|---|---|---|
| terminal | — | — | none |
| (no status yet) | — | — | none (invisible until seeded — see Seed) |
| non-terminal | yes | — | `human_owned` |
| open | no | yes | `ready` |
| open | no | no | `blocked` |
| in-progress | no | — | `in_progress` (with `waiting_on` if unresolved) |

The table is exhaustive because the board's shape is create-validated
(rev 11 closes the project's best-hidden hole): declaring `--terminal` on
a field named `status` IS opting into ready-capability — a purely
syntactic trigger, stated plainly (a board wanting rule-5
terminal-transition protection without `ready` semantics names its field
something else). A ready-capable board REQUIRES, all validated at create
with `bad_value` naming the fix: `--terminal` on `status`; non-terminal
remainder exactly `{open, in-progress}`, both present; **`--guard
status`** (ten reviewers polished the invariant's rules before one
noticed nothing required the flag that makes them apply — without it
every protection in this document was silently absent); and `--guard
blocked-by` whenever `blocked-by` is declared. `ready` on a
non-ready-capable board is `bad_usage` with the create-time fix in the
hint.

- **ready**: oldest first, timestamp ties by chain position. Entry: `key`,
  `title`, `note`, `ts`, `by`, `id` (the claim ticket — note/ts/by/id from
  the status field's latest event, title from its first). Entries whose blockers include a terminal event with no
  evidence refs gain `unblocked_without_evidence: [keys]` — keyed to the
  property, not a vocab string. Honest framing (rev 8): the annotation is
  a floor against *omission*, not a defense against *fabrication* — refs
  are unvalidated free-form strings by design, and a pasted garbage ref
  defeats it; `ledger verify` remains v2.
- **blocked**: entries `key`, `title`, `note`, `ts`, `by`, `waiting_on`.
  **`waiting_on` entries are objects, not bare keys** (rev 10):
  `{key, state}` where `state` ∈ `terminal | open | in-progress |
  in-progress-stale | human | statusless` — direct edges only, but each
  annotated with its target's resolution state so the stop-condition walk
  is envelope-local and cannot misclassify (rev 9's walk called every
  absent key "moot," which was wrong twice over: a stale in-progress node
  fit no category, and a statusless key — legal via edges-first seeding,
  and legally *targetable* by other keys' edges — read as resolved when
  it is the opposite).
- **in_progress**: `key`, `title`, `by`, `age`, `id` (the claim event —
  the reclaim input), `stale: true` past the horizon, and `waiting_on`
  when the key has unresolved edges (rev 9 — the edge-edit idiom legally
  creates claimed-but-blocked keys mid-work, and without this field that
  state was structurally invisible and silently broke the stop condition's
  termination guarantee).
`blocked`, `in_progress`, and `human_owned` sort by key ascending
(rev 10, pinned for stable doc-harness output).

- **human_owned**: `key`, `title`, `note`, `ts`, `by`, `status`, `id` (the
  status field's latest event — rev 9: without it, acting on a stale
  human-labeled claim required a second read no other list demands), plus
  `waiting_on` when edges are unresolved. Every non-terminal human-labeled
  key lives here and only here; the label dominates status for list
  placement, consistent with rule 10's write gate.

`ready` implies `--where status=open` for its own list; a contradicting
status clause is `bad_usage`. Extra `--where` clauses apply uniformly to
ALL FOUR lists (rev 10, pinned — both readings were defensible and
produced different envelopes). Also stated as intent (rev 10): **blocked
is not locked** — no rule prevents claiming a blocked key by name with a
valid `--expect`; the fences are non-surfacing in `ready` plus doctrine,
and an out-of-order claim is legal, visible, and attributable. `ready` joins rev
13's data-verb taxonomy. **Performance requirement** (rev 8): `ready` is
the loop's hottest read and folds the whole board; a measured bound at the
parent spec's 5k-event scale is part of implementation acceptance (target:
the same ~100ms class as the measured folds it composes), stated before
merge, in the parent spec's own numbers-first style.

## The write idioms (all the same guarded write)

- **Seed**: `set <key> status=open --expect none -m "<title>" --as <you>`
  — the `-m` is the issue's TITLE, preserved as first-class derived data
  forever. Seeding WITH dependencies is **two writes, edges FIRST**
  (rev 9): `set <key> blocked-by=<k1>,<k2> --expect none --as <you>`,
  then the status seed. A key with edges but no status yet is in no list
  (truth table) — invisible, unpickable — so the edges-first order closes
  the window where a dependent key was claimable before its dependencies
  landed (status-first had exactly that window, and a partial failure left
  it permanently dependency-free). A partial failure under edges-first
  leaves an invisible statusless key: harmless, and a named triage sweep
  item. **Seed collision** (rev 10 sharpens rev 9 — the
  corrupting write is the one that SUCCEEDS): if a stranger's issue
  already holds your chosen key with a status but no edges, your
  edges-first `--expect none` write lands cleanly on their key; the
  collision surfaces only when your status seed fails `claim_lost`.
  Recovery is deterministic and worked: your own successful `--expect
  none` PROVES the key had no prior edges, so `set <key> blocked-by=
  --expect <your edge event id> -m "reverting: seed collision"` restores
  their key exactly; then re-seed yours under a new name. A `claim_lost`
  on a status seed therefore always means: key exists — read it, revert
  any edge write you made, re-seed under a new key (the hint says so).
  If the stranger's key carries the `human` label, the revert itself
  needs `--override-human` — sanctioned here, message naming the
  collision (restoring someone's key to its true state is what the
  override exists to make visible). Never chain the writes without
  checking exit codes.
- **Claim**: `set <key> status=in-progress --expect <ready id> -m
  "claiming" --as <you>`. `claim_lost` → re-run `ready`, pick again. The
  claimer's `--as` IS the assignee; the claim event's provenance names
  who, when, from where.
- **Touch-base**: re-set `status=in-progress --expect <own claim id> -m
  "still on it"` — resets age; same-author, exempt from rule 9's gate.
- **Close**: `set <key> status=closed --evidence <ref> --expect <own claim
  id>`. On `claim_lost` here you were reclaimed while working: leave your
  result as a note with kind `handoff` (mechanically distinguishable from
  chatter), and let the current claimant decide. Never re-close blind.
  **The winning claimant's duty** (rev 8 — otherwise finished work
  vanishes into an unread comment): when the chain shows the key was ever
  reclaimed, check `notes --key <key>` for `handoff` notes before closing.
- **Reclaim**: `set <key> status=in-progress --expect <its in_progress id>
  -m "reclaiming from <by>: stale <age>"` — succeeds only against a
  genuinely stale claim (rule 9). Concurrent reclaimers serialize
  (field-trialed).
- **Takeover / squat-break** (rev 9): interfering with a LIVE claim —
  evicting, force-closing, or breaking a touch-base squatter — is
  `set <key> status=<value> --expect <its id> --override-claim -m
  "<why, naming the claimant>"`. Triage-only by doctrine; the override is
  recorded on the event and greppable.
- **Reopen**: terminal→any non-terminal value with `--expect <the terminal
  event's id>` (rev 8: not restricted to `open`; reopen-and-claim in one
  write is legal).
- **Edge edit** (rev 8 — previously no worked command, and the natural
  combined form is illegal): read the current edge set, union or prune,
  write the whole set: `set <key> blocked-by=<full,new,set> --expect <the
  blocked-by field's latest event id>`. NEVER combine an edge write with a
  status write in one set — rule 2 makes it `bad_usage`.
- **Triage status writes**: guarded like everything else; label churn is
  unguarded and cannot disturb claims (harness-proven).
- **Recovery** (rev 9 — the one idiom that had prose but no command):
  after discovering a clobber or duplication, two writes:
  `note -k handoff --key <key> --from-file <what-happened>.md --as <you>`
  then, if the state itself needs correcting and you hold or can read the
  current id, the corrective guarded set with `--evidence` and a message
  naming the mistake. Idiom messages throughout this section ("claiming",
  "still on it", "reclaiming from …") are **load-bearing conventions**,
  not illustrations: consumers filter watch streams and grep history by
  them, so the skill teaches them verbatim.

## Board doctrine (the skill)

Delivery, pinned (rev 11): this section ships as a new pattern section in
`skills/using-ledger/SKILL.md` (joining Execution spine, Coordination
scoreboard, etc.), and that skill's frontmatter `description` gains the
triggers "running an issue board" and "picking unblocked work" — without
the trigger text, no agent ever surfaces the doctrine, however good it
is. Test 18 scans that file.

- First read: `ledger show --where status=open`.
- Picking loop: `ready` → claim → work → close → repeat. Stop when
  `ready` is empty (its `total` confirming nothing beyond the limit) and
  the blocked frontier resolves to workers or humans. **The walk, as an
  algorithm** (rev 9 — it was a prose predicate whose landmine, cycles,
  the spec elsewhere declares legal): with the whole envelope in hand,
  for each `blocked` entry, follow `waiting_on` keys with a visited-set;
  the walk (rev 11 — rev 10's version forgot the most common target
  state and specified the classically wrong DFS): first size the call —
  if `totals.blocked` exceeds your `--limit`, re-call `ready --limit
  <totals.blocked>`; the walk needs the whole blocked list. Then, per
  `blocked` entry, classify each `waiting_on` target: `terminal` is
  moot; non-stale `in-progress` and `human` are safe leaves;
  `in-progress-stale` and `statusless` are NOT safe (reclaim opportunity
  and triage item); **`open` is not a leaf at all** — recurse into that
  key's own `blocked` entry, and if it has none it is in `ready` by
  construction and IS a safe leaf (pickable work exists, you are not
  done). Cycle detection uses the **current path** (ancestor stack): a
  key on your own path is a cycle — NOT safe, a triage item; a key
  merely visited before via another branch is a shared dependency
  (diamond — a legal, trial-built shape) and is resolved from memo, not
  re-flagged. Stop only if every path ends at a safe leaf.
  "Don't poll," honestly (rev 11; rev 10 pointed at `watch` on the
  status field, which the tool cannot do — `watch --value` matches ANY
  field's value, unscoped, so vocab-free label tokens collide; and
  staleness fires no event at the horizon, ever): re-running `ready`
  after your own close is the loop. Waiting for OTHERS: run `watch` with
  the full status vocab as `--value` terms, accept rare label-token
  collisions as spurious wakes, and treat every watch TIMEOUT as a cue
  to re-run `ready` — timeouts are how staleness gets noticed, since no
  event announces it. A field-scoped watch filter is an upstream
  candidate, stated, not assumed.
- Touch-base cadence (rev 9): at roughly half the board's
  `--stale-after`, and only while actively working. Touch-bases are
  events; a long task under a short horizon multiplies chain volume and
  watch noise (the economics note below), so boards pick horizons matching
  their tasks — not the reverse.
- Claiming an `unblocked_without_evidence` key: name it in the claim
  message.
- Triage moment: walk `show --where status=open` — keep / close with
  evidence / wontfix with the why in `-m` / edge edits (worked command
  above; never combined with a status write) / re-label. Sweep `ready`'s
  `in_progress` list (the one that computes `stale`) for orphans; sweep
  recent history for reopen→re-resolve pairs (`tail --raw`, the rule-5
  pattern — the friction is real, the audit is yours to run). Evidence on
  wontfix is NOT required: forcing evidence of a non-decision produces
  pasted-string theater.
- Recovery idiom: on discovering you clobbered or duplicated state — read
  the key's history, correct with an evidenced write naming what happened,
  report it. Never quietly re-fix.
- Dup defense: search before create — against TITLES, which live in
  `ready`/`show`'s `title` field for live keys and in `tail --raw`
  (never the curated view — rollups may compress seed events away) for
  closed ones. Rollup summaries on boards SHOULD retain key names
  verbatim so rolled threads stay greppable — advisory doctrine, not
  validated by `rollup` (rev 10, honestly: a summary naming no keys
  silently breaks dup-search under it). Dups close `wontfix -m "dup of
  [[key]]"`.
- Squat sweep (rev 9): triage checks `in_progress` for claims
  touch-based repeatedly without progress notes or evidence; breaking one
  is a `--override-claim` takeover, message naming the claimant.
- Every paste-ready command line carries the absolute binary path (a
  trial's workers typed bare `ledger` because the doctrine's lines did;
  one silently used an old binary past every rail).
- What no mechanism supplies: honoring what the id you fetched actually
  said. `--expect` proves you read the state; rules 5, 9, and 10 narrow
  the blast radius of not respecting it; judgment does the rest.

## Timestamps, clocks, and the chain's economics

Event timestamps gain sub-second resolution in rev 14, pinned (rev 9) to
the layout `2006-01-02T15:04:05.000` (UTC, fixed milliseconds, no zone
suffix — the parent spec pinned the old layout exactly; the new one gets
the same treatment; variable-precision formats break naive comparisons).
Readers parse both layouts. `age` compares a writer's recorded `ts`
against the reader's clock: boards assume same-host clock coherence;
multi-host fleets sharing a store are a Plan 2 concern and get a one-line
warning in the skill.

Economics, stated (rev 9): a completed issue is ≥3 events (seed, claim,
close; ≥4 with dependencies — edges-first adds one) plus touch-bases, which scale with wall-clock duration, not issue
count — a cautious agent under a short horizon can multiply chain volume
15x. The `ready` cost bound below is measured against event volume
including touch-base churn, not issue count. `watch` consumers filter
touch-bases by the load-bearing message convention ("still on it"); a
`--transitions-only` watch flag is deferred until that proves
insufficient, stated here rather than silently.

Honest caveat (rev 9, retiring an inherited overstatement): the chain is a
complete record of every write that LANDED. Rejected writes —
`claim_lost`, `not_stale`, `human_owned`, `bad_usage` — append nothing,
so contention history (who tried and lost, how many piled onto a hot key)
is not preserved. Trial 1's "complete forensic record" framing predates
the invariant that made writes rejectable; the trade is deliberate and now
stated.

## Deferred, with reasons

- `min_writer` version floor: can't protect against binaries already in
  the field; next round. Fleet doctrine (same binary, absolute paths) is
  the working mitigation.
- Multi-field token filters on `watch`/`since`: wait for a consumer;
  workaround is `--key` + client-side filtering.
- Additive `block`/`unblock` verbs (sugar over the edge-edit idiom); FTS
  search; TUI; short IDs; evidence validation (`ledger verify`, v2);
  cross-machine sharing (Plan 2).

## Implementation scope (rev 14, SDD with tests)

Everything in The board, the invariant (rules 1–10 incl. `not_stale` and
`human_owned` errors), Filtered reads, `ready` (four lists, totals,
annotations, truth table, measured cost), the write idioms' mechanics,
sub-second timestamps, meta export/import round-trip, the extended race
harness as tests, the board skill, rev 13 amendments (verb taxonomy; rev 14 adds `claim_lost`, `not_stale`,
and `human_owned` to the parent's canonical error-identifier list;
`bad_usage`, `bad_value`, `empty_body`, `unknown_field`, and
`unknown_key` all predate this document — rev 11 corrects this sentence's
second wrong enumeration, which is its own small lesson in checking
citations). Spike
branch: historical evidence, never merged.

## Test plan (numbered; the parent spec's precedent)

1. Guarded plain set → `bad_usage` naming the fix; unguarded fields ride
   along with one guarded field; two guarded fields in one set →
   `bad_usage`.
2. Seed with `--expect none`; racing seeds serialize (one winner);
   `--expect none` on a touched field → `claim_lost`.
3. Claim/close/reopen chains: each conditioned on the right event; an
   OUTDATED `--expect` (id no longer current — distinct from rule 9's
   horizon-based "stale") → `claim_lost` with correct id/author/value in
   the message — including on the reclaim path (the trial's
   malformed-message bug).
4. Field-scoping: label writes racing status claims never produce
   `claim_lost` (harness round, 10/10 required).
5. First-edge race under `--expect none` (harness round, 10/10).
6. Terminal→terminal → `bad_usage`; terminal→in-progress legal with the
   terminal event's id.
7. Rule 9, all shapes: cross-author write of ANY value against a fresh
   claim → `not_stale` (age, horizon, override hint in the pinned format);
   same with `--override-claim` + message → succeeds, event records
   `override: claim`; against a stale claim → succeeds without override;
   two concurrent reclaimers → one winner; same-author touch-base and
   close always allowed; board without `--stale-after` → cross-author
   writes always require the override.
8. Human gate: guarded write on a `human`-labeled key → `human_owned`;
   with `--override-human` + message → succeeds, event records
   `override: human`; label removal then write succeeds (documented
   bypass — the two events distinct and attributable); label added
   mid-claim freezes the claimant's own close until removal/override;
   a stale human-labeled claim needs ONLY `--override-human` (staleness
   already satisfies rule 9 — rev 10 fixes a test that contradicted the
   rule's own OR); a fresh human-labeled claim cross-author needs both,
   recorded `override: claim,human` (pinned combined form); failing both
   reports `human_owned` (composition order).
9. `ready` truth table: every row, including human-labeled in-progress
   (in `human_owned` only, WITH `id`), human-labeled blocked (ditto, with
   `waiting_on`), claimed-but-blocked (in `in_progress` with
   `waiting_on`), statusless seeded keys (no list), and `title` present
   on every entry of all four lists; envelope shape matches the pinned
   example, totals correct.
10. `ready` ordering: oldest-first, tie by chain position (regression for
    the alphabetical bug); `--limit` per list with correct `total`s.
11. `unblocked_without_evidence`: fires on evidence-free terminal blockers,
    not on evidenced ones, regardless of which terminal value.
12. Key grammar on edge-declaring boards: non-kebab key rejected at first
    write with the edge-referenceability message.
13. `--terminal`/`--require-evidence` subset validation; `--stale-after`
    parse validation.
14. `show --where`: `unknown_field`, `~=` on enum → `bad_usage`, same-field
    double `=` → `bad_usage`, AND composition.
15. Export/import: meta round-trip byte-identical; `ready` identical.
16. Sub-second timestamps: staleness at sub-second horizons; old events
    (second-resolution) still parse.
17. `ready` cost at 5k events: measured, within the stated bound.
18. Doctrine examples: every command line in the board skill executes
    verbatim against a scratch board (the tool's doc-harness precedent).
19. Conditional-set precondition read under contention at 5k events:
    narrowed/reused per rule 8, not a full re-fold per retry — measured
    bound stated (the rule had no test; `ready`'s test 17 measures a
    different operation).
20. Edges-first dependent seed: key invisible to all lists between edge
    write and status write; claimable only after status lands; partial
    failure leaves an invisible key surfaced by the triage sweep.
21. Seed collision: `--expect none` status seed on an existing key → the
    key-already-exists hint (not the claim hint); the clean-landing edge
    write on a stranger's edge-free key is the documented contamination
    case — test walks the full recovery (revert-to-empty with the edge
    event's id, restoring the stranger's key's derived state exactly, then re-seed
    under a new name).
22. Titles: derived from the first status event; survive claims, closes,
    reopens; present in `show` rows on boards.
23. Status vocab validation: a third non-terminal value at create →
    `bad_value` on boards declaring `--terminal`.
24. Timestamp layout: new events carry fixed-millisecond UTC; old
    second-resolution events parse; staleness math correct across mixed
    precision.
25. Rules 9–10 harness rounds (mandatory before the validation claim
    covers them): staleness gate under forced races; both overrides;
    the combined `override: claim,human` recording; composition order.
26. `--guard` create validation: undeclared field name → `bad_value`
    (regression for the silent-total-disable typo).
27. Title enforcement: first status write without `-m` (or whitespace
    only) → `empty_body`;
    walk states: `waiting_on` objects carry correct `state` for every
    target class incl. `statusless` and `in-progress-stale`; blocked/
    in_progress/human_owned sorted by key ascending.
28. Ready-capable shape: `--terminal status=…` without `--guard status`
    (or without `--guard blocked-by` when declared) → `bad_value` at
    create (the guard-not-required hole).
29. Walk behavior, not just annotation: an `open` target recurses (and a
    ready-listed open target is a safe leaf); a diamond (two blocked
    entries sharing one blocker) produces NO cycle flag; a true on-path
    cycle does; a board with >limit blocked entries is walked in full
    after the totals-sized re-call.
30. Override hygiene: either override flag without a (trimmed) message →
    `bad_usage`; the pre-labeled seed's worked command (title + override
    justification in one `-m`) executes; `human_owned`'s pinned
    message/hint format; collision recovery on a human-labeled stranger
    (revert with `--override-human`) lands and the stranger's derived
    state matches pre-collision.
31. Watch doctrine reality: watch with full status vocab as `--value`
    wakes on claims; a label token colliding with a status value produces
    the documented spurious wake (not an error); watch timeout → re-run
    `ready` is the staleness-notice path (no event fires at the
    horizon).

## Validation record

- **Trials** (chain-audited): `research/ledger-issues-spike-trial.md` — 2
  workers; falsified claim-by-doctrine; produced `--expect`. `trial2.md` —
  3 workers, accidental mixed-version fleet; validated `--expect`;
  produced absolute-path doctrine. `trial3.md` — 4 writers incl. live
  triager; zero duplicate work, contested reclaim serialized; produced
  `human_owned` and the terminal-transition ban.
- **Harnesses**: `research/scripts/expect-race-harness.sh` (20/20,
  independently re-run) + extended spike harness (30/30).
- **Adversarial reviews**: four rounds, eight reviewers. Round 3 (on the
  rev-7 consolidation) produced rules 9 and 10, the Seed idiom, the key
  grammar rule, the truth table, totals, and this test plan. Round 4 (on
  rev 8) produced the unified `--override-claim` gate, first-class
  titles, edges-first seeding, claimed-but-blocked visibility, the pinned
  envelope and formats, the stop-condition algorithm, and the honest
  forensic caveat.
- **Kata reconnaissance**: `~/git/kata` — what we took and declined is in
  Deferred.
