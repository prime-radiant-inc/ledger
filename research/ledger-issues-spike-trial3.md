# Issues-tracker spike trial 3 — the guarded-field invariant under four writers

2026-08-15. Spike v3 (branch `wip/issues-spike`, commit 40916cc) implements
spec rev 5: `--guard`, the always-`--expect` invariant with the `none`
sentinel, field-scoped preconditions, `--terminal`/token/where validations,
`ready` v3 (three bounded lists, `in_progress` with staleness, chain-position
tie-break, `unblocked_without_evidence`). Pre-trial mechanics: 30/30 across
the extended race harness (status races, first-edge races under `--expect
none`, and the field-scoping proof — unguarded label writes never once
killed a concurrent status claim). Trial: ten keys (dependency diamond,
planted stale ghost claim, unevidenced-wontfix prerequisite, a
`human`-labeled key with a dependent), three workers (two Sonnet, one Haiku)
plus a concurrently-writing triager, doctrine v3 with absolute paths on
every command line.

## Verdict from the chain (audited, not prose)

**The invariant held under four concurrent writers: every work key closed
exactly once, nine done.log lines for nine keys, zero duplicate work, zero
double-closes.** Trial 1's headline disease is dead. Specifics:

- Eight `claim_lost`s across the trial, every one handled per doctrine
  (re-read, pick elsewhere, no retries against fresh claims).
- **The contested stale reclaim serialized perfectly**: the ghost claim went
  `stale: true` on schedule; two workers raced to reclaim it with the same
  `in_progress` id; `--expect` picked exactly one winner, the loser saw a
  clean `claim_lost` and correctly walked away. This was par-round-2's
  open question, now field-answered.
- **Conditioned closes worked**: every close carried the closer's own claim
  id; nobody closed over anyone.
- **The annotation flowed end to end**: `optional-polish` surfaced
  `unblocked_without_evidence: ["nice-to-have"]` in `ready`, and its claimer
  copied it into the claim message exactly per doctrine.
- The absolute-path doctrine fix worked: all four agents used the right
  binary (trial 2's version-skew accident did not recur).
- Workers waited for the `stale` flag rather than jumping early claims.

## Finding 1 (mechanism beats doctrine, third occurrence): the Haiku claimed the human-labeled key

`get-legal-ok` (labels=human) appeared in `ready` — open, unblocked, fully
pickable — and the doctrine's only fence was a stop-condition parenthetical.
Both Sonnets left it alone, citing the label; the Haiku claimed it, "did"
the legal signoff, closed it with evidence, and then closed its dependent
`announce`. The lesson is the same one the memory build and both prior
trials taught: **a sentence cannot outweigh a pickable entry.** Rev 6:
`ready` excludes keys carrying the board's human label into their own
`human_owned` list — quarantine by mechanism, not prose.

## Finding 2 (the trial's deepest): `--expect` guards stale reads, not stale decisions

The triager, instructed to wontfix `quick-chore`, found it already closed
with evidence, read its CURRENT status id, passed it, and legally flipped
an evidenced-closed task to unevidenced `wontfix` — no race, no error, a
perfectly valid conditional write producing the wrong outcome (and an
evidence downgrade: `--require-evidence status=closed` was satisfied, then
silently vacated by a transition to a value with no evidence rule). The
agent itself flagged the outcome as wrong while its write sat `ok: true`.
Rev 6: **terminal→terminal transitions are `bad_usage`** ("reopen first,
then re-resolve") — revision of a settled outcome becomes a two-event,
visible act, and evidence-required states can't be quietly overwritten by
evidence-free ones. (What no mechanism can supply: the judgment to honor
what the id you fetched actually said. That stays doctrine, now with a
sharper teaching example.)

## Smaller findings

- `claim_lost`'s message is malformed on one path: "event f6681aa065 by
  ash2 (status=beat you to 'old-cleanup'" — the winner's value and closing
  paren went missing (likely the reclaim-over-stale path). Implementation
  bug class for the real build's tests.
- Event timestamps have 1-second resolution; `--stale-after` below ~1s
  misfires (builder-documented). Real implementation needs sub-second
  timestamps for staleness fidelity.
- The board drained in ~12 seconds, so the triager's live label churn
  landed after the claims it was meant to interleave with — the
  field-scoping non-interference claim rests on the mechanical harness
  round (10/10), not on live overlap this trial. Honest limit.
- Workers may decline to reclaim stale entries that block nothing
  (one did); doctrine reads "reclaimable," not "must reclaim" — a board can
  end drained-except-stale-orphans. Acceptable; note for triage doctrine
  (stale entries are a triage-moment sweep item).

## Where this leaves the design

Three spike rounds, three trials, two adversarial panel rounds. The core is
now boring in the best way: picking, claiming, closing, reclaiming, and
triage all serialize through one invariant that agents demonstrably operate
from one page of doctrine, across model classes. The two rev-6 amendments
(human-label quarantine, terminal-transition ban) are both one-rule fixes
in the established pattern — mechanism replacing prose exactly where a
trial showed prose losing.
