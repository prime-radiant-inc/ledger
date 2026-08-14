# Sonnet usability test, round 3 — spike v3 (envelope + hints)

2026-08-13. Eight fresh Sonnet agents, new scenarios (word-stats module; trees/birds fleet; API-gateway 502 investigation), against spike v3: one JSON document per command with a consistent `ok`/`id`/`ledger` envelope, every error a `{error, message, hint}` triple with a copy-pasteable fix, `status <key>` drill-down, `show` with schema + head cursor, human age strings.

## Result: zero tool errors, eight for eight

No agent hit a single unexpected error, in any scenario. The only errors that occurred were **deliberately triggered** — two agents probed the vocab and evidence-required rejection paths on purpose, on scratch ledgers, and both reported the hints worked verbatim ("running the hinted `vocab add` command exactly as printed worked, and the retried `set` then succeeded").

## What the round proved about the v3 changes

- **The envelope earns its keep in loops.** Monitor: "a shell loop can unconditionally do `cursor=$(... | jq -r .cursor)` and feed it into the next `--since` without branching on success/timeout." Spine worker: "each write returns `{ok, id, ledger, key, fields}`, so I always knew what got recorded without a follow-up show."
- **Hints close the loop.** Both deliberately-triggered error classes were resolved by pasting the hint. The ambiguity hint (naming each open ledger with scope + freshness) was singled out as "no lookup needed."
- **`--help` completed its arc**: round 1's agents read the tool's *source* to learn it; round 3's used per-verb `--help` productively and safely.
- **Doctrine compounds.** The scratch-ledger dry-run rule (added after round 2's scoreboard pollution) was followed by both writer agents unprompted — one even ran its deliberate error probes there. The handoff writer made the round's best judgment call: it *declined* `--require-evidence` because the backstory offered no real artifacts, refusing to fabricate `run:` refs, and marked each claim "no run log retained, rerun X to reverify" — which the cold investigator then correctly relayed as "testimony, not artifact-backed; rerun before relying on it."
- **Cold reconstruction is now frictionless**: the investigator answered all six briefing questions via exactly the quickstart's suggested path (`show`, then `notes -k handoff --latest`) and rated reconstruction "essentially frictionless."

## Residual findings (all documentation lines, no code)

1. Quickstart: state that `set` auto-creates keys on first use; show a worked example of *writing* a handoff note (`note -k handoff --key <next-task> --from-file ...`); note `close`'s slug is positional; note closed ledgers leave default `ls`; warn against aliasing the invocation into an unquoted zsh variable (third occurrence across rounds); note short commit SHAs are fine as evidence refs; show `status <key>` composed with `--ledger`.
2. One design observation, acceptable as-is: reusing a single `status` vocab across conceptually different row types (repro, ruling, trap, hypothesis) makes values like "confirmed" mildly ambiguous — each row's `-m` annotation disambiguated instantly, and generalized fields already offer the fix (declare a second field) when an author wants it. Convention guidance, not machinery.

## Three-round trajectory

| | R1 (naive spike) | R2 (surface contract) | R3 (envelope + hints) |
|---|---|---|---|
| Agents | 9 | 12 | 8 |
| Scenario success | 6/6 | 6/6 | 6/6 |
| Unexpected tool errors | many (2 write-hazard incidents) | 1 class (silent body drop, ×2 agents) | **0** |
| Agent workaround style | read the source | read `--help` | paste the hint |

The spec's CLI surface contract, response envelope, and hint discipline are now empirically grounded three rounds deep. The tool teaches itself: cold Sonnet agents go from quickstart to correct multi-agent coordination with no human help and no source-reading.
