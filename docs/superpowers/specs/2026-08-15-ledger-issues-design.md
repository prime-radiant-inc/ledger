# Ledger as issue tracker (design)

2026-08-15, revision 3. Rev 3 folds in the second trial
(`research/ledger-issues-spike-trial2.md`), which validated `--expect`
(atomic in a 20-round forced-race harness and across a live worker's seven
claims) and — by accident — ran a mixed-version fleet: two of three workers
used the PATH binary lacking every new feature and wrote straight past the
safety rails (one double-work, one zombie-reopen of a closed issue, both
self-corrected and fully attributable in the chain). New in rev 3:
reopening a terminal-status key requires `--expect`; board meta gains a
`min_writer` floor enforced by rev-14+ binaries (forward-only guard,
honestly limited); doctrine rule that paste-ready command lines carry the
absolute binary path (proven twice now); kit's detect-correct-report
sequence canonized as the recovery idiom. Rev 2 folds in the two-worker spike trial
(`research/ledger-issues-spike-trial.md`): the "claim discipline needs no
mechanism" bet was falsified live — both workers passed the verify snapshot
and duplicated work within 90 seconds of board start — so claiming gains a
**conditional write** (`--expect`), and verify-after-claim is demoted to a
fallback. Everything else survived contact: `ready` ordered a dependency
diamond correctly across concurrent workers with zero tool errors, and
`blocked`/`waiting_on` was used by both workers (once to decide termination,
once to sanity-check the DAG). What it takes for a ledger to serve as a
cross-session issue tracker with agent work-picking, grounded in the kata
comparison (`~/git/kata`, deliberately-small issue model: two statuses,
labels, links, triage doctrine, open-by-default filtered list) and the
memory build's structural lesson (the fold needs value-level filtering;
rollups can't curate it). Three upstream tool additions (spec rev 14
candidates), one derived-state verb, and doctrine. No new storage, no
daemon, no schema migration: everything folds from events the current
store already records.

## What already works today (no changes)

Issues as keys; status vocab via enum fields; `--require-evidence
status=closed` as the only transition rule that matters; discussion as
keyed notes with kinds (`comment`, `repro`, `ruling`); attribution and
provenance per event; `watch`/`since` for live triage; CAS-safe concurrent
writers; rollups for closed-thread curation of `tail`. Priorities are just
another enum field for boards that want one.

## Addition 1: multi-valued free fields (`--multi-field`)

`create --multi-field <name>` declares a field that is **multi-valued and
vocab-free** — the folksonomy axis kata calls labels, deliberately outside
the closed-vocab discipline that governs enum fields (tags earn no
`vocab add` ceremony; misspelled tags are noise, not corruption).

- Value grammar: comma-separated kebab tokens. `set relay-storm
  labels=bug,relay,regression`. No spaces or commas inside a token.
- Fold semantics: **replace wholesale** per set, exactly like every other
  field (concurrent taggers can drop each other's edits; same accepted
  class as any field race). `labels=` (empty) clears.
- Rendered in `show`/`render` as the literal token list.

Two declared multi-fields make an issue board:

```
ledger create issues --scope "issue tracker for <repo>" \
    --field status=open,in-progress,closed,wontfix \
    --terminal status=closed,wontfix \
    --multi-field labels --multi-field blocked-by \
    --require-evidence status=closed
```

**`blocked-by` is just a multi-field whose tokens are keys.** One
mechanism, two features. Edges referencing keys get set-time validation
(addition 3); a "blocked" *status* value is deliberately absent — blocked
is derived state, and deriving it is the whole point.

## Addition 2: filtered reads (`show --where`)

`show --where <field>=<value>` (exact match) and `--where
<field>~=<token>` (token membership, for multi-fields). Repeatable flags
AND together:

```
ledger show --where status=open --where labels~=relay
```

Bare `show` stays unfiltered (it serves every ledger role; boards get
their kata-style open-by-default view from doctrine's first line, not from
a changed global default). Filter evaluation is fold-side: rows whose
named field's current value matches. A `--where` naming an undeclared
field is `bad_usage` with the declared-field list in the hint.

## Addition 3: edge validation on write

A `set` writing to a multi-field named `blocked-by` (by convention: any
multi-field whose name ends in `-by` or is declared `--edges`? — **ruling:
keep it simple, `blocked-by` is a reserved multi-field name** with edge
semantics; other multi-fields are plain tags) validates each token is an
existing key in this ledger, failing with `unknown_event`-style error
(`unknown_key`, exit 4, hint listing near-miss keys). Catches typos at
write time, same philosophy as rollup's children-must-exist. Dangling refs
can still arise later only via... nothing — keys are never deleted. Cycles
(A blocked-by B, B blocked-by A) are representable and not rejected
(detection at write time costs a graph walk; the failure mode is visible,
not silent — see `ready`).

## Addition 4: the `ready` verb (pick unblocked work)

`--terminal <field>=<v1>,<v2>` at create declares which values mean "this
key no longer blocks anything" (recorded in meta; extendable via `vocab
add`-style self-serve later if needed).

`ledger ready [--where …]` returns the work-picking view, one JSON
envelope:

- **ready**: keys where `status=open` (the availability filter — `ready`
  implies `--where status=open` unless the caller overrides) AND every
  `blocked-by` token's status is terminal. Sorted oldest-first (FIFO
  fairness for pickers).
- **blocked**: keys matching the same availability filter whose edges are
  unresolved, each with `waiting_on: [keys]` — the *why*, so agents and
  humans see the dependency frontier without a second query. A cycle
  appears here as two keys waiting on each other: visible, never silently
  dropped.

Additional `--where` flags compose (e.g. `ready --where labels~=relay`
scopes picking to a subsystem).

## Claiming (rev 2: conditional write, mechanism required)

Rev 1 bet that a claim-then-verify idiom sufficed. The spike trial falsified
it: the verify step is a point-in-time snapshot, and a claim landing after
one worker's check but before its close is invisible to both sides — both
workers legitimately "held" the same key and duplicated the work. Claiming
needs atomicity the fold can't retroactively supply.

**Addition 5: conditional writes.** `set <key> … --expect <event-id>`
succeeds only if the key's latest event is still `<event-id>` at append
time; otherwise `claim_lost`, exit 4, message naming the event that beat
you. The store's ref-CAS retry loop re-validates the precondition on every
retry, so the check is genuinely atomic, and claiming becomes first-wins
(rev 1's fold-latest rule was last-wins — a late claimer silently stole).
Generally useful for any read-modify-write on a key, not just claims.

The claim idiom becomes: read `ready` (each entry carries its key's latest
event id), then `set <key> status=in-progress --expect <id> -m "claiming"`.
A `claim_lost` means someone beat you — re-run `ready`. Verify-after-claim
remains documented only as the fallback for boards driven through tools
without `--expect`.

The **claimer's `--as` is the assignee** — no owner field exists. The
in-progress event's author and provenance answer "who has this" with more
honesty than an assignee box (it names the session that actually claimed
it, when, from which host). Unclaiming is `status=open -m "yielding: <why>"`.

**Rev 3: reopening requires proof of knowledge.** A `set` that moves a key
FROM a terminal status to a non-terminal one requires `--expect` — trial 2
produced the exact stale-write this stops (a worker on a 45-second-old read
silently reopened an issue closed with evidence; a plain set is just a
valid write, so nothing objected). A deliberate reopen trivially satisfies
the rule: read the key, pass its id. Same primitive, one more silent-clobber
class closed.

**Rev 3: `min_writer` floor.** Boards created with rev-14 features record a
minimum writer version in meta; rev-14+ binaries refuse to write to a board
above their version (clean error naming the floor). Honest limit: binaries
predating the mechanism can't be retrofitted and will still write plain
sets past every rail — trial 2 demonstrated exactly this with a v0.1.0
writer on a spike board. The floor guards future skew only; fleet-dispatch
doctrine (same binary for all workers, absolute paths in dictated commands)
guards the rest.

Doctrine additions from the trials: the stop condition spells out that
"blocked only on another worker's in-progress key" counts as finished for
your session (don't poll); `ready`'s oldest-first ordering breaks timestamp
ties by chain position; every paste-ready command line in board doctrine
carries the absolute binary path (two of three trial-2 workers typed bare
`ledger` because the command lines did); and the recovery idiom when you
discover you've clobbered someone's state: read the key's history, correct
with an evidenced write naming what happened, report it — never quietly
re-fix.

## Board doctrine (the skill sketch, `using-ledger` addition or sibling)

- First line of every board read: `ledger show --where status=open`
  (kata's default, as doctrine).
- Work-picking loop: `ready` → claim → verify → work → `status=closed
  --evidence <ref>` → repeat until `ready` returns empty and `blocked` is
  empty or human-owned.
- Triage moment (kata's `kata-triage`, adapted): walk `show --where
  status=open`, decide each — keep / close with evidence / `wontfix` with
  why / add `blocked-by` edges / re-label. At session end or on request.
- Discussion: notes with `--key`, kind `comment`; repros as kind `repro`;
  decisions as kind `ruling`. Search is the grep idiom over `tail --raw`.
- Duplicate defense: search before create (grep the board for key
  candidates); a duplicate discovered later is closed `wontfix -m
  "dup of [[<key>]]"`.
- Cross-refs: `[[key]]` in messages; commits as evidence refs.

## Deliberately not taken (from kata, with reasons)

Daemon and TUI (git is the daemon; projections are the human surface);
soft-delete/restore/purge tiers (append-only + status filtering + ref
surgery for secrets); counter short-IDs (slugs for humans, SHAs for
precision); FTS search (grep idiom until scale demands otherwise);
in-repo `.kata.toml`-style binding (store resolution already does this).
Sharing across machines/people remains Plan 2's sync layer — issue boards
are its best forcing function yet, but that's a separate decision.

## Spike scope (tiniest, throwaway, no tests — per Jesse)

Branch of the Go tool: `--multi-field` + `--terminal` recorded in create
meta; multi-field set values skip vocab validation (stored as the literal
comma string — zero model change); `blocked-by` token existence check;
`show --where` (= and ~=); `ready` verb with ready/blocked+waiting_on
output. Skip: tail filtering, cycle detection, near-miss hints, render
changes, doc changes. Validated by a two-worker field trial: seeded board
with a dependency chain, two concurrent agents running the picking loop,
success = work completed in dependency order with no double-completion
and the claim-verify idiom surviving a real race.

## Open questions the spike should answer

1. Does replace-wholesale on `blocked-by` bite when two writers edit edges
   concurrently, or is it as benign as predicted?
2. Is claim-verify discipline followable by agents from doctrine alone, or
   does it need a `claim` verb (mechanism) in the real version?
3. Does `ready`'s blocked+waiting_on output actually get used by agents,
   or do they only read the ready list?
