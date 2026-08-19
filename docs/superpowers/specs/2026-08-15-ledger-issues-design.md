# Ledger as issue tracker (design)

2026-08-16, revision 17 — the rethink (rev 12) hardened by four blind
adversarial rounds (the seventh through tenth), plus the trial-5
cycle redesign: cycles are detected regardless of who holds their
members, and every cycle entry carries its own paste-ready fix, so any
agent or person who sees a deadlock can break it immediately.
Eleven revisions, three spikes,
three chain-audited field trials, and six adversarial rounds (twelve
reviewers) validated the core mechanics and repeatedly punished the same
three composition mistakes: protection built by enumerating cases instead
of deriving from invariants (each enumeration missed one — the
claim-verify TOCTOU, the zombie reopen, the unconditioned close, the
terminal-value eviction, the guard-not-required hole); a graph algorithm
placed on the doctrine side of the tool line (the stop-condition walk bred
Criticals in four consecutive rounds — every agent re-deriving a DFS from
prose is maximally fragile); and doctrine leaning on substrate capabilities
the tool verifiably lacks (field-scoped watch, staleness events). Rev 12 is
a redesign against those diagnoses, not another patch: **one override
mechanism against tool-computed standing signals** replaces three
interference rules and the reopen special case; **the frontier verdict is
computed by `ready` itself** and the walk ceases to exist as doctrine; the
envelope is organized by what consumers do (pick, respect, wait, triage)
rather than by status value. Everything the trials and harnesses validated
is preserved unchanged. History: Validation record, bottom. Extends the
tool spec (`2026-08-13-ledger-tool-design.md`, rev 13); these additions are
the tool's rev 14.

Design center, unchanged since rev 1: an issue tracker is the
investigation-ledger pattern plus three things ledgers lacked —
multi-valued fields (labels, dependency edges), filtered reads, and a
race-safe way for agents to pick and claim unblocked work. No new storage,
no daemon. Sharing across machines stays Plan 2.

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
create — immutability makes create-time validation load-bearing, since a
bad declaration has no repair path):

- `--multi-field <name>`: multi-valued, vocab-free. Tokens match
  `^[a-z0-9][a-z0-9-]*$`, comma-separated; malformed token → `bad_value`
  naming it. Replace-wholesale per set; `name=` clears.
- `--terminal <field>=<v1>,<v2>`: values that end a key's participation as
  a blocker. Values MUST be a subset of the field's vocab (`bad_value`).
  `--require-evidence` values get the same subset check.
- `--guard <field>`: the field takes conditional writes only (the
  invariant). The named field MUST be declared (`bad_value` — a typo'd
  guard silently disabled every protection on the board).
- `--stale-after <duration>`: staleness horizon (Go `time.ParseDuration`;
  `age` outputs render the same way). Optional; without it no claim is
  ever stale.

**Ready-capability is syntactic and all-or-nothing**: declaring
`--terminal` on a field named `status` opts the board in, and create then
REQUIRES the full shape — `--guard status`; non-terminal vocab exactly
`{open, in-progress}`, both present; a `labels` multi-field declared;
`--guard blocked-by` whenever `blocked-by` is declared — each violation
`bad_value` naming the fix. (A board wanting terminal semantics without
issue-tracker behavior names its field something else.) The reserved
token `human` in `labels` — that field only, never another multi-field —
marks keys that belong to people; requiring `labels` at create is what
keeps the quarantine signal from being silently impossible on a board
whose immutable declarations forgot it. On a ready-capable board, keys must match the multi-field token
grammar, enforced at each key's first write (`bad_value`) — otherwise
legally-named keys can exist that no `blocked-by` edge can reference.

**On a ready-capable board, `blocked-by` tokens are keys**, each
validated as existing at write time (`unknown_key`, exit 4, naming the
token; no near-miss suggestions). On a plain board a multi-field named
`blocked-by` is just a multi-field — no existence validation, no edge
semantics; everything issue-tracker-specific in this document (the
rule-5 signals, `blocked-by`'s special treatment, key grammar, titles,
`ready`) is ready-capable-board behavior, and a plain board's `--guard`
buys the CAS rules alone.
Cycles are representable; the tool surfaces them (see `ready`), never
silently drops them. A "blocked" status value is deliberately absent:
blocked is derived state.

**Titles** (ready-capable boards only — plain boards keep the parent
spec's `set` semantics): a key's title is the message of its first
status event, computed from history rather than stored, STABLE BY
DEFAULT — changed only by explicit, labeled rename events (tool rev 16:
`set <key> --rename "<new title>"`, whose text wins over the seed
message from the latest rename in fold order, and which every
title-bearing surface renders as renamed with attribution) — carried by
every `ready`/`held`/`blocked` entry, every `show` row, and
`attention`'s stale-claim entries — structurally absent only where no
single titled key exists (`statusless` entries precede any status
event; `cycle` entries name several keys). The first status write
REQUIRES a
non-empty `-m` after trimming (`empty_body`, exit 4, hint naming it as
the title).

**Export/import round-trips meta byte-for-byte except `slug`**, which a
same-store import necessarily re-mints (import refuses an existing
slug); import never re-derives declarations, but it RE-VALIDATES them —
the same ready-capability shape checks create runs, same `bad_value`
errors — because import is a second meta-minting path and "no repair
path" makes every minting path load-bearing. An exported-then-imported
board's `ready` output is identical except event `id`s and the
envelope's `ledger` name; importing into a fresh store under the
original slug is identical except `id`s alone.

## The invariant: guarded fields take conditional writes only

`set` gains `--expect <event-id> | --expect none` and `--override`.
Eight rules; the first four are the harness-validated CAS core, rule 5 is
the single interference gate that replaced three enumerated ones.

1. **A set touching a guarded field MUST carry `--expect`**; a plain set
   is `bad_usage` naming the rule and the fix.
2. **A conditional set touches exactly one guarded field** (else
   `bad_usage`); unguarded fields may ride along. `--expect` on a write
   touching zero guarded fields stays legal for any single-field write
   and means what it always means — real CAS against that field's
   latest event (`claim_lost` with the generic hint on mismatch;
   `--expect none` likewise). Guarding makes `--expect` mandatory,
   never exclusive; it is never accepted-and-ignored.
3. `--expect <event-id>` (SHA prefix accepted): succeeds only if the
   written guarded field's latest event on this key is still that event
   at append time. A SECOND, rename-scoped CAS stream exists alongside
   this field-scoped one (tool rev 16): on `set --rename`, `--expect` is
   never required and, when passed, is CAS against the key's latest
   RENAME event, with `--expect none` meaning never renamed. Its
   `claim_lost` names the rename that won — `event <id> by <author>
   already renamed '<key>' to "<title>"` — and hints "read the current
   title first". **Field-scoped**: other fields' events never
   invalidate it; notes never invalidate it. Mismatch → `claim_lost`,
   exit 4; the message names the winning event's id, author, and the
   exact value it wrote to this field (tested format — a trial shipped a
   malformed one); hints, dispatched by board capability first and
   field second (a plain board must never be told to run a verb that is
   `bad_usage` for it): on ready-capable boards, status → "re-run
   ledger ready and pick again" — EXCEPT when the attempted write's
   value is terminal, where it is "you were reclaimed while working —
   leave a handoff note; never re-close blind" (the Close idiom's
   doctrine, produced by the tool itself, since a field-only hint would
   tell a failed closer to abandon finished work) — and blocked-by →
   "re-read the key's edges and merge"; every other case, including
   EVERY guarded field on a plain board whatever its name, → "re-read
   '<field>' and try again".
4. `--expect none`: succeeds only if the field has no prior event on this
   key. Racing first writes serialize to one winner; the loser's hint on
   a status seed is "this key already exists — read it; if yours is a
   different issue, re-seed under a new key." On an edge seed it is "this
   key already has edges — read it; if yours is a different issue,
   re-seed under a new key" — never a merge suggestion, which on a name
   collision is exactly the contamination the Seed idiom's recovery text
   exists to undo. Any other field falls back to rule 3's generic hint.
5. **Standing signals and `--override`.** Before landing, a guarded write
   is checked against three signals the tool computes from current state.
   Rule 5 exists only on ready-capable boards — a plain guarded board
   has no `status` vocab or `labels` reservation to compute signals
   from, so its guarded writes get the CAS rules (1–4, 6–8) and nothing
   else. **Scope, stated**: `human` is key-scoped and gates every guarded
   write — and, as a named exception class OUTSIDE guarded-field writes,
   it additionally gates a RENAME (tool rev 16), recorded with the same
   `override:` field: retitling a person's reserved issue under them is
   the friction the label exists to create. `claim` and `settled` are
   computed from the key's `status` field and gate `status` writes only;
   neither gates a rename, because a title is not an outcome. An edge edit on a claimed or
   settled key is deliberately ungated beyond CAS and `human`: an edge
   added to a live claim surfaces in its `held` entry's `waiting_on`,
   and a settled key's own edges are moot — its terminal value already
   ended its life as a blocker.
   - **claim**: the status field's current value is `in-progress`, non-stale,
     by a different author. (Your own claim is never a signal against
     you; a stale claim is not a signal — stale work is freely
     reclaimable.)
   - **human**: the key carries the `human` label. Applies to everyone,
     including the claimant and a key's very first seed — labeling is how
     a person says "mine."
   - **settled**: the status field's current value is terminal. Applies to
     everyone, including the author of the close — reopening or
     re-resolving a settled outcome is a revision, and revisions are
     visible. (This replaces the old terminal→terminal ban and the
     reopen special case: one mechanism, better audit trail — a greppable
     override marker instead of an unmarked reopen pair.)
   If any signal stands, the write fails `needs_override`, exit 4; the
   message lists every standing signal with its facts (claimant and age;
   the label; the settled value and its evidence state) and the hint is
   the paste-ready fix: `--override -m "<why>"`. With `--override` and a
   non-empty trimmed message (`bad_usage` otherwise), the write lands and
   the event records `override: <signal[,signal...]>` — computed by the
   tool, not asserted by the caller. Honest limits, stated: identity is
   asserted and `labels` is unguarded, so the human signal is friction
   and attributable visibility, not authentication — label removal is a
   bypass that is itself a separate, visible, attributable act.
6. **Staleness**: a claim is stale when its age exceeds `--stale-after`
   (no horizon declared → never stale). Staleness is computed at append
   time from the fresh read, never from the caller's view. There is no
   staleness *event* — nothing fires at the horizon; readers notice via
   `ready` (below) and watch timeouts (doctrine).
7. **Atomicity contract**: every check in rules 3–6 re-validates against
   a fresh read inside the store's CAS retry loop on every attempt; never
   a pre-loop snapshot. Validation, split honestly: the CAS core (rules
   3–4, field-scoping) is harness-validated
   (`research/scripts/expect-race-harness.sh` 20/20; the spike's extended
   harness 30/30, committed). Rule 5's signal checks are REASONED, NOT
   YET HARNESSED — their harness rounds are mandatory rev-14 tests and
   must pass before the validation claim covers them.
8. **Performance requirement**: the precondition read narrows to the
   **target key** (all its fields — a field-narrowed read cannot answer
   the label or status checks), resolving the key's status, the written
   field, labels, and staleness inputs from the most recent history
   touching them. The contract is a bound, not an algorithm: cost
   scales with the target key's touched-history depth, never the whole
   board's event count, and the per-event-subprocess pattern the parent
   spec bans stays banned. This is NEW read machinery — the parent
   spec's only batched read is the one-shot whole-chain fold, so the
   backward windowed walk that meets this bound is rev-14
   implementation work validated by the measured test, not an inherited
   primitive. An edge write's `blocked-by` existence checks resolve
   from the same single walk (a key exists once any set event names it —
   note-only keys are not blockers) —
   with the honest asymmetry that proving a token NONEXISTENT requires
   reaching the chain root, so the `unknown_key` rejection path is the
   walk's degenerate case, stated and measured with the rest.
   Honest worst case: a long-untouched key degrades toward a full scan;
   the bound test measures the common case and states the degenerate one.
   "Reuse" of reads is within-attempt only; cross-attempt caching is the
   pre-loop snapshot rule 7 forbids.

## Filtered reads (`show --where`)

`show --where <field>=<value>` (exact) and `--where <field>~=<token>`
(membership, multi-fields only; `~=` on an enum field is `bad_usage`,
and `=` on a multi-field is `bad_usage` too — sets have no
exact-string identity; use `~=`).
Repeatable, AND'd; two `=` clauses on one field are `bad_usage`
(unsatisfiable). Undeclared field → `unknown_field` with the declared
list. Bare `show` stays unfiltered; on ready-capable boards `show` rows
carry `title`.

## `ready`: the board, answered

`ledger ready [--where …] [--limit N]` returns one envelope that answers
the three questions a board serves — what can I pick, what should I
respect, does anything need a person — including the **frontier verdict**
the tool computes so no agent ever re-derives graph logic from prose
(the walk-as-doctrine bred bugs in four consecutive review rounds; it is
now ordinary tested tool code):

```json
{"ledger": "issues", "ok": true,
 "frontier": "work-available",
 "ready":     [{"key": "fix-retry", "title": "…", "note": "…", "ts": "…",
                "by": "…", "id": "8240f7351e",
                "unblocked_without_evidence": ["spike-probe"]}],
 "held":      [{"key": "sign-off", "title": "…", "kind": "human",
                "status": "open", "by": "…", "ts": "…", "id": "…"},
               {"key": "big-task", "title": "…", "kind": "claim",
                "by": "worker-2", "age": "14m", "id": "…", "stale": false,
                "waiting_on": [{"key": "dep-x", "state": "open"}]}],
 "blocked":   [{"key": "deploy", "title": "…", "note": "…", "ts": "…",
                "by": "…", "id": "…", "waiting_on": [{"key": "sign-off",
                                                      "state": "human"}]}],
 "attention": [{"reason": "stale-claim", "key": "orphaned-task",
                "by": "dead-worker", "age": "3h", "id": "…"},
               {"reason": "statusless", "key": "half-seeded"},
               {"reason": "cycle", "keys": ["a", "b"],
                "break": {"key": "b", "drop": "a", "keep": "",
                          "expect": "973b94fa05", "human": false}}],
 "totals": {"ready": 1, "held": 2, "blocked": 1, "attention": 3}}
```

- **frontier** ∈ `work-available | all-handled | attention-needed`,
  computed over the FULL board regardless of `--limit`:
  `work-available` when anything is pickable now or reclaimable
  (non-empty ready, or a stale claim on a key NOT labeled `human` — a
  human-labeled key's stale claim needs a person's override, so it
  drives `attention-needed`, never `work-available`); else
  `attention-needed` when the
  attention list is non-empty; else `all-handled` — every dependency
  chain terminates in a live claim or a non-terminal human-owned key (a
  terminal status resolves an edge regardless of label), verified
  internally with a correct DFS (path-stack cycle detection, shared-
  dependency memo — diamonds are legal and never false-flagged).
  **Cycle detection is holder-blind**: it runs over ALL non-terminal
  keys regardless of status value or labels (only terminal keys are
  excluded — their edges are moot), so a deadlock through a live claim
  or a human-reserved key is detected, lands in `attention`, and drives
  `attention-needed`; a genuinely deadlocked board can never read
  `all-handled`. (Trial 5: the earlier open-keys-only walk let exactly
  that happen — the claim was a story, the circle of arrows a fact, and
  the design let the story overrule the fact.)
- **ready**: pickable now — `status=open`, not human-labeled, every edge
  terminal. Oldest first, timestamp ties by chain position. `id` is the
  claim ticket (the status field's latest event). The
  `unblocked_without_evidence` annotation lists blockers whose terminal
  event carries no evidence refs — a floor against omission, not a
  defense against fabrication (refs are unvalidated by design; `ledger
  verify` stays v2).
- **held**: spoken for — claims (`kind: claim`, with claimant, age, claim
  id, `stale` flag, and `waiting_on` when the claimed key has unresolved
  edges: claimed-but-blocked is legal and visible) and human-owned keys
  (`kind: human`, any non-terminal status; excluded from `ready` no
  matter how pickable — quarantine is mechanism). The label dominates
  status for placement, never information: a human-labeled key that is
  also actively claimed renders `kind: human` AND carries the claim
  fields (`by` the claimant, `age`, claim `id`, `stale`), and human
  entries carry `waiting_on` under the same unresolved-edges condition
  as claims — a human-owned key's dependencies stay visible even though
  the label excludes it from `blocked`. Entries carry
  the `id` needed to act on them.
- **blocked**: waiting — open, unlabeled, unresolved edges. `waiting_on`
  entries are `{key, state}` objects, `state` ∈ `terminal | open |
  in-progress | in-progress-stale | human | statusless` — `terminal`
  wins whenever the blocker's status is terminal, labeled or not;
  `human` names only a non-terminal human-owned blocker, mirroring
  `held`'s carve-out. (`waiting_on` folds staleness into `state`
  because one discriminator covers six states; `held`'s claims are
  always `in-progress`, so a bare `stale` boolean suffices there —
  deliberate, not drift. One accepted flattening: a blocker both
  human-owned and actively claimed renders `state: human` — the claim
  detail lives in that key's own `held` entry.) `waiting_on` is informational — the frontier
  verdict already did the walking — but blocked entries carry the
  status field's latest `id`, so "blocked is not locked" (below) is
  exercisable straight from the envelope.
- **attention**: the triage queue, tool-computed — stale claims,
  statusless keys (half-seeds and orphans), and cycles. **A cycle entry
  carries its own fix**: `keys` (the detected cycle's members) plus
  `break` — `{key, drop, keep, expect, human}` — the suggested repair as
  a paste-ready guarded write: rewrite `key`'s `blocked-by` to `keep`
  (dropping `drop`, all occurrences), `--expect` the given id (the edge
  field's latest event, the CAS ticket), `human: true` when `key`
  carries the `human` label (the write then needs `--override`). The
  suggested edge is the YOUNGEST in the cycle — the write that closed
  the loop is the likeliest mistake — but the suggestion is structural,
  not semantic: at least one edge in any cycle is a false dependency,
  and an agent who can see from titles or history that a DIFFERENT edge
  is the lie breaks that one instead, saying why in `-m` (trial-proven:
  a worker correctly overrode a staged wrong suggestion). Identical
  cycle entries (same member set) are deduplicated. Entries may also
  appear in their home lists; this list is the view triage sweeps.

Lists are bounded by `--limit` (default 50, per list) with true counts in
`totals`; `frontier` never lies from truncation. The verdict prioritizes
work over triage by design — `attention-needed` shows only when nothing
is pickable, so on a continuously busy board it may never show — but the
`attention` list and `totals.attention` ride in every envelope
regardless, and doctrine makes any non-zero `totals.attention` a triage
cue in its own right, never gated on the verdict. `ready` sorts oldest
first with chain-position ties; the other lists sort by key ascending
(stable doc-harness output). Extra `--where` clauses apply uniformly to
all lists, each clause composing with each list's own membership rule —
no special cases: a clause may legitimately empty any list or every
list (`status=closed` empties all four, a claim is never `status=open`),
which is filtering, not an error.
`ready` joins rev 13's data-verb taxonomy (`--ledger`
addressing, standard envelope, exit codes). **Performance**: `ready` is
the loop's hottest read; implementation acceptance is pinned now, not
deferred: at the parent spec's 5k-event scale (including touch-base
churn, which scales with wall-clock, not issue count), on the hardware
of its measurements, the full envelope completes within 2× the measured
batched fold (70ms measured → 140ms bound), degenerate cases stated in
the test report. Stated
intent: **blocked is not locked** — claiming a blocked key by name with a
valid `--expect` is legal, visible, attributable; the fences are
non-surfacing and doctrine.

## The write idioms

All are the same guarded write; every idiom line in the shipped skill
carries the absolute binary path (trial-proven: two of three workers once
typed bare `ledger` because the doctrine's lines did). On a
human-labeled key, EVERY guarded write below — touch-base and close
included, not just the idioms that spell it out — carries
`--override -m` per rule 5; the variant isn't repeated per idiom.

- **Seed**: `set <key> status=open --expect none -m "<title>"`. With
  dependencies, **edges first**: `set <key> blocked-by=<k1>,<k2>
  --expect none`, then the status seed — a statusless key is unpickable
  and in neither `ready` nor `blocked` (a `ready` run inside the window
  shows it only under `attention` as a half-seed: momentary, harmless),
  so the dependency window doesn't exist. Seed collision:
  the corrupting write is the one that SUCCEEDS (your edge write landing
  on a stranger's edge-free key); your own `--expect none` success proves
  the key had no prior edges, so recovery is deterministic —
  `set <key> blocked-by= --expect <your edge event id> -m "reverting:
  seed collision"` (add `--override -m` if the stranger's key is
  human-labeled — sanctioned, the message names the collision) — then
  re-seed under a new name. Never chain the two writes without checking
  exit codes. Seeding a pre-`human`-labeled key (a legitimate way to
  reserve planned work): the one `-m` is BOTH title and override
  justification by design — `set <key> status=open --expect none
  --override -m "<title> — reserved for <who>: <why>"`.
- **Claim**: `set <key> status=in-progress --expect <ready id> -m
  "claiming"`. `claim_lost` → re-run `ready`, pick again. The claimer's
  `--as` IS the assignee; provenance names who, when, from where.
- **Touch-base**: re-set `status=in-progress --expect <own claim id> -m
  "still on it"` at roughly half the horizon, only while actively
  working. Touch-bases are events; boards pick horizons matching their
  tasks, not the reverse.
- **Close**: `set <key> status=closed --evidence <ref> --expect <own
  claim id> -m "done"`. A `claim_lost` here means you were reclaimed
  while working: leave your result as a `handoff` note and let the
  current claimant decide — never re-close blind. The winning claimant's
  duty: when the chain shows the key was ever reclaimed, check
  `notes --key <key>` for `handoff` notes before closing.
- **Reclaim**: a stale claim (from `attention`) is retaken with
  `set <key> status=in-progress --expect <its id> -m "reclaiming from
  <by>: stale <age>"` — no override needed unless the key is also
  human-labeled (`human` has no staleness exception); staleness
  dissolved the claim signal. Concurrent reclaimers serialize on the
  same id.
- **Revise a settled outcome** (reopen, re-resolve, wontfix a closed
  issue): one write with `--override -m "<why>"` against the terminal
  event's id — the event records `override: settled`, greppable, which
  is the visibility the old two-step reopen never actually had.
- **Break a squat / evict a live claim**: triage-only by doctrine. Free
  the key: `set <key> status=open --expect <the live claim's id>
  --override -m "<why, naming the claimant>"`; or take it directly with
  `status=in-progress` and your own `--as` — same write, same override.
  Either records `override: claim`.
- **Edge edit**: read the current set, union or prune, write whole:
  `set <key> blocked-by=<full,new,set> --expect <the edge field's latest
  id>`. Never combined with a status write (rule 2); a human-labeled
  key needs `--override` here like everywhere.
- **Break a cycle** — any agent or person does this IMMEDIATELY,
  whenever a cycle appears in `attention`, verdict regardless; no
  permission, no triage escalation. The entry's `break` object is the
  whole fix: `set <break.key> blocked-by=<break.keep> --expect
  <break.expect> -m "breaking cycle [<keys>]: dropping <break.drop>"`
  (clear with `blocked-by=` when `keep` is empty; add `--override` when
  `break.human` — sanctioned, the message names the cycle). Apply the
  suggestion OR a better break: the suggestion is structural (youngest
  edge); when titles or history show a different edge is the false
  dependency, break that one and say why. `claim_lost` on a break means
  a peer already fixed it — re-run `ready`. After ANY break, re-run
  `ready`: overlapping cycles surface one at a time.
- **Label edit**: the same read-union-write pattern, with the optional
  CAS rule 2 already grants: `set <key> labels=<full,new,set> --expect
  <the labels field's latest id>` (`--expect none` on a key's first
  labels write, including the `human` reservation's label step).
  `labels` is unguarded, so the tool never demands this — but
  replace-wholesale means two unprotected concurrent label edits
  silently clobber (no error, nothing greppable), and `labels` carries
  the `human` reservation; the skill teaches the protected form
  verbatim.
- **Recovery** (after discovering a clobber or duplication): a `handoff`
  note with what happened, then the corrective guarded write with
  `--evidence` and a message naming the mistake. Never quietly re-fix.

Idiom messages ("claiming", "still on it", "reclaiming from …") are
**load-bearing conventions**, not illustrations — consumers filter watch
streams and grep history by them; the skill teaches them verbatim.

## Board doctrine (the skill)

Ships as a new pattern section in `skills/using-ledger/SKILL.md`, whose
frontmatter `description` gains the triggers "running an issue board" and
"picking unblocked work" — undiscoverable doctrine is no doctrine.

- First read: `ledger ready`. The envelope is the whole picture; `show
  --where status=open` is the flat listing when you want one.
- **Picking loop**: while `frontier` is `work-available`: claim from
  `ready` (oldest first) or reclaim a stale entry from `attention` —
  skipping human-labeled ones, whose `needs_override` is a stop sign for
  a picker, not a form to fill; work; close; repeat — re-running `ready`
  after your own close is the loop, not polling. A non-zero
  `totals.attention` alongside available work is a cue to flag triage,
  not to wait for the verdict to flip — and a cycle in `attention` is
  broken on sight (the Break-a-cycle idiom), never merely flagged. When
  `frontier` is `all-handled`: leave; the tool has verified every chain
  ends at a recent claim or a human-labeled key, and — cycle detection
  being holder-blind — that no dependency loop hides behind either.
  When `attention-needed`: break cycles and reclaim non-human stale
  claims yourself; report only what you genuinely cannot act on
  (statusless keys, human-labeled stale claims).
- **A missing, empty, or broken-looking store is REPORTED, never
  repaired**: a worker never runs `init`, `create`, seed scripts, or any
  filesystem operation against the store, no matter how wrong the board
  looks — the most likely cause is the worker's own working directory,
  and the second most likely needs a person (trial 5: a worker hitting
  its own cwd mistake re-ran a setup script it found on disk and
  destroyed the live board). Every skill command line therefore carries
  its `cd <board dir> &&` prefix along with the absolute binary path —
  working directory travels with the command, like the binary path
  before it.
- Waiting for others (only when told to wait): `watch` with the full
  status vocab as `--value` terms — watch matches any field's value,
  unscoped, so a label token colliding with a status word causes a rare
  spurious wake; harmless, re-run `ready`. **Every watch timeout is also
  a cue to re-run `ready`** — staleness fires no event; timeouts are how
  it gets noticed.
- Claiming a key annotated `unblocked_without_evidence`: name it in the
  claim message, persisting the warning into the key's own history.
- **Triage moment**: work the `attention` list (it is the sweep — stale
  claims to reclaim or take over, statusless keys to finish seeding or
  abandon, cycles to break by edge edit); walk `show --where status=open`
  for staleness of content (close with evidence / wontfix with the why
  in `-m` / re-label via the Label-edit idiom — protected, since triage
  is exactly where label edits run concurrently / edge edits); sweep the
  chain for override events with `tail --raw -n 0 --ledger <board> | grep
  '"override"'` — unbounded (`-n 0`; `tail`'s own `--limit` default of 20
  would silently cover only the most recent events) and matching the
  JSON `override` field `tail --raw` actually emits, not prose's
  `override: <value>` shorthand for it — and review each: every override
  is somebody deciding a standing signal didn't apply, and reviewing
  them is the entire point of making them greppable. Evidence on wontfix
  is NOT required (evidence of a
  non-decision is pasted-string theater); the honest signal is the
  annotation.
- Dup defense: search titles before seeding (`ready`/`show` carry titles
  for live keys; `tail --raw` — never the curated view — for closed
  ones; rollup summaries on boards SHOULD retain key names verbatim,
  advisory). Dups close `wontfix -m "dup of [[key]]"` — `[[key]]` is a
  plain-text grep convention, no rendering semantics.
- What no mechanism supplies: honoring what the id you fetched actually
  said. `--expect` proves you read the state; the signal gate makes
  ignoring it visible; judgment does the rest.

## Timestamps, clocks, and economics

Event timestamps gain sub-second resolution, pinned to the layout
`2006-01-02T15:04:05.000` (UTC, fixed milliseconds, no zone suffix);
readers parse both old and new layouts. `age` compares a writer's
recorded `ts` against the reader's clock: boards assume same-host clock
coherence; multi-host fleets sharing a store are a Plan 2 concern, one
warning line in the skill.

Economics, stated: a completed issue is ≥3 events (seed, claim, close; ≥4
with dependencies), plus touch-bases scaling with wall-clock duration.
The chain records every write that LANDED; rejected writes (`claim_lost`,
`needs_override`, `bad_usage`) append nothing, so contention history is
not preserved — a deliberate, stated trade. One more honest limit: the
store's append-only guarantees are git-level — filesystem-level
destruction of the store directory is outside the tool's trust model
entirely, which is why doctrine forbids workers from ever "repairing" a
store and why conductor tooling never lives in a worker's reach.

## Deferred, with reasons

- Field-scoped `watch` filtering (would clean up the status-vocab
  workaround); `min_writer` version floor (can't protect binaries already
  fielded); multi-field token filters on `watch`/`since`; additive
  `block`/`unblock` verbs (sugar over the edge-edit idiom); FTS search;
  TUI; short IDs; evidence validation (`ledger verify`, v2);
  cross-machine sharing (Plan 2).

## Implementation scope (rev 14, SDD with tests)

Everything above: board declarations and ready-capability validation
(incl. `--guard` existence and the guard-required shape), the eight-rule
invariant (`claim_lost`, `needs_override`, `empty_body` formats pinned;
`override:` event recording), `show --where`, `ready` (five envelope
members, frontier verdict with correct DFS, annotations, limits/totals,
measured cost), the write idioms' mechanics, sub-second timestamps, meta
export/import round-trip, the extended race harness as real tests plus
new rule-5 signal rounds, the skill section and trigger update, and rev
13 amendments (verb taxonomy; identifiers `claim_lost` and
`needs_override` added to the canonical list — `bad_usage`, `bad_value`,
`empty_body`, `unknown_field`, `unknown_key` all predate this document).
The spike branch (`wip/issues-spike`, three rounds, implements rev 2–5
semantics) is historical evidence, never merged.

## Test plan

1. Guarded plain set → `bad_usage`; two guarded fields in one set →
   `bad_usage`; unguarded ride-along legal; `--expect` on a
   purely-unguarded single-field write performs real CAS (mismatch →
   `claim_lost`, never silently ignored).
2. Seed via `--expect none`; racing seeds serialize; `--expect none` on
   a touched field → `claim_lost`; collision hint text; the chained-write
   contamination case and its deterministic recovery (derived state
   restored exactly; human-labeled stranger variant needs `--override`).
3. Title enforcement: first status write with missing or whitespace `-m`
   → `empty_body`; titles survive claim/close/revision; present on every
   `ready`/`held`/`blocked` entry, `show` row, and stale-claim attention
   entry; structurally absent on statusless and cycle entries.
4. Field-scoping: label writes racing status claims never produce
   `claim_lost` (harness, 10/10); notes never invalidate `--expect`;
   label edits carrying the optional `--expect` serialize (one
   `claim_lost`), without it last-write-wins — the stated trade the
   Label-edit idiom exists to avoid.
5. First-edge race under `--expect none` (harness, 10/10).
6. `claim_lost` format: id, author, exact value, per-field hints —
   including the reclaim path, the terminal-value (failed-close) hint,
   the edge-seed collision hint, and the generic-field fallback hint;
   a plain board's guarded `status` or `blocked-by` field gets the
   generic hint, never the ready-capable advice.
7. Rule 5 signals, each in isolation and composed (mandatory before the
   validation claim covers them): live cross-author claim →
   `needs_override` naming claimant and age; stale claim → no signal,
   reclaim lands without override; human label → signal for everyone
   incl. claimant's own close and first seed; settled → signal for
   everyone incl. the close's own author; multiple standing signals
   listed together and recorded together (`override: claim,human`);
   override without a trimmed message → `bad_usage`; `override:` values
   computed by the tool, never caller-asserted; a `blocked-by` write
   against a claimed or settled key triggers neither `claim` nor
   `settled` (`human` still gates it).
8. Ready-capability validation: `--terminal status=…` without `--guard
   status` → `bad_value`; missing `labels` multi-field → `bad_value`;
   missing `in-progress` from vocab → `bad_value`;
   third non-terminal value → `bad_value`; `--guard` naming an undeclared
   field → `bad_value`; `ready` on a non-ready-capable board →
   `bad_usage` with the create-time fix; `--guard` on a plain board
   gives CAS only — no rule-5 signals, no `blocked-by` existence
   validation, no title enforcement.
9. Frontier verdict: `work-available` on non-empty ready AND on
   stale-claim-only boards — but a board whose only stale claim sits on
   a human-labeled key is `attention-needed`, never `work-available`;
   `all-handled` only when every chain ends at a
   live claim or non-terminal human key — a closed human-labeled blocker
   resolves its dependents' edges (verified against hand-built graphs: linear
   chains, diamonds — no false cycle; true cycles → `attention-needed`;
   statusless references → `attention-needed`; open targets recursed;
   HOLDER-BLIND: a cycle through a live claim, and one through a
   human-labeled key, are each detected — a mutually-blocked
   open/claimed pair reads `attention-needed`, never `all-handled`);
   verdict computed over the full board when lists are `--limit`
   truncated.
10. Envelope: five members and totals match the pinned example's shape;
    held merges claims and human keys with correct `kind`, `id`, `stale`,
    and claimed-but-blocked `waiting_on`; a human-labeled claimed key
    renders `kind: human` with the claim fields present, live or stale;
    a human-labeled key with unresolved edges carries `waiting_on` in
    `held` (claimed and unclaimed variants) and never appears in
    `blocked`; blocked entries carry the status field's latest `id`;
    attention entries for
    stale-claim / statusless / cycle; cycle entries carry a well-formed
    `break` object (member key, dropped token, keep value, a
    currently-valid `expect` id, `human` flag matching the break
    target's labels) whose paste-ready write actually breaks the cycle;
    identical-member cycle entries (doubled edges) deduplicate; a
    residual overlapping cycle surfaces on the next `ready` after one
    break; ordering (ready oldest-first with
    chain-position ties; others key-ascending).
11. `show --where`: `unknown_field`, `~=` on enum → `bad_usage`,
    same-field double `=` → `bad_usage`, AND composition, uniform
    application across `ready`'s lists — including `--where
    status=in-progress` and `status=closed` composing per-list (lists
    empty legitimately, no error).
12. `unblocked_without_evidence`: fires on evidence-free terminal
    blockers regardless of vocab value; not on evidenced ones.
13. Key grammar on ready-capable boards: non-kebab key rejected at first
    write with the edge-referenceability message; a `blocked-by` write
    naming a nonexistent token → `unknown_key` naming it (the
    existence-check degenerate case measured under item 16).
14. Declaration validation: `--terminal`/`--require-evidence` subset
    checks; `--stale-after` parse; export/import meta round-trip with
    identical `ready` output modulo re-minted `id`s and, on same-store
    import, the re-minted `slug`/envelope `ledger` name; import
    re-validates the ready-capability shape (a hand-broken export
    missing `--guard status` → `bad_value`, never a minted board).
15. Timestamps: fixed-millisecond UTC on new events; old events parse;
    staleness math across mixed precision; sub-second `--stale-after`
    behaves.
16. Performance: `ready` at 5k events within the stated bound (event
    volume including touch-base churn); conditional-set precondition
    read narrowed to the target key, not a full re-fold per retry —
    measured, degenerate case stated.
17. Doctrine: every command line in the shipped skill section executes
    verbatim against a scratch board; the skill's frontmatter triggers
    include the board scenarios.
18. Watch doctrine reality: full-vocab `--value` watch wakes on claims;
    a colliding label token produces the documented spurious wake;
    timeout → `ready` re-run is the staleness path.

## Validation record

- **Trials** (chain-audited, not prose-graded):
  `research/ledger-issues-spike-trial.md` — two workers; falsified
  claim-by-doctrine (duplicate work in 90 seconds); produced `--expect`.
  `trial2.md` — three workers, accidental mixed-version fleet; validated
  `--expect` where present; produced the absolute-path doctrine.
  `trial3.md` — four writers including a live triager; zero duplicate
  work, contested stale-reclaim serialized by `--expect`, annotation flow
  proven; produced the human quarantine and the settled-outcome
  protection.
  `trial5.md` — three workers (two Sonnet, one Haiku) on a bedlam board
  of six cycles; validated the self-service cycle design end to end: all
  cycles broken in ~90s, a staged WRONG tool suggestion correctly
  overridden with reasons, the human-cycle override executed by the
  Haiku, residual overlapping cycles resolved through the re-run loop,
  zero double-closes; produced holder-blind detection, the break
  object, entry dedup, the break-on-sight doctrine, and the
  store-recovery prohibition (a worker destroyed the store re-running a
  setup script it found next to the doctrine — mechanism beats
  doctrine, fifth occurrence).
- **Harnesses**: `research/scripts/expect-race-harness.sh` (status races,
  20/20 — a round-10 reviewer caught its `create` line predating the
  ready-capable shape, so the citation validated an older ruleset; the
  harness now declares the full shape, seeds via `--expect none`, and
  reproduced 20/20 on re-run); the spike's extended harness (first-edge
  and interleaved-triage rounds, 30/30). Rule 5's signal gate postdates
  both — its rounds are test-plan item 7, mandatory.
- **Adversarial reviews**: six rounds, twelve reviewers — two rounds run
  blind at the operator's direction, which outperformed steered review on
  a mature document (the blind rounds found the guard-not-required hole,
  the walk's missing common case, and the watch-doctrine falsification).
  The recurring failure patterns those rounds exposed — enumerated
  protection, doctrine-side algorithms, unbacked substrate assumptions —
  are what this revision is designed against, not merely patched for.
  A seventh round, blind like the fifth and sixth, ran against rev 12:
  its two reviewers found the rule-5 scope ambiguity (claim/settled vs
  edge writes — the general rule contradicted the seed-collision idiom),
  the missing `labels` requirement in the ready-capable shape (both
  reviewers, independently), terminal-vs-human edge precedence, the
  human+claim composite `held` shape, and the unpinned `ready` bound —
  all folded here as rev 13. One reviewer independently rebuilt the
  spike and re-ran both harnesses (20/20 and 30/30 reproduced) and
  verified the trial citations verbatim.
  An eighth round, also blind, ran against rev 13: both reviewers
  independently caught rule 8 asserting a windowed read path the parent
  spec verifiably does not have (rewritten as a contract, not an
  algorithm); one caught the pinned bound's arithmetic (2×70 is 140,
  not 150) and the undefined `--expect`-on-unguarded-field case; the
  other caught `work-available` counting human-labeled stale claims —
  funneling ordinary pickers toward the very override the Reclaim
  idiom reserves for people — the one place the trial-3
  mechanism-beats-doctrine lesson hadn't reached. All folded as rev 14.
  First round with no adjudicated Critical.
  A ninth round, also blind, ran against rev 14 — the second straight
  round with no Critical, and the first whose findings all sat in old
  material rather than the newest fixes: the labels field had no
  CAS-safe edit idiom (concurrent label edits silently clobbered on the
  field carrying the `human` reservation — enumeration missing a case,
  again); rule 5 and `blocked-by`'s special treatment were never scoped
  to ready-capable boards; the byte-for-byte export claim was false
  against the import code (slug re-mint); the close-time `claim_lost`
  hint told a failed closer to abandon finished work, contradicting the
  Close idiom; `held`'s human entries never said whether they carry
  `waiting_on`. All folded as rev 15.
  A tenth round, also blind, against rev 15 — third straight with no
  Critical: the hint matrix dispatched ready-capable advice on plain
  boards (interaction with rev 15's own scoping fix); the race
  harness's `create` line was illegal under the rules it validates
  (fixed, re-run, 20/20); import was a second meta-minting path with no
  shape validation (now re-validates); `blocked-by` existence checks
  had no cost model or `unknown_key` test; the `--where`-vs-`status`
  special case was incoherent (dropped for uniform per-list
  composition); titles overclaimed for statusless/cycle attention
  entries; touch-base/close idioms omitted their human-label override
  variants. All folded as rev 16.
- **Kata reconnaissance**: `~/git/kata` — two-status minimalism, labels,
  triage doctrine, open-by-default lists; what we took and declined is in
  Deferred.
