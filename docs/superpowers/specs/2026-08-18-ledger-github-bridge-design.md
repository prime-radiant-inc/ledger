# Ledger GitHub bridge: rename events and the additive sync (design)

2026-08-18, revision 1 — written FROM a completed spike (branch
`spike/bridge`, all five trial steps green against a real repo), not ahead
of one. Two of this spec's laws exist because the spike falsified their
naive forms live: the bridge ordering law (intake-first produced a
fabricated human edit and two sides disagreeing in opposite directions
within one run) and the state-persistence law (an unconditional cursor
note can never converge). Validation record at bottom.

**Scope**: (A) a first-class RENAME event in the core tool — tool rev 16,
amending the issues spec's immutable-title law; (B) `ledger-gh`, a
companion bridge command — Levels 1 (ledger→GitHub mirror) and 2
(GitHub→ledger additive intake) ONLY. Level 3 (label/assignee state sync)
is explicitly out of scope, unproven demand. **This spec resolves the
coexistence decision: the board is canonical; GitHub is the intake and
display window.**

Operator requirements: the bridge is one idempotent command, safe to
re-run, host-portable; every cross-system write is attributed and
greppable; the board's doctrine (CAS, standing signals, evidence) applies
to the bridge exactly as to any agent.

## Part A — the rename event (core tool, rev 16)

**Write**: `ledger set <key> --rename "<new title>"`.

- **Encoding, pinned**: a top-level `"rename"` field on a `type:"set"`
  event — NOT a sixth event type. Every existing consumer (cursors, watch
  filters, idempotency dedupe, rollup coverage, sync) keeps treating it as
  the ordinary keyed write it is; the fold sees it with one string test.
- **One assertion per event**: `--rename` combined with `field=value`,
  `-m`, `--evidence`, or `--override`… `--override` IS legal (see the
  gate below); the others are `bad_usage` — one commit never asserts two
  things. `--idempotency-key` is allowed and scoped to rename events (a
  rename can never dedupe against a field write sharing its key).
- **Fold rule**: a key's title = the latest rename event's text in fold
  order, else the first status event's `-m` (the existing law, untouched
  for never-renamed keys). Concurrent renames across replicas resolve
  last-in-fold-order; losers persist in the chain as prior titles.
- **The gate, decided** (the spike pinned "fully unguarded" and flagged it
  as the ruling most worth re-litigating; this spec re-litigates it):
  `claim` and `settled` do NOT gate a rename — a title is not an outcome.
  **`human` DOES gate it**: retitling a person's reserved issue under them
  is exactly the friction the label exists to create, so a rename on a
  human-labeled key is `needs_override`, satisfied by `--override -m
  "<why>"` like any standing-signal write (this is the one flag
  combination `--rename` accepts). `--expect` stays optional; when passed
  it is CAS against the key's latest rename event (`--expect none` = never
  renamed).
- **Existence**: rename requires an existing titled key locally
  (`unknown_key`, hint = the seed command). The fold stays total: a rename
  arriving BY SYNC ahead of its seed titles the key, with no seed entry in
  its prior list — write-time checks are local, like `blocked-by`
  existence checks. Ready-capable boards only (`bad_usage` elsewhere —
  plain boards have no titles).
- **Render, mandatory labeling**: everywhere a title-bearing row renders,
  a renamed title is visibly a renamed title with attribution reachable —
  JSON rows carry `renamed: {by, ts, id, prior[]}` (prior = every earlier
  title oldest first, seed included); TTY title lines carry
  `(renamed by <author>)`; `show`/`render`'s identity header lists one
  line per renamed key with prior → now and the event id. ABSENT (never
  false/placeholder) on unrenamed keys, so unrenamed boards render
  byte-identically to rev 15. Two render repairs ride along, both pinned:
  `ready`'s TTY blocked line gains the title it already carried in JSON,
  and `status <key>`'s drill-down — previously the one title-free per-key
  view — gains the title with its rename info.
- **Determinism**: title derivation is pure fold; the standing determinism
  test covers it unchanged (spike-verified).

Amendment to the issues spec, explicit: the immutable-title law becomes
**"stable by default: a title changes only by explicit, labeled rename
events."** The `ledger-issues` skill's title doctrine is rewritten to
match (the seed's `-m` is still the title to write well — renames are
corrections, not a workflow).

## Part B — `ledger-gh`, the bridge

A companion command living at `ledger/bridge/` (own main package, built
separately), one verb:

```
ledger-gh sync --repo <owner/repo> --ledger <slug> [--store <path>]
```

Transport: the authenticated `gh` CLI, subprocess-only, v1 (token
plumbing and a direct API client are v2 concerns). Board access: the
`ledger` CLI, subprocess-only — spike-proven sufficient, and keeping the
bridge on the public surface keeps the CLI honest. Output: one JSON
report `{ok, repo, ledger, gh_mutations, board_writes, cursor, actions[],
warnings[]}`; the `cursor` field is the PERSISTED cursor (a no-op run
reports the stored one, not the drain tip — spike cosmetic finding,
fixed by pin).

**Identity map**: board key ↔ issue number. Ledger side: a note, kind
`github-link`, body `github: issues/<n>`, authored `github-bridge`. GitHub
side: first line of the issue body, `ledger-key: <key>` (first-line-only,
CRLF-tolerant).

**Bridge state**: one note, kind `bridge-state`, under the reserved NOTE
key `github-bridge-state` — a note key, never a `set` key (a set key
would put a permanent statusless attention entry on every bridged board;
spike finding). Body carries the drain cursor and per-issue
comment-import high-water marks by REST comment id (monotonic per repo —
bounded state, replacing the spike's unbounded node-id sets).
**Persistence law (spike-falsified naive form): state persists ONLY when
the run changed something.** An unconditional write can never converge —
the state note lands after the cursor it records, so every run drains it,
forever. A no-op run leaves the cursor stale at the cost of one
suppressed event on the next drain.

**The ordering law (spike-falsified naive form, stated as law)**: *a
bridge run reads its outbound drain before it writes anything, and treats
the remote's view of any aspect it is about to push as stale, never
authoritative.* Concretely: (1) `ledger sync`; (2) READ the outbound
drain (`since <cursor>`); (3) intake GitHub→board, with every key that
carries an un-mirrored `status` or `rename` event off-limits to intake
FOR THAT ASPECT (per-aspect, not per-key — a pending rename must not
suppress a genuine GitHub close); (4) mirror board→GitHub; (5) `ledger
push` if intake wrote; (6) persist state if anything changed. Step (1)
is itself a law: **the bridge syncs first** — two drifted replicas
running unsynced bridges would each duplicate the other's keys as new
issues because the link notes haven't merged (spike finding 13); syncing
first makes the bridge host-portable (spike-verified: the cursor, links,
and import marks all travel on the chain).

**Echo suppression, three rules, deliberately independent**: the outbound
mirror skips events authored `github:@*` (everything intake writes) and
notes of kind `bridge-state`/`github-link` (bookkeeping, matched by KIND,
never author); inbound intake skips comments whose GitHub author login is
the bridge's own `gh` identity (primary check — the spike's body-prefix
marker `**<author>** (via ledger):` demotes to a secondary heuristic,
because a human typing the prefix must not be silently dropped).

**Level 1 — mirror out** (drained via `since`, idempotent by event id):
new keys → `gh issue create` (title, `ledger-key:` body line) + link
note; status closed/wontfix → close (wontfix = "not planned"), reopen →
reopen; renames → retitle; notes → comments prefixed
`**<author>** (via ledger):`. **Close fidelity, pinned** (the spike's
most visible information loss): a close mirrors as a comment carrying the
close message and evidence ref, THEN the close — a GitHub reader must
never see a bare unexplained closure. `blocked-by` edges have no GitHub
representation and are not mirrored — stated, not silent.

**Level 2 — intake, additive only**: new GitHub issues (no `ledger-key`
line, not bridge-created) → seed, key = slugified title
(collision-suffixed), author `github:@<login>`, links written both ways;
new comments by others → notes authored `github:@<login>`; title edits on
linked issues → `set --rename` authored `github:@<login>`; close/reopen →
the matching guarded write. **Attribution is real**: GitHub's issue JSON
names the resulting state, not the actor, so intake pays one timeline
call per changed issue for the true `closed`/`reopened`/`renamed` actor
(fallback: issue author, with a warning). Closes carry evidence
`gh:<owner>/<repo>#<n>`, satisfying `--require-evidence`.

**Guarded writes follow the doctrine verbatim**: `--expect` from a fresh
read; on `claim_lost` re-read once and retry; on failure after retry,
write a `handoff` note naming what could not be applied and warn in the
report — never a blind re-write. **The override ruling, decided** (spike
flagged auto-override of every standing signal): the bridge auto-passes
`--override` for `claim` and `settled` signals — a GitHub actor's state
change is a real decision by a real person, and the override is
tool-recorded and attributed to `github:@<login>` for later triage. For
a **`human`-labeled key the bridge never overrides**: it leaves the
handoff note AND posts a GitHub comment ("this issue is reserved on the
board; a maintainer must apply this change there") — the bridge cannot
know whether the GitHub actor IS the reserving human (login↔author
identity mapping is v2), and eroding the one label that means "a person
owns this" at machine speed is the exact failure the issues spec warns
about. Same rule for human-gated renames arriving from GitHub.

**Out of scope, named**: Level 3 state sync (labels/assignees — the
`github:@name` author namespace is the only assignee-shaped thing in v1);
issue deletion/transfer (detected as a broken link at sync time → warning
+ handoff note, no tombstone machinery); webhooks (polling only — the
bridge runs when someone runs it, offline-first); pagination beyond the
first 200 issues per run; rate-limit backoff. CLI rough edges the spike
surfaced go to the tool's backlog, not this spec: a latest-event-of-kind
read, and write-then-fold in one call.

## Test plan

1. Rename fold: none/one/several; concurrent across replicas via real
   sync (converge + loser greppable); title survives claim/close; seed
   message never resurrected; sync-arrival-before-seed titles the key.
2. Rename gates: human-labeled key ⇒ `needs_override`, satisfied by
   `--override`; claim/settled do NOT gate; all bad_usage combos;
   `--expect` CAS both ways; plain-board and unknown-key refusals.
3. Render: every title-bearing surface labeled (JSON renamed info, TTY
   marker, identity header, blocked-line title, status drill-down title);
   unrenamed boards byte-identical to rev 15; determinism test extended
   with a renamed key in its fixture.
4. Bridge ordering: the spike's falsification as a regression fixture —
   close a key, run the bridge once, assert exactly one GH close, zero
   board writes, and no fabricated attribution.
5. Idempotence: double-run is 0/0 with byte-identical board head; state
   persists only on change.
6. Echo: `github:@*` events never mirror; bookkeeping notes never mirror;
   bridge-authored comments never intake (author-login check primary).
7. Guarded intake: close against a claimed key retries once then leaves a
   handoff note; human-labeled key ⇒ no write, handoff note + GH comment.
8. Close fidelity: mirrored close carries message + evidence comment.
9. High-water marks: comment import survives re-runs bounded; a new
   comment below an imported id is not re-imported.
10. Host portability: bridge state rides the chain; post-sync bridge run
    from a second replica is 0/0 (spike-verified, kept as regression).
11. Bridge tests run against a FIXTURE transport (recorded gh JSON), plus
    one live smoke against the scratch repo — the live trial is the
    acceptance gate, not CI.

## Trial plan (acceptance, live)

Re-run the spike's five steps against a scratch repo with the production
binaries, PLUS: the human-labeled refusal path (reserve a key, close its
issue on GitHub, verify handoff note + GH comment + no board write); a
rename needing `--override` from both directions; and the previously
unexercised override-on-intake path (claimed key closed on GitHub).

## Validation record

- Spike (branch `spike/bridge`, report in the session workspace; trial
  against prime-radiant-inc/ledger-bridge-spike, all five steps green;
  rename ≈190 LOC + 318 test, bridge ≈1034 LOC + 139 test; full suite
  green with the standing determinism test). Falsified live and fixed:
  intake-first ordering (fabricated `override: settled` attributed to a
  human who did nothing, then opposite-direction disagreement in one
  run); unconditional state persistence (cursor chases its own tail —
  idempotence false by construction). Spike-pinned and spec-adopted:
  rename as a set-field; note-key bridge state; kind-based bookkeeping
  suppression; drain-before-write; doctrine-path guarded writes; real
  actor attribution via timeline. Spec-overridden from the spike: renames
  now human-gated (spike: fully unguarded, flagged by its own builder);
  bridge never auto-overrides `human` (spike: overrode everything with
  attribution); comment tracking by bounded high-water (spike: unbounded
  node-id sets); bridge-comment recognition by author login (spike: body
  prefix); close mirrors carry message+evidence (spike: bare close);
  bridge syncs first (spike: documented hazard only).
