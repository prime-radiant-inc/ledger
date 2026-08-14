# ledger quickstart — orchestrator

You're dispatching and coordinating other agents. Read `ledger quickstart`
first — this section only adds the fleet-dispatch layer on top of it.

## Dictated grammar

A dispatched child doesn't share your shell or your cursor. Every child
prompt you write must dictate, explicitly, in the text the child reads:

- **`--as <role>`** — the child's identity on every write it makes.
  `$LEDGER_AUTHOR` only substitutes for this when you control the child's
  env directly (e.g. an env var you set on a subprocess you spawn) — a
  Task-tool child that only reads a prompt string needs the flag stated.
- **`--ledger <slug>`** — which ledger the child reads and writes. Once
  you and your children share a store, more than one ledger is usually
  open at once (your own spine plus the fleet scoreboard, say), and
  ambient resolution stops being unambiguous the moment it is.
- **`--store <path>`** — where the store lives, if it isn't the child's
  own working directory's ancestry (a child spawned into a scratch
  workdir needs to be told explicitly).

## `watch --follow` is the fleet monitor

One long-lived call watches the whole fleet instead of you polling each
child's ledger in a loop: `ledger watch --follow --ledger <slug>` streams
one JSON line per matching event, forever, until you kill it. Each line
carries its own `id` — treat it as that event's resume cursor if the
monitor restarts. `--follow` implies no timeout; combining it with an
explicit `--timeout` is a bad-value error.

## Cold-start rule

A cursorless `watch` starts at the *current* head — if a child's first
write can land before your first `watch` call runs, that write is
invisible to a plain `watch --follow` started after dispatch. Seed
`--since` from `create`'s reported event id (captured before you spawn
anything), or start your `watch` running before you spawn children, never
after.

## Plan-tool coexistence

A plan or todo-list step can cite a ledger entry as durable authority
instead of re-deriving state from scratch: "see `ledger status task-3`"
outlives the current context window the way an in-memory todo item
doesn't. The plan tool still owns the step sequence; the ledger owns the
verified state each step left behind.

## Walkthrough — a coordination scoreboard alongside the quickstart's own ledger

`qs-demo` from `ledger quickstart` is still open here, so this is also a
live demonstration of rule 2: with two ledgers open, `--ledger` stops
being optional.

```
ledger create qs-fleet --scope "fleet coordination scoreboard (quickstart demo)" --owner orchestrator -m "spawns workers, tracks status here"
ledger set worker-1 status=open --as orchestrator --ledger qs-fleet -m "child dispatched"
ledger status  # expect: exit 4 error ambiguous_ledger
ledger status --ledger qs-fleet
ledger watch --ledger qs-fleet --timeout 1  # expect: exit 2
```
