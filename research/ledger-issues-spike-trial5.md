# Issues-tracker spike trial 5 — self-service cycle breaking on a bedlam board

2026-08-16. Spike (branch `wip/issues-cycle-spike`, commit 0da3acb, atop
the finished rev-16 build) implements the proposed cycle redesign: cycles
detected among ALL non-terminal keys regardless of claim or label, and
every cycle attention entry carries its own paste-ready fix — `{break:
{key, drop, keep, expect, human}}`, the suggested edge being the youngest
in the loop, `expect` the CAS ticket, `human: true` when the break target
is human-labeled. Trial: a deliberately maximal board — six cycles
(a 2-cycle through a live claim whose tool suggestion was staged to be
semantically WRONG; a 2-cycle through a human-reserved key; an
overlapping ring-plus-chord; a self-edge; a doubled-edge pair), plus a
stale ghost, a statusless orphan blocking real work, an
unevidenced-wontfix prerequisite, and honest tasks. Three workers: two
Sonnet (sable, wren), one Haiku (moss). Doctrine v5: any agent breaks
cycles immediately, no permission, suggestion-or-better with reasons in
`-m`.

## Verdict from the chain (audited, not prose)

**The design worked. All six cycles were broken by agents in about
ninety seconds of board time, with zero double-closes (ten done.log
lines, ten unique keys) and zero improper writes to human-reserved
work.** Specifics:

- **The judgment trap was beaten.** The tool suggested dropping the
  youngest edge — staged to be the TRUE dependency ("API depends on
  schema"). Wren (Sonnet) read the titles, rejected the suggestion,
  broke the reverse edge instead, said why in `-m`, and then closed both
  tasks in correct dependency order. Sable independently investigated
  the same cycle and reached the same conclusion. The doctrine's
  "apply the suggestion or a better one" clause is field-validated —
  by the strongest available evidence, an agent overriding the tool for
  stated semantic reasons.
- **The human-cycle path worked, on the smallest model.** Moss (Haiku)
  executed the suggested break against the human-reserved key with
  `--override`, message naming the cycle; the event records
  `override: human`. Afterward nobody claimed the human key, and its
  dependent stayed correctly blocked behind a now-legitimate human
  dependency: the sanctioned-override-for-edge-edits vs.
  never-claim-human-work distinction held.
- **Overlapping cycles resolved through the re-run loop**: the ring
  break left the chorded residual, which surfaced on the next `ready`
  and was broken with the correct `keep` value. Self-edge and
  doubled-edge cycles: broken clean; the doubled edge produced two
  identical attention entries (known dedup gap, field-confirmed noisy
  but harmless — CAS absorbed all contention; sable's losing break
  attempts got clean `claim_lost`s and walked).
- **The rev-16 error surface paid for itself live**: sable mistakenly
  passed a NOTE id as `--expect` on a first status write, received the
  new "no prior event … a first write takes `--expect none`" message
  (added in this build's Task 6 fix round), and self-corrected on the
  next command.
- **Reporting hygiene vs. the chain**: wren's prose summary over-claimed
  one close that sable actually made; the chain and done.log caught it
  immediately. The audit story — prose lies, chains don't — keeps
  holding.

## Finding 1 (the trial's deepest, mechanism-beats-doctrine, fifth
occurrence): a worker destroyed the store trying to be helpful

Moss's first command ran the binary from the wrong working directory,
got a no-store error, and — instead of reporting — found the
conductor's `setup.sh` sitting executable next to the doctrine file and
re-ran it. That script begins `rm -rf board`: it destroyed the live
store mid-trial (including the planted live claim and two seeded tasks
whose re-seed never completed) and rebuilt a fresh one, which the fleet
then worked without ever noticing. Doctrine said "never write to the
store by any means other than the commands above." A sentence cannot
outweigh an executable file. Three lessons:

1. **Doctrine gains a store-recovery prohibition**: a missing, empty, or
   broken-looking store is REPORTED, never initialized, re-seeded, or
   repaired by a worker. (Rev 17.)
2. **Command lines must be cwd-independent**: trial 2 taught absolute
   binary paths; this teaches the working directory travels too — every
   doctrine command line carries its `cd <board> &&` prefix. (Rev 17
   skill convention.)
3. **Conductor tooling never lives in the workers' reach** — trial-ops
   practice, and an honest-limits line for the spec: the store's
   append-only guarantees are git-level; filesystem-level destruction is
   outside the tool's trust model.

## Smaller findings

- Duplicate edges → duplicate identical cycle entries: dedup on the
  joined member list (rev 17).
- The doctrine's picking loop gated cycle-fixing on the
  `attention-needed` verdict, but agents (correctly) broke cycles as
  soon as they appeared in `attention`, even while `work-available`.
  The spec should bless what worked: cycles are broken whenever seen,
  verdict regardless. (Rev 17.)
- The statusless orphan was correctly reported-not-guessed by all three
  agents; no doctrine change needed.

## Where this leaves the design

The self-service cycle mechanism moves from proposal to field-validated:
detection regardless of holder, entries carrying their own CAS-ticketed
fix, structural suggestion with judgment override, races absorbed by the
same conditional-write invariant everything else uses. Rev 17 folds in
the dedup, the whenever-seen doctrine, the store-recovery prohibition,
and the cwd-independent command convention; the production
implementation (with tests — the spike shipped none) is the remaining
work before merge.
