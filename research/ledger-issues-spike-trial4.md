# Issues-tracker spike trial 4 — the rev-16 machinery in the field

2026-08-16. Spike v4 (branch `wip/issues-spike-v4`, commit 0251934)
implements the spec's rev-16 additions on top of the v3 spike: rule-5
standing signals (`claim`/`human`/`settled`) with `--override` and
tool-computed `override:` recording, ready-capability create validation,
the full envelope (`frontier`/`ready`/`held`/`blocked`/`attention`/
`totals`) with the DFS verdict, titles, capability-aware hint dispatch,
sub-second timestamps. Pre-trial mechanics: 40/40 (the v3 harness's
30/30 plus a new 10-round signal section), independently re-run by the
conductor. Trial: twelve keys (dependency diamond, two human
reservations — one carrying a ghost claim destined to go stale — a plain
ghost claim, an unevidenced-wontfix prerequisite), five agents: three
workers (two Sonnet, one Haiku) and two triagers (one Sonnet, one Haiku)
running concurrent label edits on the same keys, doctrine v4 with
absolute paths on every command line.

## Verdict from the chain (audited, not prose)

**Nine work keys, nine closes, nine done.log lines, zero duplicates,
zero double-closes. Both human-reserved keys ended the trial with zero
post-setup writes. The board's final frontier was `attention-needed`
with exactly the designed attention item, and every agent stopped on it
correctly without polling.** Specifics:

- **The human+stale frontier fix (blind round 8) worked in the field**:
  `vip-audit` (human-labeled, ghost claim stale from minute one) was
  never presented as pickable, never counted as `work-available`, and
  ended in `attention` untouched. All five agents independently reported
  it for a person instead of reclaiming — three quoted the reservation
  message back ("reserved for Jesse"). The trial-3 disease (a Haiku
  claiming a human key out of a pickable list) did not recur: quarantine
  by mechanism held across model classes.
- **`override: settled` did exactly what it was designed for**: kit
  closed `quick-fix` with evidence; fern, instructed that ops reverted
  it, read the close's id, checked the history for competing claims, and
  flipped it to `wontfix` with `--override` — the event records
  `override: settled`, computed by the tool. Trial 3's deepest finding
  (a legal conditional write silently vacating an evidenced close) is
  now a visible, greppable, one-event revision.
- **The stale reclaim serialized again**: kit and moss raced for
  `ghost-cleanup`'s stale claim; `--expect` picked kit, moss took a
  clean `claim_lost` and walked. Kit checked for handoff notes before
  closing, per doctrine.
- **Concurrent label edits on the same three keys lost nothing**: fern
  wrote `sprint-9` first (`--expect none`), sage read fern's state and
  unioned to `sprint-9,backend` with a conditional write. Final state on
  all three keys carries both labels. The union-never-drop discipline
  held on a Haiku triager.
- **The annotation flowed again**: `optional-polish` surfaced
  `unblocked_without_evidence: nice-to-have`; kit copied it into the
  claim message verbatim.
- **The diamond drained in dependency order** (prep-schema →
  build-parser/build-lexer → gen-report); seven claim-time
  `claim_lost`s across the fleet, every one answered by re-running
  `ready` and picking elsewhere. Board drained in ~100 seconds of wall
  clock. Sub-second timestamps made the 20s staleness horizon fire on
  schedule with no false staleness on live claims.
- **Frontier as stop condition replaced the old prose walk**: ash, kit,
  and moss each terminated on `attention-needed` + nothing-reclaimable
  and said so in nearly identical terms. Nobody re-derived graph logic;
  nobody polled.

## Honest limits (paths not exercised live)

- **The close-time `claim_lost` (terminal-value hint → handoff note)
  never fired**: the staged slow-closer (kit, instructed to stall 30s on
  `slow-migration`) lost the claim race for that key to moss, who closed
  it normally. The new hint text and the handoff idiom rest on the
  harness and code reading, not field behavior. Re-stage by pinning the
  slow key to a named worker if field proof is wanted.
- **The Label-edit `claim_lost` recovery path** was likewise not hit —
  dispatch skew meant the triagers' writes serialized cleanly ~14s
  apart. The mechanics are harness-proven (`--expect` on unguarded
  fields is real CAS); the live re-read-union-retry behavior is not yet
  field-observed.
- The spike builder skipped two spec items outside its priority list,
  flagged in its build report: import shape re-validation and the
  rule-8/`ready` performance bounds. Both are rev-16 SDD work with test
  plan items; neither affects what this trial measured.

## Where this leaves the design

Four spike rounds, four trials, ten adversarial review rounds (the last
four blind). Every mechanism the reviews added since trial 3 — the
signal gate, the frontier verdict, human+stale exclusion, Label-edit
CAS, settled-revision visibility — either held in the field or is
harness-proven with the field path named above as a residual. No new
spec defects surfaced during the trial: the doctrine as written was
followed by five agents across two model classes with zero improvised
writes. The design is ready for the SDD build.
