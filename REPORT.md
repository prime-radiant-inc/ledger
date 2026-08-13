# Ledgers and Their Uses by Coding Agents

*Research report — 2026-08-13. Corpus: the agentsview archive (79,704 sessions across Claude Code, Codex, and eight other agents, 2025-08 through 2026-08-13), mined via `scripts/extract-ledger-activity`. Six cluster deep-dives in `research/deep-dives/` carry the full evidence; this report synthesizes them.*

## Summary

Coding agents in this corpus have converged on the ledger — a durable, structured working-state file — as their main defense against the two ways agent work dies: context loss and orchestrator death. The pattern spread in three waves: organic handoff documents (peaking February 2026), the superpowers SDD progress ledger (shipped June 16, 2026), and a July explosion in which both the encoded convention and agent-invented variants took off across every active project. Codex is now the heaviest user by session count (~1,200 sessions touching SDD ledgers in July–August, versus ~120 for Claude via file tools), all of it driven through shell commands.

Ledgers demonstrably work: five orchestrators relayed one build through two API-error deaths and two stalls with zero re-done work; a fleet ledger coordinated 23 parallel agents; a month-old scenario ledger still governs what counts as verified in teststrip. They fail in recurring, well-documented ways: identity confusion, fragile append mechanics, no lifecycle, poor discovery, and trust decay. Every failure class has been patched by convention — headers, grammars, sweeps, forensics — and every one of those conventions is a workaround for a primitive no tool provides. That is the case for a generalized ledger tool, and the observed behavior specifies its design in some detail.

## 1. How we got here: three waves

**Wave 1 — handoffs (Nov 2025 – Jun 2026).** Before anyone said "ledger," long sessions wrote handoff documents when context ran out: 52 sessions in February 2026 alone. The trigger was almost always explicit — Jesse asking "we're about to run out of context, what do you want to write down?" — and the flow is unmistakable in the data: writers average dozens of compactions; consumers are almost uniformly fresh sessions with zero. Agents in this era independently invented a two-artifact taxonomy, stated outright in sen-core's WORKLOG.md: the *journal* ("append-only running record... never delete or rewrite entries") versus the *checkpoint* ("snapshot for compaction"). Checkpoint writes cluster 3–7 messages before a compact boundary; journal appends track task events instead.

**Wave 2 — the SDD progress ledger (June 2026).** Superpowers v6.0.0 (#994) encoded the pattern: a progress ledger "lets a controller that loses its context resume instead of redoing finished work." It has already lived through a compressed evolution: moved out of `.git/` when Claude Code blocked writes there (#1780); plan-scoped after in-the-wild contamination, with an identity first line (`# SDD ledger — plan: <path>`); extended to record evidence rather than assertions (#2080); and given an end-of-life (workspace deleted once the final review is clean).

**Wave 3 — generalization (July 2026 →).** With the vocabulary established, ledgers appeared everywhere: orchestrators mandating ledgers in their children's prompts, an agent inventing a fleet situation board unprompted, the story-loop skill building a whole verification methodology around one, and eval campaigns using seeded ledgers as grading contracts. July's counts: 80 Claude SDD-ledger sessions, ~918 Codex ones, plus 18 sessions inventing ad-hoc named ledgers.

## 2. What a ledger is: six roles

The word covers six distinct roles in the corpus. A generalized tool must serve at least the first four; the last two fall out nearly free if the first four are done well.

**a. Execution spine (SDD progress ledger).** Per-plan record of task completions with commit ranges, controller rulings, parked findings, standing rules, and a terminal SHIPPED entry. Writers are controllers; implementer and reviewer subagents are readers. 102 of 123 sessions touch it read-first. Its highest use is the relay: serf's sweep-a ledger carried five successive orchestrators (two API deaths, two stalls) with zero re-dispatched work, on completion lines plus "do not re-dispatch" sentinels.

**b. Capability catalog (scenario LEDGER.md).** One row per capability card, keyed by stable ID, status flow Spec'd → Tested-Pass/Fail → Fixed → Verified, each row binding its verdict to evidence identity (commit, VM, run dir, fixture). Unlike the execution spine, it is git-committed and permanent — "the cards are the spec; the ledger is the tracker. Both outlive the loop as the repo's regression suite." Teststrip's is 117 rows and a month old, with 30 commits from 16+ sessions.

**c. Coordination scoreboard / IPC channel.** A parent agent seeds a file with a row grammar (`| N | DONE | <sha> | <note> |`), dictates the path in its children's prompts, then literally watches it: `tail -F <ledger> | grep "DONE\|FAILED\|BLOCKED\|GATE"` — set up eleven seconds before spawning the child. Eleven runner subagents appending verdict lines to one scoreboard is the same shape. The ledger here is a channel as much as a record.

**d. Handoff checkpoint.** The overwrite-snapshot written at a context boundary: where things are, what's running, decisions-not-to-relitigate, and — the payload fresh sessions actually obey — anti-knowledge: "Lace (Jesse's main checkout, DO NOT USE)... Stay out."

**e. Evidence chain.** The e2e verifier's `evidence/ledger.md`: per-assertion verdicts each pointing at a sibling artifact (exit codes, sha256 before/after), a chain-of-custody index a skeptical reader can re-verify without trusting the narrative.

**f. Ground-truth contract.** The eval campaigns' seeded-truth ledgers: pre-registered answer keys, never shown to the agent under test, pairing every claim with an executable detection criterion and regex signatures over the *agent's own ledger grammar* — grading is two ledgers meeting. Jesse's "Durable Review Ledger" note (Feb 2026) is the same role aimed at code review: findings as tracked artifacts that must be explicitly resolved, with a verifier confirming resolution.

## 3. What's working

**Resume and relay are real, not aspirational.** The strongest evidence in the corpus: the sweep-a five-orchestrator relay (2026-07-05/06); session 1ff381a4 reading the ledger first call, cross-checking git, and correcting the record; the kata-fleet ledger surviving into a second session, a mid-session compaction, and promotion into git. The plan-scoped workspace eval made it quantitative: 25/25 controller reps refused to treat a stale foreign ledger as license to skip work.

**Evidence-anchored entries earn trust.** Every successful recovery paired ledger claims with git (`Task 1: complete (commits 340a027e..e1ab4637...)`); scenario rows bind verdicts to run dirs and fixtures, which is exactly what later let audits decide which PASSes still held. The skill text now encodes the lesson: trust the ledger and `git log` over your own recollection.

**The honesty machinery works when exercised.** A fresh-context audit demoted 24 of 108 scenario rows with per-row justifications; the "Reconciled — NOT re-run" status preserves stale evidence while refusing to let it masquerade as current; an SDD gate entry reads "INCONCLUSIVE — NOT GREEN, NOT RED... I do not get to pick the answer I like."

**Cheap failure, not failure prevention.** Ledgers did not stop orchestrators from stalling or dying; they made those deaths cheap. One orchestrator burned 95k tokens and landed nothing — the ledger's value was purely in the post-mortem restart. That is the correct claim for the pattern: it converts catastrophic loss into bounded loss.

**Correlations point the right way, weakly.** SDD-ledger sessions grade 105 A / 13 B / 5 C-or-worse on agentsview's health score, with 122/123 completing. Subagent SDD sessions match their project peers; top-level SDD sessions grade lower but run twice as long — these are the marathon jobs. No causal claim; the pattern is at least not associated with distress.

## 4. What's failing

Each failure class below recurs in the corpus, and each has an existing workaround that costs tool calls, attention, or trust.

**Identity confusion.** The founding failure: a fixed-path `progress.md` with no plan identity. Follow-up plans read the previous plan's ledger as their own progress; controllers invented `progress-p2.md`/`progress-p3.md` to dodge collisions; briefs silently overwrote briefs; 68-file graveyards accumulated. The eval found controllers never blindly trusted foreign ledgers — but paid a mean 9.0 tool calls of git forensics per resume proving it. The one-line identity header made misattribution structurally impossible. Ad-hoc ledgers avoided the whole class by unique naming and headers binding ledger → plan/base-SHA.

**Append is not a primitive anywhere.** The natural ledger operation is "add one entry, atomically." No harness provides it. Claude routes 81% of its shell ledger writes through `printf >>` and separately invented 968 python-heredoc read-modify-write calls to flip checkboxes; its Edit-based appends double-fired and lost lines when anchors moved. Codex appends by `apply_patch` — a diff that must quote a moving tail — and "Failed to find expected lines" is its top ledger failure; prose dies in three quoting layers (JS string → JSON → zsh). This is the largest observed error class.

**No lifecycle.** Ledgers are deleted too early and too late. Too early: SDD's own finishing step deletes the ledger before eval capture, forcing a ~750-line forensic reconstruction tool (`extract_ledger.py`) to grade runs; `git clean -fdx` kills working-tree ledgers. Too late: stale checkpoints sat 18 days until a staleness-audit agent, not a worker, touched them; nothing garbage-collects. The observed successes handled end-of-life explicitly — a SHIPPED terminal entry, or promotion to git ("ledger: FIFTH WAVE dispatch record" committed mid-campaign).

**Discovery is the bottleneck for handoffs.** Write→read consumption is roughly 55–60%, and one variable perfectly predicts the split: every consumed handoff had an explicit pointer (a brief listing it as required reading, a user prompt, a well-known committed path); every orphan sat at an ad-hoc path nobody was told about. Worst case: an agent "wrote" a requested handoff only into the conversation transcript — the one channel guaranteed to die.

**Trust decay.** Ledgers lag reality (a feature complete through Task 6 while the ledger stopped at Task 5) and — worse for the catalog role — reality invalidates ledgers silently: any code change voids a "Verified" with nothing mechanical connecting rows to the surfaces they verified. Teststrip manages this with recurring manual reconciliation sweeps (24 demotions one day, ~30 another), and diverging branch copies of the ledger disagree right now. Git is always the tiebreaker; the ledger is testimony, not truth.

**Format drift and bloat.** "One-line notes" become paragraph essays inside table cells, degrading the grep-ability that parents' monitors and graders' regexes depend on; the unified-shell ledger's narrative grew into a resume tax; teststrip's longest row is 1,506 characters. Agents cram two registers — machine-scannable state and human narrative — into one file. And prescribed entry grammars fire selectively: in the X8-B eval, agents emitted the easy `Approval:` lines 2/3 of the time and the hard `Ruling:` lines 0/3 — the case the mechanism existed for.

**Single-writer is etiquette, not mechanism.** Every write-discipline rule (single writer, proposers propose, authors don't touch the ledger) is enforced by prompt repetition and quietly relaxes under pressure.

## 5. Design implications for a generalized ledger tool

The six deep-dives converge on a consistent specification. Ordered by strength of evidence:

1. **Append as a first-class, atomic, anchor-free operation.** Entry text as a parameter, never as shell payload or diff context. This one primitive eliminates the largest observed error class in both harnesses, on both sides of the quoting wall.

2. **Identity at creation; refuse foreign reads by default.** Every ledger carries owner/scope/plan/base-ref in structured metadata. The measured value of one identity line: ~9 tool calls of forensics per resume, converted to a structural check.

3. **Two registers, structurally separated.** A machine-scannable state spine (keyed entries, status enum plus free qualifier, `set_status(key, value)` mutations) and a narrative journal (rulings, gate essays, anti-knowledge), linked by entry ID. Agents invented this split three separate times — journal vs checkpoint, catalog vs per-run evidence, log rows vs situation board — and every bloat failure comes from collapsing it.

4. **Evidence pointers as fields.** A completion or verdict entry carries its anchor: commit range, artifact path, run ID, hash. Resume-and-verify beats resume-and-trust, and validity anchors are the only path out of the stale-"Verified" disease — a tool can flag entries whose anchors have moved; hand-run reconciliation sweeps are today's substitute.

5. **Lifecycle verbs, and durability that outlives the agent.** Create → append → close (promote to git / summarize into memory / discard), with terminal states explicit. Record every state transition in an out-of-band append-only event log so cleanup, `git clean`, or a crash can never destroy history — the eval campaign's transcript-replay tool is the expensive proof this is needed.

6. **Discovery built in.** A well-known per-project registry, surfaced automatically at fresh-session start. The consumer profile is always a compaction-count-zero session doing read-first steps; today it finds the ledger only if someone remembered to point at it.

7. **Watch as an interface.** Parents already tail-grep ledgers as IPC and poll with hand-drifted `tail -N` windows. Offer `tail(n)`, keyed reads, read-since-cursor, and "notify on status transition," and the orchestrator poll loop collapses.

8. **Roles as mechanism.** Single-writer, proposer, reader — the disciplines that today live in prompt text and erode. The audit-proposal flow (read-only auditor proposes, owner applies) is worth making structural.

9. **Travel and branch-awareness.** Repo-anchored, not absolute-path or host-local; the cross-host handoff case failed until content moved into git. Branch-diverging catalog ledgers need either event semantics or per-claim branch qualification so divergence is visible.

10. **Visibility classes.** The grader-only seeded ledger and Jesse's durable review ledger both need entries some agents must not see. One-way visibility as a property, not a discipline.

Two cautions from the data. First, prescribed grammars fire selectively — agents comply on easy cases and skip exactly the hard ones — so the tool should make correct entries cheaper than prose, not merely mandated. Second, Codex's built-in `update_plan` shows ephemeral in-session checklists and durable file ledgers coexisting happily (~10% co-occurrence, plan steps citing the ledger as authority); a ledger tool should interoperate with plan/todo tools, not replace them.

## 6. Open questions for the tool design phase

- **File-backed vs service-backed.** Everything observed is plain markdown in the repo or tmp — greppable, committable, zero-infrastructure, and every failure mode above comes with it. How much structure can the tool add before losing "it's just a file" (agents can always fall back to cat; git remains the durable record)?
- **Where does the catalog role live?** Execution spines are scratch; capability catalogs are product truth. Same tool with different lifecycle policies, or two tools?
- **Compaction hook.** The best checkpoints came when Jesse manually prompted a pre-compaction "what only lives in your head?" audit. Should the harness fire that automatically at context pressure?
- **Relation to existing memory systems.** Ledgers overlap session memory dirs, journals, and handoff docs; agents already double-write as insurance. A tool should reduce that tax, not add a fourth place to write.

## Appendix: corpus and method

- Source: `~/.agentsview/sessions.db` (agentsview v0.40.1), 79,704 sessions, 2025-08-08 → 2026-08-13. Claude 59,469; Codex 19,850; others ~400.
- Extraction: `scripts/extract-ledger-activity` → `data/ledger_activity.db`. File-path matching for Claude-style tools (1,515 calls); `input_json` pattern matching for shell-driven agents (25,021 calls). Ledger contents reconstructed by replaying archived Write/Edit inputs — including files since deleted from disk.
- Noise rates matter: filename matching on "handoff"/"progress" is ~50–70% false-positive (domain code, fixtures); ~52% of the shell cluster is incidental mentions. All headline numbers above are post-filtering; see deep-dives for per-cluster method.
- Codex's `exec` tool wraps commands in JS harness scripts, not JSON — analyses that parse `input_json` as JSON silently drop all Codex writes.
- Health-grade comparisons are correlational; SDD selects for planned work, and session shapes differ.
