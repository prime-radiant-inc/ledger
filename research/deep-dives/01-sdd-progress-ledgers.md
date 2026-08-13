# Deep-dive: SDD progress ledgers

*Produced by a research subagent mining the agentsview archive, 2026-08-13. Scope: 327 tool calls (123 Read / 23 Write / 181 Edit) across 123 sessions, cluster `sdd-progress`, 2026-06-23 → 2026-08-10, spanning serf (36 sessions), superpowers (35), teststrip (28), agentsview (7), session_eval (5), and others. Ledgers reconstructed by replaying Write/Edit `input_json` in timestamp order.*

## (a) Ledger anatomy

Reconstructed in full or in part: teststrip unified-shell (86 calls, 2026-08-07→08-10, sessions dc34188c / agent-a8ebc8def89b9e634 / agent-ac844ef317), teststrip autopilot-ghost-derivation (2026-08-06, agent-a68217...), serf sweep-a "consistency-sweep Track A" (2026-07-05→07-06, five successive orchestrator subagents), superpowers-autoresearch cost-pathologies-evals (2026-08-01, four+ controller subagents), session_eval digestion-model-eval (2026-08-08→08-09).

Entry types actually observed:

1. **Identity header** (post-fix ledgers): `# SDD ledger — plan: docs/superpowers/plans/2026-08-08-digestion-model-eval.md` plus branch/base-commit line (digestion, 2026-08-08). Richer headers pin plan+spec+branch+worktree: unified-shell names plan @ commit, spec @ commit, branch, and "Controller brief: controller-brief.md (binding)".

2. **One-line task completions with commit ranges** — the dominant record: `Task 1: complete (commits 340a027e..e1ab4637, review clean — Approved; Minor: attention.go doc comment narrates history..., not fixed, non-blocking)` (sweep-a, 2026-07-05).

3. **Rulings** — controller adjudications with reasoning, some escalated to Jesse: `### A7 — plan-text conflict — CLOSED. Ruling: Jesse, 2026-08-07, via main. Ruling: the plan's arm is overruled. Drop it.` and `Ruling (controller, not escalated — dead code, no product content)` (unified-shell). The autopilot-ghost ledger has a numbered block "Controller decisions on plan-author flags" (7 decisions) and full amendment rationales ("A1 — ... Controller ruling: authorized ... it is the only scope under which the code path exists", 2026-08-06).

4. **Pre-flight conflict-scan output**: autopilot-ghost's "## Pre-flight plan scan" with findings P1 (doc slip), P2 (rubric-vs-plan, pre-adjudicated), P3 (empirical, watch) — each pre-ruled before Task 1. Unified-shell records the scan verdict plus one accepted deviation. A tabular "Task status" table (| Task | Model | Base..Head | Review | Notes |) appears in autopilot-ghost.

5. **Parked/deferred minors for final-review triage**: "Minor findings (for final review triage)", "Flake watch", `Minor (not fixed, recorded): style.css ~1218 ... left for the completeness sweep` (sweep-a, 2026-07-05).

6. **Standing rules / process amendments** — self-imposed binding rules discovered mid-run: "### A31 — the load was MY doing... Widened rule, binding for the rest of this push: a gate run whose result I intend to trust must not overlap a live agent." (unified-shell, 2026-08-08); "Standing rules for this push: Haiku banned from any test-writing... All gates/VM steps FOREGROUND" (autopilot-ghost).

7. **Gate/verification results with evidence**: "## ⚠️ GATE AT `5af2479c` IS INCONCLUSIVE — NOT GREEN, NOT RED... I do not get to pick the answer I like." (unified-shell, 2026-08-08).

8. **Handoff sentinels and carry-forwards**: "STOPPED HERE PER ORCHESTRATOR INSTRUCTIONS — do not proceed to Task 9+ without a fresh dispatch" (sweep-a, 2026-07-06); "CARRY-FORWARD to Task 11 (X5 arm authoring): the LEASE-RECEIPT/... grammar ... is the SPEC cp/x5a MUST emit" plus running spend tracking ("Running campaign total: $234.19") and review-rejection records ("Task 9: review round 0 REJECTED — 1 Critical + 5 Important...") (cost-pathologies, 2026-08-01).

9. **Terminal state**: "## SHIPPED 2026-08-06/07. Merged to main as 4686e9e8... Worktree removed, branch deleted, pushed" (autopilot-ghost).

## (b) Observed lifecycle

- **Writers are controllers/orchestrators; implementers and reviewers are readers.** 91% of calls come from subagent sessions (relationship_type='subagent'), because the controller itself usually runs as a dispatched ORCHESTRATOR subagent. Session access patterns: 77 read-only (implementer/reviewer subagents handed the ledger as dispatch context — e.g. "You are the task reviewer for Task 8..." session_eval), 30 read+write (resuming controllers), 16 write-only (fresh controllers creating it). Writing sessions average 4.4 ledger writes (max 44 — agent-a8ebc8def89b9e634's ~31-hour unified-shell run).
- **Read-at-start is real**: 102 of 123 sessions' first ledger touch is a Read; only 16 start with a Write.
- **Append cadence**: one edit per task completion, plus rulings as they happen; unified-shell shows 83 write/edits over three days.
- **Resume-from-ledger genuinely happens, repeatedly.** The serf sweep-a ledger was written by five successive orchestrator subagents in relay (agent-a8769ad589 "resuming Track A PHASE 1 after the prior orchestrator died on an API error", then a5c8538b3c, a6cd5801f2 ("Two prior orchestrators STALLED by BACKGROUNDING a fix subagent"), a6dd4d04b4, a9abe189f8 "Tasks 1–13 done and committed. Execute Tasks 14–17" — all 2026-07-05/06). Each was told to resume from the ledger's completion lines and explicit "PHASE 1 COMPLETE. Do not re-dispatch Tasks 1-4" markers. Concrete recovery: session e2b806cf (2026-07-05) verified from git + ledger that a stalled Track 0 orchestrator landed nothing ("ledger shows only the pre-flight note"), then re-dispatched knowing "the ledger's pre-flight carries over, so it resumes at Task 1." Session 1ff381a4 (serf, 2026-07-09, "pick up the pieces"): read the ledger first call, cross-checked git log, determined the feature was actually complete despite the ledger stopping at Task 5, and fixed the stale entry.
- **Compaction insurance is explicit**: b085f70d (teststrip, 2026-07-11) "Everything the loop needs to survive compaction is already on disk... `.superpowers/sdd/progress.md` — both SDD plans' task ledgers"; 8d122618 (superpowers, 2026-08-01) pre-compaction: "nothing load-bearing lives only in this conversation."

## (c) What's working (with citations)

- **Orchestrator relay/handoff**: the sweep-a chain (2026-07-05/06) survived two API-error deaths and two backgrounding stalls with zero re-done work; completion lines + "do not re-dispatch" sentinels carried state across 5 controller lifetimes.
- **Commit ranges as the recovery spine**: 1ff381a4 (2026-07-09) and e2b806cf (2026-07-05) both resolved ledger-vs-reality questions by pairing ledger lines with `git log` — exactly the skill's "recovery map" design.
- **Rulings as durable adjudication records**: unified-shell's A-numbered findings (A5 carried into Task 4, A7 ruled by Jesse, A31 process amendment) let a 3-day, 3-session run keep dozens of open threads straight (dc34188c → agent-a8ebc8def89b9e634 → agent-ac844ef317, 2026-08-07→08-10).
- **Cross-day campaign continuity**: cost-pathologies (2026-08-01) chained 4+ controllers through a $234 eval campaign via CARRY-FORWARD entries and spend totals.
- **Health signal (weak, no causality claimed)**: the 123 sdd-progress sessions run 122/123 completed, grades 105 A / 13 B / 2 C / 2 D / 1 F. Subagent sdd sessions match project-peer subagents on GPA (3.88 vs 3.91). Top-level sdd sessions grade lower than top-level peers (GPA 2.82 vs 3.39, n=11 vs 175) — but they're also 2× longer (avg 902 vs 421 messages); these are the marathon jobs, so the grade gap plausibly reflects job size, not ledger harm.

## (d) Failure modes and friction (with citations)

1. **No plan identity → foreign-ledger consumption and invented variants** (pre-fix, root-caused in paradise-park~25ff38b6, 2026-07-06): serf's `cc-plugin-marketplaces/.superpowers/sdd/` accumulated 68 files across three plans — P1's `progress.md`, then `progress-p2.md`/`p2-task-N-report.md` ("names the P2 controller *invented* because the defaults were occupied"), then an abandoned `progress-p3.md` stub; P2's briefs silently overwrote P1's. Worse: "A fresh session executing plan B in a worktree where plan A ran will read plan A's ledger as its own progress — a straight-line reading of the skill text tells it to skip tasks." This drove the plan-identity first line and plan-scoped `<plan-basename>/` workspaces now in SKILL.md (tested in sdd_plan_scoped_workspace, 2026-07-06).
2. **Git contamination**: same session — serf tracked `.superpowers/sdd/{task-1-report,reconciliation-report,final-review-fix-report}.md` in git, with two prior cleanup commits (8305e340d June 22, c966261a5 June 25); the self-ignoring .gitignore only appears when a script runs, and gitignore is useless once tracked.
3. **`git clean -fdx` destroys the ledger**: found in review 2026-06-18 (agent-ad30fe38a67e98c14, 67dbbe73): moving the workspace from `.git/sdd/` (immune) to the working tree made the entire workspace deletable by clean; mitigation added to SKILL.md is "recover from git log", not prevention.
4. **Ledger lags/contradicts reality**: 1ff381a4 (2026-07-09) found progress.md tracking through Task 5 while all 6 tasks had commits; agent-ae82898f3f1938830 (teststrip) "update the stale ledger entry for design surface 5f". The tiebreaker is always git, and the skill now says so.
5. **Append mechanics are fragile**: unified-shell shows a double-append retry (two overlapping edits 5s apart at 2026-08-08T00:12, appending "Task 2: complete" twice); digestion-model-eval shows a failed append (agent-a0dad2..., 2026-08-09, Edit old_string "Task 12: complete (750 digests..." not found). Anchor-based Edit appends against a file other agents also touch mis-fire.
6. **The ledger doesn't prevent stalls, only makes them cheap**: e2b806cf (2026-07-05) — orchestrator did pre-flight, wrote the ledger, then backgrounded its implementer and died having landed zero commits; 95k tokens spent. The ledger's value was purely in the post-mortem restart.
7. **Scale/bloat**: the unified-shell ledger grew to hundreds of lines of narrative (multi-paragraph gate essays, A1–A36 findings). It worked, but it's a controller-context tax on every resume; entries like the INCONCLUSIVE-gate essay are journal prose, not resumable state.

## (e) Implications for a generalized ledger tool

1. **Identity is non-optional.** The single worst observed failure class (foreign ledger consumed as own progress; invented `progress-p2.md` variants) came from an identity-free fixed path. A ledger tool should bind each ledger to its task/plan identity at creation and refuse cross-plan reads by default.
2. **Make appends atomic and anchor-free.** The natural entry is an append (task done, ruling made). Anchor-based text edits caused retries, duplicates, and one lost completion line. An append-only API (or append verb) eliminates the whole class.
3. **Entries should pair claim + external evidence.** Every effective resume paired ledger lines with `git log`; the recoveries that worked cited commit SHAs. A ledger schema should make an evidence pointer (commit range, artifact path, test-run ID) a first-class field so a resumer can verify rather than trust.
4. **Separate the resumable spine from narrative.** Two ledger registers coexist in the data: terse machine-scannable completion lines (what resumers actually use) and long ruling/gate narratives (valuable, but a resume tax at 3,000+ lines). A tool should structurally separate "state" from "journal" so resume reads stay cheap.
5. **Plan for the ledger's death.** `git clean -fdx`, wrong-cwd contamination, and git-tracking leaks all happened. Decide placement deliberately (working-tree scratch vs. protected store), and build end-of-life in: the "no end-of-life" gap left 68-file graveyards that later agents misread. The SHIPPED terminal entry (autopilot-ghost, 2026-08-06) is the pattern worth institutionalizing.
