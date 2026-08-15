# Rollup acceptance re-run — real binary (spec test 44)

2026-08-14, ~1 hour after the rollup merge (main @ 2913611). Three fresh agents against the
built Go binary with `using-ledger` as entry point and `ledger quickstart` as mechanics:
Sonnet curator and Sonnet corrector on the same preloaded 23-event investigation (corrector's
copy carried the planted inverted rollup), Haiku cold reader on the curated result. Preload
adapted from the spike eval unchanged except the binary path — the spike and shipped CLI
grammars aligned exactly, which is itself a validation.

## Verdict: test 44 PASSES

All three scenarios completed. Zero tool defects. Four errors occurred across the round,
every one either deliberate machinery working as designed or self-inflicted and
self-recovered within one retry:

- The corrector's correction line tripped the **new 300-byte summary bound** (`bad_value`,
  "got 311") and the hint fixed it in one retry — a fix-wave feature validated live within
  the hour of merging.
- The curator passed `--as` to `show` (`bad_usage`, dropped it) and ran one command without
  `cd` (`unknown_ledger`, re-ran with cd) — both self-diagnosed, the second matching the
  skill's own store-scoping line.

## What the round proved

- **The corrector caught and fixed the planted inversion cold**, exactly as in the spike
  round: drilled the suspect rollup, compared summary against children and the live spine,
  rolled it under a corrected line `--as resumer`, verified `status` untouched. It also
  used the fix-batch's `shadowed_store` breadcrumb correctly and — the best honesty note of
  the round — declined to mark evidence "verified" because the synthetic environment's
  artifacts don't exist on disk, flagging that instead of papering over it.
- **The curator exercised real judgment, not maximal compression**: three thread-shaped
  rollups (repro, refuted cache theory, root-cause diagnosis), while *deliberately* keeping
  the standing ruling, both environment gotchas, and the finished-but-load-bearing
  fix-design thread visible for the next implementer. It applied the bridge-note rule
  straight from bare `rollup`'s guidance payload (the F1 JSON fix earning its keep) and
  verified after each write that `show`'s rows were untouched.
- **The Haiku reader briefed accurately without ever drilling a rollup** — `show` + notes
  first, then (novel this round) reading the phantom-ref commits directly with `git show`.
  It independently caught the evidence-adjacency nuance (the refuted-hypothesis row is
  itself unevidenced; the graph lives on the metrics task) that a prior Sonnet reader also
  caught — the distinction is legible across model classes.

## Findings (all documentation-weight)

1. **`rollup_due` reads as an obligation.** The curator finished with due=8 and wondered if
   that meant "8 more things you should roll" — the count includes records it deliberately
   kept visible. Fixed this round: the skill now says the count is unrolled records, not a
   quota, and keeping load-bearing records loose is correct curation.
2. **The bridge-note rule lived only in bare `rollup`'s guidance.** Promoted to the skill's
   curation paragraph this round (quickstart is at its 95-line budget; the skill has none).
3. Which verbs accept `--as` is implicit (quickstart says "on every write"); the `bad_usage`
   error self-corrected in seconds. Noted, no change — the general flags rule covers it.
4. Non-contiguous child selection around a kept-loose record (roll a, skip b, roll c)
   worked and interleaved correctly in the roots view — undocumented but behaved as hoped.

| | Spike round (v4 prototype) | Real-binary round |
|---|---|---|
| Agents | 8 (6 Sonnet, 2 Haiku) | 3 (2 Sonnet, 1 Haiku) |
| Scenario success | 8/8 | 3/3 |
| Tool defects | 0 | 0 |
| Planted-error catch | caught + corrected | caught + corrected (and tripped the new size bound en route) |
| New feature surfaces exercised | — | 300-byte bound, guidance payload, shadowed_store breadcrumb, causal roots order |
