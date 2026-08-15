# Ledger-backed agent memory (design)

2026-08-15, revision 1. Replaces the file-based per-project agent memory
(MEMORY.md index + one markdown file per fact) with a memory ledger, dogfooding
`ledger` v0.1.0 as the first real consumer of the committed-projection pattern
deferred to v2 in the tool spec. Grounded in Jesse's rulings this session and
the tool spec (`2026-08-13-ledger-tool-design.md`, rev 13).

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
- Strictly **append-only**: a retraction appends a `status=retracted` event;
  the fold hides the fact, the chain keeps it. No mutation anywhere.
- MEMORY.md becomes a **generated projection** (the harness auto-loads it;
  that hook is fixed and is the one channel that can override the harness's
  default file-writing instructions).
- The workflow ships as a new **`ledger-memory` skill**.

## Architecture

One bare store per project memory directory, one ledger in it:

```
~/.claude/projects/<project-slug>/memory/
  .ledger.git      # bare store (non-repo workdir path the tool already supports)
  MEMORY.md        # generated projection — never hand-edited
```

Created once per project:

```
cd <memory-dir> && ledger init
ledger create memory --scope "agent memory for <project>" \
    --field type=user,feedback,project,reference \
    --field status=current,retracted
```

`type` carries today's memory taxonomy (closed vocab; extension is self-serve
`ledger vocab add` and is itself a recorded decision). `status` is the
retraction axis. No evidence-required fields: memory entries are testimony by
nature, and `(no evidence)` is already the honest trust marker.

## Fact model

Each memory is a **key**, named with today's kebab-case slugs.

- **Save**: `ledger set <name> type=project status=current -m "<one-line hook>"
  [--evidence commit:… | file:… | session:<id>]`. The `-m` line is what the
  projection shows — it replaces the old frontmatter `description`.
- **Body**: `ledger note -k body --key <name> --from-file <tmp>` for anything
  longer than the hook line. Written via a temp file, never inline heredocs
  (the quickstart's `--from-file` doctrine). The latest body is
  `ledger notes -k body --key <name> --latest`; older bodies remain as history.
- **Update**: another `set` (fold supersedes) and/or a new body note.
- **Retract**: `ledger set <name> status=retracted -m "wrong because <why>"`.
  The message is the vaccine: it must say why the belief was wrong, so the
  projection line inoculates future sessions against re-deriving it.
- **Cross-links**: `[[name]]` in hook lines and bodies keeps today's link
  convention; names are keys now, so a link is checkable against `ledger show`.
- **Provenance**: `--as` defaults to the writing harness's session identity
  (e.g. `--as session-<8-char-id>`); the envelope's committer/host/cwd
  provenance replaces the old `originSessionId` frontmatter for free.

## Projection (MEMORY.md)

Regenerated after **every** memory write — a write isn't finished until the
projection is. Composition, by the skill's `render-memory` script:

1. A fixed header, the behavioral override: this file is generated from the
   memory ledger; never edit it or Write files here; the exact drill commands
   (`ledger --store <abs-store-path> show`, `… notes -k body --key <name>
   --latest`); the install one-liner in case `ledger` is missing from PATH.
2. The body: `ledger render --to` output (deterministic spine + recent notes —
   byte-stable for unchanged state, so regeneration is idempotent and
   diff-friendly).

Current facts render as spine rows (hook line visible). Retracted facts stay
visible as vaccine rows while the misleading evidence that produced them is
still plausible, then get rolled up — the same keep-loose-vs-roll judgment the
`using-ledger` skill teaches; `rollup_due` is a count, not a quota. The
projection must stay a screenful; curation, not truncation, keeps it there.

Failure isolation: if `ledger` is missing or the store is damaged, the last
rendered MEMORY.md still loads and still names the recovery step. Memory
degrades to read-only, never to gone.

Concurrency: appends are CAS-safe natively. Two sessions rendering
concurrently both produce a projection derived from some recent fold; because
rendering is idempotent from state, the next write's render converges. Benign.

## The `ledger-memory` skill

`skills/ledger-memory/SKILL.md` + `render-memory` script. Teaches, in order:

- **When to save** (unchanged from today's doctrine): user facts, feedback
  with the why, project state not derivable from the repo, references. Not
  things the repo/git already records; not single-conversation trivia.
- **The write shapes** above, each as a paste-ready command, ending with:
  run `render-memory` — a memory write without a render is an unfinished write.
- **Retraction discipline**: retract the moment a memory is found wrong,
  with the why in the message; never re-save a corrected fact under a new
  key when a `set` on the old key is the truth-preserving move.
- **Curation moments**: roll up finished threads at session end and at
  project-phase boundaries; keep standing rulings and live gotchas unrolled.
- **Reading**: MEMORY.md arrives free at session start; drill before acting
  on any fact whose staleness would be expensive (memories are testimony from
  prior sessions — the resume-and-verify doctrine applies to yourself).
- **Never write secrets into memory** — events are immutable; the tool
  spec's remediation runbook is the only eraser.

## Migration

Per project, one-time, scripted: for each existing memory file, `set` its
slug with its `type`, its description as `-m`, evidence `file:<old-path>`,
authored `--as migration`; body note from the file's body. Render, eyeball
the projection against the old MEMORY.md, then delete the old fact files.
The old files' `modified` timestamps are not preserved (the chain's clock
starts at migration); their content and provenance-of-origin are.

## Dependency and dogfood contract

Memory now requires `ledger` on PATH (tap or curl|sh install). This is
deliberate: memory is a daily-use, self-inflicted workload. Friction found
here (verbose command shapes, projection legibility, rollup ergonomics) is
field evidence for the tool, captured as memories in the memory ledger itself.

## Acceptance

1. Fresh session, given only the generated MEMORY.md in context: correctly
   recalls a fact, drills its body, saves a new memory, and re-renders —
   without touching the old file workflow.
2. Retraction round-trip: a planted wrong memory is retracted with a why;
   the projection shows the vaccine line; the raw chain retains both events.
3. Concurrent writes from two sessions: both facts land, projection converges.
4. Kill `ledger` from PATH: session still reads memory and reports the
   degraded state instead of failing silently.
5. Migration of this project's real memory dir round-trips with no lost facts.
