# Ledger-backed agent memory (design)

2026-08-15, revision 2. Replaces the file-based per-project agent memory
(MEMORY.md index + one markdown file per fact) with a memory ledger, dogfooding
`ledger` v0.1.0 as the first real consumer of the committed-projection pattern
deferred to v2 in the tool spec. Grounded in Jesse's rulings this session, the
tool spec (`2026-08-13-ledger-tool-design.md`, rev 13), and a six-lens critique
panel (rev 1 → rev 2; panel verdicts in the session record). Rev 2's forced
changes: curation by status flips instead of rollups (rollups verifiably never
reach the spine projection), retraction scars against the stale-session
re-assert race (verified live), a wrapper script as the only write path, and
the `type` field cut.

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
- The workflow ships as a new **`ledger-memory` skill**.

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
ledger-memory save <name> -m "<hook>" [--evidence <ref>] [--body <file>]
ledger-memory retract <name> -m "wrong because <why>"
ledger-memory archive <name>
ledger-memory render
ledger-memory drill <name>          # show + latest body + key history
```

Contract:

- **Store path is resolved internally** (the memory dir is the script's anchor,
  taken from `LEDGER_MEMORY_DIR` or the paste-ready invocation in the header);
  the agent never types `--store`.
- **Every mutating subcommand ends by rendering.** Renders are idempotent from
  state, so any earlier crash between write and render self-heals on the next
  invocation. Every ledger write uses an `--idempotency-key` derived from
  (subcommand, key, content) so a retried tool call cannot double-append.
- **Bootstrap**: if the memory dir has no `.ledger.git`, `save` runs
  `init` + `create` first, then proceeds. If `.ledger.git` **exists** but reads
  come back empty or failing, the script stops with "store may be damaged —
  tell the human"; it never follows the tool's `no_open_ledger` create hint
  (panel-verified: a corrupt store misdiagnoses as empty, and auto-create
  would silently orphan the entire memory).
- **Atomic projection writes**: compose to a temp file, rename over MEMORY.md.
  A crash can never leave a truncated or header-only projection.
- **Size nag**: when the projection exceeds ~60 lines, the render appends a
  visible "curation due" line. Advisory only; there is no hard backstop, and
  the failure mode of lapsed curation is growing token cost, not data loss.

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

**Sanitization**: projection body lines are single-line by construction —
control characters stripped, newlines collapsed — so a poisoned hook line
cannot forge header-lookalike structure. Note bodies never render into the
projection at all; they are drill-only. (The tool's own `EscapeControls`
guarantee is documented TTY-scoped; the projection is a file sink, so the
wrapper owns this.)

**Failure isolation**: if `ledger` is missing or the store is damaged, the last
rendered MEMORY.md still loads, and its header carries the recovery step.
Before the first successful render a project has no projection and therefore
no override channel — that bootstrap window is a known, accepted limitation;
the wrapper's bootstrap closes it at first save, and a harness SessionStart
hook (Jesse's config, out of this spec's scope) is the robust closure if the
window proves costly in practice.

**Concurrency**: appends are CAS-safe natively; renders converge on the next
write. The *semantic* race (stale-informed overwrite) is handled by scars plus
the drill-before-overwrite doctrine — not by CAS, which cannot see it.

## The `ledger-memory` skill

`skills/ledger-memory/SKILL.md` + the wrapper script. Teaches, in order:

- **When to save** (unchanged doctrine): user facts, feedback with the why,
  project state not derivable from the repo, references. Not what the repo
  already records; not single-conversation trivia.
- **The save moment that matters most**: before compaction or session end, run
  the audit — "what do I know that lives only in my head?" — and save what it
  surfaces. (The research's strongest-evidenced trigger; today it depends on
  the human asking.)
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

One-time launch checklist (not regression criteria): bootstrap on this
project, hand-migration round-trips all facts, old fact files deleted,
degraded-read check (binary off PATH, projection still loads and names the
recovery step).
