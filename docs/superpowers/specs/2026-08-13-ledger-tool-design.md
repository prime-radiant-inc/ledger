# Ledger tool — design

2026-08-13, revision 6. Grounded in `REPORT.md`, design discussion with Jesse, and five adversarial review rounds (empirical verification in rounds 3–4). Storage: git phantom refs (Jesse's proposal, rev 4); sync hardened rev 5; rev 6 fixes the round-4 findings and resolves the capability-catalog scope question. YAGNI rule stands: machinery only for failures observed in the field; one honest scope sentence for everything else.

Round-3/4 validated assumptions (empirical, git 2.50.1, scratch repos): update-ref CAS resolves real append races including appender-vs-syncer (36/36 events, one sentinel merge); appends ~50ms; 500-event fold 78ms, `since` 18ms; `merge-base --is-ancestor` 10ms on a 5,000-commit packed chain; `git gc --prune=now` preserves ref'd events; worktrees share refs; shallow clones fetch full history; tracking-namespace fetch survives `fetch.prune=true` and remote wipes; `since`/fold identical on both hosts after sync convergence.

## What this is

`ledger`: a CLI giving coding agents durable, structured working-state — execution spines, coordination scoreboards, handoff checkpoints — with the primitives the research showed every agent hand-rolling: atomic append, identity, lifecycle, freshness, cursor-based coordination, and travel.

**Scope cut, stated honestly (resolves REPORT §6's open question):** v1 does **not** serve the capability-catalog role. The observed catalog (teststrip's `LEDGER.md`) is git-*committed* product truth — it diverges with branches, its claim changes ride PR diffs and review, and branch merge is its convergence mechanism. Phantom refs are structurally the opposite: one branch-unaware chain, invisible to `git status` and PR review. Events still auto-capture branch/HEAD (cheap, and the reconcile-sweep workflows want the data), but the catalog role — a committed, reviewable projection with defined refresh semantics — is deferred to v2 as a design problem, not smuggled in as a display feature.

Form factor: CLI now, MCP facade later. Go, single static binary, no daemon. Storage rides git plumbing.

## Storage: phantom refs

Each ledger lives at **`refs/ledger/<slug>`**. An event is a **non-merge commit** carrying `event.json`; the creation commit also carries `meta.json` (immutable: slug, scope, base, created, created_by, owner, supersedes). Vocabulary and open/closed state fold from events. Sync merge commits carry a sentinel `event.json` of `type:"sync"` and are skipped by every read, fold, count, and idempotency scan.

- **Shared across worktrees** (refs live in the common dir); invisible to `git status`; immune to `git clean` and `rm -rf .superpowers`; gc-safe while ref'd; the tool never deletes refs.
- **Atomicity via ref CAS**: concurrent writers race on `update-ref`; losers re-read and retry. Slug uniqueness and supersede ordering ride the same CAS.
- **Commit identity is synthetic**: author = asserted `author` `<author@ledger.invalid>`, committer = harness marker, set per commit via `-c`/env — never the user's gitconfig (agent events must not be email-attributed to the human; gitconfig-less containers must be able to write).
- **Event identity = commit SHA.** Cursor = a SHA, valid iff an ancestor of the current head; anything else (including a foreign ledger's SHA from the shared odb) yields `reset_required`. Event order is total: topological, timestamp-tiebroken, SHA-tiebroken last. `since` with no cursor reads from the beginning — that is also the `reset_required` recovery path: drop the cursor, re-drain, resume.
- **Slug grammar**: `[a-z0-9][a-z0-9-]*`, max 64 chars, enforced at `create`/`import` (verified: unconstrained slugs hit ref-name fatals, D/F conflicts, and macOS/Linux case-aliasing).

### Sync and push

Tracking refs live at **`refs/ledger-remote/<remote>/<slug>`** — a private namespace, *not* `refs/remotes/`, which git's default branch refspec also populates (verified fatal collision when a branch is named `ledger/<x>`). Tradeoff accepted: `git remote rename/remove` won't maintain this namespace; `sync`/`push` verify-and-install the refspec for the named remote on every run (fixes late-added remotes too) and prune tracking refs for remotes that no longer exist.

- `ledger sync [--remote <r>]` — fetch tracking refs, then per slug: tracking ancestor of local ⇒ no-op; local ancestor of tracking ⇒ fast-forward; true divergence ⇒ one sentinel merge under CAS (verified: without the ff rule, chains grow one merge per sync forever); no local ref ⇒ **CAS-create local at the tracking head** (remote-only adoption — the cross-host resume path). **Sync never pushes.**
- `ledger push [--remote <r>] [<slug>...]` — non-force, default all local slugs, selective by argument (privacy: push one handoff ledger without pushing everything). Per-slug outcomes reported; exit 0 all-ok, 3 partial. On non-fast-forward rejection, push fetches tracking refs (so root mismatches are diagnosed with the two-creator error) and prints "run `ledger sync`, then retry `ledger push`" — suppressing git's own 'git pull' hint, which is wrong for phantom refs. The sync-then-push race is a retry loop, stated.
- **Same-root rule**: sync merges only chains sharing a creation commit. Different roots (two clones independently created one slug) refuse, naming both creators from `meta.json`. **Exit ramp** (round 4: the previous remedy was unreachable): `ledger adopt --remote <r> <slug> --rename-local <new-slug>` atomically renames the local chain to a fresh slug (a lifecycle event records the rename) and CAS-creates the local ref from the tracking head. No refs are deleted; both histories survive under distinct slugs.
- **Lifecycle across merges**: `close` is terminal; among causally unordered closes, the first in the total order wins and the loser is flagged. Post-close `set`/`vocab` arriving via merge are flagged as anomalies; post-close `note`s are legal everywhere and never flagged.
- **Degraded modes**: no remote configured ⇒ clean no-op with message; sync/push run with `GIT_TERMINAL_PROMPT=0` and fail fast with a "credentials needed" error (a prompt inside a non-interactive harness is a stall); tracking ref vanished while local exists ⇒ sync warns "remote may have lost this ledger; `ledger push` restores it" (verified restorable).
- **What can still destroy the remote copy**, named: `git push --mirror` from a clone that never fetched ledger refs, and force-pushes. Quickstart names both; controlled remotes should set `receive.denyDeletes`/deny-non-fast-forward.
- Cross-host **resume** is in scope (work, push, sync elsewhere, resume). Live cross-machine coordination is not: `watch` observes the local ref; between hosts, coordination is resume-grade in v1.

**Every clone bootstraps itself**: refspec and config are repo-local and do not clone. The breadcrumb and quickstart both say `ledger init && ledger sync`; `ledger ls`, finding a breadcrumb but no installed refspec, prints the bootstrap command instead of an empty listing (round 4, verified: a fresh clone otherwise sees nothing at all).

**Committed breadcrumb**: `.ledger.toml` — a marker ("this repo uses ledger; run `ledger init && ledger sync`") plus optionally the default sync remote. `init` writes it and prints "commit this file so clones discover the ledger"; it **never commits** (agent-initiated commits violated observed norms). Until committed, the passive leg is inert, stated plainly.

**Privacy, stated**: pushed refs are fetchable by anyone with repo read access — public repo, public refs — and events carry hostnames, cwd paths, session ids, and exactly the content the corpus shows in handoffs (rulings, do-not-touch warnings about specific people's checkouts). v1 rule: everything pushed is visible to everyone with read access; quickstart warns; selective `push <slug>` is the mitigation; visibility classes are v2.

**Non-repo workdirs**: `ledger init` in a non-git directory creates a bare store `./.ledger.git` (with `core.logAllRefUpdates=always` — bare-default reflogs are off and `true` doesn't cover custom refs; verified) and no `.ledger.toml`. Repo `init` sets `core.logAllRefUpdates=always` too, as the recovery net. **Store resolution**: `--store <path>` > `$LEDGER_DIR` (flag beats env — env inherits into subagents) > nearest ancestor directory containing `.ledger.git` or `.git` (if one directory has both, `.ledger.git` wins); whenever both kinds exist in the ancestry, the tool prints which store it chose. Fleet doctrine: parents dictating a shared scoreboard pass `--store` in the dictated command grammar, exactly as they dictate `--as`.

**Export/import**: `ledger export <slug>` emits self-contained JSONL (stdout; `--to`) for repo-committed permanence and non-git consumers; `git bundle` also works. `ledger import <path> [--as <slug>]` creates a new ledger, refuses existing slugs. Stated limit: export preserves payloads, not identities — cursors and `--id` refs don't cross the JSONL boundary.

### Event schema (`event.json`)

```json
{
  "ts": "2026-08-01T06:44:18Z",
  "type": "status",                 // status | note | vocab | lifecycle | sync
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

`id` = commit SHA (not stored). `branch`/`head` auto-captured; detached HEAD records `(detached@<sha>)`. `type:"sync"` appears only in sentinel merges, which reads skip.

## Entry model: two registers

**Status events** — `ledger set <key> <status> [-m note] [--evidence <ref>]... [--idempotency-key K]`. Keys are case-sensitive stable IDs; a key within small edit distance of an existing key (not identical) warns; brand-new keys are the normal case and warn on nothing.

**Note events** — `ledger note [-k <kind>] [--key <key>]`, body from stdin, `-m`, or `--from-file <path>`. Kind conventions: `ruling`, `standing-rule`, `carry-forward`, `handoff`, `gotcha`, `postmortem`.

**Evidence** — `--evidence <type>:<ref>`: `commit:`, `run:`, `file:`, free-form. Doctrine: `file:` refs are ephemeral; prefer `commit:`/`run:`. (`ledger verify` and per-status required-evidence flags are v2.)

**Ledger addressing**: every read/write verb takes a global `--ledger <slug>` flag. Resolution: explicit flag > sole open ledger (output leads with slug + scope) > error listing open slugs. Multiple open ledgers were the corpus norm (fleet board + SDD spine simultaneously); fleet prompts dictate `--ledger` alongside `--as` and `--store`.

**Quoting, honestly**: stdin/`-m` kills the anchor-edit failure class and serves Claude in one call; Codex long bodies take the two-call `--from-file` path (write file with native tools, pass the path). The extra call on exactly the hard-entry class is a known, accepted cost: the observed alternative was not one cheap call but one *failure-prone* call (quoting deaths, anchor retries); correctness of the hard entries is the point. Quickstart says so.

**Retries**: a write is a no-op iff any prior event on that key (same kind+key for notes) carries the same idempotency key — full-history dedupe, sync merges included. Deduped calls succeed with `"deduped": true`.

## Vocabulary

Declared at `create --vocab a,b,c` (omitted ⇒ default `open, done, failed, blocked`), extended by `vocab` events. Undeclared status is a **hard error** listing valid values and the exact `ledger vocab add <slug> <status> -m "why"` command (Jesse's ruling: no warn-and-accept; growth is a recorded, attributed decision). Case-sensitive.

## Identity

- **`author`** — asserted: `--as` > `$LEDGER_AUTHOR` > harness marker when harness env is detected > `$USER` only with no harness detectable. Quickstart: `LEDGER_AUTHOR` is for spawners that control child env; parents dispatching Agent-tool children put `--as <role>` in the dictated grammar (subagents inherit parent env — spike-verified).
- **`origin`** — auto-captured: host, cwd, pid, branch, head, harness session id + source var. Spike findings (2026-08-13): Claude subagents see the parent's session id — origin is context, never authorship; Codex `CODEX_THREAD_ID` distinctness is an open probe (test 6).

## Lifecycle

- `ledger init` — installs refspec + `logAllRefUpdates=always`, writes the breadcrumb (repo case), prints quickstart pointer + suggested CLAUDE.md/AGENTS.md stanza (printed, never auto-edited). Required once per clone.
- `ledger create <slug> --scope <ref> [--vocab ...] [--owner <name>] [--supersedes <old>]` — CAS-refuses any existing slug, open or closed; slugs never reused. Supersede: close old (with `superseded_by`) first, then create; retried create completes a crash-dangled supersede.
- `ledger close <slug> --as-state shipped|abandoned|superseded [-m note]` — terminal. `superseded` requires the successor link. Closed refuses `set`/`vocab add` (error names the successor), accepts `note`.
- `ledger sync` / `ledger push` / `ledger adopt` — see Sync and push.
- `ledger ls` — open plus recently-closed (30d; `--all`), recency-sorted, time since last event; offline; shows unsynced tracking-only slugs; prints the bootstrap hint when the breadcrumb exists but the refspec isn't installed.

Reads on a superseded ledger print the redirect first.

## Reads and coordination

- `ledger show` — the render. `ledger status [<key>] [--branch <name>]` — spine only.
- `ledger notes [-k kind] [--key key] [--id <sha>] [--latest | -n N]` (default latest 10); `--latest` leads with age and author.
- `ledger tail -n N`; `ledger since [<cursor>]` (no cursor = from start; the `reset_required` recovery).
- `ledger watch --since <cursor> [--key K] [--status A,B,C] [--kind K] [--timeout T]` — drain matching events after the cursor; if none, block (200ms ref poll) until at least one match, then **deliver the whole current batch and exit** with the new cursor. The cursor advances past non-matching events (documented: filtered events are skipped, not replayed). No `--timeout` blocks forever; quickstart doctrine: **always set `--timeout` below the harness command timeout** — a harness-killed watch emits no cursor. `--follow` streams line-per-event JSON indefinitely for background monitors — one call watching a whole fleet, matching the observed single `tail -F` (round 4: without it, a 10-child fleet costs 10–20 watch calls where the corpus paid one).
- `--json` everywhere. Error contract: machine-readable error identifiers in JSON output (`reset_required`, `vocab_unknown`, `closed`, `root_mismatch`, `non_fast_forward`, `ambiguous_ledger`, `credentials_needed`); exit codes: 0 success, 1 error, 2 watch timeout (cursor still emitted), 3 partial push. Full `--json` schemas are implementation-plan surface, pinned by tests.
- **Read-time freshness**: when a slug's tracking ref is ahead of local (an out-of-band fetch ran; sync didn't), `show`/`status` append "N unmerged remote events — run `ledger sync`" — the information is local and free, and a confidently stale spine violates the tool's own staleness principle.

### Renderer

`ledger show`: identity header (slug, scope, base, supersedes/superseded_by); spine table — latest status per key with author, absolute timestamp, branch, evidence; latest note per kind plus last 15 notes as first-line + author + timestamp + SHA (full text: `ledger notes --id <sha>`); footer with event count. Deterministic per head (absolute timestamps; age only in `ls`/`--latest`), size-bounded (long bodies never inline). When a key's two most recent status events come from different branches with different statuses, the row is flagged (`⚠ branch-divergent: also <status> on <branch>@<sha>`) — display of the data, resolving nothing. `ledger render --to <path>` writes the projection to a file; nothing is auto-written into the worktree.

## Onboarding and discovery

`ledger quickstart` — kata-style agent doctrine: numbered rules, examples, author resolution, prefer `--json`, cursor contract incl. `reset_required` recovery, `--from-file` and its stated cost, `--timeout` on watch, sync-at-start / push-at-human-sanctioned-checkpoints, selective push for privacy, mirror/force-push hazards, no destructive verbs. Discovery legs: committed breadcrumb (`init && sync` bootstrap line), `ledger ls` (bootstrap-aware), init-printed stanza + README SessionStart-hook snippet.

## SDD coexistence (sketch)

`ledger create --scope plan:<path>` at plan start; `close --as-state shipped` at finish. Briefs/reports stay workspace files, cited as `commit:`/`run:` refs or accepted as ephemeral. Workspace deletion and `git clean` can't touch the refs. Eval capture: any harvest that pushes or bundles refs gets full history.

## Deferred to v2 (deliberate cuts)

Capability-catalog role (committed, reviewable projection with refresh semantics — the design problem named above); owner/role enforcement; visibility classes; `ledger verify` + per-status required-evidence flags; MCP facade; auto-sync; cross-branch reconcile/auto-demotion machinery (the v1 flag is display only); hand-edit recovery; compaction hooks; ref GC/retention.

## Test plan notes

1. Concurrent `set` ×2: CAS resolves, both land, no loss. Appender-vs-syncer race: all events land, one sentinel merge.
2. Concurrent create of one slug: exactly one wins, clean error for the loser.
3. Idempotency: dedupe across full history incl. sync merges; interleaved retry can't revert newer status; fresh-key re-transition applies; `deduped:true`.
4. Vocab: hard error lists values + exact command; default vocab; `vocab add` unblocks.
5. Lifecycle: slug grammar enforced; existing slugs refused; crash-dangled supersede completed; superseded-without-successor refused; closed refuses set/vocab, accepts note; superseded reads redirect; dueling closes: first in total order wins, loser flagged.
6. Harness probes: parent-session-id inheritance (confirmed; re-check on upgrades); `CODEX_THREAD_ID` distinctness.
7. Worktrees: shared ref; `watch` in one sees `set` from the other.
8. Fetch safety: installed refspec + `fetch.prune=true`: plain `git fetch` never touches local `refs/ledger/*`; branch named `ledger/x` coexists (tracking namespace outside `refs/remotes/`).
9. Sync: ancestor ⇒ no-op (10 idle cycles: zero growth); behind ⇒ ff; divergence ⇒ one sentinel merge, skipped by all reads/counts; different-root refuses naming both creators; `adopt --rename-local` resolves it with both histories intact; remote-only slug adopted at tracking head; post-close set flagged, post-close note not.
10. Cursor: foreign-ledger SHA ⇒ `reset_required`; recovery via cursorless `since` re-drain.
11. Push: non-force; rejection prints sync-then-retry (git's 'git pull' hint suppressed) and fetches tracking for root diagnosis; per-slug outcomes; exit 3 on partial; selective `push <slug>`.
12. Identity: writes succeed with no gitconfig; author/committer synthetic.
13. `watch`: drain/block/batch-exit semantics; cursor advances past filtered events; timeout exit 2 with cursor; `--follow` streams a 10-child fleet in one call.
14. Import/export: payload round-trip into fresh slug; existing slugs refused; identities documented as non-crossing.
15. Renderer: deterministic per head; size-bounded; branch column; divergence flag fires.
16. Keys: near-miss warns, brand-new silent; identity line on implicit resolution; `ambiguous_ledger` error lists open slugs.
17. Store resolution: nested clone in scratch workdir resolves to the clone; `--store` overrides for scoreboard children; both-kinds-in-ancestry prints the choice; `--store` beats `$LEDGER_DIR`.
18. gc: `--prune=now` loses nothing; CAS works on packed refs.
19. Bootstrap: fresh clone `ls` prints init/sync hint; `init && sync` fetches existing ledgers; late-added remote gets refspec on first sync; removed remote's tracking refs pruned.
20. Degraded: no remote ⇒ clean no-op; credential prompt ⇒ fast `credentials_needed` failure; tracking-vanished warning; mirror-wipe then push restores.
