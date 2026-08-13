# Ledger tool — design

2026-08-13. Grounded in `REPORT.md` (phase-1 research) and the design discussion with Jesse; decisions below cite the evidence that forced them.

## What this is

`ledger`: a CLI that gives coding agents durable, structured working-state files — execution spines, coordination scoreboards, capability catalogs, handoff checkpoints — with the primitives the research showed every agent hand-rolling: atomic append, identity, lifecycle, freshness, and cursor-based coordination.

Form factor: **CLI over plain files now; MCP facade later**, once the verbs stabilize. Rationale: Codex (the heaviest ledger user) speaks only shell; Claude routed half its ledger traffic through Bash anyway; the dominant failure class is content passing through quoting layers, which stdin-based writes eliminate.

## Storage model

Source of truth is an **append-only `events.jsonl`**; the readable `ledger.md` is a **rendered projection** the tool regenerates. The accountant's split: journal of events → ledger view.

Layout, per ledger, at the repo root:

```
.ledgers/<slug>/
  meta.json       # identity + config (see below)
  events.jsonl    # append-only truth; one JSON object per line, O_APPEND writes
  ledger.md       # rendered projection; regenerated on write; never hand-edited
```

- `.ledgers/` is **tracked by default**. Durability and cross-host travel came from committing (kata-fleet promotion, cross-host handoffs); gitignored scratch died to `git clean -fdx`. `--scratch` at create makes a gitignored ledger for ephemeral IPC/evidence use — a deliberate choice, never an accident.
- Appends are single `O_APPEND` line writes: atomic, anchor-free, near-conflict-free under git merge. After any merge, `ledger render <slug>` regenerates `ledger.md` from merged events (a git merge driver is a possible later refinement, not v1).
- The tool **never deletes**. `close` is an event, not removal. History destruction is what cost the eval campaign a 750-line forensic reconstruction tool.

### meta.json

```json
{
  "slug": "webui-fleet",
  "scope": "goal:katas-2026-07-30",          // plan path, goal ref, or free text
  "base": "97fa9758b",                        // base commit at creation
  "created": "2026-07-31T04:10:18Z",
  "created_by": {"author": "controller", "origin": {...}},
  "supersedes": null,                         // slug link, set by --supersedes
  "vocab": ["started", "done", "failed", "blocked", "gate"],
  "scratch": false,
  "state": "open"                             // open | closed:<reason>
}
```

The identity block is the structural version of the SDD identity header that converted a ~9-tool-call forensics tax per resume into a check and made cross-plan misattribution impossible.

### Event schema (one JSONL line)

```json
{
  "id": 147,                        // monotonic per ledger; the cursor
  "ts": "2026-08-01T06:44:18Z",
  "type": "status",                 // status | note | vocab | lifecycle
  "key": "task-3",                  // status events only
  "status": "done",                 // status events only
  "kind": "ruling",                 // note events only; free string
  "text": "...",                    // note body or status -m annotation
  "evidence": ["commit:340a027e..e1ab4637", "run:smoke-1786062713"],
  "author": "task-3-implementer",
  "origin": {"host": "...", "cwd": "...", "pid": 43547, "session": "ecb81c5a-...", "session_source": "CLAUDE_CODE_SESSION_ID"},
  "idempotency_key": "task-3-done"  // optional; dedupes retried writes
}
```

## Entry model: two registers

**Status events** — the machine-scannable spine. `ledger set <key> <status> [-m note] [--evidence <ref>]...`. Keys are stable IDs (task-3, cull-029, agent-s9xc). Status values are validated against the ledger's declared vocabulary — see Vocabulary below.

**Note events** — the narrative register. `ledger note [-k <kind>] [--key <key>]`, body on **stdin** (or `-m` for short notes). Kinds are free strings with documented conventions: `ruling`, `standing-rule`, `carry-forward`, `handoff`, `gotcha`. This is where rulings, gate essays, and anti-knowledge live without bloating the spine. `--key` optionally attaches a note to a spine item.

**Evidence fields on anything**: `--evidence <type>:<ref>` — `commit:`, `run:`, `file:`, or free-form. The renderer places evidence next to the claim. (A `ledger verify` that checks whether commit anchors still exist is v2.)

Rationale for the split: agents invented it independently three times (journal vs checkpoint, catalog vs per-run evidence, log rows vs situation board), and every observed bloat failure came from collapsing the two registers into one file.

## Vocabulary: hard errors, self-service extension

`ledger set` with a status outside the declared vocabulary is a **hard error**, and the error message carries the fix:

```
error: "reconciled" is not in this ledger's status vocabulary.
valid: started, done, failed, blocked, gate
to add: ledger vocab add webui-fleet reconciled -m "why this status is needed"
```

`vocab add` appends a `vocab` event — vocabulary growth is deliberate, attributed, and auditable. Ruling (Jesse): no warn-and-accept escape hatch; the X8-B eval showed agents skip optional grammars precisely on the hard cases, and teststrip's mid-flight status inventions ("Reconciled — NOT re-run") deserve to be recorded decisions, not drift.

## Identity

Two layers per event:

- **`author` — asserted.** Resolution: `--as <name>` > `$LEDGER_AUTHOR` > `$USER`. Parents dictate `--as <role>` in the command grammar they hand Agent-tool children; `LEDGER_AUTHOR` serves tmux/csd-style workers whose spawner controls env. Humans sign the same way (`--as jesse`). Asserted, not authenticated — the same trust model as git's author field.
- **`origin` — auto-captured.** Host, cwd, pid, and any harness session identifier found in the environment, recorded with its source var name.

**Spike finding (2026-08-13):** a Claude subagent's tool calls see the *parent's* `CLAUDE_CODE_SESSION_ID` — env cannot distinguish subagents, so `origin.session` is root-session context, never authorship. Codex's binary references `CODEX_THREAD_ID`; whether each Codex subagent exposes a distinct value inside `exec_command` is an open test (in the test plan below).

## Lifecycle

- `ledger create <slug> --scope <ref> [--vocab a,b,c] [--scratch] [--owner <name>] [--supersedes <old-slug>]` — refuses to overwrite an existing open slug. `--supersedes` closes the old ledger with a link, paving the follow-up-plan path that produced `progress-p2.md`.
- `ledger close <slug> --as-state shipped|abandoned|superseded [-m note]` — terminal event; files remain.
- `ledger promote <slug>` — graduates a `--scratch` ledger into the tracked registry, events intact (the kata-fleet commit-to-git move as a verb).
- `ledger ls` — every ledger with state and **time since last event**, open-first, recency-sorted. Staleness is visible by default, not a query mode:

```
LEDGER          SCOPE                              STATE     LAST WRITE  EVENTS
webui-fleet     goal:katas-2026-07-30              open      2h ago      147
decisionhub-v1  plan:2026-08-12-decisionhub-v1.md  open      3d ago      58
sidebar-rebuild plan:sidebar-rebuild.md            shipped   12d ago     41
```

## Reads and coordination

Slug resolution, all verbs: an explicit `<slug>` argument wins; otherwise, if the repo has exactly one open ledger, it is implied; otherwise the command errors and lists the open slugs.

- `ledger show [<slug>]` — rendered view. `ledger status [<key>]` — spine only, or one key.
- `ledger tail -n N` — last N events.
- `ledger since <cursor>` — events after cursor plus the new cursor; the poll primitive that ends the observed re-read tax (p90 of 4 full re-reads per file per session). Includes a `reset_required` response if event IDs regress (history rewrite), after kata's cursor contract.
- `ledger watch [--key K] [--status S] [--kind N] [--timeout T]` — block until a matching event, print it, exit. Replaces the observed `tail -F | grep "DONE\|FAILED\|BLOCKED\|GATE"` contortions.
- `--json` on every verb; agent doctrine says prefer it.

## Writes: quoting and retries

- Note bodies arrive via stdin; no ledger content ever rides inside shell quoting (the largest observed error class: Codex apply_patch anchor failures, JS→JSON→zsh quoting deaths, Claude's 968 python-heredoc mutations).
- `--idempotency-key` on `set` and `note`: a retried command with the same key is a no-op, killing the observed duplicate-append wobble after timeouts.

## Onboarding

`ledger quickstart` — agent-facing doctrine emitted by the tool, modeled on `kata quickstart`: numbered rules, copy-pasteable examples, author resolution order, prefer `--json`, cursor contract, and the destructive-action policy (there are no destructive verbs; say so). Skills reference "run `ledger quickstart`" instead of embedding usage text that drifts.

## Deferred to v2 (deliberate cuts)

- **Owner enforcement / roles** (`--owner` is recorded in v1 but not enforced; proposer/apply flow later).
- **Visibility classes** (grader-only ledgers for eval and durable-review use).
- **`ledger verify`** (evidence-anchor checking; the structural fix for stale-"Verified").
- **MCP facade**, git merge driver, harness compaction hooks, session-start auto-surfacing beyond `ledger ls`.

## Test plan notes

Normal TDD applies. Specific tests the research demands:

1. Atomic append under concurrent writers (two processes `set` simultaneously; no interleaved/lost lines).
2. Idempotency-key dedupe across process restarts.
3. Vocabulary hard-error message contains valid values and the exact `vocab add` command.
4. Merge behavior: two branches append; git merge; `render` produces a correct union; `since` cursors remain monotonic or signal `reset_required`.
5. `--scratch` → `promote` preserves event history byte-for-byte.
6. Live harness probes: (a) confirm Claude subagent env shows parent session ID (done 2026-08-13, reconfirm on harness upgrades); (b) whether `CODEX_THREAD_ID` is set and distinct per Codex subagent inside `exec_command`.
7. Renderer stability: rendered `ledger.md` is deterministic for a given events.jsonl (byte-identical re-renders).

## Implementation notes

Language: Go, single static binary, no daemon — matching the kata model and the rest of the toil-suite tooling. (Override at planning time if Jesse prefers otherwise.) The renderer and event append are pure file operations.
