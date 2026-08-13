# Ledger tool — design

2026-08-13, revision 5. Grounded in `REPORT.md`, design discussion with Jesse, and four adversarial review rounds. Rev 2 moved storage out of tracked files into a kata-style machine store; rev 3 applied a YAGNI pass; rev 4 replaced the machine store with **git phantom refs** (Jesse's proposal — the git-bug/git-appraise pattern); rev 5 hardens the sync layer after round-3 reviewers found (and empirically verified) that rev 4's one-sentence sync design lost data under plain `git fetch`. YAGNI rule stands: machinery only for failures observed in the field; one honest scope sentence for everything else.

Round-3 validated assumptions (empirical, scratch repos): update-ref CAS resolves real append races (60/60 events landed, retries observed); appends ~50ms; 500-event fold 78ms, `since` 18ms; `git gc --prune=now` preserves ref'd events and CAS works against packed refs; worktrees share refs both directions; shallow clones fetch full ledger history through the refspec.

## What this is

`ledger`: a CLI giving coding agents durable, structured working-state — execution spines, coordination scoreboards, handoff checkpoints, branch-qualified verification state — with the primitives the research showed every agent hand-rolling: atomic append, identity, lifecycle, freshness, cursor-based coordination, and travel.

Form factor: CLI now, MCP facade later. Go, single static binary, no daemon. Storage rides git plumbing.

## Storage: phantom refs

Each ledger lives at **`refs/ledger/<slug>`** in the project's git object database. An event is a commit: `event.json` in the tree, the ref advanced by `git update-ref` with compare-and-swap; the creation commit also carries `meta.json` (immutable facts: slug, scope, base, created, created_by, owner, supersedes). Vocabulary and open/closed state fold from the event chain.

Consequences, all load-bearing:

- **Shared across worktrees** (refs live in the common dir): the parent-tails-children scoreboard works across a fleet's worktrees.
- **Invisible to `git status`, immune to `git clean`, safe from `rm -rf .superpowers`**; objects are gc-safe while the ref exists, and the tool never deletes refs.
- **Atomicity via ref CAS**: concurrent appenders race on `update-ref`; the loser re-reads and retries. No flock, no torn-tail repair machinery — a failed append simply never advances the ref. Create/supersede races resolve the same way (ref creation is itself CAS).
- **Event identity is the commit SHA.** Dense integer IDs are gone (two review rounds showed they die outside a single locked store). The cursor is a commit SHA, valid iff it is an **ancestor of the current head** (`merge-base --is-ancestor`); anything else — unknown, orphaned by repair, or a foreign ledger's SHA from the shared object database — yields `reset_required`. `since <cursor>` returns events reachable from the head but not from the cursor, ordered topologically, timestamp-tiebroken, **commit-SHA-tiebroken last** (a total order; same-second appends are the agent norm).
- **An event is a non-merge commit carrying `event.json`.** Sync merge commits carry a sentinel `event.json` of `type: "sync"` and are skipped by every read, fold, count, and idempotency scan. (Round 3 verified the alternative: a merge inheriting a parent tree gets delivered by `since` as a duplicate event and can resurrect a stale status at fold time.)
- **Git author = ledger author, synthetically.** Each event commit sets author and committer explicitly per commit (`-c`/env): author = asserted `author` `<author@ledger.invalid>`, committer = harness marker. Never the user's gitconfig — agent events must not be email-attributed to the human, and gitconfig-less containers (eval capture, /tmp fleets) must still be able to write. `git log refs/ledger/<slug>` remains a legible audit trail.

### Sync: fetch-safe, push-deliberate

Round 3's verified criticals: the naive mirror refspec (`+refs/ledger/*:refs/ledger/*`) lets any plain `git fetch` force-clobber local unpushed events, and with `fetch.prune=true` (present in Jesse's real gitconfig) delete never-pushed local refs entirely — with no reflog for custom refs. And a remote is a second, CAS-less namespace. Therefore:

- **Tracking-namespace fetch**: `init` installs `+refs/ledger/*:refs/remotes/<remote>/ledger/*` (per remote). Local `refs/ledger/*` is never a fetch target; out-of-band fetches can't touch it. `init` also sets `core.logAllRefUpdates=always` in the repo as a recovery net.
- **`ledger sync [--remote <r>]` is fetch + merge only**: fetch tracking refs, merge tracking→local under local CAS. **It never pushes.** `ledger push [--remote <r>]` is a separate verb, always non-force, so harness permission allowlists can distinguish local state-keeping from the outward-facing action — the corpus norm is that pushes are gated, deliberate events. Doctrine: sync at session start; push at human-sanctioned checkpoints.
- **Same-root rule**: sync merges only chains whose root (creation) commit matches. Divergent histories with different roots — two clones independently created the same slug — refuse to merge, with an error naming both creators from `meta.json` and suggesting `import --as <new-slug>` for one of them.
- **Lifecycle across merges**: `close` is terminal regardless of interleaving — events that postdate a close on a merged sibling chain do not reopen or advance the spine; `show` flags them as post-close anomalies.
- **What can still destroy the remote copy**, named honestly: `git push --mirror` from a clone that never fetched ledger refs deletes `refs/ledger/*` remotely (verified), and force-pushes discard events. Quickstart names both; controlled remotes should set `receive.denyDeletes`/deny-non-fast-forward.
- Cross-host **resume** is in scope (work, push, fetch elsewhere, resume). Live cross-machine coordination is not: `watch` observes the local ref only; between hosts, coordination is resume-grade in v1.

**Discovery of unfetched ledgers**: `ledger ls` is offline (tracking refs make fetched-but-unmerged ledgers visible locally); `ledger sync` reports what it fetched. No listing command makes network calls (a credential prompt inside `ls` is the classic non-interactive harness stall).

**Slug grammar**: `[a-z0-9][a-z0-9-]*`, max 64 chars, enforced at `create` and `import`. (Verified: unconstrained slugs hit git ref-name fatals, D/F conflicts between `task-3` and `task-3/sub`, and case-aliasing between macOS and Linux clones.)

**Committed breadcrumb**: `.ledger.toml` survives in minimal form, written by `init`: a marker comment ("this repo uses ledger; run `ledger ls`") plus optionally the default sync remote. `init` **never commits it** — it prints "commit this file so clones discover the ledger" and stops; until a human or controller commits it, the passive discovery leg is inert, and the spec says so plainly (agent-initiated commits to the user's repo violated observed norms and produced documented cleanup work). No identity config: the store *is* the repo.

**Privacy, stated**: pushed refs are fetchable by anyone with repo read access — on a public repo, by the public — and events carry hostnames, cwd paths, and session ids in `origin`, plus exactly the content the corpus shows in handoffs (rulings, gotchas, do-not-touch warnings about specific people's checkouts). v1's rule: everything pushed is visible to everyone with read access; quickstart warns before the push verb, and visibility classes remain v2.

**Non-repo workdirs** (the July-4 `/tmp` scoreboards): `ledger init` in a non-git directory creates a bare store at `./.ledger.git` (no `.ledger.toml` — the store is self-describing) and subsequent verbs in that tree use it. Store resolution order, explicit: `$LEDGER_DIR` env > `--store <path>` flag > nearest `.ledger.git` by cwd walk-up > normal git discovery. If both a `.ledger.git` and an enclosing git repo are found, the nearer one wins and the tool prints which store it chose — a scratch workdir *inside* a repo, or a clone *inside* a scratch workdir (the observed July-4 topology), must never silently write scoreboard events into the wrong store.

**Export**: `ledger export <slug>` still emits self-contained JSONL (stdout; `--to`) for repo-committed permanence and for consumers without ref access; `git bundle` also works and quickstart says so. `ledger import` creates a new ledger, refuses an existing slug (`--as <new-slug>`), and is only for crossing non-git boundaries — host-to-host flow is `sync`/`push`. Stated limit: export preserves event payloads, not identities — import mints new SHAs, so cursors and `--id` references do not survive the JSONL boundary.

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
- `ledger sync [--remote <r>]` — fetch tracking refs + merge into local (same-root rule; never pushes). `ledger push [--remote <r>]` — non-force push of local ledger refs; the deliberate, gateable verb.
- `ledger ls` — open plus recently-closed (30d; `--all` for everything), recency-sorted, time since last event; offline (fetched-but-unmerged tracking refs appear as "unsynced").

Slug resolution: explicit > sole open ledger (output leads with an identity line: slug + scope) > error listing open slugs. Reads on a superseded ledger print the redirect first.

## Reads and coordination

- `ledger show [<slug>]` — the render. `ledger status [<key>] [--branch <name>]` — spine only.
- `ledger notes [-k kind] [--key key] [--id <sha>] [--latest | -n N]` (default latest 10); `--latest` output leads with age and author — a stale checkpoint must announce itself; `--id` fetches one event's full text.
- `ledger tail -n N`; `ledger since <cursor>`; `ledger watch --since <cursor> [--key K] [--status A,B,C] [--kind K] [--timeout T]` — drain then block; blocking = polling the ref SHA (200ms), which is one cheap read; the drain/arm gap closes by re-checking the ref before sleeping. Timeout: exit 2, cursor still emitted. Filters take comma lists.
- `--json` everywhere with documented stable schemas; every cursor-consuming read returns the next cursor.

### Renderer

`ledger show`: identity header (slug, scope, base, supersedes); spine table — latest status per key with author, absolute timestamp, branch, evidence; latest note per kind plus last 15 notes as first-line + author + timestamp + event SHA (full text: `ledger notes --id <sha>`); footer with event count. Deterministic for a given head (absolute timestamps; age only in `ls`/`--latest`), size-bounded (long bodies never inline). `ledger render --to <path>` writes the same projection to a file for anyone who wants a grep-able artifact; nothing is auto-written into the worktree.

## Branch-qualified verification (minimal)

Every event carries `origin.branch`/`origin.head` (detached HEAD records `(detached@<sha>)`); the spine renders a branch column; reads accept `--branch`. "Verified — on branch X at SHA Y" is expressible and filterable. One fold-time courtesy: when a key's two most recent status events come from different branches with different statuses, `show`/`status` flag the row (`⚠ branch-divergent: also <status> on <branch>@<sha>`) — the observed disease was branch copies disagreeing *silently*; a flag is display, not divergence machinery. No auto-demotion; reconcile sweeps stay a workflow, now with the data to run them cheaply.

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
5. Lifecycle: create refuses existing slugs (open and closed) and enforces the slug grammar; crash-dangled supersede completed by retried create; superseded-without-successor refused; closed refuses set, accepts note; superseded reads lead with redirect.
6. Harness probes: Claude subagent env shows parent session id (confirmed; re-check on upgrades); `CODEX_THREAD_ID` presence/distinctness per Codex subagent.
7. Worktrees: two worktrees of one repo see one ledger; `watch` in one sees `set` from the other.
8. Fetch safety: with the installed refspec and `fetch.prune=true`, plain `git fetch` neither clobbers nor deletes local `refs/ledger/*` (tracking namespace only).
9. Sync: same-root divergence merges; `since` across the merge is deterministic (SHA-total-order) and complete; sync merge commits are invisible to tail/since/show/count/idempotency; different-root same-slug refuses with both creators named; post-close events from a merged chain don't reopen the spine and are flagged.
10. Cursor: non-ancestor-but-known SHA (foreign ledger, pre-clobber orphan) → `reset_required`, not full replay.
11. Push: `ledger push` is non-force; rejected non-fast-forward surfaces cleanly; `sync` never pushes.
12. Identity: writes succeed in a gitconfig-less environment; commit author/committer are synthetic (never the user's gitconfig email).
13. `watch --since`: drain, block, no replay/drop under concurrent writers; timeout exits 2 with cursor.
14. Import/export: JSONL round-trip into a fresh slug preserves payloads; import refuses existing slugs; documented that identities don't cross.
15. Renderer: deterministic per head; size-bounded with oversized bodies; branch column present; branch-divergent row flag fires.
16. Keys: near-miss warns, brand-new silent; identity line leads implicit-resolution output.
17. Store resolution: nested clone inside a scratch workdir writes to the intended store; ambiguity prints the chosen store; `$LEDGER_DIR`/`--store` override.
18. gc-safety: `git gc --prune=now` in a repo with active ledger refs loses nothing and CAS still works against packed refs.
