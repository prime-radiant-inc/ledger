# Ledger as issue tracker (design)

2026-08-15, revision 8. Rev 7 consolidated six revisions into final-form
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
- `--guard <field>`: conditional writes only (the invariant).
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
   attempt; never a pre-loop snapshot. Validated:
   `research/scripts/expect-race-harness.sh` + the spike's extended
   harness (status, first-edge, interleaved-triage rounds; 30/30); these
   ship as real tests.
8. **Performance requirement**: the precondition read must not re-fold the
   full chain per retry (the spike did: ~70ms per 5k events per attempt).
   Narrow to the target key/field or reuse the attempt's read.
9. **Reclaim is staleness-gated** (rev 8; reviewer-proven: without this,
   any live claim was hijackable by a fully legal write — "stale" was
   prose). A write setting the availability field to its claiming value
   (`in-progress`) whose `--expect` target is itself an `in-progress`
   event **by a different author** succeeds only if that claim is stale at
   append time (fresh-read, per rule 7). Otherwise `not_stale`, exit 4,
   message with the claim's age and the board's horizon. Same-author
   re-claims (touch-base) are unaffected. On a board without
   `--stale-after`, cross-author reclaim is impossible — take-overs happen
   by yield or by triage.
10. **The human gate** (rev 8; the quarantine alone only closed the
    `ready` door — doctrine's own `show --where status=open` still
    surfaced the key, and nothing stopped the write). A write to a guarded
    field on a key carrying the board's `human` label is `human_owned`,
    exit 4, unless the set carries `--override-human` (message required).
    Honest limits, stated: identity is asserted, so this is friction and
    visibility, not authentication — and `labels` is unguarded by design,
    so removing the label first is possible; the gate's value is that
    either path (override flag or label removal) is a separate, visible,
    attributable act, the same two-event philosophy as rule 5.

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
bounded by `--limit` (default 50, per list) and each carrying `total`
(the unbounded count — rev 8: a bounded list with no truncation signal
can't size a backlog or a fanout; `since` pairs its bound with a cursor,
`ready` pairs its with totals).

List membership is a function of (status, human label, edges), exactly:

| status | `human` label | edges resolved | list |
|---|---|---|---|
| terminal | — | — | none |
| non-terminal | yes | — | `human_owned` |
| open | no | yes | `ready` |
| open | no | no | `blocked` |
| in-progress | no | — | `in_progress` |

- **ready**: oldest first, timestamp ties by chain position. Entry: `key`,
  `note`, `ts`, `by`, `id` (the claim ticket — all from the status field's
  latest event). Entries whose blockers include a terminal event with no
  evidence refs gain `unblocked_without_evidence: [keys]` — keyed to the
  property, not a vocab string. Honest framing (rev 8): the annotation is
  a floor against *omission*, not a defense against *fabrication* — refs
  are unvalidated free-form strings by design, and a pasted garbage ref
  defeats it; `ledger verify` remains v2.
- **blocked**: entries `key`, `note`, `ts`, `by`, `waiting_on: [keys]` —
  **direct edges only** (rev 8, stated): the stop condition's transitive
  walk happens over the whole envelope, not one entry.
- **in_progress**: `key`, `by`, `age`, `id` (the claim event — the reclaim
  input), `stale: true` past the horizon.
- **human_owned**: `key`, `note`, `ts`, `by`, `status`, plus `waiting_on`
  when edges are unresolved — every non-terminal human-labeled key lives
  here and only here (rev 8: shape and scope were unspecified; the label
  dominates status for list placement, consistent with rule 10's write
  gate).

`ready` implies `--where status=open` for its own list; a contradicting
status clause is `bad_usage`. Extra clauses compose. `ready` joins rev
13's data-verb taxonomy. **Performance requirement** (rev 8): `ready` is
the loop's hottest read and folds the whole board; a measured bound at the
parent spec's 5k-event scale is part of implementation acceptance (target:
the same ~100ms class as the measured folds it composes), stated before
merge, in the parent spec's own numbers-first style.

## The write idioms (all the same guarded write)

- **Seed** (rev 8 — the most common write on any board, previously
  unstated): `set <key> status=open --expect none -m "<title>" --as
  <you>`. Seeding WITH dependencies is **two writes** (rule 2: one guarded
  field per conditional set): the status seed above, then
  `set <key> blocked-by=<k1>,<k2> --expect none --as <you>`.
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

## Board doctrine (the skill)

- First read: `ledger show --where status=open`.
- Picking loop: `ready` → claim → work → close → repeat. Stop when
  `ready` is empty (and its `total` confirms nothing beyond the limit) and
  every `blocked` entry traces, over the envelope, only to non-stale
  `in_progress` or `human_owned` keys. Don't poll.
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
- Dup defense: search before create; dups close `wontfix -m "dup of
  [[key]]"`.
- Every paste-ready command line carries the absolute binary path (a
  trial's workers typed bare `ledger` because the doctrine's lines did;
  one silently used an old binary past every rail).
- What no mechanism supplies: honoring what the id you fetched actually
  said. `--expect` proves you read the state; rules 5, 9, and 10 narrow
  the blast radius of not respecting it; judgment does the rest.

## Timestamps

Event timestamps gain sub-second resolution in rev 14: the 1-second format
makes short `--stale-after` values misfire and overworks tie-breaking.
Additive change; readers parse both.

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
harness as tests, the board skill, rev 13 amendments (verb taxonomy, error
identifiers `claim_lost`/`not_stale`/`human_owned`/`unknown_key`). Spike
branch: historical evidence, never merged.

## Test plan (numbered; the parent spec's precedent)

1. Guarded plain set → `bad_usage` naming the fix; unguarded fields ride
   along with one guarded field; two guarded fields in one set →
   `bad_usage`.
2. Seed with `--expect none`; racing seeds serialize (one winner);
   `--expect none` on a touched field → `claim_lost`.
3. Claim/close/reopen chains: each conditioned on the right event; stale
   `--expect` → `claim_lost` with correct id/author/value in the message —
   including on the reclaim path (the trial's malformed-message bug).
4. Field-scoping: label writes racing status claims never produce
   `claim_lost` (harness round, 10/10 required).
5. First-edge race under `--expect none` (harness round, 10/10).
6. Terminal→terminal → `bad_usage`; terminal→in-progress legal with the
   terminal event's id.
7. Reclaim: cross-author claim-over-claim on a fresh claim → `not_stale`
   with age+horizon; on a stale claim → succeeds; two concurrent
   reclaimers → one winner; same-author touch-base always allowed; board
   without `--stale-after` → cross-author reclaim always `not_stale`.
8. Human gate: guarded write on a `human`-labeled key → `human_owned`;
   with `--override-human` + message → succeeds and the event records the
   override; label removal then write succeeds (documented bypass — test
   asserts the two events are distinct and attributable).
9. `ready` truth table: every row, including human-labeled in-progress
   (appears in `human_owned` only) and human-labeled blocked (ditto, with
   `waiting_on`).
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

## Validation record

- **Trials** (chain-audited): `research/ledger-issues-spike-trial.md` — 2
  workers; falsified claim-by-doctrine; produced `--expect`. `trial2.md` —
  3 workers, accidental mixed-version fleet; validated `--expect`;
  produced absolute-path doctrine. `trial3.md` — 4 writers incl. live
  triager; zero duplicate work, contested reclaim serialized; produced
  `human_owned` and the terminal-transition ban.
- **Harnesses**: `research/scripts/expect-race-harness.sh` (20/20,
  independently re-run) + extended spike harness (30/30).
- **Adversarial reviews**: three rounds, six reviewers. Round 3 (on the
  rev-7 consolidation) produced rules 9 and 10, the Seed idiom, the key
  grammar rule, the truth table, totals, and this test plan.
- **Kata reconnaissance**: `~/git/kata` — what we took and declined is in
  Deferred.
