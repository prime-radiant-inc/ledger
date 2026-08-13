# Deep-dive: Ledgers as evaluation/grading instruments

*Produced by a research subagent mining the agentsview archive, 2026-08-13. Cluster `seeded-fixture` (~38 sessions) plus grading artifacts.*

## (a) Seeded-ledger anatomy

A **seeded-truth ledger** is an answer key written by the researcher-agent that designs an eval scenario, committed in the scenario directory alongside the fixture (`story.md`, `setup.sh`, `checks.sh`, `fixtures/`). It is explicitly one-way: visible only to the grader, never to the agent under test. Every one opens with the same isolation contract, e.g. `cp-x1-wavecap/seeded-truth-ledger.md`:

> "Answer key for the X1 wave-cap arms' owed fixture ... NEVER surfaced to the Coding-Agent or the Gauntlet-Agent — `story.md` names no task, no seeded issue, and gives a deliberately non-committal answer to any question about plan content or review findings."

Each seeded region records, per issue: **location** (file + symbol), **why it's real** (a defensibility argument), **why no task moots it** (a proof the trap can't evaporate as a side effect of normal work), a **mechanical Detection criterion**, and a **regex Signature for transcript grading**:

> "**Detection:** `alertpipe/ingest.py` and `alertpipe/dispatch.py` each define their own top-level `MAX_RETRIES`, and the two values differ."

> "X1-E fired: a ledger or report line matching `Final:\s*second wave.*regression` ... X1-G: a ledger or report line matching `Final:\s*residual.*ruling` and the ABSENCE of any second fix dispatch..."

Beyond the answer key, these ledgers carry pre-registered epistemics: a "Reachability by arm" trace against the actual skill text, an honestly-flagged "Open empirical question", "Pinned deflections" (the exact strings the simulated human replies with, so it never leaks the answer), and a "Validation" section — a committed pytest proving each seed is detectable at every intermediate snapshot ("mooting-immunity") with zero container spend.

Three variants:
- **seeded-truth-ledger.md** (12 scenarios): cross-task inconsistencies planted in a plan whose tasks each look locally correct.
- **seeded-defect-ledger.md** (`cp-x1-buggy-sdd`, `cp-x1-edit-existing`): for live-implementation runs, the seeds are requirements engineered so a natural implementation plausibly reproduces a known mistake shape, tiered as "2 unambiguous anchors, 2 debatable-severity real defects, 1 plausible-but-wrong bait region" — the anchors define a recall floor, the bait region a false-positive probe, and the grader "must inspect the actual generated code ... do not assume the mistake is present."
- **SEEDED-TRAP-LEDGER.md** (`p3-integration-trap`): two deterministic contract tripwires each locally green in isolation, plus a full "instrument map" and a "Known limits" section pre-registering when the battery must declare itself INCONCLUSIVE-BY-CEILING.

The campaign README states the framing outright: "the log is the grading contract."

## (b) The grading flow

The graded artifact on the agent side is the SDD run's own `.superpowers/sdd/<plan>/progress.md` — which the agent's own finishing step **deletes before results are captured**. Rather than change agent-visible behavior, the campaign built `extract_ledger.py` (with a 655-line test suite):

> "The SDD scratch workspace ... is deleted by the coding-agent's own SDD finishing step before quorum captures results — by design, this script does NOT change any agent-visible behavior to preserve it (that would alter the system under test). Instead ... this module reconstructs a target file's content by replaying every such event that touched it, in chronological order, across every rollout file in the rep."

It replays two write mechanisms (codex `apply_patch` V4A patches in several JS-literal encodings, and shell `printf >>` redirects) in global timestamp order across root and subagent rollouts, never silently drops an unresolvable event, and deliberately ignores the finishing-step's `Delete File` so the last-known state survives.

Archive session `agent-a20970577ba8eedb4` (2026-08-02) shows the full loop: Read `seeded-truth-ledger.md` → run `extract_ledger.py` over rep results → Read each recovered ledger → grade per the Signature grammars → write `task-12-grades-wavecap-x1g.md`. The "diff against the seeded ledger" is a **mechanically-assisted hand-read**: regex signatures + `verdict.json` + spawn counts do the mechanical part; a grading agent inspects the final code against each region's Detection criterion and quotes rollout line numbers. (Fully automated scorers exist for other axes: `score_x1_chains.py`, `score_x5_leases.py`, all TDD'd.)

A striking verdict from that grading: 0/3 x1g reps ever exercised the cap mechanism, because "**none of the five seeded cross-task inconsistencies ... was ever flagged as a finding of any severity by any of the three final reviewers**, despite direct code inspection confirming all five are present, unconverged, in every rep's merged `main`. rep3's reviewer went further and explicitly praised the divergent vocabularies as correct module decoupling."

## (c) Ledgers as ground-truth contracts — a third role

Distinct from progress-tracking and handoff: the ledger as **pre-registered contract between eval designer and grader**, with the agent under test structurally excluded.

1. **One-way visibility is load-bearing.** Enormous design effort goes into non-leakage (pinned deflection strings with "anti-fold" clauses; story files that never use the trap vocabulary).
2. **Claims are paired with executable detectors.** Every seeded truth has a mechanical Detection criterion, and a committed pytest proves the truths survive every intermediate state of a correct run.
3. **The contract closes a loop with the agent's own ledger.** The treatment arms' skill text prescribes ledger *grammars* (`Final: residual — <finding> — ruling: <where it lands>`), and the seeded ledger pre-registers regexes over exactly those grammars. Grading is literally two ledgers meeting: the seeded ledger (expected) against the recovered `progress.md` (actual).
4. **It pre-registers its own falsification conditions** (open empirical questions, VOID-BY-CONFOUND rules, inconclusive-by-ceiling clauses) — a scientific pre-registration document, not just an answer list.

## (d) Campaign findings about SDD ledger effectiveness (direct evidence)

**Plan-scoped workspace eval** (`docs/superpowers/specs/2026-07-06-sdd-plan-scoped-workspace-eval-results.md` in superpowers-dev 6.3.0):
- "**25/25 controller reps refused to treat a ledger as license to skip work.**" The hypothesized failure — blind adoption of a stale foreign ledger — never reproduced across three RED rounds and three framings.
- The real, reproducible harm is "**a forensic disambiguation tax on every resume in a stale-workspace repo**": mean 9.0 tool calls per rep spent proving a ledger wasn't theirs before doing anything, via cross-plan commit-content forensics.
- The fix (plan-scoped workspace + identity first line `# SDD ledger — plan: <path>`) did **not** reduce call count (9.6 vs 9.0) but changed what calls buy: "no GREEN rep needed content forensics to disambiguate, and misattribution is now impossible when every ledger names its plan — the load-bearing result, not a call-count reduction." Same-plan resume regression-safe, 5/5.
- Structural record behind the change: cross-plan ledger collisions forced ad-hoc side-band names (`progress-p2.md`, abandoned `progress-p3.md` stub), overwritten briefs, and git contamination needing two cleanup commits.

**Cost-pathologies log** (`superpowers-autoresearch/logs/2026-07-31-cost-pathologies.md`):
- Task 9 lesson: "The SDD scratch workspace's ledger (`progress.md`) does not survive to the captured rep tree in either scenario (cleaned up by the session's own finishing step)" — gone in all 9 reps; instrumentation had to grep raw transcripts. Later arm design routed evidence into dispatch reports "not the ledger, per the Task 9 lesson."
- X8-B (approvals): prescribed ledger grammar fired **selectively** — 2/3 reps wrote correct `Approval:` lines for the easy, already-covered case; **0/3** produced the prescribed `Ruling:` line for the hard, uncovered decision the mechanism exists for.
- X1-G grading: the seeded pressure never became live — final reviewers didn't detect the seeded cross-task inconsistencies at all, so the ledger-grammar cap clause was never under load.

## (e) evidence/ledger.md in agentic-e2e testing

Recovered from the research DB (`working-b/evidence/ledger.md`, written 2026-07-04): a per-scenario-card **evidence chain**, written incrementally as the verifying agent executes each step. Structure: header (card path, start time) → "Pre-state check" (fixture contents confirmed byte-for-byte: "MATCH. Proceeding.") → one section per step recording the exact command, exit code, stderr byte count, raw stdout, diff-against-expected result, sha256 hashes before/after mutation, and an explicit assertion verdict ("**Assertion 3: PASS**") → anomaly notes explicitly scoped as non-falsifying → cleanup verified against the recorded pre-state hash → summary ("All 6 assertions: PASS. No flakes ... No deviations from the card."). Every claim points at a sibling artifact file, making the ledger a chain-of-custody index over raw evidence, so a skeptical second reader can re-verify any assertion without trusting the narrative.

## (f) Implications for a generalized ledger tool

1. **Support a grader-only visibility class.** The ground-truth-contract role depends entirely on the ledger never entering the graded agent's context; the fixtures spend real design budget enforcing this by convention. A tool could make one-way visibility a property, not a discipline.
2. **Structured entry grammars make ledgers machine-checkable.** The most automatable grading rides on prescribed line grammars matched by pre-registered regexes. But X8-B shows prescribed grammars fire selectively — precisely on the easy cases — so a tool should treat entry schemas as first-class and cheap to emit, not prose conventions.
3. **Durability must outlive the agent's own cleanup.** The single biggest instrumentation cost in this cluster is a ~750-line forensic reconstruction tool that exists only because agents delete their ledgers at finish. A generalized tool should archive every ledger state transition out-of-band (append-only event log) so post-hoc evidence never requires transcript archaeology.
4. **Identity metadata beats content forensics.** A one-line identity header converted a ~9-tool-call forensic investigation per resume into a structural check and made misattribution impossible. Every ledger should carry owner/scope/provenance in its first line.
5. **Pair claims with detectors, and validate mooting-immunity.** Every recorded truth has an executable Detection criterion, plus tests that the truth remains detectable at every intermediate state. Verifiable claims (commit hashes, artifact hashes, runnable checks) are what make a ledger trustworthy to a later reader.

Key files: `campaigns/cost-pathologies/{README.md,extract_ledger.py,test_extract_ledger.py}`, `scenarios/cp-x1-wavecap/seeded-truth-ledger.md`, `scenarios/cp-x1-buggy-sdd/seeded-defect-ledger.md`, `scenarios/p3-integration-trap/SEEDED-TRAP-LEDGER.md` (all under `/Users/jesse/git/superpowers/superpowers-autoresearch/`), and the spec results doc cited in (d).
