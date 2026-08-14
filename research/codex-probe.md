# Codex harness probe — CODEX_THREAD_ID distinctness (spec test 6b)

2026-08-14. Two `codex exec` invocations (codex-cli 0.144.4, model gpt-5.6-luna) against the
built `ledger` v1 binary, in a scratch git repo. Raw logs: session scratchpad `codex-probe/run{1,2}.log`.

## Findings

1. **`CODEX_THREAD_ID` is distinct per invocation and equals the session id.**
   Run 1: `01a000e3-b72d-7021-98d2-cc64a8b298d5`; run 2: `01a000e5-d223-7473-865b-dab3c4b8026a`.
   Each matches the "session id" codex prints in its banner. Two exec runs → two distinct ids.
2. **Child processes inherit it.** Both a `sh -c` grandchild and the `ledger` binary saw the
   same value as the codex shell, so `origin.session` can carry it. Spec test 6b: **confirmed** —
   the variable distinguishes Codex writers at session granularity (per-thread; codex has no
   in-session subagent fan-out to probe further).
3. **Codex's default `workspace-write` seatbelt blocks all writes under `.git`.** Run 1
   (default sandbox): `ledger init` failed (`could not lock config file .git/config: Operation
   not permitted`) and `create` failed (`unable to create temporary file` in the object db) —
   codex's sandbox deliberately keeps `.git` read-only even inside the writable workdir. With
   `--sandbox danger-full-access` (run 2), the same commands succeeded. **Consequence: a
   phantom-ref ledger store cannot be written from a default-sandboxed Codex session.** The
   errors surfaced cleanly as `git_failed` with the underlying git message, so the failure is
   at least diagnosable; but Codex support in practice means either an escalated sandbox, an
   approval-mediated write, or a store outside `.git` (the bare `.ledger.git` path is still
   inside the workdir but dodges the `.git`-specific exclusion — untested, worth a follow-up
   probe).
4. **Nested-harness detection mislabels.** The codex process inherited `CLAUDECODE=1` (this
   probe was orchestrated from a Claude Code session), and our marker precedence checks
   Claude's vars first, so the event codex wrote carries `author: claude-code`,
   `created_by: claude-code`, and `origin.session` = the *Claude* session id via
   `CLAUDE_CODE_SESSION_ID` — not the codex thread id. A codex launched from a plain terminal
   carries only `CODEX_*` vars and resolves correctly. Limitation to document: when harnesses
   nest, the marker names the outermost harness whose env survives, not the immediate spawner.
   (Same class of ambiguity in the other nesting direction; no env-only fix exists.)
5. **Error-envelope UX under a foreign model.** gpt-5.6-luna hit `vocab_unknown` (from a
   deliberately malformed probe command) and the copy-pasteable hint rendered fine. One real
   wart it also hit: `ledger show <slug>` (positional) returns
   `{"error":"unknown_verb","message":"unknown command \"probe-a\" for \"ledger show\"","hint":"run `ledger --help` for the verb list"}` —
   reads take `--ledger <slug>`, and the cobra positional-arg rejection masquerades as an
   unknown *verb* with a hint pointing at the verb list instead of at `--ledger`. Filed as a
   fix candidate with the skill-acceptance-eval findings.

## Spec/test disposition

- Test 6b (`CODEX_THREAD_ID` distinctness): **pass** — distinct per session, inherited by
  children, valid as `origin.session` context.
- New open item for the spec's harness notes: Codex default-sandbox `.git` write exclusion
  (finding 3) and nested-harness marker precedence (finding 4).
