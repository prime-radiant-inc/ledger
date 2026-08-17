# Ledger sync (design)

2026-08-17, revision 1. Extends the tool spec
(`2026-08-13-ledger-tool-design.md`, rev 14) and the issues spec
(`2026-08-15-ledger-issues-design.md`, rev 17). This is Plan 2 —
sharing ledgers across machines — designed to three requirements set by
the operator: it works offline (reduced guarantees accepted), it is
simple to understand, and any host reading a synced ledger after the
fact produces exactly the same output as any other.

The whole design in one breath: **sync is a fetch, a deterministic
merge commit, and a push; everything else is the fold learning to read
a DAG.** No daemon, no locks, no leader, no new storage, no new
configuration store.

## The model

Your machine is always right about itself. Sync is two couriers
swapping mail. If two writers touched the same dial while apart, the
board says so at the next sync, and the ordinary recovery idioms apply.

Stated as guarantees:

- On one replica, guarded writes are race-proof exactly as today —
  nothing about local operation changes.
- Across replicas, guarded writes are race-proof for everything both
  sides have seen. Work done while apart can collide; a collision is
  never lost, never silently resolved, and always surfaced
  (`contested`, below).
- `--expect` is a promise about your replica. A merge may reveal that
  another replica superseded you; the record shows both, and the fold's
  one ordering rule decides current state identically everywhere.

## Remotes are git remotes

There is no ledger-specific remote configuration.

- A repo-embedded store syncs to the repo's own remotes; `sync`
  defaults to the current branch's upstream remote, else `origin`
  (`--remote <name>` overrides).
- A standalone store (`.ledger.git`) uses its own git remote config,
  set with plain `git remote add` against that git dir.
- Ledger refs live under `refs/ledger/*`, which default refspecs do not
  carry; `sync` supplies `refs/ledger/*:refs/ledger/*` explicitly.
  Hosts accept such refs; their UIs simply don't show them.

**Sharing is the act of syncing, not a setting.** `ledger sync <name>`
shares a ledger the first time it is named; bare `ledger sync` syncs
every local ledger that already exists on the remote — the remote's own
ref list is the configuration, inspectable with plain git
(`git ls-remote <remote> 'refs/ledger/*'`). One warning line in the
skill: syncing a ledger to the project's origin shares it with everyone
who can read the repo — investigation ledgers on public repos deserve a
thought first.

## The sync verb

`ledger sync [<name>] [--remote <r>]`, per ledger:

1. Fetch the remote ref.
2. If either side is an ancestor of the other: fast-forward. Done.
3. Otherwise mint the **deterministic merge commit**: parents sorted by
   SHA; committer and author pinned to a fixed identity ("ledger-sync");
   commit timestamp = the later of the two parent tips' committer
   times; empty tree delta (a merge commit carries NO event — it is a
   pure join, and the fold skips it). Determinism means two replicas
   merging the same two heads mint the identical SHA and converge
   without coordinating.
4. Push with `--force-with-lease` against the fetched value; on
   rejection (the remote moved), refetch and repeat — the CAS retry
   loop, remote edition. Bounded attempts, then a clean error telling
   the user to re-run.

Local writes are never blocked by sync state; `sync` is never run
implicitly. Doctrine, not machinery: sync when you sit down and when
you finish something — the `git pull`/`git push` reflex.

Merges of merges need nothing special: step 3 is always a two-parent
join of the local and remote heads, whatever shape lies beneath them.
Many replicas converge pairwise through the shared remote (a star, with
the remote at the center, purely by usage — the tool has no topology
concept).

Export/import is unchanged and different on purpose: import re-mints
identities into a new store; sync converges replicas of the SAME store,
and IDs are stable across it forever.

## History is a DAG; reading is linear

Merges never rewrite anything. The record permanently keeps the shape
of what happened — including "these writes did not know about each
other," which is evidence, not noise. Event IDs (commit SHAs) never
change.

Every reader linearizes the DAG with ONE rule, stated once: **ancestry
first; concurrent events by event timestamp; exact ties by SHA.** All
derived surfaces (latest-per-field, titles, the envelope, the frontier,
staleness inputs) fold over that order unchanged. `tail` and `status`
show the derived sequence — a sequence is what humans read — and the
raw DAG stays underneath for anyone who asks git.

`Events()` learns to walk multi-parent histories (deduplicating by SHA)
and emit the pinned order; nothing downstream changes.

## `contested`: partition races are fold-derived, not sync-recorded

Sync writes no markers. The DAG already encodes concurrency, so the
fold computes it, the way it computes staleness and cycles:

- **Definition**: two or more writes to the same GUARDED field of the
  same key that are concurrent (neither an ancestor of the other) and
  not superseded — no later write to that field has all of them as
  ancestors.
- **Surface**: an `attention` entry
  `{reason: "contested", key, field, ids: [...], by: [...]}` carrying
  the competing events' ids and authors, titles per the stale-claim
  convention; drives `attention-needed` like any attention item.
- **Clearing**: any subsequent write to that field lands after the
  merge, supersedes all competitors, and the entry evaporates — the
  corrective write is the resolution, made with the existing Recovery
  idiom (handoff note, corrective guarded write, message naming what
  happened). For contested claims specifically: one claimant keeps the
  key (their `--expect` is the fold-order winner's id), the other's
  work arrives as a handoff note — trial-1's duplicate-work outcome,
  now detected by mechanism instead of luck.

Unguarded fields (labels) cross-partition behave exactly as unguarded
fields behave on one store: last write in fold order wins, both writes
stay in history, nothing is flagged. That is the existing stated trade,
extended; the Label-edit idiom's optional CAS still protects
same-replica races.

## Determinism is a requirement, not a property

**Every read verb's output is a deterministic function of
(chain, evaluation time).** Same chain + same evaluation time = same
bytes, on any host, always. Enforced two ways:

- Every read verb gains `--at <ts>` (the pinned UTC layout) fixing the
  evaluation time for age/staleness math; omitted means now. With
  `--at`, output is byte-reproducible forever — audits can re-render
  the board exactly as it stood at any moment, from any replica.
- A standing determinism test: clone a store, run every read verb on
  both copies with a fixed `--at`, diff the bytes. Any future feature
  that sneaks host state into a read path fails it.

Clocks get one honest sentence: event timestamps are recorded by the
writer and travel with the chain (identical everywhere); cross-host
staleness and the fold's concurrent-event ordering assume NTP-class
clock sync, which is why board horizons are hours, not milliseconds. A
writer with a badly wrong clock mis-orders only against events it was
already concurrent with — ancestry always wins first.

## What sync deliberately does not do

Auto-sync (hooks, watchers, daemons); partial or filtered sync; sync
topology (mesh, relay); encryption at rest; permissions beyond the
remote's own access control; conflict resolution beyond surfacing
(`contested` names the race; people and doctrine resolve it); rebasing
or history rewriting of any kind, ever.

## Implementation scope

`sync` verb (fetch/ff/deterministic-merge/lease-push with retry);
DAG-aware `Events()` with the pinned total order; `contested` in the
fold + envelope + one skill paragraph (break-on-sight style: the entry
carries the competing ids; resolution is one Recovery write);
`--at <ts>` on every read verb; the determinism test; refspec plumbing
and remote defaulting; skill section for the sync habit + the sharing
warning; the two-replica trial.

## Test plan

1. Fast-forward sync both directions; no-op sync is a no-op.
2. Divergent sync: both replicas mint the IDENTICAL merge commit SHA
   independently; both converge; no history rewritten (all pre-merge
   SHAs stable).
3. Fold order over a merged DAG: ancestry beats timestamp; concurrent
   events order by ts then SHA; derived state identical on both
   replicas (byte-diff of every read verb with fixed `--at`).
4. `contested`: concurrent guarded writes to one key/field on two
   replicas → after sync, both replicas show the identical contested
   entry; frontier `attention-needed`; a corrective write clears it on
   both after the next sync; non-concurrent (sequential-with-sync)
   writes never flag.
5. Unguarded concurrent writes: fold-order winner identical on both
   replicas; no flag; both events in history.
6. Claims across partition: both replicas claim the same key offline →
   contested after sync; the fold-order winner's id is what a
   subsequent `--expect` must name; the loser's close attempt gets
   `claim_lost` naming the winner.
7. Push race: two replicas syncing simultaneously → lease rejection →
   retry converges; bounded-retry error path exercised.
8. `--at`: byte-identical output for a fixed past ts across replicas
   and across repeated runs; age/staleness math honors the pinned time.
9. Refspec/remotes: repo-embedded store syncs to branch upstream by
   default; standalone store uses its own git remote; `sync <name>`
   shares a previously unshared ledger; bare `sync` picks up exactly
   the remote's ledger ref list.
10. Determinism guard: the clone-and-diff test over every read verb.
11. Doctrine: the skill's sync section commands execute verbatim
    (doctrine-test pattern); the sharing warning is present.
12. Merge commits carry no event and never surface in `tail`/`notes`;
    `Events()` deduplicates across merge parents.

## Trial plan

Two checkout directories against one bare "remote" directory; a staged
partition (both sides claim, close, label, and break cycles offline);
sync; chain-audit: exactly one keeper per contested key, byte-identical
envelopes on both replicas afterward, IDs stable throughout. Fleet
agents drive both replicas per the existing trial pattern.
