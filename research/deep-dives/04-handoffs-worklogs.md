# Deep-dive: Handoff documents and worklogs/checkpoints

*Produced by a research subagent mining the agentsview archive, 2026-08-13. Clusters `handoff` (~133 sessions, 361 calls), `worklog` (~42 sessions, 354 calls), `progress-other` (~20 sessions).*

## (a) Corpus split — most of the "handoff" cluster is noise

766 calls / ~180 sessions across the three clusters. Classified by path (extension + directory conventions, spot-checked by reading content):

- handoff (361 calls): 249 calls (69%) are domain code or test fixtures — a `handoff.ts`/`handoff-runs.server.ts` feature in the brainstorm app, `handoff_smoke.yaml` toil workflows, a vendored copy of openai-agents-python's `handoffs/` module, and `brainstorm-handoff` e2e test fixtures under /private/tmp/superpowers-tests. 28 calls are notes ABOUT handoffs (zettels like "handoff-as-team-dysfunction-root", Obsidian ideas, a playbook pattern doc). Only 84 calls / ~24 files are genuine agent working-state.
- worklog (354 calls): 164 calls (46%) are noise — nearly all from one feature branch literally named `working-state-metrics` in serf (Go code). 185 calls are genuine, dominated by two artifacts: sen-core `WORKLOG.md` (113 calls) and its `docs/work-log/` checkpoints.
- progress-other (51 calls): 47 genuine, mostly teststrip `.superpowers/card-runs/progress.md` (29 calls, 11 sessions).

Net behavioral corpus: ~316 calls over ~25 genuine files. Lesson for the methodology: filename-matching on "handoff"/"progress" has a ~50-70% false-positive rate; you must read content.

## (b) Handoff anatomy and triggers

Triggers observed (every genuine handoff traced had an explicit trigger; none were spontaneous mid-work):

1. **Imminent compaction / "out of context", usually user-prompted.**
   - sen2 session 474b1fdd (2026-05-16, 56 compactions), ordinal 10651, Jesse: "Do you feel like you want to compact? If so, what other documentation do you want for yourself before you do that So that you can knock it out of the park when you resume?" Agent: "Honest answer: yes... a clean handoff would let resuming-me be sharper than drift-me. But I'd want to capture some things first. Let me audit what's already on disk vs. what only lives in my head." → audits disk, finds "No 'current state' doc anywhere", writes `sen-core-v2/docs/HANDOFF.md`, commits it with message "docs: HANDOFF for resuming-me — where everything lives, what's running, what NOT to do".
   - study-skills 5c75e0c0 (2026-05-02), ordinal 483, Jesse: "ok. We're about to run out of context. What process improvements do you feel like we need to make? And what's the intro guide to hand to another agent...?" Agent: "Writing both artifacts to disk so they survive the context boundary." → HANDOFF-technical-pm.md + IMPROVEMENTS.md.
2. **Cross-host / cross-machine transfer.** serf 59f97d2e (2026-07-05), Jesse: "all that future work: where is it for me to point another session out?... please be concise, since we're about to run out of tokens" then "it will be on a different host. please write another doc with all relevant memory content." → `docs/superpowers/agent-handoff-notes.md`, committed to git so it travels between machines.
3. **Explicit user request to brief a different agent.** claude-plugin-stats 518ce43a (2026-06-11): "can you write me up a handoff document to give to another agent that's more familiar with some of the tools we're using?" (RECOVERY-HANDOFF.md).
4. **End-of-milestone ritual.** serf 55ae8122 (2026-06-12): after closing Linear tickets, writes `~/.claude/projects/.../memory/serf-handoff-state-2026-06-12.md` unprompted, as part of wrap-up. teststrip a1f78c0c (2026-07-06): handoff written immediately after the final integration gate of a 6-hour build.

Anatomy — recurring sections:
- **Orientation preamble addressed to a future agent**: "If you are an instance of Claude picking this up cold, read this first. It tells you where everything is, what's running, what's already been decided, and what the load-bearing anti-patterns are." (sen-core-v2 HANDOFF.md)
- **Where-things-are map** (paths, repos, remotes), including trap-marking: a table row labeled "**Lace** (Jesse's main checkout, DO NOT USE)... WIP territory. Stay out." and "The 'which lace tree' distinction is the single most important operational fact."
- **Live state**: "What's running right now" (tmux session ids, branch, worker task progress with commit hashes).
- **Decisions not to relitigate + anti-patterns**: commit message says it "Captures: ... key decisions (the ones not to relitigate), critical anti-patterns."
- **Process that works**: agent-handoff-notes.md has "## Process that worked (Jesse-endorsed)" and "## Repo operational facts" (gotchas: "go build does not compile test files", lint quirks).
- **Reading order**: study-skills handoff opens with "Read these skills first, in this order", then setup steps, then "Non-negotiable discipline" and what's "deliberately unfinished so you can refine it as you go."

## (c) Consumption — write→read rate roughly 55-60%, with a sharp asymmetry

Of 16 distinct genuine working-state files with a Write in the archive window, 9 were demonstrably consumed by a different, later session; 7 were never read outside the writing session.

Consumed (9): sen-core-v2/docs/HANDOFF.md (read at ordinal 13 by 8cc07437, 2026-05-16, "Reading the source-of-truth docs in order", and at ordinal 3 by 231f33e8, 2026-05-17 — both consumers had compaction_count 0); study-skills HANDOFF-technical-pm.md (read+edited next day — living document); serf agent-handoff-notes.md (Read by 6+ sessions on 2026-07-05/06 across other checkouts and four `.worktrees/sweep-*` copies — traveled via git, so exact-path matching undercounts consumption); teststrip 2026-07-06-teststrip-session-handoff.md (read next day by agent-ac4b65d9); serf-hub-handoff and serf-handoff-state-2026-06-12 (next-day mentions via FTS in codex + subagent sessions); WORKLOG.md (read by 5 fresh subagent sessions whose briefs list it under "Required reading before you write code"; implementers append their own entries); teststrip card-runs/progress.md (11 subagent sessions read then append); sdd-cull-followups/progress.md (read same day).

Not consumed (7): deploy-runner 2026-06-09-HANDOFF.md (17 edits, self-use only), RECOVERY-HANDOFF.md, broker_backlog_handoff.md (self-use across 3 days), pri2012-5-shim-rework-progress.md (only touched 18 days later by a "Audit memory store for staleness" agent), teststrip unified-shell HANDOFF.md (written 2026-08-10, nothing yet), and the two sen-core `docs/work-log/` checkpoint files.

**The asymmetry: writers are long, compaction-heavy sessions (compaction_count 56, 21, 17, 13, 12, 9, 8...); consumers are almost uniformly compaction_count 0. Handoffs flow from dying contexts to fresh ones — that is their observed function.**

## (d) Worklogs / checkpoints — and the agents' own taxonomy

The sen-core WORKLOG.md opens with an explicit self-specification: "Append-only running record of what was done, what broke, and how it was fixed. Newest entries at the bottom. Never delete or rewrite entries" with an entry format (Goal / Did / Problems & fixes / State at end of entry). It distinguishes itself from its sibling: "`2026-05-15-rebuild-progress.md` ... is a *checkpoint* (snapshot for compaction), this one is the *ongoing journal*." The agents themselves evolved a two-artifact model: journal (append-only, multi-writer) vs checkpoint (overwrite snapshot, written right before compaction — that checkpoint's own header: "A complete-as-possible snapshot for the compact-and-resume").

The teststrip card-runs/progress.md is a third shape: an orchestrator-seeded scoreboard. The orchestrator (dc34188c, 2026-07-28) writes a header with the plan, rules ("never weaken negative assertions; card bugs fixed in worktree... app bugs reported back, NOT fixed by runners") and an append protocol: "(append: cull-0NN: <verdict> (report file, card commit if any))". Eleven sequential runner subagents each Read it for context, then Edit-append their verdict line, sometimes amending earlier lines ("card commit pending" → "card commit 9193233e").

Compaction proximity (messages from Write to next compact boundary, same session): sen HANDOFF.md 5; rebuild-progress checkpoint 7; broker_backlog_handoff 7; v1-build worklog 19; RECOVERY-HANDOFF 3; teststrip session-handoff (2nd write) 17; deploy-runner HANDOFF 34-35. Routine journal updates (WORKLOG appends, scoreboard appends) show no proximity (200-600). **Checkpoints/handoffs cluster tightly at compaction boundaries; journals/scoreboards are event-driven (per task completion). Both are defenses against context loss, on different schedules.**

## (e) Failure modes

1. **Handoff-as-chat-text.** 518ce43a wrote the requested handoff into the conversation; Jesse: "where is the doc?" Agent: "I only wrote it as text in the conversation — I didn't save it to a file." The default output channel is the transcript, which is precisely the thing that dies.
2. **Orphaned handoffs (~44%).** RECOVERY-HANDOFF.md and unified-shell HANDOFF.md were never read by anyone. The deploy-runner HANDOFF got 17 edits but only ever served its own session. Discovery is the bottleneck: consumed handoffs all had an explicit pointer (a brief listing it as required reading, a user prompt, or a well-known committed path); orphans sat at ad-hoc paths nobody was told about.
3. **Path fragility.** agent-handoff-notes.md consumption is invisible to file-path matching because reads happened in other checkouts/worktrees; memory-dir files (`~/.claude/projects/.../memory/`) are host-local and can't travel — hence Jesse's "it will be on a different host, write another doc" correction.
4. **Stale accumulation.** pri2012-5-shim-rework-progress.md was next touched 18 days later by a staleness-audit agent, not a worker. Checkpoints have a shelf life of about one resume; nothing garbage-collects them.
5. **Duplicate/no canonical home.** The same session wrote handoff content into 3 different artifacts (HANDOFF.md, WORKLOG.md, work-log checkpoints) and had to invent the journal/checkpoint distinction on the fly, each time re-deciding paths and formats.

## (f) Implications for a generalized ledger tool

1. **Two primitives, not one**: an append-only journal/scoreboard (multi-writer, event-driven, entry schema like Goal/Did/Problems/State) and an overwrite checkpoint (single-writer snapshot for compact-and-resume). Agents independently invented both and explicitly distinguished them.
2. **Wire checkpointing to the compaction lifecycle.** The strongest writes happened 3-7 messages before a compact boundary, and the best one started with a "disk vs. head" audit ("what only lives in my head"). A pre-compaction hook that prompts exactly that audit would capture what today depends on Jesse asking "do you feel like you want to compact?"
3. **Solve discovery, not just storage.** 100% of consumed handoffs were reached via an explicit pointer; orphans had none. A ledger tool needs a canonical, per-project well-known location plus automatic surfacing at fresh-session start (the consumer profile is always a compaction_count-0 session doing "read first" steps).
4. **Make ledgers travel.** Git-committed handoffs crossed hosts and worktrees; `~/.claude/projects/*/memory/` files could not. Content-addressed or repo-anchored (not absolute-path or host-local) storage, or the cross-host case fails.
5. **The high-value payload is anti-knowledge**: DO-NOT-USE paths, decisions-not-to-relitigate, "load-bearing anti-patterns", verified gotchas. Templates should ask for traps and settled decisions explicitly, not just "state and next steps" — that's what distinguished the handoffs that fresh sessions actually obeyed.
