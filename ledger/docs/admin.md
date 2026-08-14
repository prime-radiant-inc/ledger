# Ledger admin runbook

This is the material `ledger init` points at but doesn't print in full: the
hazards that only matter to whoever administers a shared remote, not to an
agent doing day-to-day reads and writes. Agent-facing doctrine lives in
`ledger quickstart`; this file is for humans setting up or recovering a
shared store.

## What can destroy the remote copy

Ledger refs (`refs/ledger/*`) are ordinary git refs living alongside your
branches. Two operations can wipe them on the remote:

- **`git push --mirror`** from any clone that never fetched the ledger
  refspec. A mirror push makes the remote's ref set match the pusher's
  exactly — refs the pusher never fetched are refs the remote loses.
- **Force-pushes** to `refs/ledger/*`, whether accidental (a script that
  force-pushes everything) or a rebase tool that doesn't know these refs
  are append-only history, never branches to be rewritten.

Neither is ledger-specific; they're generic git hazards that happen to be
sharper here because these refs aren't visible in `git branch` and nobody's
watching them the way they watch `main`.

**Mitigation**: on any remote you control, set `receive.denyNonFastForwards`
(or the newer `--force-with-lease`-only workflow) and consider
`receive.denyDeletes` — see the tradeoff below before turning that on.

## `receive.denyDeletes`: the tradeoff

`receive.denyDeletes` on the remote blocks any push that deletes a ref,
which stops an accidental `git push --mirror` or `git push -d` from wiping
ledger history. It also blocks the one legitimate reason to delete a
ledger ref: secrets remediation (below), which needs a delete-and-replace
push against the remote to actually remove the secret from the shared
copy. Decide per remote: `denyDeletes` is the safer default; a remote that
expects to need secrets remediation should leave it off, or the admin
needs push access to toggle it off temporarily when an incident happens.

## Secrets incident runbook

Ledger events are immutable and, once pushed, fetched into every clone's
object database. There is no "delete the bad commit" — once a secret has
been written and synced, treat it as compromised. Steps, in order:

1. **Rotate the credential first.** This is the only true fix — nothing
   below undoes exposure, it only cleans up copies of a secret that no
   longer works.
2. **On every clone that has fetched**, before its next sync: delete the
   local ref that carries the secret —
   `git update-ref -d refs/ledger/<slug>`. Do this before the next sync,
   not after: a surviving local ref would re-plant the secret into the
   remote (and every other clone) the moment that clone syncs again.
3. **Push the deletion to the remote** to remove the shared copy. This is
   the operation `receive.denyDeletes` blocks — if the remote has it set,
   an admin needs to disable it temporarily to land this push, then
   decide whether to re-enable it.

None of this scrubs the object database of clones that already have the
blob — git's reflog and unreferenced objects can keep it around locally
for a while (`git gc --prune=now` is the closest thing to a purge, and even
that isn't a guarantee). Rotation is what actually neutralizes the
exposure; ref surgery just stops the ledger from continuing to hand it to
future readers.
