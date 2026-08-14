# Sonnet usability test, round 2 — spike v2 (spec rev 10 surface)

2026-08-13. Twelve fresh Sonnet agents re-ran the three scenarios against spike v2, which implements rev 10's CLI surface contract (argparse help everywhere, slug validation, `no_open_ledger`, universal JSON, `create` reports its SHA, `-m` rendered in spine rows, `--require-evidence`, generalized enum fields, by-branch folds). New scenario content (temperature module; fruits/rivers/metals fleet; websocket memory leak) to prevent carryover.

## Headline: every round-1 friction class went to zero

| Round-1 finding | Round 2 |
|---|---|
| `--help` created real ledgers / dumped streams (3 agents) | Zero. Help worked everywhere; nobody even listed probing as friction. |
| Silent empty `ls` / raw tracebacks | Zero. Announced empties; argparse usage errors. |
| `create` doesn't return a cursor | Fleet parent used `create`'s id directly as the monitor cursor, cross-checked via cursorless watch's `starting_cursor`. |
| Positional/`--ledger` inconsistency ate a slug as a cursor | Zero occurrences. |
| JSON stragglers (`close`, `vocab`) | Zero. |
| Misleading zero-open `ambiguous_ledger` | Not hit (distinct error exists). |

Behavior quality also rose: **both writer agents adopted `--require-evidence` unprompted** (spine: `status=done`; fleet: same) with sensible one-line rationales; the round-2 cold resume verified claims *deeper* than round 1 (`git show --stat` per commit, re-ran documented CLI examples before asserting them in a README); the investigator invented key-prefix categorization (`repro-*`, `hypo-*`, `task-*`) and attached a ruling note to its specific task via `--key`.

## New findings (round 2's own crop)

1. **`note -m` + `--from-file` silently dropped the file body** — hit independently by two agents, both of whom caught it only via read-back verification and diagnosed it from source. Fixed mid-round (hard error `conflicting_body`). Spec rule: a note has exactly one body source; combining is an error, never silent precedence. This is the round's `create --help`: independent replication of a silent-loss trap.
2. **`show` vs `status` is underexplained, and per-key drill-down was guessed at** — the cold investigator tried `show <key>` (matching the spec's own `status [<key>]`, which the spike lags), burned two invocations learning it. Quickstart needs the distinction and a `status <key>` example.
3. **Evidence-requirement is invisible to readers.** The investigator couldn't tell whether the handoff ledger's bare-testimony rows meant evidence was optional or skipped. Reads should surface the schema: `show`'s identity header lists declared fields, vocabularies, and which values are evidence-required.
4. **Dry-run doctrine.** The fleet parent verified CLI syntax by writing test rows into its real scoreboard, then — with no delete — closed it as superseded and recreated. Its own lesson is the doctrine line: verify mechanics against a disposable scratch ledger, never the one real agents will consume.
5. Minor: role strings should be stated as free-form; `watch` timeout exit 2 reads as an "error" in shell-land and deserves its parenthetical kept prominent.

## Verdict

The rev-10 surface contract is validated: every safety and consistency fix eliminated its friction class on the first re-test. Remaining findings are one silent-loss rule (body sources), two quickstart lines, and one render addition (schema in the identity header) — all folded into the spec. Coordination, resume-and-verify, evidence discipline, and the generalized field model all worked cold at Sonnet level, twice.
