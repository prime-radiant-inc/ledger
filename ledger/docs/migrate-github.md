# Migrating a GitHub backlog onto a board

A one-time bulk seed: every open GitHub issue becomes a key on a
ready-capable ledger board, each carrying a note that says where it came
from. This is a documented loop, not a verb — `ledger import` reads
whole-ledger JSONL and is the wrong tool for seeding a board.

Migrating 11 issues by hand took ~33 typed commands and lost one error in a
`jq` pipe. Use the loop.

## Before you start

- The board exists and is ready-capable — `ledger create issues --scope
  <ref> --field status=open,in-progress,closed,wontfix --terminal
  status=closed,wontfix --multi-field labels --multi-field blocked-by
  --guard status --guard blocked-by --stale-after <horizon>`.
- `ledger init` has run **at the repo root** and `gh auth status` is clean.
- You have decided the board is canonical for this work, or that GitHub is.
  A migration does not answer that question; it just moves the text.

## 1. Fetch the issues to a file

Fetch once, to disk. A file is reviewable before it writes anything, and
re-running the loop after a failure costs no API calls.

```
gh issue list --state open --limit 500 --json number,title > issues.json
```

`--limit` matters: `gh issue list` returns **30** issues by default and
says nothing about the ones it dropped.

## 2. Seed one key per issue

```sh
tab=$(printf '\t')
jq -r '.[] | [.number, .title] | @tsv' issues.json |
while IFS="$tab" read -r n title; do
  key=$(printf '%s' "$title" | tr 'A-Z' 'a-z' | tr -cs 'a-z0-9' '-' | cut -c1-64 | sed 's/^-*//; s/-*$//')
  ledger set "$key" status=open --expect none --idempotency-key "gh-$n" \
      -m "$title" --as migrator --ledger issues &&
    ledger note --key "$key" -m "migrated from issues/$n" --idempotency-key "gh-$n" \
      --as migrator --ledger issues ||
    { echo "STOPPED at issue #$n ($key)" >&2; break; }
done
```

**Check the exit code of every command.** That is what `&&` and `|| break`
are doing. A pipeline reports only its LAST command's status, so a `ledger`
call piped into `jq` looks successful no matter how it exited — the
migration this recipe comes from lost its `ledger init` error exactly that
way and spent an hour on a store that was never initialized.

**The `-m` on the seed IS the key's title**, carried by every listing
forever. Write a title ("fix the retry storm bug"), never a status update
("migrating issue 7"). `set <key> --rename "<new title>"` corrects a wrong
one, but a rename is a CORRECTION with attribution, not a workflow step —
it costs a permanent, attributed event. Get it right in the loop, where it
is one `jq` expression, rather than one rename per issue afterwards.

**Re-running is a resume, not a re-sync.** `--idempotency-key "gh-$n"`
makes an already-migrated issue a no-op (`deduped: true`), so after a
`STOPPED at` you fix that one row and re-run the whole loop. It also means
a title edited on GitHub after the migration does NOT propagate: the board
is its own record from here.

## 3. Check what landed, then publish

```
ledger show --ledger issues
ledger ready --ledger issues
ledger push
```

`show` lists a key per issue with its title; `ready` is the first read
every picker does. Nothing is visible to anyone else until `push`.

## What this deliberately does not migrate

Labels, assignees, comments, and issue bodies. Labels and assignees have
board equivalents (`labels`, and `--as` on the claim) but no honest
automatic mapping: a GitHub label is a taxonomy, `labels=human` on a board
is a stop sign. Add what you actually want in a second pass, per key.
Dependencies (`blocked-by`) are a judgment call per issue — seed them by
hand, edges before status where you can.
