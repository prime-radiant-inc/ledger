---
name: ledger-issues
description: Use when working a shared ledger issue board — picking and claiming tasks, closing with evidence, breaking cycles, and reconciling contested state after sync.
---

# Ledger issues

Doctrine for a shared ledger issue board: the picking loop, claims and
evidence, cycle-breaking, and the sync habit that keeps a board honest
across hosts. Every command shape here is spelled out in full in `chit
quickstart`; read that before your first real write. The `using-ledger`
skill covers the ledger's other roles — execution spines, coordination
scoreboards, checkpoints, resume-and-verify, investigation ledgers, and
the discipline that keeps any ledger trustworthy.

## Issue board

For coordinating unblocked work on a shared board: create it with guarded
`status`/`blocked-by` fields and a `labels` reservation (`chit create
--help` has the declaration flags; this pattern is everything downstream
of that). Pick `--stale-after` from the board's TEMPO: hours for an agent
fleet running the claim-reclaim loop, a week or more for a human-paced
backlog — a horizon shorter than the real work cadence turns every honest
claim stale and invites reclaiming work that is still being done. First
read is always `ready` — its envelope answers what to
pick, what to respect, and whether anything needs a person, including a
computed `frontier` verdict, so no agent re-derives graph logic by hand;
`show --where status=open` is the flat listing when you want one.

**Picking loop**: while `frontier` is `work-available`, claim the oldest
entry in `ready`, or reclaim a stale entry from `attention` — skip any
that's human-labeled; its `needs_override` is a stop sign for a picker,
not a form to fill in. Work it, close it, re-run `ready` — that re-run
*is* the loop, never polling. A non-zero `totals.attention` alongside
available work is a cue to flag triage, not a reason to wait for the
verdict to flip — and break any cycle in `attention` on sight (the
Break-a-cycle idiom below), verdict regardless, never merely flagged. A
`contested` entry is resolved the same on-sight way: read BOTH heads
with `show --id` on each of `contest.ids` before collapsing with
`--expect <contest.expect>`, adding `--override` where the collapse
trips the settled gate — a seed collision can hide two distinct tasks
under one key, and splitting them (or renaming one KEY into another) is
a human call, never a picker's. An entry whose `contest.field` is
`title` is the exception that is entirely yours: two replicas raced the
wording, nothing about the work is in question, and the frontier
deliberately stays `all-handled` over it. Collapse it with the ticket —
`set <key> --rename "<keeper>" --expect <contest.expect>`, no
`--override` (settled never gates a rename).
When `frontier` is `all-handled`, leave — the tool has verified every
dependency chain ends at a live worker or a human, and — cycle detection
being holder-blind — that no dependency loop hides behind either. When
it's `attention-needed`, break cycles and reclaim non-human stale claims
yourself; report only what you genuinely cannot act on (statusless keys,
human-labeled stale claims).

**Override ethics**: the settled gate (`needs_override` on
`settled`/`claim` signals) exists so terminal states change only on
purpose. Override to CORRECT state — collapsing a contested close,
reopening genuinely wrong work, reclaiming from a dead claimant — and
say why in `-m`; never to decorate STATE for cosmetic reasons (tidiness,
a status that reads better: the record is immutable and rewriting it
buys nothing). A wrong TITLE is no longer a reason to touch state at
all — `set <key> --rename` is the legitimate correction, and it never
moves a status. A HUMAN label is
different in kind: it is a stop sign, not a gate — walk away, report,
and leave the override to a person. The field trial's one misjudgment
was a cosmetic override-reopen of a settled close; the durable record
made it auditable, but the write bought nothing.

**A missing, empty, or broken-looking store is REPORTED, never
repaired**: never run `init`, `create`, a seed script, or any filesystem
operation against the store, no matter how wrong the board looks — the
likeliest cause is your own working directory, and the next likeliest
needs a person, not a fix attempted alone. That's why every command below
carries its `cd <board dir> &&` prefix alongside the absolute binary
path — working directory travels with the command, same as the binary
path did before it.

```
cd <board dir> && ~/path-to/ledger ready --ledger issues
```

On a human-labeled key, every guarded write below — touch-base and close
included, not just the idioms that spell it out — carries `--override -m
"<why>"` per the standing-signal rule; that variant isn't repeated per
idiom below.

**`--expect` scope**: on a guarded field (`status`, `blocked-by`)
`--expect` is REQUIRED — that is the invariant every claim, close and
edge edit below rests on. On an unguarded field it is optional but
VALIDATED whenever you pass it: `--expect none` against a field that
already has history fails `claim_lost` ("beat you to"), which is CAS
working, not a bug — re-read the field and pass its latest id instead
(the Label-edit idiom below is that protected form). Omit the flag
entirely for a plain unguarded write.

**Titles**: the seed's `-m` IS the key's title — carried by every
listing forever, so write a title ("fix the retry storm bug"), never a
status update ("creating retry task"). Titles are stable, not immutable:
`set <key> --rename "<new title>"` corrects one, and every listing then
shows the new title labeled `(renamed by <author>)` with the old ones
under `renamed.prior`. A rename is a CORRECTION with attribution, not a
workflow — it costs a permanent, attributed event, and a `human`-labeled
key charges an `--override` on top. Get the seed right; retitle when it
is wrong, not when it could be better. Field trial: an agent who seeded
with a procedural message then overrode a settled close just to fuss
with the title — the override was pure cost even then, and now the
rename is the honest fix.

- **Seed**: `set <key> status=open --expect none -m "<title>"`. With a
  dependency, edges first — a statusless key is unpickable and in
  neither `ready` nor `blocked` (a `ready` run inside the window shows it
  only under `attention` as a half-seed: momentary, harmless):

  ```
  cd <board dir> && ~/path-to/ledger set spike-probe status=open --expect none -m "spike probe: investigate retry storm" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set fix-retry blocked-by=spike-probe --expect none --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set fix-retry status=open --expect none -m "fix the retry storm bug" --as ash --ledger issues
  ```

  Seed collision: the corrupting write is the one that SUCCEEDS — your
  edge write landing on a stranger's edge-free key. Your own `--expect
  none` success proves the key had no prior edges, so recovery is
  deterministic: clear what you wrote and re-seed under a new name (add
  `--override` if the stranger's key turns out to be human-labeled — the
  message names the collision). Never chain the two writes without
  checking exit codes:

  ```
  cd <board dir> && ~/path-to/ledger set cache-warm status=open --expect none -m "warm the cache on boot" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set cache-warm blocked-by=spike-probe --expect none -m "dependency edge" --as kit --ledger issues
  cd <board dir> && ~/path-to/ledger set cache-warm blocked-by= --expect <your own edge event id> -m "reverting: seed collision" --as kit --ledger issues
  cd <board dir> && ~/path-to/ledger set cache-warm-2 blocked-by=spike-probe --expect none -m "dependency edge" --as kit --ledger issues
  cd <board dir> && ~/path-to/ledger set cache-warm-2 status=open --expect none -m "kit's actual issue, re-seeded after the cache-warm collision" --as kit --ledger issues
  ```

  Seeding a pre-`human`-labeled key is a legitimate way to reserve
  planned work — label first, then seed with `--override`; the one `-m`
  is both the title and the override justification:

  ```
  cd <board dir> && ~/path-to/ledger set design-review labels=human --expect none -m "reserving for jesse" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set design-review status=open --expect none -m "pick the retry API shape" --as ash --ledger issues  # expect: exit 4 error needs_override
  cd <board dir> && ~/path-to/ledger set design-review status=open --expect none --override -m "pick the retry API shape -- reserved for jesse: needs a human call on the retry contract" --as ash --ledger issues
  ```

- **Retire a mistake**: a junk key — a probe, a typo'd name, a seed that
  turned out to be somebody else's issue — cannot be deleted; the chain is
  immutable, so you RETIRE it. In order: clear a `human` label if it
  carries one (a plain unguarded write, no `--expect` — the label's
  history means `--expect none` would lose, and clearing it is what lifts
  the stop sign); seed a status if the key has none, since `wontfix` is a
  guarded write and needs something to CAS against; then `wontfix` with a
  message saying plainly that the key is an artifact, so a reader sweeping
  the board later doesn't mistake it for abandoned work:

  ```
  cd <board dir> && ~/path-to/ledger set probe-key labels=human --expect none -m "reserving while probing CAS behavior" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set probe-key labels= --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set probe-key status=open --expect none -m "probe key: minted while probing CAS behavior" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set probe-key status=wontfix --expect <probe-key's seed id> -m "retiring: artifact of a CAS probe, never real work" --as ash --ledger issues
  ```

- **Retitle** (rare, and never a workflow step): `set <key> --rename
  "<new title>"` when a title is WRONG. The rename is the whole event —
  no field assignments, no evidence, no `-m` — so it never touches state;
  `--expect` is optional and targets the key's latest rename (`--expect
  none` means never renamed). A `human`-labeled key charges
  `--override -m "<why>"` for it, exactly as a person's reserved issue
  should:

  ```
  cd <board dir> && ~/path-to/ledger set spike-probe --rename "spike probe: retry storm under backpressure" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set design-review --rename "pick the retry contract" --as ash --ledger issues  # expect: exit 4 error needs_override
  cd <board dir> && ~/path-to/ledger set design-review --rename "pick the retry contract" --override -m "jesse asked for the retitle in standup" --as ash --ledger issues
  ```

- **Claim**: `set <key> status=in-progress --expect <ready id> -m
  "claiming"`. `claim_lost` means someone beat you to it — re-run `ready`
  and pick again. Your `--as` IS the assignee; provenance names who,
  when, from where.

  ```
  cd <board dir> && ~/path-to/ledger set spike-probe status=in-progress --expect <the seed id> -m "claiming" --as ash --ledger issues
  ```

- **Touch-base**: re-set `status=in-progress --expect <own claim id> -m
  "still on it"` at roughly half the staleness horizon, only while
  actively working. Touch-bases are events; boards pick horizons
  matching their tasks, not the reverse.

  ```
  cd <board dir> && ~/path-to/ledger set spike-probe status=in-progress --expect <own claim id> -m "still on it" --as ash --ledger issues
  ```

- **Close**: `set <key> status=closed --evidence <ref> --expect <own
  claim id> -m "done"`.

  ```
  cd <board dir> && ~/path-to/ledger set spike-probe status=closed --evidence run:demo-1 --expect <own claim id> -m "done" --as ash --ledger issues
  ```

  A `claim_lost` here means you were reclaimed while working: leave a
  `handoff` note with your result and let the current claimant decide —
  never re-close blind. The winning claimant's duty: whenever the chain
  shows a key was ever reclaimed, check `notes --key <key>` for a
  `handoff` note before closing:

  ```
  cd <board dir> && ~/path-to/ledger set retry-config status=open --expect none -m "tune retry backoff config" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set retry-config status=in-progress --expect <the seed id> -m "claiming" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set retry-config status=in-progress --expect <ash's claim id> -m "reclaiming from ash: stale 350ms" --as moss --ledger issues
  cd <board dir> && ~/path-to/ledger set retry-config status=closed --evidence run:demo-2 --expect <ash's claim id> -m "done" --as ash --ledger issues  # expect: exit 4 error claim_lost
  cd <board dir> && ~/path-to/ledger note -k handoff --key retry-config -m "finished the backoff tuning before losing the claim: new config is exponential base 200ms cap 5s, verified against a local repro; evidence run:demo-2" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger notes -k handoff --key retry-config --ledger issues
  cd <board dir> && ~/path-to/ledger set retry-config status=closed --evidence run:demo-2 --expect <moss's reclaim id> -m "done, per ash's handoff note" --as moss --ledger issues
  ```

- **Reclaim**: a stale claim (from `attention`) is retaken with `set
  <key> status=in-progress --expect <its id> -m "reclaiming from <by>:
  stale <age>"` — no override needed unless the key is also
  human-labeled (`human` has no staleness exception); staleness
  dissolved the claim signal. Concurrent reclaimers serialize on the
  same id — the reclaim line inside the Close example above is this
  idiom in action.

- **Revise a settled outcome** (reopen, re-resolve, wontfix a closed
  issue): one write with `--override -m "<why>"` against the terminal
  event's id — the event records `override: settled`, greppable, which
  is the visibility the old two-step reopen never actually had.

  ```
  cd <board dir> && ~/path-to/ledger set docs-typo status=open --expect none -m "typo in readme install section" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set docs-typo status=closed --evidence commit:abc111 --expect <the seed id> -m "done" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set docs-typo status=wontfix --expect <the close id> --override -m "dup of [[readme-typo-2]]" --as kit --ledger issues
  ```

- **Break a squat / evict a live claim**: triage-only by doctrine. Free
  the key with `status=open --expect <the live claim's id> --override
  -m "<why, naming the claimant>"`; or take it directly with
  `status=in-progress` and your own `--as` — same write, same override.
  Either records `override: claim`.

  ```
  cd <board dir> && ~/path-to/ledger set urgent-fix status=open --expect none -m "urgent: prod alert flapping" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set urgent-fix status=in-progress --expect <the seed id> -m "claiming" --as moss --ledger issues
  cd <board dir> && ~/path-to/ledger set urgent-fix status=open --expect <moss's claim id> --override -m "freeing: moss went dark, urgent-fix needs a new owner" --as triager --ledger issues

  cd <board dir> && ~/path-to/ledger set hotfix-now status=open --expect none -m "hotfix: payment webhook 500s" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set hotfix-now status=in-progress --expect <the seed id> -m "claiming" --as kit --ledger issues
  cd <board dir> && ~/path-to/ledger set hotfix-now status=in-progress --expect <kit's claim id> --override -m "taking over: kit unresponsive, needed now" --as triager --ledger issues
  ```

- **Edge edit**: read the current set, union or prune, write whole:
  `set <key> blocked-by=<full,new,set> --expect <the edge field's latest
  id>`. Never combined with a status write; a human-labeled key needs
  `--override` here like everywhere.

  ```
  cd <board dir> && ~/path-to/ledger set deploy blocked-by=fix-retry --expect none --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set deploy status=open --expect none -m "deploy the retry fix" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger status deploy --field blocked-by --ledger issues
  cd <board dir> && ~/path-to/ledger set deploy blocked-by=fix-retry,cache-warm-2 --expect <the edge field's latest id> -m "also wait on the cache warm fix" --as ash --ledger issues
  ```

- **Break a cycle** — any agent or person does this IMMEDIATELY, whenever
  a cycle appears in `attention`, verdict regardless; no permission, no
  triage escalation. The entry's `break` object is the whole fix: `set
  <break.key> blocked-by=<break.keep> --expect <break.expect> -m
  "breaking cycle [<keys>]: dropping <break.drop>"` (clear with
  `blocked-by=` when `keep` is empty; add `--override` when
  `break.human` — sanctioned, the message names the cycle). Apply the
  suggestion OR a better break: the suggestion is structural (youngest
  edge, the write that most likely closed the loop by mistake); when
  titles or history show a DIFFERENT edge is the false dependency, break
  that one instead and say why in `-m`. `claim_lost` on a break means a
  peer already fixed it — re-run `ready`. After ANY break, re-run
  `ready`: overlapping cycles surface one at a time.

  ```
  cd <board dir> && ~/path-to/ledger set cycle-x status=open --expect none -m "cycle x" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-y status=open --expect none -m "cycle y" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-x blocked-by=cycle-y --expect none --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-y blocked-by=cycle-x --expect none --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger ready --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-y blocked-by= --expect <cycle-y's edge id> -m "breaking cycle [cycle-x cycle-y]: dropping cycle-x" --as kit --ledger issues
  ```

  The human variant — `break.human` names a reserved key, so the fix
  needs `--override` like every other guarded write against it:

  ```
  cd <board dir> && ~/path-to/ledger set cycle-human-a status=open --expect none -m "cycle human a" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-human-b labels=human --expect none -m "reserving for jesse" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-human-b status=open --expect none --override -m "cycle human b -- reserved for jesse: needs a human call" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-human-a blocked-by=cycle-human-b --expect none --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-human-b blocked-by=cycle-human-a --expect none --override -m "closing the demo cycle" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger ready --ledger issues
  cd <board dir> && ~/path-to/ledger set cycle-human-b blocked-by= --expect <cycle-human-b's edge id> -m "breaking cycle [cycle-human-a cycle-human-b]: dropping cycle-human-a" --as kit --ledger issues  # expect: exit 4 error needs_override
  cd <board dir> && ~/path-to/ledger set cycle-human-b blocked-by= --expect <cycle-human-b's edge id> --override -m "breaking cycle [cycle-human-a cycle-human-b]: dropping cycle-human-a" --as kit --ledger issues
  ```

**Break ticket schema**: every `cycle` entry in `attention` carries a
machine-readable `break` ticket; its fields, exactly:

- `key` — the key whose `blocked-by` you edit. Only this key.
- `drop` — the dependency to remove from that field.
- `keep` — the value the field should hold AFTER the write. `""` means
  "clear the field"; it is never a literal value to insert.
- `expect` — the CAS ticket: the field's current event id, passed as
  `--expect <expect>` on your write. If it fails, re-run `ready` — the
  board moved.
- `human` — `true` means the break touches a human-reserved key: stop
  and report instead of writing.

A fresh cycle between `task-parse` and `task-lex` earns the ticket
`{key: task-lex, drop: task-parse, keep: "", expect: <id>, human:
false}` — `task-lex`'s edge is the younger one, so it's the key the
ticket names:

```
cd <board dir> && ~/path-to/ledger set task-parse status=open --expect none -m "parse task" --as ash --ledger issues
cd <board dir> && ~/path-to/ledger set task-lex status=open --expect none -m "lex task" --as ash --ledger issues
cd <board dir> && ~/path-to/ledger set task-parse blocked-by=task-lex --expect none --as ash --ledger issues
cd <board dir> && ~/path-to/ledger set task-lex blocked-by=task-parse --expect none --as ash --ledger issues
cd <board dir> && ~/path-to/ledger ready --ledger issues
cd <board dir> && ~/path-to/ledger set task-lex blocked-by= --expect <task-lex's edge id> -m "breaking cycle per ready's break ticket: dropping task-parse" --as ash --ledger issues
```

- **Label edit**: the same read-union-write pattern, `--expect <the
  labels field's latest id>` (`--expect none` on a key's first labels
  write, including the `human` reservation's label step). `labels` is
  unguarded, so the tool never demands this — but replace-wholesale
  means two unprotected concurrent label edits silently clobber (no
  error, nothing greppable), and `labels` carries the `human`
  reservation; use the protected form, never drop a label a concurrent
  writer just added.

  ```
  cd <board dir> && ~/path-to/ledger set fix-retry labels=needs-triage --expect none -m "flagging for triage review" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger status fix-retry --field labels --ledger issues
  cd <board dir> && ~/path-to/ledger set fix-retry labels=needs-triage,perf --expect <the labels field's latest id> -m "also perf-relevant" --as kit --ledger issues
  ```

- **Recovery** (after discovering a clobber or duplication): a
  `handoff` note with what happened, then the corrective guarded write
  with `--evidence` and a message naming the mistake. Never quietly
  re-fix.

  ```
  cd <board dir> && ~/path-to/ledger set db-migrate status=open --expect none -m "run the pending schema migration" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger set db-migrate status=closed --evidence commit:wrong0000 --expect <the seed id> -m "done" --as ash --ledger issues
  cd <board dir> && ~/path-to/ledger note -k handoff --key db-migrate -m "closed with the wrong evidence ref (copy-pasted from deploy); correcting below, not re-closing quietly" --as kit --ledger issues
  cd <board dir> && ~/path-to/ledger set db-migrate status=closed --evidence commit:c9f1a02 --expect <the bad close's id> --override -m "correcting: evidence ref was copy-pasted from deploy, see handoff" --as kit --ledger issues
  ```

Claiming a key `ready` annotates `unblocked_without_evidence`: name it in
the claim message, persisting the warning into the key's own history.

**Triage moment**: work the `attention` list — it IS the sweep (stale
claims to reclaim or take over, statusless keys to finish seeding or
abandon, cycles to break by edge edit). Walk `show --where status=open`
for staleness of content (close with evidence / `wontfix` with the why
in `-m` / re-label via the Label-edit idiom above — protected, since
triage is exactly where label edits and edge edits run concurrently).
Sweep the chain for override events and review each — every override is
somebody deciding a standing signal didn't apply, and reviewing them is
the entire point of making them greppable: `tail --raw` emits each
event's `override` field as JSON, so grep for the quoted key — unbounded
with `-n 0`, since `tail`'s own `--limit` default of 20 would silently
cover only the most recent events, not the whole chain:

```
cd <board dir> && ~/path-to/ledger tail --raw -n 0 --ledger issues | grep '"override"'
```

Evidence on `wontfix` is NOT required — evidence of a
non-decision is pasted-string theater; the honest signal is the
annotation itself. Any non-zero `totals.attention` is a triage cue on
its own, regardless of what `frontier` says.

```
cd <board dir> && ~/path-to/ledger show --where status=open --ledger issues
```

Dup defense: search titles before seeding (`ready`/`show` carry titles
for live keys; `tail --raw` — never the curated view — for closed ones;
rollup summaries SHOULD retain key names verbatim, advisory). Dups close
`wontfix -m "dup of [[key]]"` — `[[key]]` is a plain-text grep
convention, no rendering semantics; the `docs-typo` example above is one.

What no mechanism supplies: honoring what the id you fetched actually
said. `--expect` proves you read the state; the signal gate makes
ignoring it visible; judgment does the rest.

**Waiting for others** (only when told to wait): `watch` with the full
status vocab as `--value` terms — watch matches any field's value,
unscoped, so a label token that happens to equal a status word (e.g.
`labels=open`) causes a rare spurious wake; harmless, just re-run
`ready`. Every watch timeout is also a cue to re-run `ready` — staleness
fires no event, so a timeout is how it gets noticed.

```
cd <board dir> && ~/path-to/ledger watch --value open,in-progress,closed,wontfix --timeout 1 --ledger issues  # expect: exit 2
```

Run `chit quickstart` for general mechanics; `chit create --help`
for the board declaration flags.

## Sync

Sync and push are cross-host doctrine, not board-only — the habit
applies to every ledger you touch, coordination boards most of all.
Start of a session: `chit sync` fetches and merges remote history,
never pushes. End of a session, or whenever a handoff needs to reach
someone else: `chit push`. Bare `push` publishes every local slug;
naming slugs publishes only those — the privacy lever for a ledger
that isn't ready to be seen, since everything pushed is readable by
anyone with read access to the repo.

Clock skew is an asymmetric threat to claim staleness: board horizons
MUST exceed expected inter-host clock skew, so claims are not born
stale. That covers only one direction — a peer whose clock runs ahead
has no horizon setting that helps; clock discipline is the only
defense there.

A `contested` attention entry is a partition's fingerprint: two
replicas raced the same guarded field — or, when `contest.field` is
`title`, the same key's title. Recovery reads BOTH heads with
`show --id` on each of `contest.ids` before collapsing anything — a
seed collision can hide two distinct tasks under one key, and the
title alone won't reveal it. Read both heads for the title history
too: under a collision the LOSING root's seed title appears in no
`renamed.prior` list anywhere, because `prior` is fold-path history,
not a complete inventory — the two heads are the only place that
title still exists. Collapse with `--expect <contest.expect>`; where
the collapse re-asserts a settled value it trips the settled gate, so
add `--override` and say why in `-m`, the same as any other
settled-outcome revision. A `title` contest collapses with
`set <key> --rename "<keeper>" --expect <contest.expect>` and needs no
`--override`. Splitting a seed-collided key, or renaming one KEY into
another, is a human call, never a recovery agent's to make alone.

A multi-root refusal (a grafted or foreign chain arriving via sync)
wedges that slug for the whole fleet: push is non-force and sync
refuses, so no tool operation can repair or worsen it locally. The
refusal error names the tracking ref
(`refs/ledger-remote/<remote>/<slug>`) so the operator can inspect the
refused chain with plain git — the fix is remote-side ref surgery, the
same class of human-run repair as a leaked secret: an admin deletes or
force-replaces the poisoned ref, and the slug stays wedged until they
do.

```
chit sync
chit push
```

Run `chit quickstart` for mechanics.

## A partition, healed — worked example

What multi-replica recovery actually looks like, compressed from a live
six-agent trial. Two replicas worked the same board through a network
partition. On one side an agent seeded `task-signup` and a colleague
closed it; on the other side a different agent independently seeded the
SAME key with a different title, a third closed it, and the seeder
overrode the close to reopen it. Then the network healed.

The first recovering agent ran `sync` (result: `no-op` — its side was
strictly ahead) and `push`. The second ran `sync` and got one sentinel
merge plus EIGHT `contested` attention entries — every key both sides
had written during the partition, including `task-signup` showing
open-vs-closed. For each entry it read both heads (`show --id` on each
of `contest.ids`), collapsed with `--expect <contest.expect>` (adding
`--override` where the settled gate tripped), and said which side it
kept and why in `-m`. Had either side retitled during the partition
there would have been a `field: "title"` entry too, collapsed the same
way with `set <key> --rename "<keeper>" --expect <contest.expect>` and
no `--override`. Eight writes later: `attention: []`, `frontier:
all-handled`, one `push`, and both replicas byte-identical — with every
resolution carrying a permanent `contested_resolved: [<losing ids>]`
record in the history. Total agent confusion across the recovery: zero.
That is the intended shape of a heal: sync, read the tickets, collapse
with their `expect`, push.
