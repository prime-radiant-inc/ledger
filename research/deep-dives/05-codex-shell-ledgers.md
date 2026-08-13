# Deep-dive: Shell-mediated ledger use (Codex and Claude-via-Bash)

*Produced by a research subagent mining the agentsview archive, 2026-08-13. Cluster `shell-ledger` (25,021 calls / 4,423 sessions, deliberately over-inclusive).*

## (a) Noise-filtered counts

Method: parsed every call's command text (Claude `Bash` JSON, Codex `exec_command` JSON, and Codex `exec` — which turned out to be JS harness scripts calling `tools.exec_command(...)` / `tools.apply_patch(...)`, not JSON; a first pass that assumed JSON silently dropped all 8,505 `exec` rows and made Codex look like it never writes). A "ledger doc" = path under `.superpowers/sdd/`, `.toil/handoff/`, `docs/superpowers/handoffs/`, or a `*ledger*/*handoff*/*worklog*` file with a doc extension (.md/.tsv/.jsonl/.json/.log); code files excluded.

| agent | read | write | incidental (path-adjacent) | incidental (no ledger doc path) |
|---|---|---|---|---|
| claude | 2,413 (1,022 sess) | 1,332 (180 sess) | 2,473 | 3,021 |
| codex | 7,440 (1,504 sess) | 786 (246 sess) | 1,977 | 5,579 |

~52% of the cluster is noise: grep hits on domain code named `*handoff*` (sen-core-v2/slackline Slack handoff feature, brainstorm `use-tutorial-handoff.ts`), `/tmp/sprout-*-handoff-canary*` marker files, `favor_ledger` flags. The path-adjacent incidental bucket is ledger lifecycle without I/O: `ls .superpowers/sdd/`, `mkdir -p`, `git add progress.md`, `test -f`.

Write mechanisms diverge sharply:
- **Codex: 92% of writes are `apply_patch`** (update 462, add 258, delete 9), embedded in exec-JS as `text(await tools.apply_patch(patch))`. Shell redirects are rare (33 `>`, 24 tee, ~0 `>>`).
- **Claude (Bash only): 81% of writes are `printf/cat >> file` appends** (1,084), plus 169 truncating `>`, 54 mv/cp, 14 `sed -i`. Claude's shell writes coexist with native tools: on `.superpowers/sdd` paths the full archive also shows 4,356 Read, 1,171 Write, 802 Edit calls — yet Claude still routed ~half its ledger traffic through Bash, almost entirely because **append has no native tool**.
- Reads are the same on both sides: bounded page reads (`sed -n '1,240p'`, `nl -ba | sed`, `tail -N`) ~70%, `rg`/`grep` ~25%.

Top read+write projects: remux/codex 1,939; teststrip/codex 1,403; serf/codex 1,159; Scantastic/codex 1,101; serf/claude 853; teststrip/claude 829; superpowers/claude 827; decisionhub/codex 329.

## (b) Decisionhub case study (codex, 2026-08-12/13)

86 sessions in the cluster = **1 root orchestrator + 85 subagent sessions**, all Codex, running superpowers SDD (plugin cache `~/.codex/plugins/cache/claude-plugins-official/superpowers/6.3.0/`) against `.superpowers/sdd/2026-08-12-decisionhub-v1/`. Breakdown: 231 reads, 98 apply_patch writes, 131 incidental. The burst pattern (sessions starting seconds apart, e.g. five between 03:18:15–03:18:42) is parallel task workers.

**Ledger creation** — orchestrator `codex:019ff8...9c1b1ff7`, 02:05:08:
```
const patch = "*** Begin Patch\n*** Add File: /Users/jesse/git/projects/decisionhub/.superpowers/sdd/2026-08-12-decisionhub-v1/progress.md\n+# SDD ledger — plan: docs/superpowers/plans/2026-08-12-decisionhub-v1.md\n+..."
```

**Worker reads its brief** (02:05:42): `tools.exec_command({"cmd":"sed -n '1,120p' .superpowers/sdd/2026-08-12-decisionhub-v1/task-1-brief.md", "max_output_tokens":12000})`

**Orchestrator polls worker progress** by re-tailing the report file, bundled with git status in one compound command (02:10–02:24, repeatedly), with an `if [ -f ]` guard against not-yet-written reports; tail windows drift 60 → 100 → 140 lines as the file grows.

**Appending to the ledger** requires quoting existing tail lines as an anchor — apply_patch context hunks, 19 progress.md updates. Reviewer sessions read brief + report + a `review-428f7f4..1441abd.diff` review package stored in the same sdd dir.

`update_plan` appears in only 2 decisionhub calls — an ephemeral in-session checklist whose steps reference the file ledger ("Extract Task 3 controller rulings R1-R4 and R6 from the progress ledger"). Cluster-wide: 213 of 2,160 codex ledger sessions (~10%) touch `update_plan`; it complements rather than competes — it doesn't persist across sessions, so SDD state still lives in the file.

## (c) Shell-mediated friction (with citations)

1. **JS-harness quoting collapses.** Embedding ledger/review prose in exec-JS template literals breaks the script itself: `codex:019f537e-4213-72a2-a2ff-e4f6e91a2ea3` (2026-07-11) — "Script error: SyntaxError: Unexpected identifier 'Overall'". Also `codex:019f8604-f3cf` (2026-07-21): a one-line Delete File patch died on "SyntaxError: missing ) after argument list".
2. **apply_patch context-anchor failures**: 32 failures on ledger-ish inputs, dominated by "apply_patch verification failed: Failed to find expected lines" — e.g. `codex:019f8b23-5b47-7472-aff8-46179abc315f` (2026-07-22) failed twice in a row updating `task-4-report.md`. Appending via diff means re-quoting a tail that may have changed. Also "invalid hunk" when report content itself contains diff-like lines (`codex:019f69a2-eece`, 2026-07-16).
3. **zsh-isms eating commands**: `codex:019f5f1d-4848-78e1-9668-888751b3be13` (2026-07-14, ghost-pepper): `for f in .superpowers/sdd/review-*.diff; do echo ==== $f;` → `zsh:1: === not found` (zsh `=cmd` expansion). Bare-glob "no matches found" errors (16), jq-inside-JSON-inside-shell quoting failures (`codex:019f53c3-8d26`, 2026-07-12), awk escaping failures (`codex:019f984b-fa6a`, 2026-07-25).
4. **Path confusion / missing files**: 83 codex "No such file" results on ledger-ish reads. Claude hits it too when appending: `paradise-park~24bd5813` (serf, 2026-07-20) — `printf ... >> .superpowers/sdd/progress.md` → "(eval):1: no such file or directory" because cwd was a per-stream worktree; retried with the absolute worktree path.
5. **Re-reading whole files**: median reads per (ledger file, session) is 1, but p90 = 4 for both agents; 274 codex file-session pairs re-read the same ledger 3+ times (max 67), because shell reads return text that scrolls out of context and polling = re-tail. Truncation adds noise: "Warning: truncated output (original token count: 47242)" (`codex:019f8bf8`, 2026-07-22).
6. **Claude-side workaround worth flagging**: 968 Bash calls across 325 claude sessions mutate ledgers via `python3 - <<'PY' ... s=s.replace("- [ ] Task A1:","- [x] Task A1:") ...` (e.g. teststrip `dc34188c-e83e-40a4-820f-5b9287242379`, "Mark A1 complete in ledger") — scripted read-modify-write invented to get checkbox-flip + append in one atomic call.

## (d) Ergonomics comparison (same SDD convention, matched projects)

Tool mix on SDD progress/report files across all sessions: claude = Bash 3,295 + Read 1,741 + Write 1,035 + Edit 740 (plus 1,798 Agent dispatches whose prompts carry the ledger path); codex = exec 2,869 + exec_command 885 + apply_patch 173.

Matched traces:
- CODEX serf `codex:019f63c7-b6f0`: 40 progress.md calls — 19 reads, 19 writes, tight 1:1; each write is an apply_patch with quoted context; even ledger rotation is a patch (`*** Update File: ... *** Move to: progress-2026-07-15-previous-program.md`). Batches with `Promise.all([tools.update_plan(...), tools.apply_patch(...)])` to amortize round trips.
- CLAUDE serf `paradise-park~24bd5813`: 277 progress.md calls — 180 reads, 88 writes. Appends are single fire-and-forget `printf '...' >> progress.md` one-liners, no prior read needed, no context anchor to break. First touch is the read-or-create idiom `mkdir -p .superpowers/sdd && cat progress.md 2>/dev/null || printf '# ...' >`.

Per-interaction cost: a Claude append = 1 call with near-zero failure surface (its observed failures were cwd issues, not quoting). A Codex append = 1 read (re-establish the tail) + 1 apply_patch that can fail on context mismatch + occasional retry; a Codex structured edit inside the ledger costs the same as a code edit. Claude pays elsewhere: appends can't edit (hence the 968 python-heredoc mutations), and `Edit` requires a prior full `Read` of files that grow monotonically.

## (e) Implications for a generalized ledger tool

1. **Append is the missing primitive.** Both agents contort around it: Claude drops out of its native toolset into `printf >>` (81% of its shell writes), Codex abuses a diff tool that requires quoting a moving tail (its top failure mode). A first-class `ledger.append(entry)` — no context anchor, no quoting, atomic — eliminates the single largest error class observed.
2. **Content-in-command encoding is the failure surface, not the ledger logic.** Every Codex friction class is prose passing through 2–3 quoting layers (JS string → JSON → zsh). A tool interface that takes entry text as a parameter, not as shell payload, removes SyntaxError/glob/quote failures entirely.
3. **Reads want windows and watches, not cat.** The dominant patterns are "last N lines" (poll) and "section for task K" (brief lookup), executed as re-tails with hand-drifted line counts and `if [ -f ]` guards. A tool offering `tail(n)`, keyed-section read, and read-since-cursor would collapse the p90=4 re-read pattern and the orchestrator poll loops.
4. **Structured mutations exist and deserve support**: checkbox/status flips (Claude's python s.replace hack), ledger rotation (Codex's `*** Move to:` rename, 168 Claude rename ops), and machine-readable variants Codex already invented under shell pressure (`candidate_ledger.jsonl` 232 calls/45 sessions, `ledger.tsv`, calibration-ledger.md family — 119/56 codex vs 1/1 claude). A schema-light `set_status(key, value)` plus JSONL-friendly entries would meet demonstrated demand.
5. **Don't fear the built-in plan tool.** Codex's `update_plan` co-occurs in only ~10% of ledger sessions and is used as an ephemeral per-session checklist that cites the file ledger as the durable authority. A ledger tool should interoperate (a plan step can reference a ledger entry id), not replace either.

Data caveat for the study: any analysis of this cluster that parsed `input_json` as JSON undercounted Codex by the entire `exec` tool (8,505 calls, all its apply_patch writes).
