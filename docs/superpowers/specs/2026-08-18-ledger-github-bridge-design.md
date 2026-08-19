# Ledger GitHub bridge: rename events and the additive sync (design)

2026-08-18, revision 4 — rev 3's gauntlet (two reviewers, both
building adversarial tests against the spike's own fixture transport)
cracked both of rev 3's foundations and retracted one of its tool
amendments: the marker oracle went blind across `export`/`import`
(ids re-mint, so the safe-recovery path re-imported the bridge's whole
mirrored history as human notes); the derived idempotency index was
scope-blind (a 12-character decoy note = silent censorship of a real
comment); duplicate link notes made both issues inbound writers (an
unbounded flip-flop minting a fabricated override per run); and the
`--override`-symmetry amendment, implemented, broke five shipped tests
including two load-bearing pins — the symmetry argument fails because
`claim` dissolves on the clock and `human` does not. Rev 4 is the
corrective; the marker/stamp/laws architecture held. Validation record
at bottom.

Rev 3 history: spike round 2 built the six laws end to end (16-
injection crash sweep, live trial incl. crash-resume) under the
multi-login ruling — no dedicated identity, verified-marker echo
suppression — and folded 28 findings, two of them rev-2 fixture
falsifications.

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
7. **Parent tool spec, error contract** (tool rev 16): the
   `needs_override` error document carries `"signals": [...]` —
   machine-readable signal names; the bridge must never parse
   English. **The rev-3 `--override`-symmetry half is RETRACTED**
   (rev 4): the gauntlet implemented it and broke five shipped tests
   including two load-bearing pins (`TestOverrideWithNoStandingSignal-
   IsLegalNoOp` pins the opposite ruling by name;
   `TestOverrideResetsAcrossLosingCASAttempt` constructs the killer:
   a claim that goes STALE between CAS attempts makes the
   caller-did-everything-right write die as a usage error inside the
   retry loop). The symmetry argument was unsound at the root: a
   rename's only gate is `human`, which never dissolves; `set`'s
   signals dissolve on the clock, so no-signal-no-op is the correct
   retry-safe semantics there — the flag's effect is conditional, not
   absent, which is why it isn't the forbidden accepted-and-ignored
   pattern. Rename keeps its own `bad_usage` (its gate cannot
   dissolve). Consumer note for the same contract: sync/push exit-3
   outcome documents go to stdout.
8. **Sync spec Addition 3, the contested streams** (rev 4): the
   write-heads antichain extends to the RENAME stream — rename events
   contest as pseudo-field `"title"` (same definition, ids
   fold-ordered winner-last, `expect` = the winner rename id, usable
   directly as `--rename --expect`). Probed rationale: concurrent
   cross-replica renames merged in SILENCE while the identical status
   race raised a contested entry — in a design whose bridge
   manufactures rename races at machine speed, and where `prior[]`
   cannot distinguish a race-loss from a sequential retitle. Law 5's
   "loses loudly" now holds cross-replica, not just same-store.
9. **Quickstart** (rev 4): one line teaching `set --rename` (cold
   agents must not keep operating under the immutable-title belief
   amendment 1 retires); the line budget rises 120 → 124 — the guard
   test's constant moves with a ruling, exactly as the sync build's
   110 → 120 did.

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
vocabulary and REFUSES a board that lacks them — or whose flags name
a NON-TERMINAL value (declared AND outside `{open, in-progress}`,
derivable with no new verb; rev 4 closes the probed
`--done in-progress` hole, which passed rev 3's membership-only check
and then closed GitHub issues on a non-terminal state) — naming the
failing flag, the declared vocabulary, and the fix command. **The
bridge also refuses a run whose issue listing SATURATES the window**
(`--limit 200` returning exactly 200): outside the window the bulk
maps are zero-valued, which silently disables the comment dedupe, the
state diff, and adoption — duplicates and un-adoptable orphans, probed
via a limit-faithful fixture. A loud stop naming pagination as the
fix, until pagination lands (backlog). Outbound:
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
  comments (observed live against the round-1 format). **The oracle's
  domain is `{id} ∪ {imported_from}`** (rev 4): `export`/`import`
  re-mints every event id, preserving the old id only in
  `imported_from` — an id-only oracle goes blind on exactly the
  recovery path Law 2 calls safe, and re-imports the bridge's entire
  mirrored history as human-attributed notes (gauntlet-2 probed,
  end-to-end). The marker also earns its keep OUTBOUND: it is what
  lets the note backfill and the drain recognize each other's posts,
  and what makes crash re-runs duplicate-free.
- **Cost, priced**: verification and Law 2's dedupe share ONE
  whole-chain read per run — it answers "does this id (or
  imported_from) resolve" AND "which idempotency keys are spent". The
  derived index is **scoped exactly as the tool's dedupe is** —
  `(author, kind, key, idempotency-key)`, never the bare key string
  (rev 4; the bare-string index was probed as a censorship primitive:
  one decoy note under any author/kind/key silently suppressed a real
  comment's import AND deleted the `deduped:true` impersonation
  detector the parent spec names). A chain event carrying a
  `gh-comment-*` key OUTSIDE the bridge's own write shape is warned,
  and the bridge writes anyway — the tool's scoped dedupe is the
  arbiter, so the poison fails loudly instead of succeeding silently.
  A bulk ids/keys read verb is a named tool-backlog wish, not v1.
- **The board's `github-link` notes are the authority** for key↔issue,
  read with two rev-4 hardenings the gauntlet forced. (1) **One link
  per key, BOTH directions**: the key's newest link note names its one
  issue, and an issue that is not that key's current link is NOT a
  link inbound either — rev 3's reader kept every issue ever linked as
  an inbound writer, so a duplicate create (two concurrent bridge
  runs) produced an unbounded closed/reopened flip-flop minting a
  fabricated `override: settled` per run, probed; "wasteful, not
  corrupting" was false and is retracted. Duplicate link notes are
  warned every run until a human resolves them. (2) **Link and
  bridge-state notes are read author-filtered**: only notes authored
  `github-bridge` count, and a link note that CHANGES an existing
  link is a refusal-with-handoff, never a silent repoint — rev 3's
  any-author last-write-wins read let one note from any board writer
  re-point a linked key or wedge the bridge's state (probed both).
  Honesty clause: authorship is asserted, so `--as github-bridge`
  impersonation remains possible and greppable — the tool's stated v1
  trust model, hardened by owner enforcement in v2; the operator
  runbook for a poisoned state note is a corrective note authored
  `github-bridge`.
  The issue-body `ledger-key:` line is a HINT, honored only when the
  link note agrees. An unlinked issue claiming a LINKED key is warned
  and never intaken (the probed hijack). **Adoption ruling**: the
  bridge writes `<!-- ledger-bridge -->` into every issue body it
  creates, and ADOPTS an unlinked issue only when the stamp AND the
  key hint are present AND the key has no linked issue — recovering
  crashed creates from the bulk list already in hand. A stamped
  forgery can bind a stranger's issue to a not-yet-linked key and can
  never touch a linked one — bounded, accepted, stated. The issue
  body is thereby a SECOND, independent copy of the identity map, and
  it — not the sync-first law — is what closes the unsynced-replica
  duplicate hazard (spike-verified both ways).
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
(5) **persist state when ANY of**: the run mutated GitHub, the run
wrote to the board, the drain carried any event outbound suppression
does NOT skip, or the refusal-record set changed (rev 4 states all
four disjuncts — rev 3's "anything the mirror owns" inverted under
its own example, since an `in-progress` write is precisely not
mirror-owned yet must advance the cursor; and a run whose only change
is a RESOLVED divergence must persist or the pruned record never
lands and the divergence's next real recurrence is silently
swallowed).
(6) **`ledger push <slug>` — always, selectively, LAST.** Always:
link notes and bridge state must reach the remote or the sync-first
law protects nothing (probed at rev 1). Selectively: bare `push`
publishes every local slug — the privacy lever. `partial_failure`
(exit 3), scoped per the outcomes array (rev 4): sync ⇒ abort IFF the
bridge's OWN slug failed, warn on other slugs' failures (rev 3's
blanket abort coupled the bridge's availability to every dead remote
and refused slug anywhere in the operator's store); push ⇒ warn,
retry next run. Consumer note: sync/push write the exit-3 outcomes
document to STDOUT (every other error is stderr) — a bridge parses
both streams; one sentence for the parent spec's CLI contract.

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
- **State writes are STATE-CONVERGENT in both directions** (rev 4 —
  the rule that actually makes state intake idempotent, which rev 3
  omitted from the law that claims it): before any status/rename
  write, compare the target's CURRENT value and write only on
  difference. Inbound, an intake close against an already-closed key
  is a no-op (rev 3 as written re-fired an attributed
  `override: settled` every run, forever — probed); outbound, a state
  mirror fires only when GitHub's current state differs. Comments and
  notes stay event-driven (markers and keys make them idempotent);
  state is level-triggered.
- `reset_required` on the stored cursor: warn and re-drain from empty.
  Safe FOR CONTENT because comments/notes are marker/key-idempotent
  and state writes are convergent (above); and on a re-drain run the
  divergence warning is SUPPRESSED ENTIRELY — `mirroredView` has no
  meaning when the drain is the whole chain, and rev 3's fallback
  accused a human of edits the bridge itself had made, then replayed
  the board's whole state history at GitHub (probed; both closed by
  this bullet and the convergence rule).
- Concurrency: single-instance operation is a stated constraint (no
  lock machinery in v1); adoption, dedupe, and state convergence make
  the overlap window inefficient — and the duplicate-create case
  resolves to a warned duplicate link (identity section), not a
  flip-flop.

**Law 3 — refusals converge** (rev 1's human-refusal path spammed a
note and a GitHub comment per run, forever): a refusal (human-labeled
key, unresolvable divergence) is recorded in bridge state as (issue,
aspect, observed-state); while unchanged on both sides it is silently
skipped, counted in `divergences`. The handoff note and the one
GitHub comment ("reserved on the board; a maintainer must apply this
there") are written ONCE per distinct divergence. **Record
lifecycle** (rev 4 — the fold dropped it): a record NOT re-observed
this run is PRUNED (the state note stays bounded, and a cleared
divergence is forgotten so its next real occurrence notes afresh);
pruning is a state change for Law 1's persistence rule. Suppression
notes (Law 1 step 3) share this exact machinery.

**Law 4 — attribution is paginated or absent**: the actor for a
close/reopen/rename comes from the FULL issue timeline —
`per_page=100 --paginate`, every page (rev 4 drops rev 3's "rel=last
walk" alternative: a last-page-only read misses any state event
followed by a page of comments, the mirror image of the probed
single-call failure). The match is the NEWEST event of the type. "No
matching event found" is the only fallback: issue author + a warning.
Cost, priced honestly: ceil(timeline/100) calls per changed aspect,
uncached — two aspects changing on one busy issue pay it twice.

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
notes `--as github-bridge` with kinds
`bridge-state`/`github-link`/`handoff`. **Outbound suppression is by
AUTHOR, not kind** (rev 4, a gauntlet-verified simplification that
also fixes a real loss): skip events authored `github:@*` or
`github-bridge` — full stop. Rev 3's kind list silently ate HUMAN
`handoff` notes, the issues spec's designated reclaim channel and the
highest-value note class on the board, while mirroring every other
kind; author suppression is strictly simpler and mirrors a human's
handoff like any other note.

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
    warns; BRIDGE-authored notes never mirror while a HUMAN `handoff`
    note DOES (author suppression, not kind); `suppressed_authors`
    counts a poisoned `--as github:@x` event; `signals: [...]`
    present in `needs_override` documents; rename `--override`
    without signal stays `bad_usage` while `set` keeps its pinned
    no-op (the retraction, asserted both ways).
12. Rev-4 regressions, each from a gauntlet probe: marker oracle
    resolves `imported_from` (export/import the board, re-run, ZERO
    re-imports); derived index scoped (the decoy note fails loudly —
    comment imports, warning emitted); duplicate links (both-linked
    fixture converges to 0/0 with one inbound writer and a warning,
    no flip-flop, no fabricated overrides); state convergence (intake
    close on a closed key writes nothing; re-drain run fires no
    divergence notes and replays no state mutations); link/state
    notes author-filtered (a mallory link/state note is inert +
    warned; a changed link is refusal-with-handoff); saturation
    refusal at exactly 200; `--done in-progress` refused;
    sync partial_failure on a foreign slug warns while own-slug
    failure aborts; refusal-record pruning persists (cleared
    divergence's record lands, next occurrence notes afresh);
    `reset_required` re-drain, one-board-one-repo refusal,
    sync-failure abort, and deleted/transferred-issue warning each
    get their named item (rev 3's orphans).
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
- Rev 3 gauntlet (two reviewers, both writing adversarial tests
  against the spike's fixture transport; 10 findings each, a tie on
  count, H1 on severity weight; H2's best probe IMPLEMENTED the
  override amendment and broke five shipped tests). All three
  re-litigable items resolved: the override symmetry RETRACTED
  (dissolving-signal TOCTOU; two load-bearing pins red), the
  whole-store sync abort scoped to the bridge's own slug, the
  adoption bound upheld but its "can never touch a linked one" claim
  falsified by the CHEAPER board-side path (any-author link notes) —
  now author-filtered with change-refusal. Foundations cracked and
  repaired: marker oracle blind across export/import (domain now
  {id} ∪ {imported_from}); derived index scope-blind (a decoy note =
  silent censorship + deletion of the deduped:true detector — now
  tool-scoped with loud failure); duplicate links made both issues
  inbound writers (unbounded flip-flop, fabricated override per run —
  one-link-per-key both directions); intake state writes re-fired
  attributed overrides forever on rev 3's own text (state convergence
  was the spike's unstated load-bearing rule — now Law 2's first
  bullet); re-drain manufactured false human-edit accusations and
  replayed state history (suppressed + convergent now); kind
  suppression ate human handoff notes (author suppression, simpler
  and lossless); rename races merged silently while status races
  contested (title stream added to the antichain — amendment 8);
  saturation blindness past 200 (loud refusal); terminality unchecked
  (--done in-progress hole); Law 4's price understated and its
  rel=last alternative wrong (full paginate, honest ceil(N/100));
  persistence rule inverted under its own example and dropped the
  refusal-set disjunct + pruning (all four disjuncts + lifecycle now
  stated); quickstart amendment missing (entry 9, budget 120 → 124).
