# Ledger GitHub bridge: rename events and the additive sync (design)

2026-08-19, revision 7 — the structural rewrite agreed at rev 6:
narration moved to the validation record (bottom), vocabulary
front-loaded, every law restated whole with rev 6's consolidated
rulings folded into the law text. **No semantic change from rev 6**,
with three named exceptions, all corrections toward text that was
already binding: (1) two stale test-plan lines claimed rename
`--override` without a signal is `bad_usage` — they now match
amendment 7's completed retraction (legal no-op on every write verb);
(2) the test plan's duplicate item-12 numbering is fixed; (3) the
missing gauntlet-3 entry is added to the validation record. The build
gate remains as ruled: Part A (rename) is in production build;
Part B builds after this document passes a reviewer round.

**Scope**: (A) a first-class RENAME event in the core tool — tool rev
16; (B) `ledger-gh`, a companion bridge — Level 1 (ledger→GitHub
mirror) and Level 2 (GitHub→ledger additive intake) ONLY. Level 3
(label/assignee state sync) stays out: unproven demand. Coexistence
resolved: **the board is canonical; GitHub is the intake and display
window.**

**Operator requirements**: one idempotent command, safe to re-run,
safe to crash anywhere, host-portable; every cross-system write
attributed and greppable; board doctrine (CAS, standing signals,
evidence) binds the bridge exactly as it binds any agent.

## Vocabulary

- **ASPECT** ∈ {status, title} — the closed list every
  refusal/suppression record key and Law 4 cost count uses.
- **DRAIN** — the outbound event stream `ledger since <cursor>`:
  the EDGES since the last persisted cursor.
- **LEVEL** — the board's current FOLDED value for a key: the thing
  state mirrors push (never the drain's edges).
- **MIRROREDVIEW** — fold(chain − this run's drain's NON-SUPPRESSED
  events): the board state the bridge last put on GitHub, derived,
  stateless, replica-stable. Intake writes stay IN the view — they
  describe what GitHub already shows.
- **MARKER** — the verified comment prefix `**<author>** (via ledger,
  <event-id>):` — the bridge's proof of authorship for every comment
  it posts. A versioned wire format: every prior format stays
  recognized forever.
- **STAMP** — `<!-- ledger-bridge -->` in an issue body the bridge
  created: the adoption credential and the second, independent copy
  of the identity map.
- **ESTABLISHED LINK** — for a key, the oldest non-retracted
  `github-bridge`-authored `github-link` note: the one issue that is
  that key's link, in both directions.
- **ORACLE** — the id-resolution test behind the marker: does this
  event id resolve on the board's chain? Domain
  `{id} ∪ {imported_from} ∪ {ids this run wrote}`, both directions.
- **TERMINAL PAIR** — the board's two terminal status values, named
  by `--done` and `--not-planned`.

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
  render and is never a title. **`--override` without a standing signal
  is a legal no-op, exactly as on `set`** — the `human` gate can
  dissolve mid-write (`human` is an unguarded labels token any writer
  or sync merge clears), so a `bad_usage` here is the mid-CAS-loop
  TOCTOU the shipped pins (`TestOverrideWithNoStandingSignalIsLegalNoOp`,
  `TestOverrideResetsAcrossLosingCASAttempt`) exist to prevent; the
  flag's effect is conditional, not absent (amendment 7's completed
  retraction — see the validation record). `--idempotency-key` is
  allowed, scoped SYMMETRICALLY: rename events dedupe only against
  rename events, and field writes dedupe only against field-carrying
  events — a field write never dedupes against a rename sharing its
  (author, key, idem).
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
  (`--expect none` = never renamed) — a second, rename-scoped
  `--expect` stream alongside field-scoped CAS (amendment 4). Rule-3
  contract rows for the stream: rename `claim_lost` message =
  `event <id> by <author> already renamed '<key>' to "<title>"`, hint
  = read the current title first; `needs_override` carries `signals`
  as everywhere.
- **`title` is a RESERVED field name**: `bad_value` at
  `create`/`import`/`vocab` for `--field`/`--multi-field`/`--guard
  title`. A legal board could otherwise declare and guard `title`,
  splitting the contested read path (which unions both streams) from
  the write path (renames only) — probed as an unresolvable ticket and
  an empty `contested_resolved`.
- **Existence**: rename requires a locally existing titled key
  (`unknown_key`, hint = the seed command); ready-capable boards only.
  Fold totality, two distinct cases: (a) reachable and testable — a
  rename PRECEDING a colliding seed in fold order (two-root merge)
  titles the key, and its `prior` carries the fold-path seed; (b)
  hand-built only — a rename with NO seed anywhere in the chain still
  titles the key (totality; fixture-crafted, since sync merges whole
  chains and cannot ship a rename without its seed).
- **`prior` is fold-path history, not a complete inventory**:
  `renamed: {by, ts, id, prior[]}` lists earlier titles ON THE FOLD
  PATH, oldest first, fold-path seed included. Under a two-root
  collision the losing root's seed title appears in no rename structure
  — the read-both-heads doctrine remains the way to see a collided
  key's whole story, and the skill says so.
- **The contested title stream** (amendment 8): the write-heads
  antichain extends to the rename stream — rename events contest as
  pseudo-field `"title"`: same definition, ids fold-ordered
  winner-last, `expect` = the winner rename id (usable directly as
  `--rename --expect`), `contested_resolved` recorded on the
  collapsing rename. The pass's scope is **"ready-capable boards:
  their guarded fields, plus the rename stream"** — the title stream
  is unguardable, so a `len(Guard)==0` short-circuit no longer bounds
  it. Rationale, probed: concurrent cross-replica renames merged in
  SILENCE while the identical status race contested; live-verified
  end to end, byte-identical entries on both replicas, ticket usable
  verbatim. Priced: bounds unchanged at 5k events with 72 contests
  across BOTH streams.
- **Title contests surface in `attention` ONLY**: they do NOT set
  per-entry `contested: true` and do NOT flip `frontier` off
  `all-handled` — a cosmetic cross-replica retitle must not hold a
  fleet in the loop or drag pickers into name-the-contest doctrine
  (both probed). The title-collapse idiom is
  `set <key> --rename "<keeper>" --expect <contest.expect>` — no
  `--override` (settled never gates renames).
- **Render, mandatory labeling**: every title-bearing surface shows a
  renamed title as renamed with attribution reachable — JSON rows carry
  `renamed`; TTY title lines carry `(renamed by <author>)`; the
  identity header lists prior → now per renamed key; contested and
  stale-claim attention entries carry the current title with `renamed`
  info (they are title-bearing rows; this supersedes the sync spec's
  title clause — amendment 2). **Entry titles exist whenever the KEY
  has a title**: a statusless key with a fold-total rename renders
  that title on ALL its entries — statusless and contested alike —
  one title per key per envelope; `title` is omitted only when no
  title exists at all. The `renamed` structure is ABSENT on unrenamed
  keys. **Byte-compatibility, scoped honestly**: rename-LABELING adds
  nothing to unrenamed output, but rev 16 makes THREE deliberate
  output changes on all READY-CAPABLE boards (plain boards have no
  titles and are untouched): `ready`'s TTY blocked line gains the
  title, `status <key>`'s drill-down gains title + rename info, and
  `show --id` renders `rename:` on rename events (it is the pinned
  ticket-read path for contest heads, and rendered nothing of a
  rename on a TTY — probed) — each with its own fixture, not a
  byte-identity test. The bare `status` SPINE is ruled the other way:
  its note column IS the event message, deliberately unlabeled — the
  seed message rendering verbatim on a renamed key is the spine
  showing history, not a defect; the drill-down and every list
  surface carry the labeled title.
- **Determinism**: pure fold; the standing determinism test gains a
  renamed key in its fixture (an unbuilt production requirement, not a
  spike deliverable).
- **Scale**: the standing scale fixture contests BOTH streams at full
  strength (a single-stream fixture prices half the pass), and a
  guard-free ready-capable board is priced by its own named fixture
  (the pass's new population). Bounds hold at the shipped 350ms/140ms
  class.
- **Quickstart**: one line teaching `set --rename` (cold agents must
  not keep the immutable-title belief amendment 1 retires); the line
  budget rises 120 → 124 — the guard test's constant
  (internal/docs/docs_test.go:21) moves with this ruling, exactly as
  the sync build's 110 → 120 did. Unbuilt production requirement.

### Amendment inventory (complete)

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
   (rename events dedupe only against rename events, symmetrically).
6. **`skills/ledger-issues/SKILL.md`**: the Titles doctrine (seed `-m`
   is still the title to write well; renames are corrections with
   attribution, not a workflow) AND the Override-ethics paragraph —
   its "never to decorate (wording, titles…)" sentence is rewritten:
   the rename event is now the legitimate title correction; the
   prohibition that survives is overriding STATE for cosmetic reasons.
   The picking-loop contested paragraph, recovery paragraph, and
   worked example teach the title-collapse idiom; the "renaming is a
   human call" sentence is scoped to KEY IDENTITY (rename/split of
   colliding keys) — collapsing a title contest with its ticket is a
   picker's act.
7. **Parent tool spec, error contract** (tool rev 16): the
   `needs_override` error document carries `"signals": [...]` —
   machine-readable signal names; consumers must never parse English.
   The rev-3 `--override`-symmetry half is RETRACTED, in BOTH
   directions: `--override` with no standing signal is a legal no-op
   on EVERY write verb, rename included — retry-safe by the same
   argument everywhere (the gate dissolves; see the validation
   record's rev-3-gauntlet and rev-6 entries for the two probes).
   Consumer note for the same contract: sync/push exit-3 outcome
   documents go to stdout.
8. **Sync spec Addition 3, the contested streams**: the write-heads
   antichain extends to the rename stream — Part A's contested-title
   bullet is the definition; the pass's scope sentence changes as
   stated there.
9. **Quickstart**: the `set --rename` line; budget 120 → 124.
10. **Sync spec Addition 5**: its read-class sentence covers the
    rename gate's whole-chain read, and its single-pair
    `contested_resolved` derivation covers the `title` pseudo-field
    (a rename touches zero guarded fields; a literal reading computes
    no heads for it).

## Part B — `ledger-gh`, the bridge

### The command

One verb: `ledger-gh sync --repo <owner/repo> --ledger <slug>
[--store <path>] [--done <value>=closed] [--not-planned <value>=wontfix]
[--list-limit <n>=250]`.
Transport: the `gh` CLI, subprocess-only (v1). Board access: the
`ledger` CLI, subprocess-only.

**Report**: `{ok, repo, ledger, gh_mutations, board_writes, cursor,
divergences, suppressed_authors, actions[], warnings[]}` — `cursor` is
the PERSISTED cursor (a no-op run reports the stored one),
`divergences` counts standing refusals, `suppressed_authors` counts
outbound events skipped per `github:@*` author (poisoning is visible
even though the namespace is unenforced — author enforcement rides the
existing owner-enforcement v2 item, not a one-prefix carve-out).

**Exit contract**: 0 = report on stdout; 1 = error document on
stderr; 2 = usage. The operator's lock/cron wrapper is written
against this.

**Startup checks, all refusals naming the fix**:

- **Binary capability**: the bridge probes its `ledger` binary and
  refuses a pre-rev-16 one by name — Law 5's `signals` field fails
  closed, and against an old binary every `needs_override` would take
  the refusal path, so the operator is told to upgrade instead of
  wondering why nothing auto-overrides.
- **Vocabulary, configured for the TERMINAL pair only**: `ledger
  create` PINS the non-terminal vocabulary of a ready-capable board to
  `open`+`in-progress`; only terminals are free, so `done`/`dropped`
  is legal but `todo`/`doing` is not. The bridge reads the board's
  declared vocabulary and REFUSES a board that lacks the flags'
  values — or whose flags name a NON-TERMINAL value (declared AND
  outside `{open, in-progress}`, derivable with no new verb; the
  `--done in-progress` hole passed a membership-only check and closed
  GitHub issues on a non-terminal state, probed). The refusal names
  the failing flag, the declared vocabulary, and the fix — and the
  remedy tells the truth: a ready-capable board's status vocabulary
  is immutable (`vocab add` is refused by the tool itself, probed),
  so the fix is the board's own values as flags, or export/import to
  a re-declared board; never `vocab add`. There is no reopen flag:
  since non-terminal vocab is pinned, reopen ⇒ `open`, always.
- **Saturation**: the bridge refuses a run whose issue listing
  saturates the window (listing ≥ the limit; real ceiling limit−1 =
  249 at the default): outside the window the bulk maps are
  zero-valued, which silently disables the comment dedupe, the state
  diff, and adoption — duplicates and un-adoptable orphans, probed
  via a limit-faithful fixture. The fix is a CONSTANT, not a project
  (probed live: `gh` paginates internally — `--limit 250` returns
  250): `--list-limit` is the escape hatch so a board's Nth issue is
  never a permanent brick.
- **One board ↔ one repo**: the bridge refuses a run whose state note
  names a different repo; multi-repo bridging is v2.

**Status mapping**. Outbound: done-value ⇒ close (completed),
not-planned-value ⇒ close (not planned). Inbound: close completed ⇒
done-value with evidence `gh:<owner>/<repo>#<n>`; close NOT_PLANNED ⇒
not-planned-value with NO evidence (evidence on wontfix is
"pasted-string theater"); reopen ⇒ `open`, always.

### Identity — four pins

- **No dedicated bridge identity; no login comparison anywhere.** Any
  number of GitHub logins operate the bridge, each with their own `gh`
  auth, while the same logins participate as humans. The bridge never
  calls `gh api user`. Echo suppression is the **verified MARKER**:
  every comment the bridge posts — mirrored notes, close/reopen
  explanations, divergence notices, ALL of them; an unmarked bridge
  comment does not exist (the one unmarked comment class in the spike
  echoed back as board state attributed to a person, live) — opens
  with the marker. Inbound, a comment is bridge-authored iff it
  matches the format AND the embedded event id RESOLVES on this
  board's chain. Edges, all verified live: a pasted marker with a
  resolving id suppresses the paster's own comment (self-inflicted,
  stated); a marker with a garbage id imports normally. **The marker
  is a versioned wire format**: any future format change must keep
  every prior format recognized, or the bridge re-imports its own
  history as human comments (observed live against the round-1
  format).
- **The ORACLE's domain is `{id} ∪ {imported_from} ∪ {ids this run
  wrote}`, applied to BOTH directions**: `export`/`import` re-mints
  ids, so the inbound "is this comment mine" test AND the outbound
  "have I posted this" test both resolve `imported_from` (outbound
  blindness re-posted the bridge's whole history on recovery,
  live-caught); and the oracle is NEVER a run-start snapshot — every
  marker id the run emits joins the domain immediately, or the
  bridge's own same-run comments import as board notes (live-caught
  twice, through different doors). **The bound, honest**:
  `imported_from` is SINGLE-HOP — the tool overwrites it on each
  import — so the oracle survives exactly ONE export/import; a SECOND
  recovery has the same effect as re-bridging a fresh board and
  re-imports mirrored history (probed). Tool-backlog fix filed:
  `imported_from` becomes an ancestry list; nothing less closes it.
  A comment resolving to NO author is refused with a warning, never
  written as a bare `github:@` (the second independent guard). **The
  marker is board-scoped only by luck** — a well-formed marker from
  ANOTHER board's bridge does not resolve here and imports as if
  human (live-observed): therefore **one repo binds to one board,
  permanently** — re-bridging a repo to a fresh board re-imports
  every prior mirror, stated; a board-discriminated marker (format
  v3) is the v2 fix.
- **Cost, priced**: marker verification and Law 2's dedupe share ONE
  whole-chain read per run — it answers "does this id (or
  imported_from) resolve" AND "which idempotency keys are spent". The
  derived index is **scoped exactly as the tool's dedupe is** —
  `(author, kind, key, idempotency-key)`, never the bare key string
  (the bare-string index was a censorship primitive: one decoy note
  suppressed a real comment's import AND deleted the `deduped:true`
  impersonation detector, probed). A chain event carrying a
  `gh-comment-*` key OUTSIDE the bridge's own write shape is warned,
  and the bridge writes anyway — the tool's scoped dedupe is the
  arbiter, so the poison fails loudly instead of succeeding silently.
  A bulk ids/keys read verb is a named tool-backlog wish, not v1.
- **The board's `github-link` notes are the authority** for key↔issue,
  with these hardenings:
  - **The read is UNBOUNDED** — `notes -k github-link -n 0`; the
    default limit of 10 silently truncates the identity map at ten
    issues and mints duplicates for everything past it.
  - **One link per key, BOTH directions — the ESTABLISHED link wins**
    (oldest non-retracted; the only merge-stable choice — newest-wins
    flips when a loser's note arrives on a later sync, and cannot
    coexist with "a changed link is refused, never repointed"). An
    issue that is not the key's established link is NOT a link inbound
    either — keeping every issue ever linked as an inbound writer
    produced an unbounded flip-flop minting a fabricated override per
    run, probed.
  - **Link retraction**: a `github-bridge`-authored note
    `github: retracted issues/<n>` removes <n> from the key's
    candidate set; established = oldest non-retracted; merge-stable
    (set union, deterministic). Under append-only oldest-wins a
    duplicate was PERMANENTLY unresolvable, probed — and note the two
    bookkeeping classes have OPPOSITE tie-breaks: `github-link` reads
    oldest-wins, `bridge-state` reads newest-wins. **The cleanup
    doctrine that actually converges** (probed; the previous one did
    not): close the duplicate issue on GitHub AND write its
    retraction note — the warning clears next run. Duplicate link
    notes warn every run until retracted. The stamped-forgery bound,
    re-priced honestly: under oldest-wins a forged binding on a
    not-yet-linked key is permanent until retracted, not
    self-correcting.
  - **Intake seeds only OPEN issues** — a closed, hintless unknown
    imports nothing (previously stripping a duplicate's hint minted a
    permanent junk key, probed).
  - **Link and bridge-state notes are read author-filtered**: only
    notes authored `github-bridge` count, and a link note that
    CHANGES an existing link is a refusal-with-handoff, never a
    silent repoint — an any-author last-write-wins read let one note
    from any board writer re-point a linked key or wedge the bridge's
    state (probed both). Honesty clause: authorship is asserted, so
    `--as github-bridge` impersonation remains possible and greppable
    — the tool's stated v1 trust model, hardened by owner enforcement
    in v2; the operator runbook for a poisoned state note is a
    corrective note authored `github-bridge`.
  - The issue-body `ledger-key:` line is a HINT, honored only when
    the link note agrees. An unlinked issue claiming a LINKED key is
    warned and never intaken (the probed hijack). **Adoption**: the
    bridge writes the STAMP into every issue body it creates, and
    ADOPTS an unlinked issue only when the stamp AND the key hint are
    present AND the key has no linked issue — recovering crashed
    creates from the bulk list already in hand. A stamped forgery can
    bind a stranger's issue to a not-yet-linked key and can never
    touch a linked one — bounded, accepted, stated. The issue body is
    thereby a SECOND, independent copy of the identity map, and it —
    not the sync-first law — is what closes the unsynced-replica
    duplicate hazard (spike-verified both ways).
- **The reserved state key defends itself**: intake never mints
  `github-bridge-state` (collision-suffixed), and a link hint naming
  it is refused (probed live against a real seizure artifact).

### Bridge state

One note, kind `bridge-state`, under reserved NOTE key
`github-bridge-state` (a note key: no board key, no attention noise),
read newest-wins, author-filtered. Body: repo, cursor,
standing-refusal records. **No comment high-water marks** — deleted by
Law 2 (two reviewers independently derived the simplification; the
map was also unmergeable across replicas, being last-write-wins).

### Law 1 — ordering

(1) `ledger sync` — sync FIRST, always; failure aborts the run.
(Today this merges the WHOLE store — `sync` takes no slug selector; a
slug-selective sync, symmetric with push's privacy lever, is a named
tool-backlog item the bridge adopts when it exists.)
(2) READ the outbound DRAIN (`since <cursor>`).
(3) Intake GitHub→board, per-aspect pending suppression: a key with an
un-mirrored status/rename event is off-limits to intake for THAT
aspect — and when suppression fires AND the remote differs from the
last MIRRORED value, the bridge warns and leaves a board note: that is
a genuine concurrent GitHub edit being discarded. The comparison is
against MIRROREDVIEW — what the bridge last put there — never the
outgoing value (which flags every ordinary close as a discarded human
edit, fixture-falsified) and never a view that excludes intake writes
(which fires a false "a human's edit was discarded" accusation on
every board-side reversal of an intaken close, probed). A key with no
pre-drain history compares as a fresh open issue. Suppression notes
get the same convergence treatment as Law 3 refusals: once per
distinct divergence, then counted.
(4) Mirror board→GitHub.
(5) **Persist state when ANY of** (all four disjuncts): the run
mutated GitHub; the run wrote to the board; the drain carried any
event outbound suppression does NOT skip (an `in-progress` write is
not mirror-owned yet must advance the cursor); or the refusal-record
set changed (a run whose only change is a RESOLVED divergence must
persist, or the pruned record never lands and the divergence's next
real recurrence is silently swallowed).
(6) **`ledger push <slug>` — always, selectively, LAST.** Always:
link notes and bridge state must reach the remote or the sync-first
law protects nothing (probed). Selectively: bare `push` publishes
every local slug — the privacy lever. `partial_failure` (exit 3),
scoped per the outcomes array: sync ⇒ abort IFF the bridge's OWN slug
failed, warn on other slugs' failures (a blanket abort couples the
bridge's availability to every dead remote in the operator's store);
push ⇒ warn, retry next run. Consumer note: sync/push write the
exit-3 outcomes document to STDOUT (every other error is stderr) — a
bridge parses both streams; one sentence for the parent spec's CLI
contract.

### Law 2 — idempotence by construction, not by cursor

Crash anywhere, re-run safely; recovery CONVERGES — it may take two
or three runs to reach the 0/0 fixed point, because recovery
bookkeeping is itself events; never promise "the next run is a
no-op".

- **Intake comments**: `note -k comment --idempotency-key
  gh-comment-<rest-id>` — the REST id parsed from the comment `url`'s
  `#issuecomment-<id>` fragment (the `--json` `id` field is a GraphQL
  node id, NOT ordered — pinned so nobody re-derives it).
  Already-spent keys are skipped via the shared whole-chain read's
  DERIVED index — no per-comment subprocess, nothing stored, nothing
  to lose on a merge (without it the law costs one `ledger note`
  invocation per GitHub comment per run, forever). **`deduped: true`
  in the write response is part of the contract**: a deduped write is
  not a write — anywhere the bridge writes, refusal and divergence
  notes included — or a converged run can never report zero.
- **Mirrored comments** carry the source event id in their marker;
  before posting, the mirror checks the issue's comments for that id.
  **Per-issue comment saturation** (probed live: the bulk listing
  returns the OLDEST 100 comments per issue, newest silently missing
  — a busy issue stopped importing forever with a clean 0/0 report,
  and crash re-runs double-posted past the cap): when the bulk read
  returns exactly 100 comments for an issue, the bridge re-reads THAT
  issue completely (`gh issue view <n> --json comments`, probed
  complete at 140) before any dedupe, intake, or posting decision
  touches it. The fixture models the cap — the faithfulness law.
- **Issue creation**: the crash window is closed by ADOPTION, not
  search — the identity section's stamp rule, using the bulk list
  already in hand.
- **Handoff and suppression notes** carry idempotency keys derived
  from (issue, aspect, observed state).
- **State writes are STATE-CONVERGENT in both directions, and the
  level is the FOLD, never the drain**: before any status/rename
  write, compare the target's CURRENT value and write only on
  difference — and the value the mirror pushes is the board's current
  FOLDED state (post-intake), with the drain supplying only marker
  ids and, when the event IS the level, the message. A drain-derived
  level skips suppressed intake writes and pushed a recovery run into
  reopening a closed issue and restoring a superseded title
  (live-caught). When the current value came from intake, GitHub
  already shows it: nothing is pushed. **The mirror is a function of
  the board's current state, never of its history.** Comments and
  notes stay event-driven. Contract clause riding here: the tool's
  idempotency dedupe is LOCAL-VIEW — two replicas importing the same
  comment during a partition both write, and the merge keeps both
  (the tool's stated contract; accepted, greppable, one sentence so
  nobody reads Law 2's keys as global).
- **`reset_required` on the stored cursor**: warn and re-drain from
  empty. Safe FOR CONTENT because comments/notes are
  marker/key-idempotent and state writes are convergent; and on a
  re-drain run the divergence warning is SUPPRESSED ENTIRELY —
  MIRROREDVIEW has no meaning when the drain is the whole chain, and
  the naive fallback accused a human of edits the bridge itself had
  made, then replayed the board's whole state history at GitHub
  (probed; both closed by this bullet and the convergence rule).
- **Concurrency, stated honestly** (the "inefficient, not corrupting"
  claim was PROBED FALSE with real overlapping processes): two
  concurrent runs — cron overlap on one store, or two replicas'
  operators at once — mint PERMANENT artifacts: duplicate issues per
  key, duplicate link notes, a doubly-imported or doubly-posted
  comment. Adoption and the stamp close the re-run window, not the
  overlap window (both list before either creates), and idempotency
  keys don't cross a partition. What holds: the damage is bounded per
  overlap, nothing flip-flops, no override is ever fabricated, and
  the established link resolves to one inbound writer. **There is NO
  failure signal at run time** — both runs exit 0, ok:true; the
  first signal is the NEXT run's duplicate-link warnings, which name
  every affected key and count divergences (probed) — and that
  warning fires ONLY for overlaps that create issues: comment-shaped
  overlaps leave permanent public duplicates with NO signal on any
  run, ever (probed). Therefore **single-instance operation is an
  operating REQUIREMENT the operator must enforce** (one designated
  runner, non-overlapping cron, or flock in the invocation — the
  bridge provides no lock in v1). Cleanup of issue duplicates is the
  retraction doctrine; a board-CAS reservation scheme that shrinks
  the same-store window is v2, with its stated limit that no board
  mechanism can close the cross-replica window (that IS the
  partition).
- **Availability under flakiness, priced**: no retry, no backoff
  anywhere — one transient transport failure aborts the run. SAFE
  (verified: six flake configurations all converge exactly, with
  progress accumulating across failed runs) and UNAVAILABLE under
  sustained flakiness (at 33% per-call failure across 14 calls,
  effectively no run completes). Retry-with-backoff is backlog, with
  measured shape instead of hope.

### Law 3 — refusals converge

A refusal (human-labeled key, unresolvable divergence) is recorded in
bridge state as (issue, ASPECT, observed-state); while unchanged on
both sides it is silently skipped, counted in `divergences`. The
handoff note and the one GitHub comment ("reserved on the board; a
maintainer must apply this there") are written ONCE per distinct
(issue, aspect, observed-state) — **EVER, not per episode**: Law 2
keys the note on exactly that triple, so a recurrence dedupes by
design, and un-keying it would duplicate the note on every crash
between note and persist. What recurs afresh on a re-observation is
the COUNT, the report line, and the re-persisted record — the
original note remains the greppable record of the divergence's
content, which is identical by construction. **Record lifecycle**: a
record NOT re-observed this run is PRUNED (the state note stays
bounded); pruning is a state change for Law 1's persistence rule, and
record-set comparison is SET comparison, order-blind (walk order is
an artifact; list comparison would persist a new state note every
run, forever). Suppression notes (Law 1 step 3) share this exact
machinery. Consequence of author-suppression stated: a FORGED
bookkeeping note (human-authored github-link/bridge-state) is inert
on the board but mirrors to GitHub as an ordinary comment — the
poisoning becomes visible in two places, one public; accepted.

### Law 4 — attribution is paginated or absent

The actor for a close/reopen/rename comes from the FULL issue
timeline — `per_page=100 --paginate`, every page (a last-page-only
read misses any state event followed by a page of comments; a
single-call read finds NOTHING past 30 events — both probed). The
match is the NEWEST event of the type. "No matching event found" is
the only fallback: issue author + a warning. Cost, priced honestly:
ceil(timeline/100) calls per changed ASPECT, uncached — two aspects
changing on one busy issue pay it twice.

### Law 5 — guarded writes follow the doctrine, including its terminal exception

`--expect` from a fresh read; intake renames pass `--expect` from the
rename stream (Part A's CAS — a board rename racing an intake rename
loses loudly, not silently). On `needs_override` from
`claim`/`settled`: auto-`--override`, attributed `github:@<login>` —
a real person's decision, tool-recorded for triage. On
`needs_override` from `human`: never — Law 3's refusal path
(login↔label identity mapping is v2). On `claim_lost` for a TERMINAL
value: straight to the handoff note — "never re-close blind";
non-terminal `claim_lost`: one re-read retry, and the same rule
applies to the retry (a retry that hits a signal takes the signal's
rule). **The signal names come from the error document, not its
prose**: tool rev 16's `"signals": ["human", ...]`. **The field
FAILS CLOSED** (a reader that failed open auto-overrode the one
write Law 3 exists to prevent, against any pre-rev-16 binary,
probed): a `needs_override` whose document carries no `signals` is
UNKNOWN and takes Law 3's refusal path; the startup capability probe
is the operator's early warning.

### Law 6 — mirror fidelity

EVERY state mirror carries its message — a close mirrors as a marked
comment carrying the close message and evidence THEN the close, and a
REOPEN likewise comments its reason before reopening. **Non-terminal
status transitions mirror to NOTHING**, messages included
(claim/touch-base messages never reach GitHub); a marked-comment
mirror for them is v2. `blocked-by` edges have no GitHub
representation and are not mirrored — stated. Notes on keys with no
linked issue: at ISSUE-CREATION time the bridge backfills the key's
existing non-bookkeeping, non-GitHub-authored notes (the
statusless-seed window; the marker is what keeps backfill and drain
from double-posting), and a note whose key never gains an issue is
dropped WITH a warning naming the event id. Bridge-authored board
writes are pinned: intake events `--as github:@<login>`; bookkeeping
notes `--as github-bridge` with kinds
`bridge-state`/`github-link`/`handoff`. **Outbound suppression is by
AUTHOR, not kind**: skip events authored `github:@*` or
`github-bridge` — full stop. A kind list silently ate HUMAN `handoff`
notes — the issues spec's designated reclaim channel and the
highest-value note class on the board — while mirroring every other
kind (probed); author suppression is strictly simpler and mirrors a
human's handoff like any other note.

### Slugification, pinned

Lowercase, non-grammar characters → `-`, collapsed, 48-char truncate;
empty result → `issue-<n>`; collision → `-<n>` suffix computed
locally (two replicas intaking concurrently can still collide or
diverge — the board's own two-root machinery is the net, stated).

### Tool backlog (named, not v1)

`imported_from` as an ancestry list (closes the oracle's single-hop
bound); a bulk ids/keys read verb; slug-selective sync; a
latest-event-of-kind read; write-then-fold in one call; `ledger
notes` surfacing `imported_from` (only event documents carry it — the
bridge derives it from its whole-chain read, and a consumer without
that read cannot).

### Out of scope, named

Level 3 state sync; issue deletion/transfer (broken link at read time
⇒ warning + one-time handoff note); webhooks; pagination beyond the
issue-listing limit; rate-limit backoff (transient failures are safe
by Law 2; the measured availability shape lives in Law 2's flakiness
bullet); multi-repo boards; author namespace enforcement
(owner-enforcement v2 carries it); marker format v3
(board-discriminated); board-CAS same-store reservation; login↔label
identity mapping; non-terminal transition mirroring.

## Test plan

1. Rename fold: none/one/several; concurrent via real sync (converge,
   loser greppable); title survives claim/close; seed message never
   resurrected; fold-order-precedence case (two-root + rename, `prior`
   carries fold-path seed); hand-built no-seed totality case.
2. Rename gates and flags: human ⇒ `needs_override` ⇒
   `--override -m` lands with `override: human` and the message
   rendering as override text; claim/settled do NOT gate; `--override`
   without a signal is a LEGAL NO-OP (amendment 7; the shipped pins
   stay green unchanged); the full bad_usage matrix (`--rename` with
   `field=value`/`--evidence`; bare rename with `-m`); rename
   `--expect` CAS both ways incl. `--expect none`;
   `--idempotency-key` dedupes rename-vs-rename only, both symmetric
   cases; plain-board and unknown-key refusals; `title` reserved at
   create/import/vocab.
3. Rename ecosystem: `since`/`watch` deliver renames; rollup coverage
   counts them; export/import round-trips them; determinism fixture
   includes a renamed key; contested and stale-claim entries carry
   renamed titles; the entry-title rule (a statusless key with a
   fold-total rename titles ALL its entries); title contests in
   attention only (no per-entry flag, frontier stays all-handled) and
   the collapse idiom works verbatim; the three deliberate render
   changes have their own fixtures (no byte-identity claim);
   `signals[]` present in needs_override documents from every
   emitting verb; both-streams + guard-free scale fixtures hold the
   bounds; quickstart line + budget 124 with the guard constant
   moved.
4. Ordering: the round-1 falsification as regression (close → one run
   → one GH close, zero board writes, no fabricated attribution); the
   divergence warning fires against the last-MIRRORED value and does
   NOT fire on an ordinary close (pinned); the mirrors-to-nothing
   case (claimed key accepts a GitHub close on the next run — the
   permanent-suppression falsification, pinned); the push-hole
   regression (mirror-only run on A, synced B run ⇒ ZERO duplicate
   issues).
5. Idempotence: crash injection at every transport call site in BOTH
   modes — fail-BEFORE and fail-AFTER the effect (fail-before alone
   never creates the orphan that mints duplicates) ⇒ every replay
   CONVERGES to a 0/0 fixed point (may take 2-3 runs; never assert
   "next run is clean") with no duplicate comments, notes, or issues;
   the `deduped: true` contract (a converged run reports zero
   writes); report's `cursor` = persisted cursor on no-op runs.
6. Identity: multi-login (several operator logins across runs, humans
   commenting under the SAME logins — mirrored comments never import,
   human ones import once); the three marker edges (forged marker +
   real id ⇒ suppressed; garbage id ⇒ imports; prior format rule);
   hijack regression (unlinked issue claiming a LINKED key ⇒ warned,
   untouched); adoption (crash after create ⇒ stamped orphan adopted,
   one link, no duplicate; stamp+hint on an unlinked key adopts,
   absent stamp refuses); two-issues-one-key ⇒ established link
   wins; reserved-key seizure regression.
7. Refusal convergence: human-labeled divergence ⇒ exactly one note +
   one GH comment across N runs; `divergences` counted; the COUNT,
   report line, and re-persisted record recur on re-observation while
   the NOTE does not; cleared when either side changes.
8. Guarded intake: claim/settled auto-override lands attributed;
   terminal `claim_lost` ⇒ handoff, no retry; non-terminal ⇒ one
   retry; rename race loses loudly via rename-CAS.
9. Attribution: fixture timeline >30 events with a stale close cycle
   ⇒ newest actor found via pagination; no-event ⇒ author + warning.
10. Vocabulary: `done`/`dropped` board bridged with flags; missing
    vocab refused naming the fix (and the fix is not `vocab add`);
    `--done in-progress` refused; NOT_PLANNED maps in with no
    evidence.
11. Mirror fidelity: close AND reopen comments precede their state
    change; non-terminal transitions mirror to nothing; issue-creation
    backfills pre-link notes exactly once (the marker keeps backfill
    and drain apart); never-linked note drop warns; BRIDGE-authored
    notes never mirror while a HUMAN `handoff` note DOES (author
    suppression, not kind); `suppressed_authors` counts a poisoned
    `--as github:@x` event; `signals: [...]` present in
    `needs_override` documents and FAILS CLOSED (a signals-less
    document takes the refusal path); the startup capability probe
    refuses a pre-rev-16 binary.
12. Regressions from the rev-3 gauntlet, each from a probe: marker
    oracle resolves `imported_from` (export/import the board, re-run,
    ZERO re-imports); derived index scoped (the decoy note fails
    loudly — comment imports, warning emitted); duplicate links
    (both-linked fixture converges to 0/0 with one inbound writer and
    a warning, no flip-flop, no fabricated overrides); state
    convergence (intake close on a closed key writes nothing;
    re-drain run fires no divergence notes and replays no state
    mutations); link/state notes author-filtered (a mallory
    link/state note is inert + warned; a changed link is
    refusal-with-handoff); saturation refusal at exactly the limit;
    sync partial_failure on a foreign slug warns while own-slug
    failure aborts; refusal-record pruning persists (cleared
    divergence's record lands, next occurrence notes afresh);
    `reset_required` re-drain, one-board-one-repo refusal,
    sync-failure abort, and deleted/transferred-issue warning each
    get their named item.
13. Regressions from spike 3, each from a finding: the oracle covers
    both directions AND same-run writes (warm-cache own-comment
    fixture; export/import outbound re-post fixture); author-less
    comments refused; oldest-wins link tie-break under merge (a
    loser's note arriving by later sync does not flip); link
    retraction (retracted duplicate clears the warning next run;
    closed hintless unknown imports nothing); fold-not-drain level
    trigger (the export/import recovery neither reopens nor
    retitles); per-issue comment saturation (a 100-comment issue is
    re-read completely; the fixture models the cap); `deduped` never
    counts as a write on refusal paths; refusal-set comparison is
    order-blind; the unbounded link-map read (an 11th linked issue
    does not mint a duplicate); concurrency probe kept as a
    REGRESSION (overlapping processes: bounded duplicates, no
    flip-flop, no fabricated override, next-run warnings name every
    key); flake-storm convergence with accumulating progress; the
    re-bridged-repo re-import hazard pinned as documented behavior.
    FIXTURE FAITHFULNESS is itself a test-plan law: the fake
    transport honors `--limit`, models the per-issue comment cap,
    and serializes calls like the real API (an unfaithful fixture
    proved a refusal could fire while hiding what it protects
    against, and would have measured its own race in the concurrency
    probe).
14. Bridge tests run against a FIXTURE transport; ONE live acceptance
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

- **Spike** (branch `spike/bridge`): all five trial steps green live;
  falsified intake-first ordering and unconditional state persistence;
  pinned rename-as-set-field, note-key state, kind-based suppression,
  doctrine-path writes, timeline attribution. Rev 1 was written from
  it, overriding six spike behaviors deliberately.
- **Rev 1 gauntlet** (two reviewers, stateful fixture transport + live
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
- **Spike round 2** (same branch; six laws + the multi-login ruling;
  28 findings; 16-injection crash sweep in both fail modes, all
  converging; live trial green including crash-resume with injected
  failures against the real API, a >30-event pagination proof — the
  single-call read finds NOTHING, not merely a stale actor — and the
  three marker edges under one login). Fixture-falsified rev-2
  sentences, corrected in rev 3: the divergence comparison (outgoing →
  last-mirrored), the persistence rule (or-drain-carried-mirrorable),
  "next run is a no-op" (→ converges). Live-falsified: an unmarked
  bridge comment (the Law-3 notice) echoed back as board state —
  hence "an unmarked bridge comment does not exist." Jesse's ruling
  applied: dedicated identity deleted; verified marker; multi-login
  pinned by test. Spike recommendations adopted: derived idempotency
  index off the shared whole-chain read; stamp-based adoption over
  search; `deduped: true` as contract; `signals: [...]` in the error
  document; reopen messages; terminal-only vocabulary flags; both
  crash-injection modes. Deliberately re-litigable at the next
  gauntlet: the stamped-forgery adoption bound, the `--override`
  bad_usage symmetry on `set`, and the whole-store sync cost.
- **Rev 3 gauntlet** (two reviewers, both writing adversarial tests
  against the spike's fixture transport; 10 findings each, a tie on
  count, H1 on severity weight; H2's best probe IMPLEMENTED the
  override amendment and broke five shipped tests). All three
  re-litigable items resolved: the override symmetry RETRACTED
  (dissolving-signal TOCTOU; two load-bearing pins red), the
  whole-store sync abort scoped to the bridge's own slug, the
  adoption bound upheld but its "can never touch a linked one" claim
  falsified by the CHEAPER board-side path (any-author link notes) —
  now author-filtered with change-refusal. Foundations cracked and
  repaired in rev 4: marker oracle blind across export/import (domain
  grew `imported_from`); derived index scope-blind (a decoy note =
  silent censorship + deletion of the deduped:true detector — now
  tool-scoped with loud failure); duplicate links made both issues
  inbound writers (unbounded flip-flop, fabricated override per run —
  one-link-per-key both directions); intake state writes re-fired
  attributed overrides forever (state convergence promoted to Law 2);
  re-drain manufactured false human-edit accusations and replayed
  state history (suppressed + convergent); kind suppression ate human
  handoff notes (author suppression); rename races merged silently
  while status races contested (title stream added — amendment 8);
  saturation blindness past the listing limit (loud refusal);
  terminality unchecked (`--done in-progress` hole); Law 4's price
  understated and its rel=last alternative wrong (full paginate,
  honest ceil(N/100)); persistence rule inverted under its own
  example and dropped the refusal-set disjunct + pruning (all four
  disjuncts + lifecycle); quickstart amendment missing (entry 9,
  budget 120 → 124).
- **Spike round 3, the conservative pass** (all ten rev-4 deltas
  built with regressions; 28-injection sweep across every rev-4
  transport shape; full live trial re-run + three new live steps
  incl. the two-replica rename contest resolved with its ticket
  verbatim and export/import recovery; scale bounds re-measured at 72
  contests across both streams, 106-110ms vs 350ms). Live-only
  catches, folded into rev 5: the run-start-snapshot oracle imported
  the bridge's own divergence comment under an empty login (round 2's
  finding through a new door — oracle now includes same-run writes,
  author-less comments refused); the drain-derived level trigger
  reopened a closed issue and restored a superseded title on recovery
  (the mirror is a function of current state, never history).
  Falsified: "inefficient, not corrupting" (permanent duplicates,
  zero run-time signal, both overlap shapes probed with real
  processes) — restated as an honest operating requirement with
  next-run detection; "notes afresh" (unachievable against Law 2's
  keying — narrowed to the count); rev 4's link tie-break
  self-contradiction (oldest-wins, the merge-stable reading).
  Verified: flake-storm safety with accumulating progress (and the
  no-retry availability cost priced); the signals[] field with
  prose-fallback pinned OUT; the title stream live end to end.
  Standing hazard newly dimensioned: markers are not board-scoped —
  one repo binds to one board permanently, or a prior board's mirrors
  re-import (format v3 is the v2 fix).
- **Rev 5 gauntlet, three reviewers** (16/10/9 findings; J3 took the
  round on count; five Criticals, all probed; NOT clarity-grade, so
  the build gate escalated to Jesse per the standing conservative
  instruction). The five: (1) the amendment-7 retraction rationale
  was FALSE — rev 5 claimed rename's human gate "cannot dissolve,"
  but `human` is an unguarded labels token any writer or sync merge
  clears, making the mid-CAS-loop TOCTOU exactly the killer
  construction the retraction condemned — so rename keeps `set`'s
  no-op symmetry (amendment 7 completed in both directions); (2)
  `signals[]` failed OPEN — an empty read auto-overrode human stop
  signs against any pre-rev-16 binary (now fails closed + startup
  capability probe); (3) `gh`'s bulk listing returns the OLDEST 100
  comments per issue, so busy issues silently stopped importing and
  double-posted on crash (per-issue saturation re-read); (4) the
  oracle survives exactly ONE export/import — `imported_from` is
  single-hop (bound stated; ancestry-list fix filed); (5)
  `--reopened` was simultaneously mandated and denied (flag deleted;
  reopen ⇒ open, always). Also folded: title reserved as a field
  name; title contests attention-only with the collapse idiom;
  entry-title rule; link retraction + converging cleanup +
  open-issues-only seeding; comment-only overlaps stated
  undetectable; unbounded link-map read; exit contract;
  non-terminal-mirrors-nothing; vocabulary-refusal honesty; the
  vocabulary block; scale-fixture and test-plan repairs; validation
  honesty on two unbuilt claims (quickstart teaching, determinism
  fixture — carried as production requirements in Part A). Rev 6
  applied every ruling; rev 7 is the agreed structural rewrite of
  the same content.
