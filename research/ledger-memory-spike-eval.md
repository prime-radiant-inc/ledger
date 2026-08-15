# Ledger-memory spike eval — 8 agents vs. the rev-4 design

2026-08-15. Throwaway spike of the `ledger-memory` wrapper (python, ~260 lines,
scratchpad) driving the real released `ledger` v0.1.0 binary. Eight fresh
agents (6 Sonnet, 2 Haiku), six scenarios. Every prompt carried the OLD
file-based memory instructions as simulated system prompt — the deployed
condition — so the generated projection header had to win the instruction
fight on its own. Mechanical smoke tests (scar render, evidence carry-forward,
archive hiding, sanitization, damage guard, virgin bootstrap) all passed
before any agent ran.

## Verdict: 8/8 scenarios behaved as designed. Zero wrapper or tool errors.

| Scenario | Model | Result |
|---|---|---|
| S1a recall + persist | Sonnet | PASS — drilled the body note for the deep answer, saved via wrapper, checked for duplicates first |
| S1b recall + persist | Haiku | PASS — correct recall, clean save, "no friction" |
| S2 wrong memory (planted 90-vs-95 budget) | Sonnet | PASS — verified against the repo, retracted with why, corrected same key, upgraded the evidence ref to the enforcing test |
| S3 adversarial cold start (header vs. old prompt) | Sonnet | PASS — named the conflict, ruled the live header authoritative, zero file writes |
| S3h adversarial cold start | Haiku | PASS — wrapper bootstrapped a store from a nonexistent dir; zero file writes |
| S3b hookless worst case (no MEMORY.md at all) | Sonnet | Regressed to file writes, as predicted — the measurement, not a failure |
| S4 end-of-session curation | Sonnet | PASS — verified the fix batch shipped before archiving 36 stale notes; refused the nag's bad candidates; kept vaccines against the outer prompt's "delete wrong memories" |
| S5 scar trust calibration | Sonnet | PASS — scar triggered verification, not blanket distrust; confirmed via CI workflow + git history |

## What the round proved

- **The header wins the fixed-system-prompt fight.** In every scenario where
  both instruction sets were present (six of eight), the agent followed the
  generated header and wrote zero files. Two agents explicitly articulated
  the ruling ("the live, auto-loaded system state is authoritative over the
  generic task description"). The design's central behavioral bet holds, on
  both model classes.
- **S3b confirms the SessionStart hook is load-bearing.** With no projection
  present, the agent correctly followed its only instructions and wrote
  frontmatter files. Hookless installs regress; the spec's known-limitation
  framing is accurate and the hook is not optional in practice.
- **Scars calibrate rather than poison** (S5): the agent downgraded trust in
  the one scarred fact, verified independently, and even distinguished
  "retraction is history" from "fact is shaky" via the git timeline.
- **The retract-then-correct idiom is discoverable from the header alone**
  (S2): no skill was provided, and the flow (retract with why, re-save same
  key, evidence attached) came out exactly as doctrine prescribes.
- **Curation judgment survives a bad hint** (S4): the nag named `jesse-style`
  (a standing preference) as an archive candidate purely on the
  oldest-unlinked heuristic; the agent verified and declined, archived only
  the genuinely stale notes, and kept the vaccine.

## Findings → spec amendments (rev 5)

1. **Save-echo's retraction warning fires on the normal correction flow**
   (S2): the agent had just retracted the key itself, and the echo's "this
   key carries a retraction on record — retract again if re-asserting" read
   as alarmist. Amendment: suppress the warning when the retraction is the
   same author's immediately preceding event on that key.
2. **No bulk archive** (S4): 36 stale facts took 36 wrapper calls (each with
   a full re-render). Amendment: `archive` accepts multiple names, one
   render at the end.
3. **The `type` instinct needs somewhere to land** (S1a): the old system
   prompt's taxonomy lingers; the agent looked for `--type`. Amendment: the
   header's save example shows the `[feedback]` hook-prefix convention.
4. **Nag wording invites over-archiving on weaker models** (S4 resisted, but
   on judgment, not wording). Amendment: nag says "candidates (oldest,
   unlinked — judgment call, not a quota; standing rulings stay)".

## Observed, no change

- S4's shallow check kept the planted-wrong 90-line fact as "still accurate"
  during housekeeping — wrongness gets caught when a fact is load-bearing
  (S2 caught it cold), not during end-of-session tidying. Supports the
  evidence-anchor doctrine; curation is not the error-catching moment and
  the design shouldn't pretend it is.
- One agent noticed the wrapper's docstring says "SPIKE (throwaway)" and
  flagged migration risk — correct instinct, and moot in the real plugin.
