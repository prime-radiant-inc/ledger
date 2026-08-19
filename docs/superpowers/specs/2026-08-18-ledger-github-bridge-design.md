# Ledger GitHub bridge: rename events and the additive sync (design)

2026-08-18, revision 3 — a second spike built rev 2's six laws end to
end (16-injection crash sweep green, live trial green including
crash-resume against the real API) under one Jesse ruling that
replaces rev 2's identity model: **no dedicated bridge identity; the
bridge works with any number of GitHub logins operating it**, and echo
suppression becomes a VERIFIED MARKER. Rev 3 folds in the spike's 28
findings — including one bug only the live trial could catch (the
bridge's own unmarked refusal comment echoing back as board state) and
two rev-2 sentences the fixtures falsified (the divergence test fired
on every ordinary close; persist-when-changed plus per-aspect
suppression produced permanent suppression). Validation record at
bottom.

Rev 2 history: rev 1's gauntlet produced ~28 findings and six
Criticals; rev 2 rebuilt Part B as six laws and grew the amendment
inventory to six. The architecture survived; the sentences did not.

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
7. **Parent tool spec, error contract** (rev 3, two additions ridden
   by tool rev 16): the `needs_override` error document carries
   `"signals": [...]` (machine-readable signal names — the bridge must
   never parse English); and `--override` with NO standing signal is
   `bad_usage` on EVERY write verb, not just renames (the spike built
   the rename rule and found `set` silently accepting-and-ignoring the
   flag — the exact pattern this spec family forbids; symmetry pinned
   here, gauntlet may attack it). Consumer note for the same contract:
   sync/push exit-3 outcome documents go to stdout.

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

**Vocabulary is configured, not assumed — for the TERMINAL pair only**
(rev 3 narrows rev 2's premise, which was half wrong: `ledger create`
PINS the non-terminal vocabulary of a ready-capable board to
`open`+`in-progress`; only terminals are free, so `done`/`dropped` is
legal but `todo`/`doing` is not): `--done` and `--not-planned` name
the terminal values; at startup the bridge reads the board's declared
vocabulary and REFUSES a board that lacks them, naming the failing
flag, the declared vocabulary, and the fix command. Outbound:
done-value ⇒ close (completed), not-planned-value ⇒ close (not
planned). Inbound: close completed ⇒ done-value with evidence
`gh:<owner>/<repo>#<n>`; close NOT_PLANNED ⇒ not-planned-value with NO
evidence (evidence on wontfix is "pasted-string theater"); reopen ⇒
`open`, always — no flag, since the non-terminal vocab is pinned.

**Identity, four pins** (rev 3 — the ruling):

- **No dedicated bridge identity; no login comparison anywhere.** Any
  number of GitHub logins operate the bridge, each with their own `gh`
  auth, while the same logins participate as humans. The bridge never
  calls `gh api user`. Echo suppression is a **verified marker**:
  every comment the bridge posts — mirrored notes, close/reopen
  explanations, divergence notices, ALL of them; an unmarked bridge
  comment does not exist (the spike's live trial caught exactly one
  and it echoed back as board state attributed to a person) — opens
  with `**<author>** (via ledger, <event-id>):`. Inbound, a comment is
  bridge-authored iff it matches that format AND the embedded event id
  RESOLVES on this board's chain. A pasted marker with a resolving id
  suppresses the paster's own comment (self-inflicted, stated); a
  marker with a garbage id imports normally; both edges verified live
  under one login playing both roles. **The marker is a versioned wire
  format**: any future format change must keep every prior format
  recognized, or the bridge re-imports its own history as human
  comments (observed live against the round-1 format). The marker also
  earns its keep OUTBOUND: it is what lets the note backfill and the
  drain recognize each other's posts, and what makes crash re-runs
  duplicate-free.
- **Cost, priced**: verification and Law 2's dedupe share ONE
  whole-chain read per run — it answers "does this id resolve" AND
  "which idempotency keys are spent" (the derived index that keeps
  comment intake from costing one subprocess per comment per run).
  A bulk ids/keys read verb is a named tool-backlog wish, not v1.
- **The board's `github-link` note is THE authority** for key↔issue.
  The issue-body `ledger-key:` line is a HINT, honored only when the
  link note agrees. An unlinked issue claiming a LINKED key is warned
  and never intaken (the probed hijack). **Adoption ruling** (the
  crash window and the hijack are the same input; only a stamp
  separates them): the bridge writes `<!-- ledger-bridge -->` into
  every issue body it creates, and ADOPTS an unlinked issue only when
  the stamp AND the key hint are present AND the key has no linked
  issue — recovering its own crashed creates from the bulk list it
  already holds (no search call). A stamped forgery can therefore bind
  a stranger's issue to a not-yet-linked key, and can never touch a
  linked one — bounded like the marker edge, accepted, stated. The
  issue body is thereby a SECOND, independent copy of the identity
  map, and it — not the sync-first law — is what actually closes the
  unsynced-replica duplicate hazard (spike-verified both ways: the
  stamp adopts; erase the hint and the duplicate returns).
- **The reserved state key defends itself**: intake never mints
  `github-bridge-state` (collision-suffixed), and a link hint naming
  it is refused (probed live; the fresh spike refused rev 1's real
  seizure artifact on its first run).

**Bridge state**: one note, kind `bridge-state`, under reserved NOTE
key `github-bridge-state` (a note key: no board key, no attention
noise). Body: repo, cursor, standing-refusal records. **No comment
high-water marks** — deleted by the idempotence law below (both
reviewers independently derived this simplification; the map was also
unmergeable across replicas, being last-write-wins). One board ↔ one
repo: the bridge refuses a run whose state note names a different
repo; multi-repo bridging is v2.

**Law 1 — ordering**:
(1) `ledger sync` — sync FIRST, always; failure aborts the run.
(Today this merges the WHOLE store — `sync` takes no slug selector;
a slug-selective sync, symmetric with push's privacy lever, is a named
tool-backlog item the bridge adopts when it exists.)
(2) READ the outbound drain (`since <cursor>`);
(3) intake GitHub→board, per-aspect pending suppression: a key with an
un-mirrored status/rename event is off-limits to intake for THAT
aspect — and **when suppression fires AND the remote differs from the
last MIRRORED value, the bridge warns and leaves a board note**: that
is a genuine concurrent GitHub edit being discarded. The comparison is
against what the bridge last put there — `mirroredView`, the fold over
the chain MINUS this run's drain, derived, stateless, replica-stable;
a key with no pre-drain history compares as a fresh open issue. (Rev
2 compared against the OUTGOING value, which flags every ordinary
close as a discarded human edit — fixture-falsified.) Suppression
notes get the same convergence treatment as Law 3 refusals: once per
distinct divergence, then counted.
(4) mirror board→GitHub;
(5) **persist state when the run changed something OR the drain
carried anything the mirror owns.** (Rev 2's persist-when-changed
alone was fixture-falsified: an event that mirrors to nothing —
in-progress, a labels edit — never advanced the cursor, so its key's
status aspect stayed off-limits to intake FOREVER; a claimed key
silently stopped accepting GitHub closes.)
(6) **`ledger push <slug>` — always, selectively, LAST.** Always:
link notes and bridge state must reach the remote or the sync-first
law protects nothing (probed at rev 1). Selectively: bare `push`
publishes every local slug — the privacy lever. `partial_failure`
(exit 3): sync ⇒ abort; push ⇒ warn, retry next run. Consumer note:
sync/push write the exit-3 outcomes document to STDOUT (every other
error is stderr) — a bridge parses both streams; one sentence for the
parent spec's CLI contract.

**Law 2 — idempotence by construction, not by cursor** (crash
anywhere, re-run safely; recovery CONVERGES — it may take two or three
runs to reach the 0/0 fixed point, because recovery bookkeeping is
itself events; never promise "the next run is a no-op"):
- Intake comments: `note -k comment --idempotency-key
  gh-comment-<rest-id>` — the REST id parsed from the comment `url`'s
  `#issuecomment-<id>` fragment (the `--json` `id` field is a GraphQL
  node id, NOT ordered — pinned so nobody re-derives it). Already-
  spent keys are skipped via the shared whole-chain read's DERIVED
  index — no per-comment subprocess, nothing stored, nothing to lose
  on a merge (this derived index is what keeps both reviewers' rev-2
  simplification cheap; without it the law costs one `ledger note`
  invocation per GitHub comment per run, forever). **`deduped: true`
  in the write response is part of the contract the bridge depends
  on**: a deduped write is not a write, or a converged run can never
  report zero.
- Mirrored comments carry the source event id in their marker; before
  posting, the mirror checks the issue's already-fetched comments for
  that id — mid-run failure re-runs never double-post (verified live
  with injected crashes against the real API).
- Issue creation: the crash window is closed by ADOPTION, not search —
  the identity section's stamp rule, using the bulk list already in
  hand.
- Handoff and suppression notes carry idempotency keys derived from
  (issue, aspect, observed state).
- `reset_required` on the stored cursor: warn and re-drain from empty
  — safe, because every action above is idempotent (fixture-verified:
  no duplicate issue, no duplicate comment).
- Concurrency: single-instance operation is a stated constraint (no
  lock machinery in v1); adoption and dedupe make the overlap window
  wasteful, not corrupting.

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
TERMINAL value: straight to the handoff note — "never re-close blind";
non-terminal `claim_lost`: one re-read retry, and **the same rule
applies to the retry** (a retry that hits a signal takes the signal's
rule — the spike's `retried+override` path). **The signal names come
from the error document, not its prose**: tool rev 16 adds
`"signals": ["human", ...]` to the `needs_override` error (the spike
distinguished `human` from `claim`/`settled` by substring-matching an
English message — a prose dependency in a machine contract, deleted by
one field).

**Law 6 — mirror fidelity**: EVERY state mirror carries its message —
a close mirrors as a marked comment carrying the close message and
evidence THEN the close, and a REOPEN likewise comments its reason
before reopening (rev 2 named only the close; the reason a key came
back matters as much as why it closed). Notes on keys with no linked
issue: at ISSUE-CREATION time the bridge backfills the key's existing
non-bookkeeping, non-GitHub-authored notes (the statusless-seed
window; the marker is what keeps backfill and drain from
double-posting), and a note whose key never gains an issue is dropped
WITH a warning naming the event id. `blocked-by` edges have no GitHub
representation and are not mirrored — stated. Bridge-authored board
writes are pinned: intake events `--as github:@<login>`; bookkeeping
and handoff notes `--as github-bridge` with kinds
`bridge-state`/`github-link`/`handoff` — ALL THREE kinds
outbound-suppressed.

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
4. Ordering: the round-1 falsification as regression (close → one run
   → one GH close, zero board writes, no fabricated attribution); the
   divergence warning fires against the last-MIRRORED value and does
   NOT fire on an ordinary close (the rev-2 falsification, pinned);
   the mirrors-to-nothing case (claimed key accepts a GitHub close on
   the next run — the permanent-suppression falsification, pinned);
   the push-hole regression (mirror-only run on A, synced B run ⇒
   ZERO duplicate issues).
5. Idempotence: crash injection at every transport call site in BOTH
   modes — fail-BEFORE and fail-AFTER the effect (fail-before alone
   never creates the orphan that mints duplicates) ⇒ every replay
   CONVERGES to a 0/0 fixed point (may take 2-3 runs; never assert
   "next run is clean") with no duplicate comments, notes, or issues;
   the `deduped: true` contract (a converged run reports zero writes);
   report's `cursor` = persisted cursor on no-op runs.
6. Identity: multi-login (several operator logins across runs, humans
   commenting under the SAME logins — mirrored comments never import,
   human ones import once); the three marker edges (forged marker +
   real id ⇒ suppressed; garbage id ⇒ imports; prior format rule);
   hijack regression (unlinked issue claiming a LINKED key ⇒ warned,
   untouched); adoption (crash after create ⇒ stamped orphan adopted,
   one link, no duplicate; stamp+hint on an unlinked key adopts,
   absent stamp refuses); two-issues-one-key ⇒ linked wins;
   reserved-key seizure regression.
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
11. Mirror fidelity: close AND reopen comments precede their state
    change; issue-creation backfills pre-link notes exactly once (the
    marker keeps backfill and drain apart); never-linked note drop
    warns; handoff notes never mirror; `suppressed_authors` counts a
    poisoned `--as github:@x` event; `signals: [...]` present in
    `needs_override` documents; `--override` with no signal is
    `bad_usage` on set and rename alike.
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
  simplification: idempotency-keyed imports, marks deleted. Rev 2 was
  the corrective: the six laws, the six-entry amendment inventory, the
  configured vocabulary, the dedicated identity requirement, and a
  test plan mapping onto the laws.
- Spike round 2 (same branch; six laws + the multi-login ruling; 28
  findings; 16-injection crash sweep in both fail modes, all
  converging; live trial green including crash-resume with injected
  failures against the real API, a >30-event pagination proof — the
  single-call read finds NOTHING, not merely a stale actor — and the
  three marker edges under one login). Fixture-falsified rev-2
  sentences, corrected here: the divergence comparison (outgoing →
  last-mirrored), the persistence rule (or-drain-carried-mirrorable),
  "next run is a no-op" (→ converges). Live-falsified: an unmarked
  bridge comment (the Law-3 notice) echoed back as board state —
  hence "an unmarked bridge comment does not exist." Ruling applied:
  dedicated identity deleted; verified marker; multi-login pinned by
  test. Spike recommendations adopted: derived idempotency index off
  the shared whole-chain read; stamp-based adoption over search;
  `deduped: true` as contract; `signals: [...]` in the error
  document; reopen messages; terminal-only vocabulary flags; both
  crash-injection modes. Deliberately re-litigable at the next
  gauntlet: the stamped-forgery adoption bound, the `--override`
  bad_usage symmetry on `set`, and the whole-store sync cost.
