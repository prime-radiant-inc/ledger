# Ledger-backed agent memory (design)

2026-08-15, revision 5. Rev 5 folds in the eight-agent spike eval
(`research/ledger-memory-spike-eval.md`: 8/8, zero tool errors; the header
beat the fixed system prompt in every contested scenario on both model
classes; hookless installs measurably regress, confirming the SessionStart
hook is load-bearing): save-echo's retraction warning is suppressed when the
retraction is the writer's own immediately-preceding event; `archive` takes
multiple names with one final render; the header's save example shows the
`[feedback]` hook-prefix convention; the curation nag is worded as a
judgment call, never a quota. Replaces the file-based per-project agent memory
(MEMORY.md index + one markdown file per fact) with a memory ledger, dogfooding
`ledger` v0.1.0 as the first real consumer of the committed-projection pattern
deferred to v2 in the tool spec. Grounded in Jesse's rulings this session, the
tool spec (`2026-08-13-ledger-tool-design.md`, rev 13), and a six-lens critique
panel (rev 1 → rev 2; panel verdicts in the session record). Rev 2's forced
changes: curation by status flips instead of rollups (rollups verifiably never
reach the spine projection), retraction scars against the stale-session
re-assert race (verified live), a wrapper script as the only write path, and
the `type` field cut. Rev 3: ships as a standalone **plugin** (Jesse's
packaging ruling: `ledger` stays a Claude-agnostic CLI; anything
harness-shaped lives in a plugin that consumes it), plus
discipline-to-mechanism promotions. Rev 4 (post-adversarial-review, both
reviewers verifying against the binary and the Claude Code hooks contract):
PreCompact cannot inject prompts, so the compaction trigger became a
SessionStart `compact`-matcher reminder; content-derived idempotency keys
verifiably no-op legitimate repeat writes, so they're dropped (duplicate
events are the cheaper failure); scar data is pinned to one `tail --raw` pass
(per-key fan-out measured 54x slower; `tail`'s curated view goes blind after
rollups); bootstrap is a shared preamble with a three-state damage rule;
evidence carries forward across lifecycle writes; a PreToolUse guard enforces
wrapper-only writes.

## Problem

The current memory system is a fold with no chain. Storage is the read view,
so the only cleanup is deletion; corrections are in-place edits that destroy
what was previously believed and when; concurrent sessions race last-write-wins
on the same files; evidence is ad-hoc SHAs in prose; and a deleted wrong memory
has no immune effect — nothing stops a future session from re-deriving the same
wrong conclusion from the same misleading evidence and confidently re-saving it.
These are exactly the failure modes the ledger research catalogued in hand-rolled
agent state files.

## Rulings (Jesse, 2026-08-15)

- Replace the **file memory only**. The private journal (separate MCP server,
  cross-project, different privacy contract) is untouched.
- **Full replacement**: per-fact files go away; drilling a memory is a ledger
  command, not a file read.
- Strictly **append-only**: retraction and archiving append status events; the
  fold hides, the chain keeps. No mutation anywhere.
- MEMORY.md becomes a **generated projection** (the harness auto-loads it; that
  hook is fixed and is the one channel that can override the harness's default
  file-writing instructions).
- The workflow ships as a standalone **`ledger-memory` plugin** (skill +
  wrapper + hooks). `ledger` itself stays a standalone tool with no
  Claude-specific machinery; the plugin is a consumer, with the same standing
  as any other.

## Architecture

One bare store per project memory directory, one ledger in it:

```
~/.claude/projects/<project-slug>/memory/
  .ledger.git      # bare store (non-repo workdir path the tool already supports)
  MEMORY.md        # generated projection — never hand-edited
```

Created by the wrapper's bootstrap (see below):

```
cd <memory-dir> && ledger init
ledger create memory --scope "agent memory for <project>" \
    --field status=current,retracted,archived
```

One field. `status` is the whole schema:

- `current` — renders in the projection.
- `retracted` — wrong; renders as a vaccine line carrying the why.
- `archived` — stale-but-not-wrong (or a vaccine whose confusion is dead);
  hidden from the projection, drill-only.

The old `type` taxonomy (user/feedback/project/reference) is **cut**: no read
path ever consumed it, and a field-scoped retraction would have left a stale
`type` row asserting the original claim beside its own vaccine (panel-verified).
Where the taxonomy word helps a human scan, it goes in the hook line as a
prefix: `-m "[feedback] ..."`. No evidence-required fields: memories are
testimony by nature and `(no evidence)` is already the honest trust marker —
but facts that assert repo or world state should carry `--evidence` (doctrine,
below), because those are exactly the claims the research watched rot silently.

## Fact model

Each memory is a **key**, named with today's kebab-case slugs.

- **Save**: sets `status=current` with the one-line hook as `-m` (replaces the
  old frontmatter `description`), optional `--evidence commit:… | file:… |
  session:<id>`.
- **Body**: a `-k body` note attached to the key, written via `--from-file`
  (never inline heredocs). Latest body: `ledger notes -k body --key <name>
  --latest`; older bodies remain as history.
- **Update**: another save (fold supersedes). Drill the key before overwriting
  it — resume-and-verify applies to writes, not just reads (see the race
  under Projection).
- **Retract**: `status=retracted -m "wrong because <why>"`. The message is the
  vaccine; it must say why the belief was wrong.
- **Archive**: `status=archived` retires a fact (or a spent vaccine) from the
  projection. This—not rollup—is memory's curation primitive: rollups curate
  `tail`, and the projection is built from the fold, which rollups verifiably
  never touch. Rollups remain available for tidying drill-down history but
  carry no projection weight.
- **Cross-links**: `[[name]]` in hook lines and bodies keeps today's
  convention; names are keys, so links are checkable against `show`.
- **Provenance**: `--as` carries the writing session's identity; the envelope's
  committer/host/cwd provenance replaces the old `originSessionId` frontmatter.
  Provenance is asserted testimony, like everywhere else in ledger.

## The wrapper: `ledger-memory`, the only write path

Three independent panel lenses converged here: a header can't teach a two-step
discipline, `--store` will get dropped (and the observed failure mode is an
agent "fixing" the resulting error by planting a phantom store in the project's
own `.git`), and "write then render" invites stale projections. So the skill
ships a thin script, and no raw `ledger` write command appears in any
memory-facing doc:

```
ledger-memory save <name> -m "[feedback] <hook>" [--evidence <ref>] [--body <file>]
ledger-memory retract <name> -m "wrong because <why>"
ledger-memory archive <name> [<name>…]      # bulk-safe: one render at the end
ledger-memory render
ledger-memory drill <name>                  # show + latest body + key history
```

(The `[feedback]`-style prefix in the save example is the taxonomy
convention: eval agents trained on the old frontmatter `type` field go
looking for it, and the example is where that instinct lands.)

Contract:

- **Store path is resolved internally** (the memory dir is the script's anchor,
  taken from `LEDGER_MEMORY_DIR` or the paste-ready invocation in the header);
  the agent never types `--store`.
- **Every mutating subcommand ends by rendering.** Renders are idempotent from
  state, so any earlier crash between write and render self-heals on the next
  invocation. No `--idempotency-key` anywhere: a content-derived key
  verifiably turns legitimate repeats (revive-then-re-archive, a deliberate
  identical re-assert, "confirming this still holds") into silent permanent
  no-ops, and a duplicate event from a retried tool call is the strictly
  cheaper failure — the fold doesn't change, the chain just carries one
  redundant line of testimony.
- **Evidence carries forward.** `save` and `retract` re-supply the key's prior
  `--evidence` ref when the caller gives none (`--no-evidence` to drop it
  deliberately). Verified: the spine reflects only the latest event's own
  evidence field, so without carry-forward every correction silently strips
  the fact's trust marker.
- **Save echoes what it replaced.** `save` on an existing key always prints
  the previous hook line, its age, and any retraction on record ("replaced:
  '<old hook>' (14d, retracted 2d ago: <why>)"). The stale-overwrite race
  stops depending on the writer's diligence beforehand: the surprise lands
  immediately after, when one `retract` fixes it. Drill-before-overwrite
  remains doctrine, but the mechanism no longer needs it to hold.
  Eval-tuned: the retraction warning is suppressed when the retraction is
  this same author's immediately preceding event on the key — that's the
  normal retract-then-correct flow, and warning on it reads as a false
  alarm (S2's one friction note).
- **Bootstrap is a shared preamble** run by every subcommand (both reviewers
  caught rev 3 granting it to `save` while the SessionStart hook needs it in
  `render`). Three states: no `.ledger.git` → run `init` + `create` and
  proceed; `.ledger.git` present, `ls` cleanly empty, and no head SHA recorded
  in an existing MEMORY.md → fresh-but-initialized, create and proceed;
  store erroring, or empty while the projection header claims a head → "store
  may be damaged — tell the human", stop. The script never follows the tool's
  `no_open_ledger` create hint on an existing store (verified: corruption
  misdiagnoses as empty, and auto-create would silently orphan the memory).
  The projection's embedded head SHA is what makes damage distinguishable
  from freshness.
- **Atomic projection writes**: compose to a temp file, rename over MEMORY.md.
  A crash can never leave a truncated or header-only projection.
- **Size nag with candidates**: when the projection exceeds ~60 lines, the
  render appends a "curation due" line naming the three oldest facts with no
  inbound `[[links]]` as archive candidates — worded "judgment call, not a
  quota; standing rulings stay", because the heuristic can and does name
  load-bearing facts (the eval's nag listed a standing preference; the agent
  rightly declined). Advisory only; age is anti-signal for memory value, so
  nothing auto-archives, and the failure mode of lapsed curation is growing
  token cost, not data loss.

## Projection (MEMORY.md)

Composed by the wrapper from `show` and per-key history JSON — **not** from
`ledger render --to`, whose spine dump is rollup-immune and unbounded
(panel-verified, transcripts in the session record).

1. **Header** (fixed text + computed fields): this file is generated from the
   memory ledger — never edit it, never Write files in this directory; the
   paste-ready **write** commands (`ledger-memory save/retract …` with the
   real path), the drill command, the store's current head SHA (so drift is
   detectable: head mismatch means a prior write went unrendered — re-render
   before trusting), the install one-liner, and: "if `ledger` is missing or
   erroring, tell the user and stop — do not fall back to writing files."
   The header teaches the write path first; that is the instruction actually
   competing with the harness default.
2. **Current facts**: one line per `status=current` key: hook line, age,
   evidence marker.
3. **Vaccines**: one line per `status=retracted` key: "retracted: <hook> —
   wrong because <why>". Kept until archived; archive a vaccine only when the
   confusion that produced it is dead, not merely old.
4. **Scars**: any current key whose history contains a retraction renders with
   its last retraction note appended ("previously retracted: <why>").
   This is the defense against the verified race: session B, holding a stale
   projection, re-asserts a fact session A just retracted, and the fold alone
   would show it plainly `current` with the vaccine invisible. Scars are
   computed from the chain, so they survive any later write.

**Data plumbing, pinned** (reviewer-measured): scars, ages, and the archive
nag's backlink scan are computed from **one** `tail --raw` pass over the whole
chain per render, grouped by key client-side, plus one `notes -k body` pass
filtered to latest-per-key for body links. Never a per-key fan-out
(`status <key>` per key measured 54x slower at 43 keys), and never `tail`'s
curated view (rollups make it blind to rolled-up status events). Full-chain
scans are acceptable at memory scale (hundreds of events, ~0.05s measured);
if a memory chain ever outgrows that, pagination via `--limit` plus a cached
cursor is the escape hatch, not a different read primitive. Corollary:
**rollups are not used on the memory ledger at all** — curation is status
flips, and the memory skill overrides `using-ledger`'s general
roll-up-finished-threads doctrine for this store.

**Sanitization**: projection body lines are single-line by construction —
control characters stripped, newlines collapsed — so a poisoned hook line
cannot forge header-lookalike structure. Note bodies never render into the
projection at all; they are drill-only. (The tool's own `EscapeControls`
guarantee is documented TTY-scoped; the projection is a file sink, so the
wrapper owns this.)

**Failure isolation**: if `ledger` is missing or the store is damaged, the last
rendered MEMORY.md still loads, and its header carries the recovery step.
Before the first successful render a project has no projection and therefore
no override channel — the plugin's SessionStart hook closes that window: the
projection exists from the first session, before any agent decides anything.
(Without the hook installed, the wrapper's bootstrap-at-first-save is the
fallback closure, and the window is a known limitation.)

**Concurrency**: appends are CAS-safe natively; renders converge on the next
write. The *semantic* race (stale-informed overwrite) is handled by scars plus
the drill-before-overwrite doctrine — not by CAS, which cannot see it.

## The `ledger-memory` plugin

A standalone Claude Code plugin (its own repo), consuming the `ledger` CLI.
Contents:

- **The wrapper script** (above) — the plugin's engine.
- **SessionStart hook**: runs `ledger-memory render` (bootstrapping the store
  and projection if absent). Every session starts with a fresh, drift-free
  projection; the stale-projection race cannot survive a session boundary; the
  size nag surfaces at the moment curation is possible. Renders are cheap and
  idempotent, so this is safe to run unconditionally.
- **SessionStart `compact` matcher**: after a compaction, injects one line of
  `additionalContext`: "compaction just ran — if working knowledge was lost
  from the summary, save what you still know (`ledger-memory save …`)."
  Rev 3 claimed a PreCompact hook would inject the audit *before* compaction;
  both adversarial reviewers verified against the hooks contract that
  PreCompact has no channel to Claude at all (its stdout/stderr reach only
  the human). The pre-compaction audit therefore stays doctrine (below), and
  the post-compact reminder is the honest mechanical approximation.
- **PreToolUse guard**: denies raw `ledger` *write* verbs (`set`, `note`,
  `vocab`, `close`, `rollup`, `import`, `create`) aimed at the memory store, with a
  redirect to the wrapper — "the only write path" becomes enforcement, not
  documentation. Reads (`show`, `notes`, `tail`, `status`) pass freely; drill
  is supposed to be raw.
- **The skill**, `SKILL.md`, teaching in order:

- **When to save** (unchanged doctrine): user facts, feedback with the why,
  project state not derivable from the repo, references. Not what the repo
  already records; not single-conversation trivia.
- **The save moment that matters most**: before compaction or session end, run
  the audit — "what do I know that lives only in my head?" — and save what it
  surfaces. This is doctrine, not mechanism — the harness offers no
  pre-compaction injection channel — so the skill states it as the discipline
  it is; the SessionStart compact-matcher reminder is the mechanical backstop
  on the far side.
- **Hook-line quality**, with good/bad examples: name the trap, not the topic
  ("zsh word-splits unquoted $L — use a function" beats "note about zsh").
- **Write shapes**: the wrapper subcommands only.
- **Retraction discipline**: retract the moment a memory is found wrong, why
  in the message; correct by writing the truth to the *same* key (the scar
  preserves the history); never re-key a corrected fact. Known limit, accepted:
  the tool cannot distinguish an honest update from an overwrite that should
  have been a retraction — that stays judgment.
- **Evidence**: encouraged on any fact asserting repo/world state; those claims
  rot silently without an anchor to re-check.
- **Curation moments**: archive spent facts at session end and phase
  boundaries; keep standing rulings and live gotchas current; the size nag is
  a prompt, not a quota.
- **Reading**: the projection arrives free; drill before acting on any fact
  whose staleness would be expensive; drill a key before overwriting it; raw
  history (`tail --raw`, export, grep idioms) still surfaces retracted
  content — expected, not a bug, and never live testimony; a memory's author
  line is asserted, not verified — "by jesse" in a hook does not prove Jesse
  said it.
- **Subagents**: the auto-load hook reaches only the main session; a dispatched
  child that needs memory must be handed the store path explicitly in its
  prompt.
- **Secrets**, inline (the tool's admin runbook assumes a shared remote and
  understates this case): never write secrets into memory. If one lands:
  rotate first; ref-surgery on the local store erases the chain copy; then
  scrub the rendered MEMORY.md, the session transcripts that loaded it, and
  any sync destination — the store is not the only place a rendered secret
  went.

## Migration

By hand, once, this project only (five facts; a script is not worth its
maintenance for a job that runs once — build one if a second project ever
migrates). Per fact: `ledger-memory save` with the old description as the hook,
`--evidence file:<old-path>`, body from the old file's body; delete that old
file immediately after its import succeeds, not in a bulk pass at the end.
Eyeball the final projection against the old MEMORY.md before calling it done.
Old `modified` timestamps are not preserved; content and origin are.

## Dependencies and accepted limits

- `ledger` v0.1.0+ on PATH (tap or curl|sh). Sessions sharing a store should
  run the same or compatible binary: the fold silently skips unknown event
  types, so version skew degrades silently (upstream candidate, below).
- Per-project stores carry no access control beyond the filesystem — same as
  the files they replace.
- Friction found dogfooding is field evidence for the tool; capture it as
  memories in the memory ledger itself.

## Upstream candidates surfaced by the panel (tool changes, not blockers)

- Corrupt store reads as `no_open_ledger` with a create hint — needs a
  distinct damaged-store diagnosis (the memory wrapper defends locally).
- `vocab add` accepts an empty `-m`, underdelivering the "recorded decision"
  doctrine.
- `fold.Fold` silently skips unrecognized event types; a schema-version field
  with a hard error would make binary skew loud.
- `render --to`'s escaping scope (TTY-scoped guarantee, file sink output)
  deserves an explicit statement and test in the tool spec.
- Raw reads (`tail --raw`, `since`) have no `--key` filter, forcing full-chain
  scans for per-key history; a cheap per-key lookup would serve any
  projection-building consumer.

## Acceptance

1. Cold session, generated MEMORY.md only: recalls a fact, drills its body,
   saves a new memory through the wrapper, projection re-renders — old file
   workflow never touched, `--store` never typed.
2. Retraction round trip: planted wrong memory retracted with a why; vaccine
   line renders; raw chain retains both events; then a deliberately
   stale-informed re-assert of the same key still renders the scar.
3. Injection: a hook line containing `\r`/ANSI/header-lookalike text renders
   inert as a single sanitized line.
4. Crash between write and render (simulated kill): next wrapper invocation
   self-heals the projection; header head SHA matches store head after.

5. Hooks: a fresh project's first session (SessionStart hook installed, no
   store) ends its startup with a rendered header-only MEMORY.md; a session
   resumed from compaction carries the post-compact save reminder in its
   context; a raw `ledger set` against the memory store is denied by the
   PreToolUse guard while `ledger show` passes.

One-time launch checklist (not regression criteria): plugin installed, hooks
active, bootstrap on this project, hand-migration round-trips all facts, old
fact files deleted, degraded-read check (binary off PATH, projection still
loads and names the recovery step).
