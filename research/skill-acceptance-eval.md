# Skill-acceptance eval — `using-ledger` as entry point (spec test 38)

2026-08-14. Six fresh Sonnet agents, three new scenarios, against the **real Go binary**
(built from main @ 8b855fd). Entry point: `skills/using-ledger/SKILL.md` — agents got the
skill file, the binary path, and a work assignment; command mechanics reachable only through
`ledger quickstart`, exactly as shipped.

Scenarios: **spine** (csvstat module: writer implements 2 of 4 plan tasks and checkpoints;
cold successor resumes, verifies, finishes, closes), **fleet** (field-guide index job:
orchestrator seeds scoreboard and plays two workers; independent monitor reconstructs the
timeline from cursor-carried watches), **investigation** (gateway 502s: investigator
reproduces a planted timeout bug, records repro/hypotheses/handoff; cold investigator
answers a six-question briefing from the durable record alone).

## Verdict: test 38 PASSES

All three scenarios completed end to end. 6/6 agents finished their assignments. One agent
hit one tool error, once, and self-recovered inside a minute. The skill's seven patterns were
followed unprompted and correctly at every stage that mattered.

## What the round proved

- **The skill routes; the quickstart teaches; nothing else was needed.** Every agent read
  the skill, then ran `ledger quickstart` (the orchestrator also found `--orchestrator`),
  then worked. No agent read source, no agent guessed syntax beyond the one gotcha below.
- **Verify-before-trust is now observable behavior, three ways.** The spine successor ran
  `git show` on both cited evidence commits and reran the test suite before building on the
  claims. The fleet monitor verified a worker's claimed artifact against the actual file
  only after the fleet finished. Best of all, the cold investigator **caught an unbacked
  claim the predecessor had not flagged** (a "16/50 timed out" figure with no retained
  artifact anywhere), grepped for it, demoted it to testimony, and told the next reader to
  re-run before citing it — while accepting the predecessor's *disclosed* unretained
  numbers as honestly-labeled testimony. That is the evidence-chain doctrine working
  end to end, cold, with no human in the loop.
- **Correction-by-append is intuitive.** The spine writer cited a wrong evidence commit,
  caught it on reread, and fixed it by just re-running `set` — then documented the trap in
  its handoff, which the successor read and benefited from.
- **The fleet contract held under real concurrency.** Monitor and orchestrator ran
  simultaneously; the monitor cold-discovered the ledger by polling `ls`, drained history
  with cursorless `since`, then followed with `watch --since <cursor> --timeout N` loops —
  exit 0/2 handled cleanly — and reconstructed a 5-event timeline with authorship, all
  matching the orchestrator's report.
- **Evidence-required adoption continues** (both writers declared `--require-evidence
  status=done`/`confirmed` unprompted), and the investigator front-loaded `vocab add` for
  its epistemic statuses by reading the skill's investigation section.

## The one tool error, and a discovery gap the round exposed

1. **Positional slug on reads** — the spine successor ran `ledger show csvstat` and got
   `{"error":"unknown_verb","hint":"run `ledger --help` for the verb list","message":"unknown command \"csvstat\" for \"ledger show\""}`.
   The same mistake was made independently by gpt-5.6-luna in the Codex probe. Reads take
   `--ledger`; `set`/`close` take positionals; the instinct transfers wrongly, and the error
   misclassifies a bad positional as an unknown *verb* with a hint that doesn't name
   `--ledger`. **Fix candidate #1:** reads that receive one unexpected positional should
   return `bad_usage` with hint `did you mean: ledger show --ledger <arg>?`.
2. **Silent store shadowing** — the investigator ran `ledger init` from the sandbox root
   (not the project repo), creating a bare `.ledger.git` there; the 12-event investigation
   ledger landed in it. From inside `proj-gateway`, every read then resolves to the repo's
   own empty store: `ls` prints "no ledgers", and `unknown_ledger`'s hint (`ledger ls
   --all`) dead-ends in the same empty store — nothing ever mentions the ancestor
   `.ledger.git` two levels up. Spec test 17 promises "both-kinds-in-ancestry prints the
   choice"; it does not fire in this layout (repo `.git` at level 0, bare store at level 2).
   The cold successor escaped only because its shell happened to start where the store
   lives. This is the phase-1 discovery failure mode reproduced live with the v1 tool.
   **Fix candidate #2:** when resolution lands on a store while a *different-kind* store
   exists higher in the ancestry, say so — at minimum in `ls` empty output and in the
   `unknown_ledger`/`no_open_ledger` hints.

## Documentation findings (no code)

1. Skill says "seed a **pending** row per worker" but the default vocab is
   `open/done/failed/blocked`; the quickstart example uses `open`. Align the prose to `open`.
2. `close --as-state` values (`shipped|abandoned|superseded`) appear nowhere in skill or
   quickstart body; two agents ran `close --help` to discover them.
3. `watch`'s non-follow exit convention (0 = events, 2 = timeout with cursor) is only
   discoverable empirically; one line in the quickstart's monitor section would save a probe.
4. `since` with no cursor drains from the start — `[cursor]` is optional in help but the
   default is unstated.
5. Store scoping deserves one explicit line: commands resolve against the repo you're
   standing in (the spine writer got `unknown_ledger` from a parent directory; the
   investigator's init-from-the-wrong-cwd caused finding #2 above).
6. Minor observations, acceptable as-is: `--require-evidence` accepts values not yet in the
   vocab at create time (validated only at `set`); `show` rows carry latest-value-only, with
   `tail` as the history view (the cold investigator found this in one step); no guidance on
   reusing a claim key vs. minting a fresh one for handoff notes (the investigator chose the
   open-thread key, which read well downstream).

## Cross-harness note

The parallel Codex probe (`codex-probe.md`) adds: `CODEX_THREAD_ID` is per-session distinct
and inherited (test 6b **pass**); Codex's default seatbelt keeps `.git` read-only, so
sandboxed Codex cannot write a phantom-ref store (fails cleanly as `git_failed`); nested
harnesses resolve the marker to the outermost env that survives (documented limitation).

## Scoreboard vs. prior rounds

| | R3 (spike v3, quickstart entry) | Skill round (real binary, skill entry) |
|---|---|---|
| Agents | 8 | 6 |
| Scenario success | 6/6 | 6/6 |
| Unexpected tool errors | 0 | 1 class, 1 occurrence, self-recovered |
| New failure surface | — | store shadowing (orchestration-level, not agent-blocking) |
| Verification behavior | evidence checked | evidence checked **and** unbacked claim caught |
