# Issues-tracker spike trial 2 — `--expect` claims, and an accidental mixed-version fleet

2026-08-15. Spike round 2 (branch `wip/issues-spike`, commit 0540670): `set
--expect <event-id>` conditional writes (precondition validated inside the
store's CAS retry callback — genuinely atomic, 20/20 forced-race harness
rounds produced exactly one winner) and `ready` entries carrying their claim
ids. Trial: seven issues (three immediately ready, a dependency diamond
behind), three concurrent workers (ash and kit on Sonnet, moss on Haiku),
doctrine v2 with the `--expect` claim idiom and an explicit stop condition.

## The accident that made the trial better

The doctrine named the spike binary's absolute path at the top — and then
wrote every paste-ready command as bare `ledger`. Ash used the spike binary;
**kit and moss both ran the brew-installed v0.1.0 from PATH**, where `ready`,
`--where`, and `--expect` don't exist. The planned three-way `--expect` race
became an unplanned mixed-version fleet: one worker with the safety
mechanism, two without, all writing the same store. That's not the trial we
designed; it's the trial production would have run eventually anyway.

## What the chain shows (audited, not prose)

- **`--expect` was flawless where present**: ash made 7 atomic claims, 7
  successes, closed the entire DAG in dependency order (schema→parse/index→
  report→publish), zero tool errors.
- **`tidy-readme` was double-worked** — ash's atomic claim and moss's
  plain-set claim landed in the same second (moss's binary had no
  precondition to refuse it); both closed it. Same failure class trial 1
  produced from weak doctrine, reproduced from version skew.
- **`parse-source` was zombie-reopened**: kit, working from a stale read 45
  seconds old, plain-set it back to in-progress *43 seconds after ash closed
  it with evidence* — no error, because a plain set is just a valid write.
  Kit caught it by reading the key's history, corrected with an evidenced
  close and an honest message, and reported the near-miss unprompted:
  "the failure mode when you skip `--expect` isn't an error — it's a quiet,
  successful overwrite."
- **The board converged anyway**: zero open rows, every misstep attributable
  in the chain with author, second, and correction. The ledger contained
  the damage it couldn't prevent, and the record is why the damage was
  found at all.

## Findings → spec rev 3

1. **`--expect` is validated.** Mechanism, error shape, and idiom all
   worked; the claim design is settled.
2. **Reopening a terminal-status key requires `--expect`.** Kit's
   zombie-reopen is the exact stale-write `--expect` exists to stop, and
   deliberate reopens trivially satisfy the requirement (read the key, pass
   its id). One primitive, one more hole closed.
3. **Version skew is the dominant deployment hazard, and it fails silent.**
   An old binary doesn't reject a board with capabilities it lacks — it
   writes plain sets straight past every new safety rail. Forward fix:
   board meta gains a writer floor (`min_writer`) that rev-14+ binaries
   enforce on write; honest limit: binaries older than the floor mechanism
   itself can't be retrofitted, so the guard protects future skew only.
4. **Doctrine-authoring rule, now proven twice** (memory header, this
   trial): paste-ready commands carry the absolute binary path in every
   line, not in a preamble sentence. Two of three workers typed `ledger`
   because the command lines did.
5. Worker conduct note: kit's detect-by-history, correct-with-evidence,
   report-honestly sequence is exactly the citizenship the trust model
   wants; it belongs in the board skill as the recovery idiom.
