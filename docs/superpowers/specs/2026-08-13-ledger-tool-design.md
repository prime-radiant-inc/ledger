# Ledger tool — design

2026-08-13, revision 4. Grounded in `REPORT.md`, design discussion with Jesse, and three adversarial review rounds. Rev 2 moved storage out of tracked files into a kata-style machine store; rev 3 applied a YAGNI pass; rev 4 replaces the machine store with **git phantom refs** (Jesse's proposal) — the git-bug/git-appraise/git-notes pattern — which restores cross-host travel, deletes the project-identity machinery, and rides git's ref transactions instead of flock. YAGNI rule stands: machinery only for failures observed in the field; one honest scope sentence for everything else.

## What this is

`ledger`: a CLI giving coding agents durable, structured working-state — execution spines, coordination scoreboards, handoff checkpoints, branch-qualified verification state — with the primitives the research showed every agent hand-rolling: atomic append, identity, lifecycle, freshness, cursor-based coordination, and travel.

Form factor: CLI now, MCP facade later. Go, single static binary, no daemon. Storage rides git plumbing.

## Storage: phantom refs

Each ledger lives at **`refs/ledger/<slug>`** in the project's git object database. An event is a commit: `event.json` in the tree, the ref advanced by `git update-ref` with compare-and-swap; the creation commit also carries `meta.json` (immutable facts: slug, scope, base, created, created_by, owner, supersedes). Vocabulary and open/closed state fold from the event chain.

Consequences, all load-bearing:

- **Shared across worktrees** (refs live in the common dir): the parent-tails-children scoreboard works across a fleet's worktrees.
- **Invisible to `git status`, immune to `git clean`, safe from `rm -rf .superpowers`**; objects are gc-safe while the ref exists, and the tool never deletes refs.
- **Atomicity via ref CAS**: concurrent appenders race on `update-ref`; the loser re-reads and retries. No flock, no torn-tail repair machinery — a failed append simply never advances the ref. Create/supersede races resolve the same way (ref creation is itself CAS).
- **Travel is git**: `ledger sync [--remote <r>]` pushes and fetches `refs/ledger/*`. Cross-host resume is **in scope**: work on host A, sync, fetch on host B, resume. Hosting platforms carry custom refs (invisible in their UI, which is fine).
- **Event identity is the commit SHA.** Dense integer IDs are gone (two review rounds showed they die outside a single locked store). The cursor is an opaque commit SHA; `since <cursor>` returns events reachable from the head but not from the cursor, in deterministic order (topological, timestamp-tiebroken); an unknown cursor yields `reset_required`.
- **Divergence is defined, not forbidden**: two hosts appending between syncs produce divergent ref histories; `sync` reconciles with a merge commit (both chains preserved — append-only streams union cleanly; no content conflicts exist by construction). Idempotency-key dedupe applies across the merged history.
- **Git author = ledger author**: each event commit's author name is the asserted `author`, so `git log refs/ledger/<slug>` is a legible audit trail with zero extra machinery.

**Fetch refspec**: clones don't fetch custom refs by default — the pattern's one genuine wart. `ledger init` installs `fetch = +refs/ledger/*:refs/ledger/*` into the repo's local git config, and `sync` verifies it. A clone that never ran `init`/`sync` sees no ledgers; `ledger ls` in a repo whose remote has ledger refs it hasn't fetched says so ("remote has N ledger refs — run `ledger sync`", discoverable via `git ls-remote`).

**Committed breadcrumb**: `.ledger.toml` survives in minimal form — a marker ("this repo uses ledger; run `ledger ls`") written by `init`, because refs are invisible to passive discovery and the corpus is unequivocal that unconsumed state had no pointer. No identity config: the store *is* the repo. Project-identity derivation, remote-rewrite policy, and rename/adopt verbs are all deleted — the machinery reviewer A flagged in round 2 no longer has anything to configure.

**Non-repo workdirs** (the July-4 `/tmp` scoreboards): `ledger init` in a non-git directory creates a bare store at `./.ledger.git` and subsequent verbs in that tree use it. Path-local, shareable by every agent in the workdir, dies with it — appropriate for ephemeral coordination. One code path: it's the same backend.

**Export**: `ledger export <slug>` still emits self-contained JSONL (stdout; `--to`) for repo-committed permanence and for consumers without ref access; `git bundle` also works and quickstart says so. `ledger import` creates a new ledger, refuses an existing slug (`--as <new-slug>`), and is now only for crossing non-git boundaries — host-to-host flow is `sync`.

### Event schema (`event.json` in each commit's tree)

```json
{
  "ts": "2026-08-01T06:44:18Z",
  "type": "status",                 // status | note | vocab | lifecycle
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

The event's `id` is its commit SHA (not stored in the JSON). `branch`/`head` are auto-captured (git was the corpus's universal tiebreaker). Storage layout inside the tree is an implementation detail; the contract is the JSON schema plus SHA identity.

## Entry model: two registers

**Status events** — `ledger set <key> <status> [-m note] [--evidence <ref>]... [--idempotency-key K]`. Keys are case-sensitive stable IDs; a key within small edit distance of an existing key (not identical) warns; brand-new keys are the normal case and warn on nothing.

**Note events** — `ledger note [-k <kind>] [--key <key>]`, body from stdin, `-m`, or `--from-file <path>`. Kind conventions: `ruling`, `standing-rule`, `carry-forward`, `handoff`, `gotcha`, `postmortem`.

**Evidence** — `--evidence <type>:<ref>`: `commit:`, `run:`, `file:`, free-form. Doctrine: `file:` refs are ephemeral; prefer `commit:`/`run:` for anything that must outlive the run. (`ledger verify` is v2.)

**Quoting, honestly**: stdin/`-m` kills the anchor-edit failure class and serves Claude; Codex content still crosses JS→JSON→zsh, so the paved path for long bodies everywhere is `--from-file`.

**Retries**: a write is a no-op iff any prior event on that key (same kind+key for notes) carries the same idempotency key — full-history dedupe across sync merges included, so a delayed retry can never revert a newer transition. Deduped calls succeed with `"deduped": true`.

## Vocabulary

Declared at `create --vocab a,b,c` (omitted ⇒ default `open, done, failed, blocked`), extended by `vocab` events. Undeclared status is a **hard error** carrying the valid list and the exact `ledger vocab add <slug> <status> -m "why"` command (Jesse's ruling: no warn-and-accept; growth is a recorded, attributed decision). Case-sensitive.

## Identity

- **`author`** — asserted: `--as` > `$LEDGER_AUTHOR` > harness marker when harness env is detected (`claude-code`, `codex`) > `$USER`; bare `$USER` only with no harness detectable. Quickstart carries the inheritance warning: subagents inherit parent env, so `LEDGER_AUTHOR` is for spawners that control child env; parents dispatching Agent-tool children put `--as <role>` in the dictated grammar.
- **`origin`** — auto-captured: host, cwd, pid, branch, head, harness session id + source var. Spike findings (2026-08-13): Claude subagents see the parent's session id — origin is context, never authorship; Codex `CODEX_THREAD_ID` distinctness is an open probe (test 6).

## Lifecycle

- `ledger init` — installs the fetch refspec, writes the `.ledger.toml` breadcrumb, prints quickstart pointer + a suggested CLAUDE.md/AGENTS.md stanza (printed, not auto-edited). In a non-git dir, creates `./.ledger.git`.
- `ledger create <slug> --scope <ref> [--vocab ...] [--owner <name>] [--supersedes <old>]` — CAS ref creation refuses any existing slug, open or closed; slugs are never reused. Supersede: close the old (with `superseded_by`) first, then create; a retried create completes a crash-dangled supersede. `close --as-state superseded` without a successor is refused.
- `ledger close <slug> --as-state shipped|abandoned|superseded [-m note]` — terminal event. Closed refuses `set`/`vocab add` (error names the successor when one exists), accepts `note`.
- `ledger sync [--remote <r>]` — push + fetch `refs/ledger/*`, reconciling divergence with a merge commit.
- `ledger ls` — open plus recently-closed (30d; `--all` for everything), recency-sorted, time since last event; notes unfetched remote refs.

Slug resolution: explicit > sole open ledger (output leads with an identity line: slug + scope) > error listing open slugs. Reads on a superseded ledger print the redirect first.

## Reads and coordination

- `ledger show [<slug>]` — the render. `ledger status [<key>] [--branch <name>]` — spine only.
- `ledger notes [-k kind] [--key key] [--latest | -n N]` (default latest 10); `--latest` output leads with age and author — a stale checkpoint must announce itself.
- `ledger tail -n N`; `ledger since <cursor>`; `ledger watch --since <cursor> [--key K] [--status A,B,C] [--kind K] [--timeout T]` — drain then block; blocking = polling the ref SHA (200ms), which is one cheap read; the drain/arm gap closes by re-checking the ref before sleeping. Timeout: exit 2, cursor still emitted. Filters take comma lists.
- `--json` everywhere with documented stable schemas; every cursor-consuming read returns the next cursor.

### Renderer

`ledger show`: identity header (slug, scope, base, supersedes); spine table — latest status per key with author, absolute timestamp, branch, evidence; latest note per kind plus last 15 notes as first-line + author + timestamp + event SHA (full text: `ledger notes --id <sha>`); footer with event count. Deterministic for a given head (absolute timestamps; age only in `ls`/`--latest`), size-bounded (long bodies never inline). `ledger render --to <path>` writes the same projection to a file for anyone who wants a grep-able artifact; nothing is auto-written into the worktree.

## Branch-qualified verification (minimal)

Every event carries `origin.branch`/`origin.head`; the spine renders a branch column; reads accept `--branch`. "Verified — on branch X at SHA Y" is expressible and filterable. No auto-demotion, no divergence detection; reconcile sweeps stay a workflow, now with the data to run them cheaply.

## Onboarding and discovery

`ledger quickstart` — kata-style agent doctrine: numbered rules, examples, author resolution, prefer `--json`, the cursor contract, `--from-file`, `file:`-evidence ephemerality, sync doctrine ("sync at start and at checkpoint moments"), no destructive verbs. Discovery legs: `.ledger.toml` breadcrumb (passive), `ledger ls` with the unfetched-refs message (one command from cold), the init-printed stanza + README SessionStart-hook snippet (active).

## SDD coexistence (sketch)

`ledger create --scope plan:<path>` at plan start; `close --as-state shipped` at finish. Briefs/reports stay workspace files, cited as `commit:`/`run:` refs (or accepted as ephemeral `file:` refs). Workspace deletion and `git clean` can't touch the refs. Eval capture: any harvest that pushes or bundles refs gets full ledger history — a one-command improvement over the rev-3 caveat (the machine store died with the container home; refs ride the repo the capture already handles).

## Deferred to v2 (deliberate cuts)

Owner/role enforcement; visibility classes; `ledger verify`; MCP facade; auto-sync (sync is manual doctrine in v1); hand-edit recovery; compaction hooks; ref GC/retention; `$LEDGER_SCOPE` checks; kind near-miss warnings; branch divergence detection.

## Test plan notes

1. Concurrent `set` ×2: CAS race resolves, both events land, no lost writes.
2. Concurrent create of one slug: exactly one wins; loser gets a clean error.
3. Idempotency: retry dedupes against full history including across a sync merge; interleaved-writer retry does not revert the newer status; fresh-key re-transition applies; `deduped:true` surfaced.
4. Vocab: hard error lists values + exact `vocab add` command; default vocab when `--vocab` omitted; `vocab add` unblocks.
5. Lifecycle: create refuses existing slugs (open and closed); crash-dangled supersede completed by retried create; superseded-without-successor refused; closed refuses set, accepts note; superseded reads lead with redirect.
6. Harness probes: Claude subagent env shows parent session id (confirmed; re-check on upgrades); `CODEX_THREAD_ID` presence/distinctness per Codex subagent.
7. Worktrees: two worktrees of one repo see one ledger; `watch` in one sees `set` from the other.
8. Sync: host A and B both append; sync merges; `since` over the merge is deterministic and complete; unknown cursor → `reset_required`.
9. Refspec: fresh clone + `init` + `sync` fetches existing ledgers; `ls` names unfetched remote refs before that.
10. `watch --since`: drain, block, no replay/drop under concurrent writers; timeout exits 2 with cursor.
11. Import/export: JSONL round-trip into a fresh slug preserves events; import refuses existing slugs.
12. Renderer: deterministic per head; size-bounded with oversized bodies; branch column present.
13. Keys: near-miss warns, brand-new silent; identity line leads implicit-resolution output.
14. Non-repo: `init` creates `./.ledger.git`; scoreboard flow works in a bare workdir.
15. gc-safety: `git gc` in a repo with active ledger refs loses nothing.
