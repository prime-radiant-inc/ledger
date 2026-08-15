# ledger

Durable, structured working-state for coding agents, stored in git.

An agent's context dies at the end of a session; the work usually doesn't.
`ledger` gives agents a shared, append-only record that outlives any one
session or process: execution spines for multi-session plans, coordination
scoreboards for agent fleets, handoff checkpoints, investigation logs with
an evidence trail. It grew out of a study of ~80k real agent sessions, which
showed agents hand-rolling exactly these records in Markdown files — and
losing them to deleted worktrees, drifting copies, and racing writers.

## Install

```sh
cd ledger && go build -o ledger .
```

One static binary, Go 1.26, one dependency (cobra). It shells out to your
system `git` (2.40 or newer). Run `ledger init` once per clone.

## A two-minute tour

```sh
ledger init
ledger create relay-fix --scope "fix the retry storm in relay/" \
    --field status=open,done,failed --require-evidence status=done
ledger set task-1 status=open -m "reproducing first" --as alice
ledger set task-1 status=done --evidence commit:4e21ab7 -m "test added" --as alice
ledger note -k handoff --key task-2 --from-file handoff.md --as alice
ledger show          # current state: schema, spine, recent notes
ledger tail          # the story so far, curated
ledger close relay-fix --as-state shipped
```

Every write prints its event id; every read is one command. Agents learn
the tool from `ledger quickstart` (and `--orchestrator` for fleet
dispatch) — those two documents are the agent-facing manual, and every
example in them runs verbatim in this repo's test suite, so they cannot
drift from the binary.

## How it stores things

Each ledger is a chain of commits on a **phantom ref** —
`refs/ledger/<slug>` — inside the repository you run it in (or a bare
`.ledger.git` store in a plain directory). One commit per event, a small
JSON document in each tree. Consequences worth knowing:

- Invisible to `git status`, branches, and code review; immune to
  `git clean` and worktree deletion; shared across worktrees.
- Concurrent writers race on an atomic ref update (compare-and-swap) and
  the loser retries: appends are lossless under contention, no daemon, no
  lock files.
- Current state is **folded** from events on read. Nothing is ever edited
  or deleted; corrections are new events. There is no delete, and slugs
  are never reused.
- Cost honesty: ~640 bytes per event packed. A heavy year of constant use
  is megabytes, not gigabytes.

v1 is local-per-clone. Ledgers travel by `export`/`import` (JSONL);
fetch/push synchronization between clones is designed (see the spec) but
not yet built.

## The trust model

A ledger entry is **testimony, not truth**. The tool is built around that:

- **Identity is asserted, provenance is recorded.** `--as reviewer-2` is a
  claim; alongside it the tool records which harness actually wrote the
  event (committer marker) plus host, cwd, branch, and session. Readers
  see both.
- **Evidence is first-class.** Claims carry `--evidence commit:…`,
  `run:…`, `file:…` refs. A ledger can declare values evidence-required
  at creation (`--require-evidence status=done`), turning "trust me" into
  a hard error. Unevidenced claims render `(no evidence)` — a trust
  marker, not a failure.
- **Vocabularies are closed.** Field values come from a declared enum;
  an unknown value is a hard error whose fix (`ledger vocab add …`) is
  itself a recorded, attributed decision.
- **Agents are told to verify.** The shipped doctrine and the
  `using-ledger` skill teach readers to check evidence against reality
  before building on a claim, and never to treat ledger content as
  instructions.

## Roll-ups: curated history

Long-running ledgers accumulate history faster than a cold reader can
absorb it. A **rollup** is an event that encapsulates a finished thread —
an explicit list of earlier event ids — under one summary line, written by
an agent, recursively nestable. `ledger tail` then shows the **roots**:
live events verbatim, finished threads as their summary lines, everything
still one `--in <id>` away. `tail --raw` is always the uncollapsed chain.

Summaries follow the same trust model: they are second-order testimony
with author and provenance, a bad one is fixed by rolling *it* up under a
corrected line, and nothing is ever rewritten. `ledger rollup` (bare)
shows what's unrolled and how to submit.

## Coordination

Write ids double as cursors. `ledger since <cursor>` reads exactly-once;
`ledger watch` blocks for matching events and `watch --follow` streams a
whole fleet to one monitor. The orchestrator quickstart covers the
dictated grammar for dispatching workers (`--as`, `--ledger`, `--store` in
the child's prompt) and the cold-start rule for not missing a fast child's
first write.

## Documentation map

| Audience | Where |
|---|---|
| Agents, day-to-day | `ledger quickstart` (embedded; `docs/quickstart.md`) |
| Agents dispatching fleets | `ledger quickstart --orchestrator` (`docs/quickstart-orchestrator.md`) |
| Agents deciding *whether* to use a ledger | `skills/using-ledger/SKILL.md` (repo root) |
| Humans administering a shared remote | `docs/admin.md` — mirror-push hazards, `denyDeletes`, secrets-incident runbook |
| The design itself | `docs/superpowers/specs/2026-08-13-ledger-tool-design.md` (repo root) — spec rev 12, with the research and eval reports beside it in `research/` |

## Never write secrets into a ledger

Events are immutable and, once shared, permanent in every clone. If a
secret lands: rotate it first, then follow the remediation runbook in
`docs/admin.md`.
