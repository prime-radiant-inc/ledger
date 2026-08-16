# Ledger as issue tracker (design)

2026-08-15, revision 5. Rev 5 adjudicates the second adversarial round (two
fresh reviewers, spec-as-text; verdicts: not implementation-ready, with
stated fixes — all applied here). The consolidating ruling: three rounds of
findings each caught another unguarded status write (claim-verify's TOCTOU,
the zombie-reopen, the unconditioned close, the unconditioned triage write,
the 0→N edge race), so rev 5 stops enumerating and states the invariant —
**every write to a guarded field carries `--expect`**, with `--expect none`
as the first-touch sentinel. That one rule subsumes the reopen guard
(formerly PROPOSED), dissolves the edge-gate ambiguity, and serializes every
race class found so far. Also rev 5: the wontfix-evidence requirement is
replaced by an honest derived signal (evidence of a non-decision is theater);
`in_progress` entries specified fully; staleness horizon and human-owned get
mechanisms; `ready` gets the bounded-read treatment its sibling verbs earned;
near-miss hints dropped (rev 13's YAGNI cut stands); export/import meta
round-trip stated; the validation plan gains an edge-race harness round.
History: rev 1 design → trial 1 (falsified claim-by-doctrine) → rev 2
(`--expect`) → trial 2 (validated `--expect`, exposed version skew) → rev 3
→ par round 1 (edge drops, `--terminal` deadlock, key-scoped preconditions)
→ rev 4 → par round 2 → this. Trials: `research/ledger-issues-spike-trial.md`,
`trial2.md`; harness: `research/scripts/expect-race-harness.sh`.

## What already works today (no changes)

Issues as keys; status vocab via enum fields; `--require-evidence
status=closed` as the transition rule that matters; discussion as keyed
notes with kinds (`comment`, `repro`, `ruling`); attribution and provenance
per event; `watch`/`since` for live triage; CAS-safe concurrent writers;
rollups for closed-thread curation of `tail` (the *projection* lesson from
the memory build does not bite here: boards read through `--where` filters
and `ready`, not through an unbounded spine). Priorities are just another
enum field for boards that want one.

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

- **`--multi-field <name>`**: multi-valued, vocab-free field (kebab tokens,
  comma-separated, no spaces/commas inside tokens — enforced at write time,
  `bad_value`; the spike skipped enforcement and `~=` matching demonstrably
  broke on malformed tokens). Replace-wholesale per set. `labels=` clears.
- **`--terminal <field>=<v1>,<v2>`**: which values mean "no longer blocks
  anything." Values MUST be a subset of the field's declared vocab,
  validated at create (adversarially proven: the spike accepted
  `wontfxi`, and since meta is immutable with no extension verb, one typo
  permanently deadlocked every dependent key). `--require-evidence` gets
  the same validation.
- **`--guard <field>`**: declares a field conditional-write-only (see The
  invariant, below). Boards guard `status` and `blocked-by`; `labels` stays
  unguarded — a dropped label is noise, a dropped edge is a false-ready key.
- **`--stale-after <duration>`**: the board's staleness horizon, recorded in
  meta, surfaced as `stale: true` on `in_progress` entries past it. Optional;
  without it no entry is ever flagged stale and reclaiming is pure human
  judgment.
- Field-name convention, stated plainly: boards name their availability
  field `status` and their edge field `blocked-by`. `ready` depends on both
  and says so when they're absent. (Generalizing the names waits for a
  consumer that needs it.)
- **Export/import round-trips meta byte-for-byte** — declarations
  (`fields`, `multi_fields`, `terminal`, `guard`, `require_evidence`,
  `stale_after`) travel with the board; import never re-derives them.
  Test-plan item: an exported-then-imported board's `ready` output is
  identical to the source's.

**`blocked-by` is a multi-field whose tokens are keys.** Each token is
validated as an existing key at write time (`unknown_key`, exit 4, message
naming the token; NO near-miss suggestions — rev 13 round 7 cut that
machinery and the ruling stands). Cycles are representable, never silently
dropped: they surface in `blocked` as keys waiting on each other. A
"blocked" status value is deliberately absent — blocked is derived.

## The invariant: guarded fields take conditional writes only

`set` gains `--expect <event-id> | --expect none`. A write to a guarded
field:

- MUST carry `--expect`. A plain set on a guarded field is `bad_usage`
  naming the rule. (Three review rounds each found another unguarded write
  — claim, close, triage's wontfix, reopen, first-edge — this rule ends the
  enumeration.)
- `--expect <event-id>`: succeeds only if the guarded field's latest event
  is still `<event-id>` at append time. `<event-id>` is an event SHA
  (prefix-match, same as other id args).
- `--expect none`: succeeds only if the field has NO prior event on this
  key — the first-touch sentinel. Two workers racing to write a key's first
  edges both pass `none`; exactly one wins, the loser gets `claim_lost`,
  re-reads, merges. This dissolves the rev-4 "already has edges?" gate,
  whose snapshot evaluation was adversarially shown to reopen the
  edge-drop race on the 0→N boundary.
- On failure: `claim_lost`, exit 4, message naming the event that beat you
  — its id, author, and the guarded field's value it wrote (never a
  fabricated `status=` attribution for events that touched other fields).
  Hint: "re-run ledger ready and pick again" on status; "re-read the key's
  edges and merge" on blocked-by.
- **Scope**: the precondition guards the written field's own event history
  (field-scoped — the rev-4 correction of the key-scoped spike, which let
  any triage label kill in-flight claims). Notes never invalidate it:
  `--expect` guards field state, not discussion; reading a key's notes
  before working it stays doctrine.
- **One guarded field per conditional set.** `set key status=x labels=y
  --expect <id>` is `bad_usage`: a single `--expect` cannot speak for two
  independent field histories (adversarially flagged as four-ways-ambiguous;
  the rule removes the ambiguity). Unguarded fields may ride along with a
  guarded write only when the set touches exactly one guarded field.
- **Atomicity**: the precondition (including `none`) re-validates against a
  fresh read inside the store's CAS retry loop on every attempt — never a
  pre-loop snapshot. Citation for the mechanism:
  `research/scripts/expect-race-harness.sh` (20/20 forced-race status
  rounds, one winner + one clean `claim_lost` per round, independently
  re-run). Validation plan, pre-implementation: extend the harness with a
  first-edge round (two concurrent `blocked-by` writes under `--expect
  none`) and an interleaved-triage round (label writes racing status
  claims must NOT produce claim_lost).
- Performance note for the implementation: the spike re-read the full chain
  per retry (~70ms per 5k events per attempt, a cost no other write pays);
  the precondition read must narrow to the target key/field or reuse the
  fold the retry already holds.

## Filtered reads (`show --where`)

`show --where <field>=<value>` (exact) and `--where <field>~=<token>`
(token membership, multi-fields only — `~=` on an enum field is
`bad_usage`). Repeatable, AND'd. Two `=` clauses on the same field are
`bad_usage` (unsatisfiable by construction — flagged as a silent
always-empty footgun). `--where` naming an undeclared field is
`unknown_field` with the declared-field list in the hint (rev 13 already
owns that identifier; `bad_usage`'s hint stays "the verb's --help").
Bare `show` stays unfiltered; boards get their open-by-default view from
doctrine's first line.

## `ready`: pick unblocked work

`ledger ready [--where …] [--limit N]` — one JSON envelope, three lists,
each bounded by `--limit` (default 50, kata's default; the parent spec
earned bounded reads through a measured incident and its newest, hottest
read verb inherits the discipline):

- **ready**: keys with `status=open` whose `blocked-by` tokens all have
  terminal status. Oldest first; timestamp ties break by chain position
  (rev 4 correction — the spike's alphabetical tie-break systematically
  favored early-alphabet keys; trial 1's contrary claim was false and its
  report carries an erratum). Each entry: `key`, `note`, `ts`, `by`, and
  `id` — the status field's latest event id, the claim ticket — all derived
  from that same status event (the spike's mixed derivation desynced ticket
  from row). Entries whose blockers include a terminal event carrying no
  evidence refs gain `unblocked_without_evidence: [keys]` — generalized
  from rev 4's literal-`wontfix` annotation, keyed to the property that
  matters (an unevidenced resolution) rather than a vocab string, so
  boards with `duplicate`-style vocabs keep the signal. The annotation is
  derived and recomputable from the chain at any time; doctrine has the
  claimer copy it into the claim's `-m` so it persists on the key's own
  history.
- **blocked**: same availability filter, unresolved edges, each with
  `waiting_on: [keys]`.
- **in_progress**: keys with `status=in-progress` (filter stated; the
  rev-4 text left it inferable), each with `by`, `age`, `id` (the claim
  event's id — the input the reclaim idiom needs and rev 4 forgot to
  provide), and `stale: true` when past the board's `--stale-after`.

`ready` implies `--where status=open`; a caller clause contradicting the
availability filter is `bad_usage`. Additional `--where` clauses compose
(`ready --where labels~=relay`). Rev-14 bookkeeping: `ready` joins rev 13's
data-verb list (`--ledger` addressing, standard envelope and exit codes).

## Claiming, closing, reclaiming — all the same write

- **Claim**: `set <key> status=in-progress --expect <ready-entry id> -m
  "claiming" --as <you>`. `claim_lost` → re-run `ready`, pick again. The
  claimer's `--as` is the assignee; no owner field exists — the claim
  event's author and provenance name who has it, when, from which host.
- **Touch-base** (the liveness idiom rev 4 lacked): a long-running claimer
  re-sets `status=in-progress --expect <own claim id> -m "still on it"`,
  resetting its `in_progress` age. Reclaimers check the age they see IS the
  latest signal — that's what the mechanism gives them.
- **Close**: `set <key> status=closed --evidence <ref> --expect <own claim
  id>`. Conditioned like every guarded write — the round-2 Critical: an
  unconditioned close racing a reclaim replays trial 1's double-work at the
  loop's last step. A `claim_lost` at close means you were reclaimed while
  working: read the key's history, reconcile honestly (usually: note your
  result as a comment on the key, let the current claimant decide), never
  re-close over the newer claim blind.
- **Reclaim**: a stale `in_progress` entry is retaken with `set <key>
  status=in-progress --expect <its id> -m "reclaiming from <by>: stale
  <age>"`. Two concurrent reclaimers serialize on the same `--expect`.
- **Reopen**: a terminal→non-terminal transition is just another guarded
  write — `--expect` the status field's latest event id (the close you're
  reopening). The former PROPOSED reopen rule is subsumed by the invariant;
  nothing special remains to implement. Trial 2's zombie-reopen becomes
  structurally impossible.
- **Wontfix / triage writes**: also just guarded writes — `--expect`
  required. The round-2 Critical: an unconditioned triage `wontfix` could
  silently overwrite an active claim; under the invariant the triager gets
  `claim_lost` and sees the claim they were about to bulldoze.

## Board doctrine (the skill)

- First read: `ledger show --where status=open`.
- Picking loop: `ready` → claim (`--expect`) → work → close (`--expect`,
  evidence) → repeat. Stop when `ready` is empty and every `blocked` entry
  waits (directly or transitively) on a key that is `in_progress` (someone
  is on it) or carries the reserved label `human` (the mechanism behind
  rev 4's undefined "human-owned": `labels~=human`, stated convention).
  Don't poll.
- When claiming a key annotated `unblocked_without_evidence`, name it in
  the claim message ("prereq X resolved without evidence") — that persists
  the warning into the key's own history.
- Triage moment: walk `show --where status=open`; every status write in
  triage carries `--expect` like any other (read the row, pass its id).
  The wontfix "why" lives in `-m`; evidence on wontfix is NOT required —
  rev 5 reverses rev 4 here, adjudicating the round-2 finding that
  evidence-of-a-non-decision forces theater (an unvalidated ref pasted to
  satisfy a gate), which the trust model gets nothing from. The honest
  signal is the derived annotation above, which tells downstream pickers
  precisely what happened.
- Recovery idiom (canonized from trial 2's kit): on discovering you
  clobbered or duplicated state — read the key's history, correct with an
  evidenced write naming what happened, report it. Never quietly re-fix.
- Discussion: keyed notes (`comment`, `repro`, `ruling`); dup defense is
  search-before-create; dups close `wontfix -m "dup of [[key]]"`.
- Every paste-ready command line in board doctrine carries the absolute
  binary path (two of three trial-2 workers typed bare `ledger` because
  the command lines did; one silently used an old binary past every rail).

## Deferred, with reasons

- **`min_writer` floor** (version-skew guard): next round, not rev 14 —
  it cannot protect against the binaries already in the field (trial 2's
  v0.1.0 writer), so its value starts accruing only after it ships;
  fleet doctrine (same binary, absolute paths) is the working mitigation.
  Moved out of the implementation scope explicitly — round 2 flagged that
  "PROPOSED" inside the spec body read as maybe-in-scope.
- **Multi-field token filters on `watch`/`since`** (`--value ~=`):
  deferred until an orchestrator needs to watch label/edge changes;
  workaround is `--key` + client-side filtering. Stated, not silent.
- Additive `block`/`unblock` verbs (ergonomic sugar over guarded
  `blocked-by` writes); FTS search; TUI; short IDs; sharing (Plan 2).

## Implementation scope (rev 14, SDD with tests)

`--multi-field`, `--terminal` (validated), `--guard`, `--stale-after`,
grammar enforcement, `show --where` (incl. `unknown_field`, same-field and
`~=` rules), `set --expect <id>|none` (field-scoped, atomic per the
harness contract, single-guarded-field rule), `ready` (three bounded lists,
annotations, tie-break by chain position), meta export/import round-trip,
the extended race harness (first-edge and interleaved-triage rounds) as
real tests, the board skill, and rev-13 spec amendments (verb taxonomy,
error-identifier notes). The spike branch (`wip/issues-spike`) is
historical evidence and is not merged.
