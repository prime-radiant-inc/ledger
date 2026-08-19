# Ledger GitHub bridge: rename events and the additive sync (design)

2026-08-18, revision 2 — revision 1's gauntlet (two reviewers, a
stateful fixture transport, live probes against the spike's bridged
repo) produced the project's heaviest single-round result: ~28 distinct
legitimate findings, six Criticals between the two slates, and nine
independent convergences. Rev 2 rebuilds Part B around six explicit
laws, patches Part A's contradictions, and grows the amendment
inventory from one entry to six. The architecture (rename as a
set-field event; an additive bridge over the public CLI) survived; the
sentences did not. Validation record at bottom.

**Scope**: (A) a first-class RENAME event in the core tool — tool rev
16; (B) `ledger-gh`, a companion bridge — Level 1 (ledger→GitHub
mirror) and Level 2 (GitHub→ledger additive intake) ONLY. Level 3
(label/assignee state sync) stays out: unproven demand. **Coexistence
resolved: the board is canonical; GitHub is the intake and display
window.**

Operator requirements: one idempotent command, safe to re-run, safe to
crash anywhere, host-portable; every cross-system write attributed and
greppable; board doctrine (CAS, standing signals, evidence) binds the
bridge exactly as it binds any agent.

## Part A — the rename event (core tool, rev 16)

**Write**: `ledger set <key> --rename "<new title>"`.

- **Encoding**: a top-level `"rename"` field on a `type:"set"` event —
  not a sixth event type. Cursors deliver it, `watch --key` matches it,
  idempotency dedupes it, rollup coverage counts it, sync merges it;
  the fold sees it with one string test.
- **One assertion per event, the `-m` rule stated once**: `--rename`
  with `field=value` or `--evidence` is `bad_usage`. `-m` is
  `bad_usage` on a bare rename and REQUIRED with `--override`, where it
  is the override justification — it renders wherever override messages
  render and is never a title. `--override` without a standing signal
  is `bad_usage` (nothing to override). `--idempotency-key` is allowed,
  scoped to rename events (a rename never dedupes against a field
  write sharing its key).
- **Fold rule**: a key's title = the latest rename event's text in fold
  order, else the first status event's `-m`. Concurrent renames resolve
  last-in-fold-order; losers persist as fold-path history.
- **The gate**: `claim` and `settled` do NOT gate a rename — a title is
  not an outcome. **`human` DOES**: `needs_override`, satisfied by
  `--override -m "<why>"`, recorded as `override: human` — retitling a
  person's reserved issue under them is the friction the label exists
  to create. Cost, priced: the gate needs the key's labels, so a rename
  pays the same whole-chain read class as every rule-5 check (sync spec
  Addition 5) — acceptable; renames are rare. `--expect` stays
  optional; when passed it is CAS against the key's latest RENAME event
  (`--expect none` = never renamed) — a new `--expect` stream, named in
  the amendment inventory.
- **Existence**: rename requires a locally existing titled key
  (`unknown_key`, hint = the seed command); ready-capable boards only.
  Fold totality, two distinct cases (rev 2 splits them): (a) reachable
  and testable — a rename PRECEDING a colliding seed in fold order
  (two-root merge) titles the key, and its `prior` carries the
  fold-path seed; (b) hand-built only — a rename with NO seed anywhere
  in the chain still titles the key (totality; fixture-crafted, since
  sync merges whole chains and cannot ship a rename without its seed).
- **`prior` is fold-path history, not a complete inventory** (rev 2):
  `renamed: {by, ts, id, prior[]}` lists earlier titles ON THE FOLD
  PATH, oldest first, fold-path seed included. Under a two-root
  collision the losing root's seed title appears in no rename structure
  — the read-both-heads doctrine remains the way to see a collided
  key's whole story, and the skill says so.
- **Render, mandatory labeling**: every title-bearing surface shows a
  renamed title as renamed with attribution reachable — JSON rows carry
  `renamed`; TTY title lines carry `(renamed by <author>)`; the
  identity header lists prior → now per renamed key; **contested and
  stale-claim attention entries carry the current title with `renamed`
  info** (they are title-bearing rows; this supersedes the sync spec's
  title clause — amendment inventory). The `renamed` structure is
  ABSENT on unrenamed keys. **Byte-compatibility claim, scoped
  honestly** (rev 1's blanket claim was false by its own next
  sentence): rename-LABELING adds nothing to unrenamed output, but
  rev 16 makes two deliberate output changes on ALL boards — `ready`'s
  TTY blocked line gains the title, and `status <key>`'s drill-down
  gains title + rename info — each with its own fixture, not a
  byte-identity test.
- **Determinism**: pure fold; the standing determinism test gains a
  renamed key in its fixture.

### Amendment inventory (complete — rev 1 named only the first)

1. **Issues spec, title law**: "immutable" becomes "stable by default:
   changed only by explicit, labeled rename events."
2. **Sync spec Addition 3's title clause** ("the KEY's title under the
   issues spec's unamended law … immutable") is superseded: a contested
   entry's `title` is the key's CURRENT title (fold rule above) with
   `renamed` info attached. The entry's `expect`/ids machinery is
   untouched.
3. **Issues rule 5's scope** ("gates every guarded write"): the human
   signal additionally gates renames — a named exception class outside
   guarded-field writes, with the same `override:` recording.
4. **Issues rules 2–4's `--expect` semantics**: a second, rename-scoped
   CAS stream exists alongside field-scoped CAS.
5. **Parent tool spec, Retries**: dedupe gains the rename dimension
   (rename events dedupe only against rename events).
6. **`skills/ledger-issues/SKILL.md`**: the Titles doctrine (seed `-m`
   is still the title to write well; renames are corrections with
   attribution, not a workflow) AND the Override-ethics paragraph —
   its "never to decorate (wording, titles…)" sentence is rewritten:
   the rename event is now the legitimate title correction; the
   prohibition that survives is overriding STATE for cosmetic reasons.

## Part B — `ledger-gh`, the bridge

One verb: `ledger-gh sync --repo <owner/repo> --ledger <slug>
[--store <path>] [--done <value>=closed] [--not-planned <value>=wontfix]`.
Transport: the `gh` CLI, subprocess-only (v1). Board access: the
`ledger` CLI, subprocess-only. Report: `{ok, repo, ledger,
gh_mutations, board_writes, cursor, divergences, suppressed_authors,
actions[], warnings[]}` — `cursor` is the PERSISTED cursor (a no-op
run reports the stored one), `divergences` counts standing refusals,
`suppressed_authors` counts outbound events skipped per `github:@*`
author (poisoning is visible even though the namespace is unenforced —
author enforcement rides the existing owner-enforcement v2 item, not a
one-prefix carve-out).

**Vocabulary is configured, not assumed** (a legal ready-capable board
can use `done`/`dropped`): `--done` and `--not-planned` name the
terminal values the bridge writes and recognizes; at startup the bridge
reads the board's declared vocabulary and REFUSES a board that lacks
them, naming the fix. Outbound: done-value ⇒ close (completed),
not-planned-value ⇒ close (not planned). Inbound: GitHub close
completed ⇒ done-value with evidence `gh:<owner>/<repo>#<n>`; close
NOT_PLANNED ⇒ not-planned-value with NO evidence (the issues spec
calls evidence on wontfix "pasted-string theater"); reopen ⇒ open.

**Identity, three pins**:

- **A dedicated bridge identity is an operating REQUIREMENT.** At
  startup the bridge resolves its GitHub login (`gh api user`) and
  REFUSES to run if it equals any `github:@<login>` author already on
  the board — the bridge must never share a login with a human
  participant, because echo suppression keys on it. (Rev 1's
  operator-token model was probed dropping the operator's own
  comments.)
- **The board's `github-link` note is THE authority** for key↔issue.
  The issue-body `ledger-key:` line is a HINT: honored only when the
  board's link note for that key names this issue number. An unlinked
  issue claiming an existing key is warned and never intaken, never
  seeded over (rev 1's body-line authority was probed as a hijack: any
  GitHub user could bind their issue to any key and drive it). Two
  issues claiming one key: the linked one wins, the other is warned
  every run until a human resolves it.
- **The reserved state key defends itself**: intake never mints a key
  named `github-bridge-state` (collision-suffixed like any other), and
  a link hint naming it is refused (probed live: rev 1's bridge seeded
  its own bookkeeping key as pickable work from a GitHub issue titled
  "GitHub bridge state").

**Bridge state**: one note, kind `bridge-state`, under reserved NOTE
key `github-bridge-state` (a note key: no board key, no attention
noise). Body: repo, cursor, standing-refusal records. **No comment
high-water marks** — deleted by the idempotence law below (both
reviewers independently derived this simplification; the map was also
unmergeable across replicas, being last-write-wins). One board ↔ one
repo: the bridge refuses a run whose state note names a different
repo; multi-repo bridging is v2.

**Law 1 — ordering** (v1 falsified live at the spike; v2 closes the
push hole G1 probed):
(1) `ledger sync <slug>` — sync FIRST, always (unsynced replicas mint
duplicate issues; failure aborts the run);
(2) READ the outbound drain (`since <cursor>`);
(3) intake GitHub→board, per-aspect pending suppression: a key with an
un-mirrored status/rename event is off-limits to intake for THAT
aspect — and **whenever suppression fires AND the remote value differs
from the value about to be pushed, the bridge warns and leaves a board
note**: that difference is a genuine concurrent GitHub edit being
discarded, and silence would erase a human's action (probed: rev 1
suppressed silently);
(4) mirror board→GitHub;
(5) persist state if anything changed;
(6) **`ledger push <slug>` — always, selectively, LAST.** Always:
link notes and bridge state must reach the remote or the sync-first
law protects nothing (probed: rev 1's push-if-intake-wrote left
bookkeeping local and minted duplicate issues WITH sync-first obeyed).
Selectively: bare `push` publishes every local slug — the skill's
privacy lever — so the bridge names its board, only. Push/sync
`partial_failure` (exit 3): sync ⇒ abort; push ⇒ warn, retry next run
(everything is idempotent below).

**Law 2 — idempotence by construction, not by cursor** (crash
anywhere, re-run safely):
- Intake comments: `note -k comment --idempotency-key
  gh-comment-<rest-id>` — the REST id parsed from the comment `url`'s
  `#issuecomment-<id>` fragment (free in the bulk read; the `--json`
  `id` field is a GraphQL node id and is NOT ordered — pinned so
  nobody re-derives it). Re-import after any crash is a dedupe no-op.
- Mirrored comments carry the source event id in their marker:
  `**<author>** (via ledger, <event-id>):`. Before posting, the mirror
  checks the issue's already-fetched comments for that id — a mid-run
  failure re-run never double-posts (probed against rev 1).
- Issue creation: before `gh issue create`, search the repo for an
  existing issue whose body hint names the key AND whose creation the
  bridge recognizes as its own; one search, closes both the crash
  window and the concurrent-run window.
- Handoff and suppression notes carry idempotency keys derived from
  (issue, aspect, observed state).
- `reset_required` on the stored cursor (export/import re-mint, ref
  surgery): warn and re-drain from empty — safe, because every mirror
  action above is idempotent.
- Concurrency: single-instance operation is a stated constraint (no
  lock machinery in v1); the searches above make the overlap window
  merely wasteful, not corrupting.

**Law 3 — refusals converge** (rev 1's human-refusal path spammed a
note and a GitHub comment per run, forever): a refusal (human-labeled
key, unresolvable divergence) is recorded in bridge state as (issue,
aspect, observed-state); while unchanged on both sides it is silently
skipped, counted in `divergences`. The handoff note and the one
GitHub comment ("reserved on the board; a maintainer must apply this
there") are written ONCE per distinct divergence.

**Law 4 — attribution is paginated or absent**: the actor for a
close/reopen/rename comes from the issue timeline read to its LAST
page (`--paginate` or rel=last walk — the timeline is oldest-first,
30/page, and includes comments; rev 1's single call was probed
attributing to stale actors on busy issues and to nobody past 30
events). The match is the NEWEST event of the type. "No matching
event found" is the only fallback: issue author + a warning. Cost:
two calls per changed issue, priced and accepted.

**Law 5 — guarded writes follow the doctrine, including its terminal
exception**: `--expect` from a fresh read; intake renames pass
`--expect` from the rename stream (Part A's CAS — a board rename
racing an intake rename loses loudly, not silently). On
`needs_override` from `claim`/`settled`: auto-`--override`, attributed
`github:@<login>` — a real person's decision, tool-recorded for
triage. On `needs_override` from `human`: never — Law 3's refusal
path (login↔label identity mapping is v2). On `claim_lost` for a
TERMINAL value: straight to the handoff note — "never re-close blind"
is the doctrine's own exception (rev 1's "retry once" contradicted
it); non-terminal `claim_lost`: one re-read retry.

**Law 6 — mirror fidelity**: a close mirrors as a comment carrying the
close message and evidence, THEN the close — never a bare unexplained
closure. Notes on keys with no linked issue: the mirror defers
nothing; at ISSUE-CREATION time the bridge backfills the key's
existing non-bookkeeping notes (the statusless-seed window), and a
note whose key never gains an issue is dropped WITH a warning naming
the event id. `blocked-by` edges have no GitHub representation and
are not mirrored — stated. Bridge-authored board writes are pinned:
intake events `--as github:@<login>`; bookkeeping and handoff notes
`--as github-bridge` with kinds `bridge-state`/`github-link`/`handoff`
— and ALL THREE kinds are outbound-suppressed (rev 1 suppressed two;
its handoff notes echoed to GitHub as comments).

**Slugification, pinned**: lowercase, non-grammar characters → `-`,
collapsed, 48-char truncate; empty result → `issue-<n>`; collision →
`-<n>` suffix computed locally (two replicas intaking concurrently can
still collide or diverge — the board's own two-root machinery is the
net, stated).

**Out of scope, named**: Level 3 state sync; issue deletion/transfer
(broken link at read time ⇒ warning + one-time handoff note);
webhooks; pagination beyond 200 issues/run; rate-limit backoff
(transient failures are safe by Law 2); multi-repo boards; author
namespace enforcement (owner-enforcement v2 carries it). CLI wishes
go to the tool backlog: latest-event-of-kind read; write-then-fold in
one call.

## Test plan

1. Rename fold: none/one/several; concurrent via real sync (converge,
   loser greppable); title survives claim/close; seed message never
   resurrected; fold-order-precedence case (two-root + rename, `prior`
   carries fold-path seed); hand-built no-seed totality case.
2. Rename gates and flags: human ⇒ `needs_override` ⇒
   `--override -m` lands with `override: human` and the message
   rendering as override text; claim/settled do NOT gate; `--override`
   without signal ⇒ `bad_usage`; the full bad_usage matrix; rename
   `--expect` CAS both ways; `--idempotency-key` dedupes rename-vs-
   rename only; plain-board and unknown-key refusals.
3. Rename ecosystem: `since`/`watch` deliver renames; rollup coverage
   counts them; export/import round-trips them; determinism fixture
   includes a renamed key; contested and stale-claim entries carry
   renamed titles; the two deliberate render changes have their own
   fixtures (no byte-identity claim).
4. Ordering: the spike falsification as regression (close → one run →
   one GH close, zero board writes, no fabricated attribution); the
   suppressed-genuine-edit case warns and notes; the push-hole
   regression (fixture transport: mirror-only run on replica A, then
   synced replica B run ⇒ ZERO duplicate issues).
5. Idempotence: crash injected after every phase (fixture transport
   fails at each call site in turn) ⇒ re-run converges with no
   duplicate comments, notes, or issues; double-run 0/0 on a converged
   board; state persists only on change; report's `cursor` = persisted
   cursor on no-op runs.
6. Identity: hijack regression (unlinked issue claiming an existing
   key ⇒ warned, untouched); two-issues-one-key ⇒ linked wins;
   reserved-key seizure regression (issue titled to slugify into the
   state key ⇒ suffixed); dedicated-identity startup refusal.
7. Refusal convergence: human-labeled divergence ⇒ exactly one note +
   one GH comment across N runs; `divergences` counted; cleared when
   either side changes.
8. Guarded intake: claim/settled auto-override lands attributed;
   terminal `claim_lost` ⇒ handoff, no retry; non-terminal ⇒ one
   retry; rename race loses loudly via rename-CAS.
9. Attribution: fixture timeline >30 events with a stale close cycle
   ⇒ newest actor found via pagination; no-event ⇒ author + warning.
10. Vocabulary: `done`/`dropped` board bridged with flags; missing
    vocab refused naming the fix; NOT_PLANNED maps in with no
    evidence.
11. Mirror fidelity: close comment precedes close; issue-creation
    backfills pre-link notes; never-linked note drop warns; handoff
    notes never mirror; `suppressed_authors` counts a poisoned
    `--as github:@x` event.
12. Bridge tests run against a FIXTURE transport; ONE live acceptance
    trial against a scratch repo (below). Skill/doctrine harness
    re-runs over the amended SKILL.md lines.

## Trial plan (acceptance, live)

The spike's five steps with production binaries, PLUS: the hijack
attempt; the reserved-key seizure attempt; the human-labeled refusal
(one note + one comment, then silence); a >30-event issue attribution;
a `done`/`dropped` vocabulary board; a crash-resume (kill the bridge
mid-mirror, re-run, audit zero duplicates); and the unsynced-replica
duplicate-issue hazard demonstrated once against the fixture
transport, never live.

## Validation record

- Spike (branch `spike/bridge`): all five trial steps green live;
  falsified intake-first ordering and unconditional state persistence;
  pinned rename-as-set-field, note-key state, kind-based suppression,
  doctrine-path writes, timeline attribution. Rev 1 was written from
  it, overriding six spike behaviors deliberately.
- Rev 1 gauntlet (two reviewers, stateful fixture transport + live
  probes; 19 findings each, six Criticals, nine convergences; G1 took
  the round on severity weight). Probed: the push-if-intake-wrote rule
  left bookkeeping unpublished and minted duplicate issues WITH
  sync-first obeyed; the `ledger-key:` body line was hijackable
  authority (existing key retitled/settled by a stranger's issue, then
  flip-flopped with fabricated overrides every run); the human-refusal
  path spammed a note + comment per run forever; the operator-token
  echo check dropped the operator's own comments (same login, probed
  live); single-call timeline attribution named stale actors (>30
  events, close/reopen cycles); bare `ledger push` published private
  slugs; the byte-identity render claim was contradicted by its own
  next sentence; test 7 asserted the opposite of the override ruling
  via an unreachable path; the "reserved" state key was seized from a
  GitHub issue title; a legal `done`/`dropped` board hard-errored;
  suppression discarded genuine concurrent GitHub edits silently;
  handoff notes echoed out as comments; state-note LWW lost high-water
  marks on merge — resolved by BOTH reviewers' convergent
  simplification: idempotency-keyed imports, marks deleted. Rev 2 is
  the corrective: the six laws, the six-entry amendment inventory, the
  configured vocabulary, the dedicated identity requirement, and a
  test plan whose items map one-to-one onto the laws.
