# Ledger tool — design

2026-08-13, revision 2. Grounded in `REPORT.md` (phase-1 research), the design discussion with Jesse, and two adversarial reviews whose findings reshaped the storage architecture (worktree/merge incoherence in rev 1). Decisions cite the evidence that forced them.

## What this is

`ledger`: a CLI that gives coding agents durable, structured working-state — execution spines, coordination scoreboards, capability catalogs, handoff checkpoints — with the primitives the research showed every agent hand-rolling: atomic append, identity, lifecycle, freshness, and cursor-based coordination.

Form factor: **CLI over plain files now; MCP facade later**, once the verbs stabilize. Rationale: Codex (the heaviest ledger user) speaks only shell; Claude routed half its ledger traffic through Bash anyway.

## Storage model (kata-shaped)

The workspace carries only identity; the data lives outside git's reach.

- **`.ledger.toml`** — small committed config at the repo top, written by `ledger init`. Binds the repo to a project identity (default: derived from the git remote, like `.kata.toml`; `--project` overrides) and optionally overrides the store location (`dir = ...`, absolute or relative to the git common dir). It is also the in-repo discovery pointer: the grep-able artifact a fresh session trips over.
- **Machine store** — `~/.local/share/ledger/<project-id>/<slug>/`, one per project identity. All worktrees *and all clones* of a project on a machine share one live store (the corpus had serf checked out at two paths; kata's identity-keyed store unifies them). Immune to `git clean`, invisible to `git status`, no tracked churn, no merging of live state — the entire class of rev-1 merge/worktree findings dissolves.
- Per ledger: `meta.json` (immutable creation facts), `events.jsonl` (append-only truth), `ledger.md` (rendered projection, generated-file banner, regenerated under lock via temp+rename).
- **Concurrency**: an `flock` on the ledger directory serializes ID allocation + append + render. The lock, not O_APPEND folklore, is the atomicity mechanism. Dense integer event IDs are sound under this model (single store, single host, lock held across allocate+append).
- **The tool never deletes.** `close` is an event; files remain. (History destruction cost the eval campaign a 750-line forensic reconstruction tool.)
- **Travel and repo permanence are explicit**: `ledger export <slug> [--to <path>]` emits self-contained JSONL — commit it when a ledger deserves repo permanence (the kata-fleet "promote to git" move, and the publication path for capability catalogs); `ledger import <path>` ingests one elsewhere (cross-host handoff). A fresh clone starts with `.ledger.toml` and an empty store, exactly like kata.

There is no `--scratch` and no tracked mirror: one store, one copy, explicit export. (Rev 1 had a tracked `.ledgers/` plus scratch flag; both reviewers showed the tracked copy failed the multi-worktree and merge cases, and Jesse cut the duplication as un-DRY.)

### meta.json — immutable creation facts only

```json
{
  "slug": "webui-fleet",
  "scope": "goal:katas-2026-07-30",
  "base": "97fa9758b",
  "created": "2026-07-31T04:10:18Z",
  "created_by": {"author": "controller", "origin": {...}},
  "owner": null,
  "supersedes": "webui-fleet-p1"
}
```

Vocabulary and open/closed state are **folded from the event stream**, never stored mutably (rev-1 review: a mutable meta.json is a second source of truth that drifts). `owner` is recorded, not enforced (enforcement is v2).

### Event schema (one JSONL line)

```json
{
  "id": 147,
  "ts": "2026-08-01T06:44:18Z",
  "type": "status",                 // status | note | vocab | lifecycle
  "key": "task-3",
  "status": "done",
  "kind": "ruling",                 // note events; free string
  "text": "...",
  "evidence": ["commit:340a027e..e1ab4637", "run:smoke-1786062713"],
  "author": "task-3-implementer",
  "origin": {"host": "...", "cwd": "...", "pid": 43547, "session": "ecb81c5a-...", "session_source": "CLAUDE_CODE_SESSION_ID"},
  "idempotency_key": "task-3-done"
}
```

## Entry model: two registers

**Status events** — the machine-scannable spine. `ledger set <key> <status> [-m note] [--evidence <ref>]...`. Keys are stable IDs (task-3, cull-029, agent-s9xc), case-sensitive; quickstart doctrine says lowercase-kebab. `set` on a key the ledger has never seen (when other keys exist) prints a warning naming the nearest existing key — typo'd keys minting phantom spine rows is the same silent-drift class the vocabulary machinery blocks.

**Note events** — the narrative register. `ledger note [-k <kind>] [--key <key>]` with the body from stdin, `-m` for short notes, or `--from-file <path>`. Kinds are free strings with documented conventions: `ruling`, `standing-rule`, `carry-forward`, `handoff`, `gotcha`, `postmortem`. `--key` attaches a note to a spine item.

**Evidence fields on anything**: `--evidence <type>:<ref>` — `commit:`, `run:`, `file:`, or free-form. The renderer places evidence next to the claim. (`ledger verify` — checking whether anchors still resolve — is v2.)

Rationale for the split: agents invented it independently three times (journal vs checkpoint, catalog vs per-run evidence, log rows vs situation board), and every observed bloat failure came from collapsing the registers.

### Quoting: what the design does and doesn't fix

Stdin/`-m` input eliminates the *anchor* failure class (Codex apply_patch "Failed to find expected lines"; Claude Edit double-fires) and helps Claude's Bash heredocs. It does **not** eliminate content-in-command encoding for Codex, whose exec wraps commands in JS→JSON→zsh. The paved path for long bodies on any harness is `--from-file`: write the body with native file tools (Claude Write; Codex apply_patch *Add File*, which needs no context anchor), pass only a path through the shell. Quickstart carries this as Codex doctrine. (Rev-1 overclaimed "eliminates the quoting class"; both reviewers called it.)

### Retries

`--idempotency-key` on `set` and `note`. Semantics: the write is a no-op iff the **current latest event for the same key** (for `set`; same kind+key for `note`) carries the same idempotency key. This dedupes the observed retry-after-timeout double-append while leaving legitimate re-transitions alive (done → failed → done again compares against the `failed` event and applies). A deduped call succeeds with `"deduped": true` in `--json` output.

## Vocabulary: hard errors, self-service extension

`ledger set` with a status outside the ledger's vocabulary (declared at create, extended by event) is a **hard error** carrying the fix:

```
error: "reconciled" is not in this ledger's status vocabulary.
valid: started, done, failed, blocked, gate
to add: ledger vocab add webui-fleet reconciled -m "why this status is needed"
```

`vocab add` appends a `vocab` event — growth is deliberate, attributed, auditable. Ruling (Jesse): no warn-and-accept escape hatch. Rationale: vocabulary changes are decisions worth recording (teststrip's load-bearing "Reconciled — NOT re-run" invention deserved a paper trail), and auditability requires the extension to be explicit. Statuses are case-sensitive; declare the case you dictate to children (the observed `DONE|FAILED|BLOCKED|GATE` grammar is fine — declare it uppercase).

## Identity

- **`author` — asserted.** Resolution: `--as <name>` > `$LEDGER_AUTHOR` > harness marker when harness env is detected (`claude-code`, `codex`, from the env vars present) > `$USER`. The bare-`$USER` fallback applies only when no harness is detectable — otherwise an agent omitting `--as` would sign as Jesse, corrupting attribution in the worst direction (rev-1 review finding). Parents dictate `--as <role>` in the command grammar handed to Agent-tool children; `LEDGER_AUTHOR` serves spawners that control env (tmux/csd workers). Humans sign as themselves (`--as jesse`, or bare `$USER` at a real terminal). Asserted, not authenticated — git's trust model.
- **`origin` — auto-captured.** Host, cwd, pid, and any harness session identifier found in env, recorded with its source var name.

**Spike findings (2026-08-13):** a Claude subagent's tool calls see the *parent's* `CLAUDE_CODE_SESSION_ID` — env cannot distinguish subagents, so `origin.session` is root-session context, never authorship. Codex's binary references `CODEX_THREAD_ID`; whether it is set and distinct per subagent inside `exec_command` is an open test (test plan §6).

## Lifecycle

- `ledger init` — writes `.ledger.toml`, prints quickstart pointer.
- `ledger create <slug> --scope <ref> [--vocab a,b,c] [--owner <name>] [--supersedes <old-slug>]` — refuses an existing open slug. `--supersedes` closes the old ledger and links **bidirectionally**: the old ledger's close event records `superseded_by`, and any read of a superseded ledger prints the redirect first (the observed failure is a reader landing on the *old* artifact; the forward pointer is the load-bearing one).
- `ledger close <slug> --as-state shipped|abandoned|superseded [-m note]` — terminal event. A closed ledger refuses `set` and `vocab add` (the error names the superseding slug when one exists) and accepts only `note` (postmortems happen after shipping).
- `ledger export` / `ledger import` — see Storage.
- `ledger ls` — every ledger with state and **time since last event**, open-first, recency-sorted:

```
LEDGER          SCOPE                              STATE     LAST WRITE  EVENTS
webui-fleet     goal:katas-2026-07-30              open      2h ago      147
decisionhub-v1  plan:2026-08-12-decisionhub-v1.md  open      3d ago      58
sidebar-rebuild plan:sidebar-rebuild.md            shipped   12d ago     41
```

Slug resolution, all verbs: explicit argument wins; else if the project has exactly one open ledger it is implied **and the output leads with an identity line (slug + scope)** so a wrong implicit pick is visible; else error listing open slugs. Reads accept `--scope <ref>` as a filter; if `$LEDGER_SCOPE` is set (e.g. by a dispatching skill), implicit resolution to a ledger with a different scope is a hard error — the cheap structural form of "refuse foreign reads" (REPORT implication 2).

## Reads and coordination

- `ledger show [<slug>]` — the rendered projection (see Renderer).
- `ledger status [<key>]` — spine only, or one key.
- `ledger notes [-k <kind>] [--key <key>] [--latest | -n N]` — keyed narrative reads. `ledger notes -k handoff --latest` is the checkpoint-consumption verb: the research's overwrite-checkpoint register, served from append-only storage (rev-1 review: without this, a resumer had no cheap path to "current situation").
- `ledger tail -n N` — last N events.
- `ledger since <cursor>` — events after cursor, plus the new cursor. `reset_required` signals a rebuilt store (import/repair), after kata's cursor contract.
- `ledger watch --since <cursor> [--key K] [--status S] [--kind N] [--timeout T]` — **drain then block**: first emit matching events after the cursor, then block for the next match; output includes the new cursor. This composes with a poll loop without replaying or dropping events (rev-1's cursorless watch did both).
- `--json` on every verb; doctrine says prefer it.

### Renderer

`ledger.md` (and `show`) is a bounded summary, not the event log: identity header; the spine as a table — latest status per key with author, age, and evidence refs; latest note per kind; notes from a bounded recency window (default: last 15 note events); a footer naming event count and `ledger tail` for full history. Deterministic: byte-identical for a given events.jsonl. Full narrative history is never in the render — that's what `notes`/`tail`/`since` are for. (Rev-1 left the renderer unspecified, which silently re-created the 49KB-resume-tax the two-register split exists to end.)

## Onboarding and discovery

- `ledger quickstart` — agent-facing doctrine emitted by the tool, kata-style: numbered rules, copy-pasteable examples, author resolution, prefer `--json`, the cursor contract, `--from-file` for long bodies, commit-the-export doctrine, and the destructive-action policy (no destructive verbs exist; say so).
- Discovery has three legs: `.ledger.toml` in the repo (passive, grep-able), `ledger ls` (one command from cold), and a documented SessionStart-hook snippet that emits open-ledger one-liners into fresh contexts. The research is unequivocal that consumed handoffs had pointers and orphans had none; the hook snippet ships in the README as recommended harness config, not as tool code.

## SDD coexistence (migration sketch)

SDD's `.superpowers/sdd/<plan>/` workspace keeps briefs, reports, and review diffs — those stay files, referenced from ledger events as `--evidence file:` refs. The progress ledger itself becomes `ledger create --scope plan:<path>` at plan start and `ledger close --as-state shipped` at finish. The finishing step's workspace deletion no longer destroys history: the store is outside the repo and untouched by `rm -rf .superpowers` or `git clean` — the eval-capture problem that forced `extract_ledger.py` disappears structurally. Execution spines accumulate in the machine store, not in git history; a plan whose record deserves repo permanence gets `ledger export --to docs/…` and a commit.

## Deferred to v2 (deliberate cuts)

Owner/role enforcement; visibility classes (grader-only ledgers); `ledger verify` (evidence-anchor checking); MCP facade; remote/shared stores; hand-edit drift recovery on `ledger.md`; harness compaction hooks; store GC/retention policy.

## Test plan notes

Normal TDD applies. Tests the research and reviews demand:

1. Concurrent `set` from two processes: no lost/interleaved lines **and no duplicate IDs** (flock covers allocate+append).
2. Concurrent write + render: `ledger.md` never torn or stale-clobbered (temp+rename under the same lock).
3. Idempotency: retry dedupes; legitimate re-transition (done→failed→done, same key string) applies; `deduped:true` surfaced.
4. Vocab hard-error message contains valid values and the exact `vocab add` command; `vocab add` then unblocks the original write.
5. Closed-ledger rules: `set`/`vocab` refused (naming superseding slug when present), `note` accepted; superseded reads lead with the redirect.
6. Harness probes: (a) Claude subagent env shows parent session ID (confirmed 2026-08-13; re-check on harness upgrades); (b) whether `CODEX_THREAD_ID` is set and distinct per Codex subagent inside `exec_command`.
7. Worktrees and clones: two worktrees and a second clone of one project resolve to the same store; `watch` in one sees `set` from the other.
8. `watch --since`: drains matches after cursor, blocks for the next, no replay and no drop across sequential invocations under concurrent writers.
9. Export/import round-trip preserves events byte-for-byte; `since` against an imported store signals `reset_required`.
10. Renderer determinism: byte-identical re-render for a given events.jsonl.
11. Unseen-key warning fires on typo'd keys; identity line leads implicit-resolution output; `$LEDGER_SCOPE` mismatch hard-errors.

## Implementation notes

Go, single static binary, no daemon — kata's model. Everything is flock + files; the renderer and append are pure file operations.
