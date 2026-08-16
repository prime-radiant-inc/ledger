# Ledger as issue tracker (design)

2026-08-15, revision 7 — the consolidation. Revisions 1–6 accreted rules
through three spike rounds, three multi-agent field trials, and two
adversarial review rounds; rev 7 restates everything in final form, once,
for the implementer. History lives in the Validation record at the bottom;
where a rule exists because a trial or reviewer broke its predecessor, the
rule says so in one clause and moves on. This document extends the tool
spec (`2026-08-13-ledger-tool-design.md`, rev 13) and its additions are the
core of the tool's rev 14.

Design center, unchanged since rev 1: an issue tracker is the
investigation-ledger pattern plus three things ledgers lacked — multi-valued
fields (labels, dependency edges), filtered reads, and a race-safe way for
agents to pick and claim unblocked work. No new storage, no daemon:
everything folds from events the current store already records. Sharing
across machines stays Plan 2.

## What already works today (no changes)

Issues as keys; status vocab via enum fields; evidence-required values;
discussion as keyed notes with kinds (`comment`, `repro`, `ruling`);
attribution and provenance per event; `watch`/`since` for live triage;
CAS-safe concurrent writers; rollups for closed-thread curation of `tail`.
Priorities are just another enum field for boards that want one.

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

**Declarations** (all recorded in meta; meta is immutable, so every
declaration is validated at create):

- `--multi-field <name>`: multi-valued, vocab-free field. Tokens match
  `^[a-z0-9][a-z0-9-]*$`, comma-separated; a malformed token is
  `bad_value` naming it (unenforced grammar demonstrably broke `~=`
  matching). Fold semantics: replace wholesale per set; `name=` clears.
- `--terminal <field>=<v1>,<v2>`: values that mean "no longer blocks
  anything." Values MUST be a subset of the field's declared vocab —
  `bad_value` naming the offender (an accepted typo here permanently
  deadlocks dependents; meta has no extension verb). `--require-evidence`
  values get the same subset validation.
- `--guard <field>`: the field takes conditional writes only (the
  invariant, below).
- `--stale-after <duration>`: staleness horizon for `in_progress` entries.
  Optional; absent means nothing is ever flagged stale.
- Convention, stated: boards name the availability field `status` and the
  edge field `blocked-by`; `ready` depends on both and errors helpfully
  when they're absent. The reserved label `human` marks keys that belong
  to people. Generalizing any of these names waits for a consumer.

**`blocked-by` is a multi-field whose tokens are keys.** Each token is
validated as an existing key at write time (`unknown_key`, exit 4, message
naming the token; no near-miss suggestions — rev 13's round-7 YAGNI cut
stands). Cycles are representable and surface visibly in `blocked` as keys
waiting on each other. A "blocked" status value is deliberately absent:
blocked is derived state.

**Export/import round-trips meta byte-for-byte** — all declarations travel;
import never re-derives them. Test: an exported-then-imported board's
`ready` output is identical to the source's.

## The invariant: guarded fields take conditional writes only

`set` gains `--expect <event-id> | --expect none`. The rule exists because
five separately-discovered races (claim-verify TOCTOU, zombie reopen,
unconditioned close, unconditioned triage, first-edge 0→N) were each one
more unguarded status write; the invariant ends the enumeration.

1. **A set touching a guarded field MUST carry `--expect`**; a plain set is
   `bad_usage` naming the rule and the fix.
2. **A conditional set touches exactly one guarded field** (else
   `bad_usage`) — one `--expect` cannot speak for two field histories.
   Unguarded fields may ride along.
3. `--expect <event-id>` (SHA prefix accepted): succeeds only if the
   written guarded field's latest event on this key is still that event at
   append time. **Field-scoped**: events touching other fields never
   invalidate it (a key-scoped precondition made ordinary triage kill
   in-flight claims); notes never invalidate it (`--expect` guards field
   state, not discussion).
4. `--expect none`: succeeds only if the field has no prior event on this
   key. Two writers racing a first write both pass `none`; exactly one
   wins. (This sentinel replaced a "does the field have edges yet?" gate
   whose snapshot evaluation reopened the race it guarded.)
5. **Terminal→terminal transitions are `bad_usage`** ("reopen first, then
   re-resolve"). A trial's triager legally flipped an evidenced close to an
   unevidenced wontfix through the key's *current* id — `--expect` guards
   stale reads, not stale decisions. Revising a settled outcome is a
   two-event visible act, and an evidence-required value can never be
   silently vacated by an evidence-free one.
6. **Failure contract**: `claim_lost`, exit 4. The message names the
   winning event's id, author, and the exact value it wrote to the guarded
   field — never a fabricated or empty attribution (a trial surfaced a
   malformed message on the reclaim path; the message format is a tested
   requirement, not cosmetics). Hints: status → "re-run ledger ready and
   pick again"; blocked-by → "re-read the key's edges and merge".
7. **Atomicity contract**: the precondition (including `none`) re-validates
   against a fresh read inside the store's CAS retry loop on every attempt;
   never a pre-loop snapshot. Validated:
   `research/scripts/expect-race-harness.sh` plus the spike's extended
   harness — status races, first-edge races, and interleaved-triage rounds
   (an unguarded label write racing a status claim must never produce
   `claim_lost`), 30/30. These harness rounds ship as real tests in rev 14.
8. **Performance requirement**: the precondition read must not re-fold the
   full chain per retry (the spike did: ~70ms per 5k events per attempt, a
   cost no other write pays). Narrow to the target key/field or reuse the
   attempt's existing read.

`--expect` on a write touching zero guarded fields remains legal for any
single-field write (general read-modify-write protection).

## Filtered reads (`show --where`)

`show --where <field>=<value>` (exact) and `--where <field>~=<token>`
(token membership, multi-fields only). Repeatable, AND'd. Errors:
undeclared field → `unknown_field` with the declared-field list; `~=` on a
non-multi field → `bad_usage`; two `=` clauses on one field → `bad_usage`
(unsatisfiable). Bare `show` stays unfiltered; boards get their
open-by-default view from doctrine's first line.

## `ready`: pick unblocked work

`ledger ready [--where …] [--limit N]` — one envelope, four lists, each
bounded by `--limit` (default 50; the parent spec earned bounded reads
through a measured incident and its hottest new read verb inherits that):

- **ready**: `status=open`, not human-labeled, every `blocked-by` token
  terminal. Oldest first; timestamp ties break by chain position (an
  alphabetical tie-break systematically favored early-alphabet keys).
  Entry: `key`, `note`, `ts`, `by`, `id` — all derived from the status
  field's latest event; `id` is the claim ticket. Entries whose blockers
  include a terminal event carrying no evidence refs gain
  `unblocked_without_evidence: [keys]` — keyed to the unevidenced-
  resolution property, not to any vocab string, so `duplicate`-style
  vocabs keep the signal. The annotation is derived and recomputable;
  doctrine persists it into the claim message.
- **blocked**: same availability filter, unresolved edges, each with
  `waiting_on: [keys]`.
- **in_progress**: `status=in-progress` keys: `by`, `age`, `id` (the claim
  event — the reclaim idiom's input), `stale: true` past the horizon.
- **human_owned**: keys carrying the `human` label, excluded from `ready`
  no matter how pickable — a trial's Haiku claimed and "completed" legal
  signoff off a doctrine parenthetical; quarantine is mechanism, prose
  is not a fence.

`ready` implies `--where status=open`; a contradicting status clause is
`bad_usage`. Additional clauses compose (`ready --where labels~=relay`).
`ready` joins rev 13's data-verb taxonomy (`--ledger` addressing, standard
envelope, standard exit codes).

## The write idioms (all the same guarded write)

- **Claim**: `set <key> status=in-progress --expect <ready id> -m
  "claiming" --as <you>`. `claim_lost` → re-run `ready`, pick again. The
  claimer's `--as` IS the assignee; there is no owner field — the claim
  event's author and provenance name who, when, from where.
- **Touch-base**: a long-running claimer re-sets `status=in-progress
  --expect <own claim id> -m "still on it"`, resetting its age. Reclaimers
  see the age the mechanism actually gives them.
- **Close**: `set <key> status=closed --evidence <ref> --expect <own claim
  id>`. A `claim_lost` here means you were reclaimed while working: read
  the key's history, leave your result as a comment note, let the current
  claimant decide. Never re-close blind.
- **Reclaim**: retake a stale entry with `--expect <its in_progress id>
  -m "reclaiming from <by>: stale <age>"`. Concurrent reclaimers serialize
  on the same id (field-trialed: one winner, one clean loss).
- **Reopen**: terminal→`open` with `--expect <the terminal event's id>`.
  Just a guarded write; nothing special remains.
- **Triage**: every status write in triage is a guarded write like any
  other. Label churn is unguarded and cannot disturb claims
  (field-scoping, harness-proven).

## Board doctrine (the skill)

- First read: `ledger show --where status=open`.
- Picking loop: `ready` → claim → work → close → repeat. Stop when `ready`
  is empty and every `blocked` entry waits only on non-stale `in_progress`
  keys or `human_owned` ones. Don't poll.
- Claiming an `unblocked_without_evidence` key: name it in the claim
  message — that persists the warning into the key's own history.
- Triage moment (adapted from kata's triage skill): walk `show --where
  status=open` — keep / close with evidence / wontfix with the why in
  `-m` / add edges / re-label; sweep `in_progress` for stale entries
  (workers may decline to reclaim orphans that block nothing; triage is
  where orphans get resolved). Evidence on wontfix is NOT required —
  forcing evidence of a non-decision produces pasted-string theater; the
  honest signal is the derived annotation above.
- Recovery idiom (canonized from a trial worker): on discovering you
  clobbered or duplicated state — read the key's history, correct with an
  evidenced write naming what happened, report it. Never quietly re-fix.
- Dup defense: search before create; dups close `wontfix -m "dup of
  [[key]]"`. Discussion: keyed notes; search is the grep idiom.
- Every paste-ready command line carries the absolute binary path (two of
  three trial-2 workers typed bare `ledger` because the doctrine's command
  lines did, and one silently used an old binary past every rail).
- What no mechanism supplies: honoring what the id you fetched actually
  said. `--expect` proves you read the state; it cannot make you respect
  it. The terminal-transition ban narrows the blast radius; judgment does
  the rest.

## Timestamps

Event timestamps need sub-second resolution in rev 14: the current
1-second format makes short `--stale-after` values misfire (spike-
documented) and leaves `ready`'s tie-breaking doing more work than it
should. Additive format change; readers parse both.

## Deferred, with reasons

- **`min_writer` version floor**: cannot protect against binaries already
  in the field (a trial demonstrated a v0.1.0 writer sailing past every
  rail); value accrues only after it ships. Next round, not rev 14; fleet
  doctrine (same binary, absolute paths) is the working mitigation.
- **Multi-field token filters on `watch`/`since`**: deferred until an
  orchestrator needs them; workaround is `--key` + client-side filtering.
- Additive `block`/`unblock` verbs (sugar over guarded edge writes); FTS
  search; TUI; short IDs; cross-machine sharing (Plan 2).

## Implementation scope (rev 14, SDD with tests)

`--multi-field`, `--terminal` and `--require-evidence` subset validation,
`--guard`, `--stale-after`, token grammar, `show --where` (incl.
`unknown_field`, same-field, `~=` rules), `set --expect <id>|none`
(field-scoped, single-guarded-field, terminal-transition ban, message
format contract, atomicity per the harness, performance requirement),
`ready` (four bounded lists, annotations, chain-position ties), sub-second
timestamps, meta export/import round-trip, the extended race harness as
real tests, the board skill, and rev 13 amendments (verb taxonomy, error
identifiers). The spike branch (`wip/issues-spike`, three rounds) is
historical evidence, never merged.

## Validation record

- **Trials** (all chain-audited, not prose-graded):
  `research/ledger-issues-spike-trial.md` — 2 workers; falsified
  claim-by-doctrine (duplicate work in 90s); produced `--expect`.
  `trial2.md` — 3 workers, accidental mixed-version fleet; validated
  `--expect` where present; produced the absolute-path doctrine and the
  version-skew analysis. `trial3.md` — 4 writers incl. live triager on
  the full invariant; zero duplicate work, contested stale-reclaim
  serialized, annotation flow proven; produced `human_owned` and the
  terminal-transition ban.
- **Harnesses**: `research/scripts/expect-race-harness.sh` (status races,
  20/20, independently re-run) + the spike's extended harness (first-edge
  and interleaved-triage rounds, 30/30 total).
- **Adversarial reviews**: two rounds, four reviewers, every Critical
  adjudicated into a rule above; the two review-integrity catches (an
  unverified tie-break claim, an uncited harness) are why the trial
  reports carry errata and the harness is a committed artifact.
- **Kata reconnaissance**: `~/git/kata` — two-status minimalism, labels,
  triage doctrine, open-by-default lists; what we took and what we
  deliberately did not is recorded in Deferred.
