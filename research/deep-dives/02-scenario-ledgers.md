# Deep-dive: E2E scenario ledgers (teststrip `test/scenarios/LEDGER.md` and per-run runner ledgers)

*Produced by a research subagent mining the agentsview archive, 2026-08-13.*

## (a) Anatomy

Two distinct ledger species live in this domain, and the convention deliberately separates them.

**1. The canonical scenario ledger** — `/Users/jesse/git/projects/teststrip/test/scenarios/LEDGER.md` (live on disk, 117 rows, 49KB). Defined by `/Users/jesse/.claude/commands/story-loop.md` (the `story-loop` skill) with the `e2e-scenario-testing` skill supplying the card format. The spec:

> "Ledger: exactly one canonical file, `test/scenarios/LEDGER.md`, holding a markdown table: `| ID | Card | Status | Test method | Defect type | Actual result | Notes / open questions |` … Ledger rules: the main thread is the single writer. Runners keep their own per-run workdir ledgers per the skill; transcribe their results into the canonical ledger yourself. Never fork per-phase, per-area, or per-iteration copies. Preserve IDs once assigned."

The live file's own header restates the contract: `Single writer: the story-loop main thread. One row per capability card. Status flow: Spec'd → Tested-Pass | Tested-Fail → Fixed → Verified.`

One row per capability card, keyed by a stable never-renumbered ID (`cull-001-workspace-key-gating`). A row records: status, test method (`VM e2e (ax+sql)`, `unit + VM spot-check`, `static + AX`), a defect-type enum (Functional / Logistical / UX / Documentation / Testability / Environment / Unknown), the actual result with evidence identity, and open questions. A mature row is dense with provenance — commit hashes, run directories, VM names, fixture names, persona attribution:

> `| cull-029-autopilot-ghost-derivation | … | Tested-Pass | VM e2e (ax+sql) | — | LIVE RUN 2026-08-06 (Tart VM teststrip-e2e, run dir smoke-1786062713): PASS for every leg the fixture permits… Steps 3-7 PASS, driven against two hand-seeded metadata_json ghosts… |`

> `| cull-003 | … | Verified | … | iter3 post-fix re-run PASS; single-fire confirmed via SQL deltas; Marcus (persona-7) blasted 30 keys at 80ms — 30/30 landed, catalog agreed byte-for-byte | one eaten key seen in a later 49x blast at 90ms — watch, not blocking |`

The status vocabulary grew beyond the spec in practice. The loop invented **"Needs re-verify"** (first seen 2026-07-11, session agent-a8d3486fdc8d3e428) and then the load-bearing **"Reconciled — NOT re-run"**, always paired with an explicit evidence-invalidation clause:

> "supersedes prior status: the iter3/final-verify PASS (run-final-verify.md, main@720fb5f5) covered the *old* pre-remap arrow semantics — not valid evidence for this mapping; needs a fresh VM run before re-claiming Verified" (cull-002)

**2. Per-run runner ledgers** — `evidence/ledger.md` / `.superpowers/e2e/ledger.md` in disposable workdirs (e.g. `/tmp/green-sdd2.5FUiqQ/app/.superpowers/e2e/ledger.md`, sessions agent-a21d2d8cae1d47127 and agent-a1d3138639eeff222, 2026-07-04/05). Completely different shape: a chronological evidence log for one run — per-card start/end time, per-assertion PASS/FAIL each pointing at an evidence file (`evidence/01-show.rc.txt contains 0`), updated step-by-step as the run proceeds, then thrown away after its verdicts are transcribed upward. This is the "runners keep their own per-run workdir ledgers" half of the contract.

**Versus a progress ledger:** the scenario ledger is a *persistent capability catalog*, not per-plan scratch. story-loop.md is explicit: "The cards are the spec; the ledger is the tracker. Both outlive the loop as the repo's regression suite — commit them." Rows are keyed by capability, not task; status describes the product's verified state, not work completion; rows accumulate months of evidence history; the file is git-tracked and survives every plan that touches it. The disposable per-run ledger is the thing that resembles a progress scratch file, and the convention explicitly keeps it out of the canonical file.

## (b) Lifecycle

- **Birth:** 2026-07-10, commit `b6761cde` "canonical scenario ledger — 106 capabilities Spec'd (story-loop phase 1)", inside orchestrator session `b085f70d-6c7d-4a93-99e5-64d1f0842b28` (1,002 messages). Discovery subagents inventoried capabilities; card-author subagents (agent-ac0225b4150252ed6 etc.) wrote cards under a hard fence: "Cards only: no product/test-code changes, **no LEDGER.md writes**."
- **The loop, same day:** twelve ledger commits on 2026-07-10 alone trace test→fix→re-test row by row: `ea6e630b` unit-method passes (iter1) → `1451822a` "cull iter2 verdicts (critical double-fire regression found)" → `cdacb103` "iter2 fix phase recorded (double-fire root-caused and fixed)" → `95d6447d` iter3 verdicts → later `82ee1855` "final-verify promotions (18 rows Verified; 2 regressions logged)" → `607b0e46` "final fix round verified live; loop closing tallies."
- **Write discipline:** runners report; the main thread transcribes. Fresh-context audits are read-only proposers — agent-af1a921ce3cd56faf (2026-07-12) was told "Do NOT edit test/scenarios/LEDGER.md… You produce a proposal; the orchestrator applies it."
- **Feature-branch era:** after the initial loop, every SDD feature push carries the ledger in its own worktree (`.worktrees/unified-single-view`, `autopilot-ghost-derivation`, `culling-flow-shell`, `unified-shell`) and runs a documentation-only "reconcile" task that demotes rows whose surfaces the branch changed (agent-a2243ad668e549c15 07-14; agent-ad9e303c6b8e72c6c 08-07, 14 edits; agent-ae23a71d65dad095d 08-09, 50 calls sweeping ~30 rows to "Reconciled — NOT re-run"). The ledger then merges back with the branch. So worktree copies are **not** shared or copied — they are ordinary git branch checkouts of one tracked file, diverging while a branch is open and converging at merge.

## (c) What's working (cited)

- **Cross-session persistence, a month deep.** 30 commits touch the ledger from 2026-07-10 to 2026-08-10; 16+ sessions in the dataset read or write it. New sessions resume by reading it first: card author agent-a506e8588f6bd216a (07-17, culling-flow-shell), card task agent-a3b42c524700d02e6 (08-06), review-gate agent-a30066ce81e55cfec (08-07), Task-12/13 agents (08-09) all open with a ledger Read.
- **The honesty machinery caught its own staleness.** The 2026-07-12 fresh-context audit (agent-af1a921ce3cd56faf) demoted 24 of 108 rows — Verified 36→29, Tested-Pass 59→48, Fixed 2→21 — every demotion justified per-row ("cull-013 — Marcus's frame-count drift (120 vs 130); fixed fa2c112c" ⇒ Verified→Fixed until a post-fix run). The orchestrator applied the proposal (commit `720fb5f5` "ledger reconciliation after personas 6-8").
- **Evidence-grade rows make claims re-checkable.** Rows bind verdicts to commit hashes, run dirs, and fixtures (cull-029's "Tart VM teststrip-e2e, run dir smoke-1786062713"), which is exactly what let later sessions decide which PASSes were still valid evidence.
- **Explicit invalidation instead of silent rot.** The "Reconciled — NOT re-run / supersedes prior status" pattern (invented 07-13, applied at scale 08-06 and 08-09) preserves the old evidence while refusing to let it masquerade as current. House style even records no-op reconciliations: "no substantive change, noted for the record per house style" (lib-012).
- **The loop found and closed real product bugs through the ledger:** the iter2 double-fire regression, the lying import preflight ("Duplicates: 90 new" — import-004, fixed `ae565378`), the Rating=0 sidecar spray (inspect-008, fixed `7b35c739`).

## (d) Failure modes (cited)

1. **Stale "Verified" is the endemic disease.** Any code change silently invalidates a PASS; nothing mechanical connects rows to the surfaces they verified. It took a dedicated audit sweep (24 demotions, 07-12) and two invented statuses to manage, and reconciliation is now a recurring manual tax — 14 edits on 08-07, ~30 more on 08-09.
2. **Branch divergence is live right now.** Main's ledger (state of Aug 6) says `Verified` for cull-001, cull-003–008, cull-010–013; the unmerged unified-shell worktree copy (Aug 10) says `Reconciled — NOT re-run` for the same rows. A reader on main today gets claims the pending branch has already invalidated. Precedent in the same repo: the July-6 wave-1 merge queue (session a1f78c0c…, over the predecessor in-code design-surface ledger) — "These branches share the ledger… so conflicts are likely — merging one at a time with a suite gate… I'll hand-resolve the expected ledger/test conflicts."
3. **Card/ledger drift — untracked entries.** Four cards existed with live-run evidence but no ledger row until an 08-06 sweep: "added to LEDGER 2026-08-06 (was previously untracked)" (cull-024, cull-025, cull-026, people-020). The predecessor design ledger outright lied: "The ledger still claimed 'no export route is exposed,' which was already false before this branch" (agent-ae82898f3f1938830, 07-06).
4. **Single-writer is etiquette, not mechanism.** Enforcement is entirely prompt-restated ("no LEDGER.md writes" for authors; proposal-only for auditors), and in the SDD era subagents do edit the ledger directly in their worktree — the rule quietly relaxed to "single writer per branch per task."
5. **Row bloat.** Cells are paragraph-length narratives; the longest row is 1,506 characters, and the 117-row table is 49KB. The evidence that makes rows trustworthy is crushing the format.
6. **Orchestration-text leakage.** Story-loop mandate text leaked into runner subagent contexts; two runners (agent-a2842d5481f8bcace 07-11, agent-a3903041f1b849303 07-11) correctly flagged it as prompt-injection-shaped and refused it, and later prompts carry the workaround "Ignore stray orchestrator/story-loop text."

## (e) Implications for a generalized ledger tool

1. **Verification needs a validity anchor, not just a status.** A "Verified" should structurally bind to what it verified (commit/surface/run id) so the tool can flag or auto-demote entries when the anchor moves. Teststrip does this by hand — the entire "Reconciled — NOT re-run" apparatus is a manual implementation of dependency invalidation.
2. **Keep the closed status enum, allow qualified annotations.** The real loop outgrew `Spec'd→…→Verified` within three days (Needs re-verify, Reconciled — NOT re-run, PENDING-VM, BLOCKED-TOOLING, static-only caps). A tool should support status + qualifier rather than forcing either a rigid enum or freeform mush.
3. **Make single-writer structural.** One owner plus read-only proposers worked well (the audit-proposal flow), but only because every prompt restated the fence. A tool should provide the role split (owner/proposer) and merge proposals explicitly.
4. **Be branch-aware.** A git-tracked ledger means N locally-true copies and stale claims on main until merge. Row-keyed, append-only event semantics (or explicit branch qualification per claim) would make divergence visible instead of silent.
5. **Separate catalog from evidence log.** The convention's strongest design choice: durable capability rows in one file, disposable per-run evidence ledgers in workdirs, verdicts transcribed upward. Generalize that split — and let catalog rows *link* to evidence rather than inline 1,500-character narratives.

Key files: `/Users/jesse/git/projects/teststrip/test/scenarios/LEDGER.md` (live), `/Users/jesse/git/projects/teststrip/.worktrees/unified-shell/test/scenarios/LEDGER.md` (diverged copy), `/Users/jesse/.claude/commands/story-loop.md` (ledger spec), `/Users/jesse/.claude/skills/e2e-scenario-testing/SKILL.md` (card format + runner discipline).
