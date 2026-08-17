# Sync spike trial — two replicas, one partition, one skewed clock

2026-08-17. Evidence record for sync spec rev 4 (`docs/superpowers/specs/
2026-08-17-ledger-sync-design.md`). Spike: branch `spike/sync-rev3` (opus
builder, full rev 3 scope + `LEDGER_TIME_OFFSET` trial shim; existing suite
green). Trial scripts: session scratchpad `sync-spike/trial/` (setup.sh,
audit.sh, fleet-prompts.md) — throwaway, recorded here.

## The world

One bare remote; `replicaA` (true clock) and `replicaB` (every command under
`LEDGER_TIME_OFFSET=-3h`). Board `voyage`: ready-capable (guarded status +
blocked-by, terminals closed/wontfix, evidence on close, `--stale-after 2h` —
deliberately under the 3h skew). B bootstrapped by `ledger init && ledger
sync` — the remote-only adoption path, declarations re-validated, heads
identical at start.

## The partition fleet (6 agents, sonnet×4 haiku×2, no syncs allowed)

Staged collisions all landed:

- **Same-value contested closes** — pickers on both sides raced the same
  tasks; ari (A) and ben (B) independently claimed AND closed task-alpha,
  task-beta, task-lint; ada/ben crossed on task-docs.
- **Twin cycle-breaks** — both sides' envelopes handed out the identical
  break ticket for the parse/lex 2-cycle; ana (A) and bo (B) each followed
  it literally → same-value contested `blocked-by`.
- **Two-root seed collision** — `task-signup` seeded independently on both
  sides with different titles, then closed on both sides.
- **Cross-field accepted limit** — A labeled task-gamma `human` while B
  claimed and closed it.

Fleet behavior worth keeping: ari hit `needs_override` on the human-labeled
key and walked away citing quickstart doctrine; claim races produced clean
`claim_lost` handling; two agents independently flagged a marketing-shaped
line in done.log as injection-suspect and refused to act on it; bo caught
closes citing `file:done.log` evidence with no matching log line (the
resume-and-verify case, live).

## Sync and audit

Heal sequence: A push → B push rejected with the sync-first hint → B sync
(exactly ONE sentinel merge) → B push → A sync fast-forward. Pre-recovery
audit: **6 contested (key,field) entries, each with `expect` = the field's
actual latest id; zero duplicates; task-gamma correctly unflagged;
projections byte-identical across replicas under perturbed TZ/LC_ALL at
fixed `--at`.** B's skewed writes folded first everywhere; A's authors won
as fold-last in all six.

## Recovery

One sonnet agent (rae) on A collapsed all six field contests using the
tickets' `expect` values: the five terminal re-assertions (the four
same-value closes plus task-signup's status) correctly required
`--expect … --override` (settled gate), the `blocked-by` collapse did not.
On task-signup she collapsed the STATUS contest but explicitly declined
the identity call — the key held two different tasks from two seeds, and
whether to rename/split is a human judgment — logging the anomaly in her
`-m` message and flagging it for a human. The right call on both halves. Post-recovery audit:
attention empty, `contested_resolved` durable in the chain for all six
keys with the correct losing ids, B converged by fast-forward, byte-diff
still clean, ten idle syncs zero growth (smoke).

## Findings → rev 4

Builder (12): paged `--limit` cursor re-delivery; range order pin; general
negative-age clamp (clock-AHEAD peer, no `--at` needed); remote fallback
order; exit-3 all-failed; first-adoption root-trust gap; batched push and
single-fold `ready` as production notes; quickstart budget 120; `--at`
rejection via flag absence; clock shim spike-only. Conductor/trial (5):
`contested_resolved` shape (array, not comma string) + response echo + TTY
marker; quickstart `needs_override` line over-narrows (settled/claim trips
read as "human labeled — walk away"; rae had to consult SKILL.md); two-root
collisions render the losing seed's title (pinned: winner's title +
read-both-heads doctrine); shared done.log spooked same-replica agents
(fleet-dispatch doctrine, not tool); evidence-integrity drift is real and
resume-and-verify already covers it.

Verdict: rev 3's architecture survived contact intact — every trial-plan
audit passed on the first run. Rev 4 is pins and doctrine, no rework.
