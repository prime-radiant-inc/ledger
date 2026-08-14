# Fix batch: skill-acceptance eval findings

Branch `fix/eval-findings` off `main` (0fa8a60). Three commits, TDD throughout
(failing test first, minimal implementation, green, commit). Not merged.

```
835b5f4 docs: align skill prose with the default vocab, fill five quickstart gaps
e2bd4dd store resolution: name the store the ancestry shadowed
3c56b16 read verbs: an unexpected positional is bad_usage naming --ledger
```

Full suite: **all packages pass** (`cd ledger && go test ./...`, ~155s).

```
?   	ledger	[no test files]
?   	ledger/docs	[no test files]
ok  	ledger/internal/cmd	30.288s
ok  	ledger/internal/docs	(cached)
ok  	ledger/internal/fold	0.958s
ok  	ledger/internal/gitx	0.365s
ok  	ledger/internal/model	1.334s
ok  	ledger/internal/out	0.447s
ok  	ledger/internal/store	119.267s
```

`go vet ./...` clean, `gofmt -l .` empty.

---

## Finding 1 — positional slug on a no-positional read verb

**What changed.** `noPositionals(suggest string) cobra.PositionalArgs` in
`ledger/internal/cmd/root.go` replaces `cobra.NoArgs` on the read verbs that
address their ledger by flag. Cobra's `NoArgs` produced `unknown command
"csvstat" for "ledger show"`, which `ExecuteArgs`' substring mapping then
classified as `unknown_verb` with a hint pointing at the verb list — never at
`--ledger`. The new validator returns a typed `out.CLIError`:

- error `bad_usage`, exit 4 (unchanged exit)
- message: `ledger show takes no positional arguments (got "csvstat")`
- hint: `did you mean: ledger show --ledger csvstat?`

Verbs switched: `show`, `notes`, `tail` (read.go), `watch` (cursor.go),
`render` (render.go), `ls` (ls.go). `ls` has no `--ledger` of its own, so its
`suggest` is `show` — the hint is `did you mean: ledger show --ledger <arg>?`.

Untouched by design: `status [key]` and `since [cursor]` legitimately take a
positional; the root's `unknown command` → `unknown_verb` mapping still fires
for a genuinely unknown subcommand.

**Tests added** (`ledger/internal/cmd/root_test.go`):
`TestPositionalSlugOnReadVerbSuggestsLedgerFlag` — loops show/tail/notes/
watch/render asserting exit 4 + `bad_usage` + the exact per-verb hint, checks
`ls demo` points at `show`, and pins that `ledger frobnicate` stays
`unknown_verb`.

**Manual repro** (built binaries: `ledger-before` from main, `ledger-after`
from the branch; scratch repo with a real ledger `csvstat`):

```
=== BEFORE: ledger show csvstat
{"error":"unknown_verb","hint":"run `ledger --help` for the verb list","message":"unknown command \"csvstat\" for \"ledger show\""}
exit=4
=== AFTER:  ledger show csvstat
{"error":"bad_usage","hint":"did you mean: ledger show --ledger csvstat?","message":"ledger show takes no positional arguments (got \"csvstat\")"}
exit=4
=== AFTER:  ledger ls csvstat
{"error":"bad_usage","hint":"did you mean: ledger show --ledger csvstat?","message":"ledger ls takes no positional arguments (got \"csvstat\")"}
exit=4
=== AFTER:  ledger frobnicate
{"error":"unknown_verb","hint":"run `ledger --help` for the verb list","message":"unknown command \"frobnicate\" for \"ledger\""}
exit=4
=== AFTER:  the hinted fix — ledger show --ledger csvstat
{
 "events": 1,
 "head": "df38a3bc8c",
 "ledger": "csvstat",
```

TTY render of the same error:

```
error: ledger show takes no positional arguments (got "gateway-502")
  fix: did you mean: ledger show --ledger gateway-502?
```

## Finding 2 — ancestor-store breadcrumb on shadowing

**What changed.**

- `store.Resolve` now returns a `store.Resolution{Store, Note, Shadowed}`
  instead of `(Store, string, error)`. `Note` is the pre-existing
  same-directory collision message, unchanged; `Shadowed` is new.
- `shadowedAbove(dir, wantBare)` continues the ancestor walk *above* the
  directory Resolve stopped at, looking only for a store of the **other**
  kind: a bare `.ledger.git` above a chosen repo, or a repo above a chosen
  bare store. Stat calls only (`exists`), and it stops at the filesystem
  root — the same boundary Resolve's own walk uses (Resolve has no `$HOME`
  boundary; I matched what the code does, per instruction).
- Same-kind ancestors stay silent: a repo inside a repo is the ordinary
  nested-checkout case where nearest-wins is the intended answer.
- Ambient resolution only. An explicit `--store` or `$LEDGER_DIR` returns
  `Shadowed: ""`.
- `Ctx.Shadowed` carries it into the verbs (`root.go` PersistentPreRunE).
- `ls` (all three output paths: no refs, all-filtered-out, and a real
  listing) gains a top-level JSON field `"shadowed_store": "<path>"` and a
  trailing TTY line: `note: another ledger store exists at <path> (this one
  was chosen) — read the other with --store <path>`.
- `Ctx.shadowHint` appends ` — a second store exists at <path>: try --store
  <path>` to the `unknown_ledger` (resolve.go `Load`) and `no_open_ledger`
  (`PickLedger`) hints — the two dead ends the eval's investigator hit.

**Tests added.**

`ledger/internal/store/store_test.go`:
- `TestResolveShadowedAncestorStore` — the eval layout (bare store above a
  project repo): the repo still wins, `Shadowed` is the bare store; plus the
  reverse direction (a bare store nested in a repo shadows the repo).
- `TestResolveShadowIsAmbientOnly` — `--store` and `$LEDGER_DIR` report
  nothing.
- `TestResolveSameKindAncestorIsNotShadowing` — nested repos stay quiet.
- Existing `TestResolveOrder` / `TestResolveAncestorWalkUp` /
  `TestResolveSameDirCollision` adapted to the struct return;
  `TestResolveSameDirCollision` also now asserts the collision case is the
  `Note` case with no `Shadowed`.

`ledger/internal/cmd/shadow_test.go` (new) with a `shadowedLayout(t)` helper
that builds the eval's layout in `t.TempDir()` (misplaced `ledger init` at the
root creating the bare store with the real ledger, project repo below,
`t.Chdir` into it, `LEDGER_DIR` cleared) and an `ambient()` runner that omits
`--store`:
- `TestLsNamesShadowedAncestorStore` — `ls` and `ls --all` carry
  `shadowed_store`; a store with no shadowed ancestor has no such field.
- `TestLsTTYNamesShadowedStore` — the trailing TTY note.
- `TestUnknownLedgerAndNoOpenLedgerHintsNameShadowedStore` — both hints name
  the other store, and the hinted `--store` command actually reads the ledger.

`root_test.go`'s `initRepo` was split into `initRepo` / `initRepoAt(t, dir)`
so a test can place the repo at a specific path.

**Manual repro.** Sandbox root `$D` with `$D/.ledger.git` (created by
`ledger init` run from the sandbox root, holding `gateway-502` with two
events) and the project repo at `$D/proj-gateway`; all commands run from
inside `proj-gateway`:

```
=== BEFORE: ls
{
 "ledgers": [],
 "ok": true
}
exit=0
=== BEFORE: show --ledger gateway-502
{"error":"unknown_ledger","hint":"ledger ls --all  (lists every ledger here)","message":"no ledger 'gateway-502' here"}
exit=4
=== BEFORE: status
{"error":"no_open_ledger","hint":"ledger create <slug> --scope <what-it-tracks>  starts one; ledger ls --all lists closed ones","message":"no open ledgers in this repo"}
exit=4

=== AFTER: ls
{
 "ledgers": [],
 "ok": true,
 "shadowed_store": "/private/tmp/.../fix2-QCck/.ledger.git"
}
exit=0
=== AFTER: show --ledger gateway-502
{"error":"unknown_ledger","hint":"ledger ls --all  (lists every ledger here) — a second store exists at /private/tmp/.../fix2-QCck/.ledger.git: try --store /private/tmp/.../fix2-QCck/.ledger.git","message":"no ledger 'gateway-502' here"}
exit=4
=== AFTER: status
{"error":"no_open_ledger","hint":"ledger create <slug> --scope <what-it-tracks>  starts one; ledger ls --all lists closed ones — a second store exists at /private/tmp/.../fix2-QCck/.ledger.git: try --store /private/tmp/.../fix2-QCck/.ledger.git","message":"no open ledgers in this repo"}
exit=4
=== AFTER: hinted fix — ledger --store $D/.ledger.git show --ledger gateway-502
{
 "events": 2,
 "head": "b6efc65657",
 "ledger": "gateway-502",
 "ok": true,
```

(Paths elided to `/private/tmp/.../fix2-QCck` for width; the real output
carries the absolute path twice, as the hint shape requires.)

TTY render:

```
no ledgers in this repo — ledger create <slug> --scope <ref> starts one
note: another ledger store exists at /private/tmp/.../fix2-QCck/.ledger.git (this one was chosen) — read the other with --store /private/tmp/.../fix2-QCck/.ledger.git
```

## Finding 3 — documentation

`skills/using-ledger/SKILL.md`:
- (a) "seed a pending row per worker" → "seed a row per worker
  (`status=open`)".
- (b) close example comment: `ledger close scratch-slug --as-state abandoned
  # or shipped, or superseded`.

`ledger/docs/quickstart.md` — **still exactly 90 lines**, the enforced budget
(`TestQuickstartLengthBudget` fails above 90; the file was already at 90, so
every added line had to be paid for):
- (b) rule 12 now reads ``One call: `close <slug> --as-state
  shipped|abandoned|superseded`.``
- (c) rule 7 now leads with "**`watch` exits 0 with events, 2 on timeout** —
  cursor in the payload either way", keeping the finite-60s default,
  `--timeout 0`, and the `starting_cursor` announcement.
- (d) rule 6 gains "`since` with no cursor drains from the very beginning".
- (e) rule 2 (Orientation) now opens with "Commands resolve against the repo
  you're standing in — run them, and `init`, from inside the project, not
  from a parent."

Lines paid for by reflowing, with no doctrine dropped:
- intro: 4 lines → 3 (`ledger quickstart --orchestrator` → `--orchestrator`;
  the sentence is inside the quickstart itself).
- rule 3: 7 → 6 ("fields declared `--require-evidence` hard-error on those
  values without one, and `show` lists which values are required" → "values
  declared `--require-evidence` hard-error without one, `show` lists them").
- rule 10: 3 → 2 ("One lands, stop and tell your operator — rotate first,
  clean up second" → "One lands: stop, tell your operator, rotate first").

Nothing had to be relocated to `quickstart-orchestrator.md`; all five doc
lines fit. (Confirmed the orchestrator doc has no line budget — the docs test
only measures `quickstart.md`, though it executes fenced `ledger ...` lines in
both.)

---

## Judgment calls and deviations

1. **Verb list for fix 1 goes slightly past the named reads.** I included
   `render` (a read verb with `--ledger`, `NoArgs`, not named in the brief)
   and `ls` (which *ignored* stray positionals rather than rejecting them —
   `ledger ls csvstat` silently printed the whole listing). Both are the same
   misfire class. I did **not** touch `note`, which is `NoArgs` and
   `--ledger`-addressed too but is a write verb, outside the brief's "read
   verbs" scope — worth a follow-up decision.
2. **`store.Resolve` signature changed to a struct** rather than adding a
   fourth return value. Two adjacent bare strings (`note`, `shadowed`) at a
   call site read as a coin flip; the struct names them. Four call sites
   touched (root.go + three tests).
3. **Only the *other* kind of store counts as shadowing.** Per the brief, and
   because a repo inside a repo (worktrees, vendored checkouts, nested
   clones) is normal and a notice there would be pure noise. Consequence: two
   bare stores stacked in one ancestry stay silent.
4. **Boundary matches existing behavior: filesystem root, not `$HOME`.**
   Resolve's own loop stops only at the root (`dir == filepath.Dir(dir)`);
   there is no `$HOME` check in the code today, so the continuation walk
   mirrors it. Cost is a handful of extra `os.Stat` calls per invocation.
5. **The hint repeats the path twice** (`a second store exists at <path>: try
   --store <path>`), which is long for deep absolute paths. Kept because the
   brief specified that shape and it stays copy-pasteable; a one-mention
   variant ("try --store <path> — a second store lives there") would be
   shorter if wording is revisited.
6. **`ls` carries the breadcrumb on *every* listing**, not only empty ones —
   a non-empty repo store can shadow an ancestor just as misleadingly.
7. **Quickstart trims are rewordings, not deletions**; each doctrine point in
   the three shortened passages survives in the new text.
8. The scratchpad already contained a `.ledger.git` at its root from earlier
   sessions, which polluted a first repro attempt; the transcripts above come
   from a fresh `mktemp -d` sandbox whose nearest ancestor store is the one
   the test created.
