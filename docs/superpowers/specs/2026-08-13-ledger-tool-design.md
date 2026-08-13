# Ledger tool — design

2026-08-13, revision 3. Grounded in `REPORT.md`, the design discussion with Jesse, and two adversarial review rounds. Rev 2 replaced rev 1's tracked-in-repo storage with a kata-style machine store; rev 3 folds in the second review round under an explicit YAGNI rule: **machinery only for failures observed in the field; one honest scope sentence for everything else.**

## What this is

`ledger`: a CLI giving coding agents durable, structured working-state — execution spines, coordination scoreboards, handoff checkpoints, and (minimally) branch-qualified verification state — with the primitives the research showed every agent hand-rolling: atomic append, identity, lifecycle, freshness, and cursor-based coordination.

Form factor: CLI over plain files now; MCP facade later. Go, single static binary, no daemon; flock + files only.

## Storage

- **`.ledger.toml`** — committed, written by `ledger init`: the project identity and (optionally) a store-location override (`dir`, resolved relative to the git common dir's parent; absolute paths warn — they don't travel). Identity is **computed once at init and stored** (default: first git remote URL; no remote: repo-toplevel basename; `--project` overrides). Editing the identity re-keys the store and the old data stays under the old id — stated behavior, no migration verbs in v1 (no identity shift was ever observed in the corpus).
- **Machine store** — `~/.local/share/ledger/<project-id>/<slug>/`: all worktrees and clones of a project on a machine share one live store. Immune to `git clean`, invisible to `git status`, never merged by git.
- Per ledger: `meta.json` (immutable creation facts: slug, scope, base, created, created_by, owner, supersedes), `events.jsonl` (append-only truth), `ledger.md` (rendered projection, generated-file banner). Vocabulary and open/closed state fold from events.
- Any verb without `.ledger.toml` in reach is a hard error naming `ledger init`. Non-repo directories: `init --project <name>` works anywhere (the July-4 scoreboards ran in /tmp workdirs).
- **Concurrency**: an exclusive flock per ledger dir serializes allocate+append+render; readers take a shared flock; renders are temp+rename. A project-level lock covers `create` and supersede (slug uniqueness and the two-ledger link are checked/written under it). The lock is the atomicity mechanism; the store requires a local filesystem.
- **Crash tolerance**: reads skip a malformed final line and warn; `ledger repair` truncates the partial tail under lock and appends a `repair` event. No other repair machinery.
- **The tool never deletes.** `close` is an event; files remain. Slugs are never reused — `create` refuses any existing slug, open or closed; `--supersedes` is the paved path for follow-ups.
- **Travel**: `ledger export <slug>` (stdout; `--to <path>`) emits self-contained JSONL — commit it when a ledger deserves repo permanence. `ledger import <path>` creates a **new** ledger and refuses an existing slug (`--as <new-slug>` on collision). Live round-tripping between hosts is **unsupported in v1**; cross-host resume is out of scope beyond one-shot export/import, and the tool says so where it matters: `ledger ls` on an initialized project with an empty store prints "initialized <date>; no events on this machine — history may live elsewhere (check committed exports)."

### Event schema (one JSONL line)

```json
{
  "id": 147,
  "ts": "2026-08-01T06:44:18Z",
  "type": "status",                 // status | note | vocab | lifecycle | repair
  "key": "task-3",
  "status": "done",
  "kind": "ruling",
  "text": "...",
  "evidence": ["commit:340a027e..e1ab4637", "run:smoke-1786062713"],
  "author": "task-3-implementer",
  "origin": {"host": "...", "cwd": "...", "pid": 43547, "branch": "unified-shell", "head": "5af2479c", "session": "ecb81c5a-...", "session_source": "CLAUDE_CODE_SESSION_ID"},
  "idempotency_key": "task-3-done"
}
```

`branch` and `head` are auto-captured per event (free, structural, and the corpus's universal tiebreaker was git). IDs are dense integers minted under the ledger lock. The cursor is (slug, id); a cursor beyond the current max id yields `reset_required` (post-repair case) — no generation machinery, since import never merges into existing streams.

## Entry model: two registers

**Status events** — `ledger set <key> <status> [-m note] [--evidence <ref>]... [--idempotency-key K]`. Keys are case-sensitive stable IDs; a key within small edit distance of an existing key (and not identical) warns — brand-new keys are the normal case (spines grow one task at a time) and warn on nothing.

**Note events** — `ledger note [-k <kind>] [--key <key>]`, body from stdin, `-m`, or `--from-file <path>`. Kind conventions: `ruling`, `standing-rule`, `carry-forward`, `handoff`, `gotcha`, `postmortem`.

**Evidence** — `--evidence <type>:<ref>`: `commit:`, `run:`, `file:`, free-form. Doctrine: `file:` refs are ephemeral (workspaces get deleted); prefer `commit:`/`run:` for anything that must outlive the run. (`ledger verify` is v2.)

**Quoting, honestly**: stdin/`-m` kills the anchor-edit failure class and serves Claude; Codex content still crosses JS→JSON→zsh, so the paved path for long bodies everywhere is `--from-file` — write the body with native file tools, pass a path.

**Retries**: a write is a no-op iff **any prior event on that key** (same kind+key for notes) carries the same idempotency key — full-history dedupe, so a delayed retry can never revert a concurrent writer's newer transition, and legitimate re-transitions use fresh keys. Deduped calls succeed with `"deduped": true`.

## Vocabulary

Statuses are declared (`create --vocab a,b,c`; omitted ⇒ default set `open, done, failed, blocked`) and extended by event. An undeclared status is a **hard error** carrying the valid list and the exact `ledger vocab add <slug> <status> -m "why"` command (Jesse's ruling: no warn-and-accept; vocabulary growth is a recorded, attributed decision). Case-sensitive — declare the case you dictate (`DONE,FAILED,BLOCKED,GATE` is fine).

## Identity

- **`author`** — asserted: `--as` > `$LEDGER_AUTHOR` > harness marker when harness env is detected (`claude-code`, `codex`) > `$USER`. Bare `$USER` only when no harness is detectable, so an agent omitting `--as` never signs as Jesse. Quickstart carries the inheritance warning: subagents inherit parent env, so `LEDGER_AUTHOR` is for spawners that control child env; parents dispatching Agent-tool children put `--as <role>` in the dictated grammar.
- **`origin`** — auto-captured: host, cwd, pid, branch, head, harness session id with source var name. Spike findings (2026-08-13): Claude subagents see the parent's session id — origin is context, never authorship; Codex `CODEX_THREAD_ID` distinctness is an open probe (test 6).

## Lifecycle

- `ledger init` — writes `.ledger.toml`; prints the quickstart pointer and a suggested one-line stanza for CLAUDE.md/AGENTS.md ("this repo uses `ledger`; run `ledger ls` on session start") — printing, not editing; the stanza is the active discovery leg for both harnesses.
- `ledger create <slug> --scope <ref> [--vocab ...] [--owner <name>] [--supersedes <old>]` — under the project lock. Supersede ordering: close the old ledger (with `superseded_by: <new>`) first, then create the new one; `create` finding a dangling `superseded_by` from a crash completes the creation. `close --as-state superseded` without a successor is refused — the forward pointer is the load-bearing one.
- `ledger close <slug> --as-state shipped|abandoned|superseded [-m note]` — terminal event. Closed ledgers refuse `set`/`vocab add` (error names the successor when one exists), accept `note`.
- `ledger ls` — open ledgers plus those closed in the last 30 days (`--all` for everything), recency-sorted, with time since last event.

Slug resolution: explicit argument > sole open ledger (output leads with an identity line: slug + scope) > error listing open slugs. Reads on a superseded ledger print the redirect first.

## Reads and coordination

- `ledger show [<slug>]` — the render (below). `ledger status [<key>] [--branch <name>]` — spine only.
- `ledger notes [-k kind] [--key key] [--latest | -n N]` (default: latest 10). `--latest` output **leads with age and author** ("written 6d ago by controller") — a stale checkpoint must announce itself.
- `ledger tail -n N`; `ledger since <cursor>`; `ledger watch --since <cursor> [--key K] [--status A,B,C] [--kind K] [--timeout T]` — drain matches after cursor, then block (200ms poll; lock never held while blocked; the drain/arm gap is closed by re-checking the cursor before sleeping). Timeout: exit code 2, cursor still emitted. Filters take comma lists (the observed monitor greps four statuses).
- `--json` everywhere, with documented stable schemas; every read that consumed a cursor returns the next one.

### Renderer

`ledger.md`/`show`: identity header (slug, scope, base, supersedes link); spine table — latest status per key with author, absolute timestamp, branch, evidence refs; then notes: latest note per kind and the last 15 note events, each as first line + author + timestamp + event id, full text via `ledger notes --id N`. Footer: event count + `ledger tail` pointer. Deterministic (absolute timestamps only — age strings appear in `ls`/`--latest` output, not in the render) and size-bounded (long bodies never inline).

## Branch-qualified verification (minimal)

Every event carries `origin.branch`/`origin.head`; the spine renders a branch column; reads accept `--branch`. That makes "Verified — on branch X at SHA Y" expressible and filterable, which is what the teststrip evidence demanded. No auto-demotion, no per-branch render modes, no divergence detection — reconcile sweeps remain a human/agent workflow, now with the data to do them cheaply.

## Onboarding and discovery

`ledger quickstart` — kata-style agent doctrine: numbered rules, examples, author resolution, prefer `--json`, the cursor contract, `--from-file` for long bodies, `file:`-evidence ephemerality, export-then-commit for repo permanence, no destructive verbs. Discovery legs: `.ledger.toml` (passive), `ledger ls` (one command from cold, with the empty-store message), and the init-printed stanza plus a README SessionStart-hook snippet (active).

## SDD coexistence (sketch)

`ledger create --scope plan:<path>` at plan start; `close --as-state shipped` at finish. Briefs/reports/review diffs stay workspace files; cite them as `commit:`/`run:` refs (or accept that `file:` refs die with the workspace). Workspace deletion and `git clean` no longer touch ledger history. Caveat stated plainly: in ephemeral-home environments (eval containers, throwaway devboxes) the store dies with the home directory — capture tooling that wants the ledger must harvest `~/.local/share/ledger` or have the run export before teardown. The capture problem is moved to a defined location, not abolished.

## Deferred to v2 (deliberate cuts)

Owner/role enforcement; visibility classes; `ledger verify`; MCP facade; project rename/adopt/merge verbs; live cross-host sync or auto-export; hand-edit drift recovery; compaction hooks; store GC; `$LEDGER_SCOPE` ambient checks; kind near-miss warnings; branch divergence detection.

## Test plan notes

1. Concurrent `set` ×2: no lost/torn lines, no duplicate IDs.
2. Concurrent write + render: `ledger.md` never torn or stale-clobbered.
3. Idempotency: retry dedupes against full key history; interleaved-writer retry does NOT revert the newer status; re-transition with a fresh key applies; `deduped:true` surfaced.
4. Vocab: hard-error lists values + exact `vocab add` command; default vocab applies when `--vocab` omitted; `vocab add` unblocks.
5. Lifecycle: create refuses any existing slug; supersede crash between close-old and create-new is completed by retried create; `close --as-state superseded` without successor refused; closed refuses set, accepts note; superseded reads lead with redirect.
6. Harness probes: Claude subagent env shows parent session id (confirmed; re-check on upgrades); `CODEX_THREAD_ID` presence/distinctness per Codex subagent.
7. Worktrees/clones: two worktrees + second clone resolve to one store; `watch` in one sees `set` from the other.
8. `watch --since`: drains, blocks, no replay/drop across sequential invocations under concurrent writers; timeout exits 2 with cursor.
9. Import: refuses existing slug; `--as` re-slugs; round-trip into fresh slug preserves events byte-for-byte.
10. Renderer: byte-identical re-render; bounded size with oversized note bodies; branch column present.
11. Keys: near-miss warns, brand-new key silent; identity line leads implicit-resolution output.
12. Crash: partial final line skipped-with-warning on read; `repair` truncates under lock; post-repair stale cursor gets `reset_required`.
