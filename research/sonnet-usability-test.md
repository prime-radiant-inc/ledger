# Sonnet usability test — ledger prototype

2026-08-13. Nine Sonnet subagents drove the throwaway spike (`ledger.py` on git phantom refs, generalized enum fields) through three project scenarios, cold, with a kata-style QUICKSTART as their only teaching. Wave 1: spine implementer (works, then "dies"), fleet orchestrator (setup + dictated briefs), handoff investigator. Wave 2: cold plan resume, three parallel brief-driven workers, fleet monitor, cold investigation resume.

## Outcomes: 6/6 scenarios succeeded end-to-end

- **Execution spine + relay**: implementer seeded all plan tasks as keys, marked completions with commit-range evidence, left a handoff note. Cold successor read the spine, **verified every evidence ref against `git log` and the test suite before trusting it**, did exactly the next task, left its own evidence and handoff. Zero friction on the resume.
- **Handoff/checkpoint**: investigator structured the ledger into the two registers unprompted (keyed items for bug/hypothesis/dead-ends; notes for gotcha/ruling/handoff), hit the vocab hard error, pasted the printed fix, and **deliberately declined to invent evidence refs**, flagging claims as "testimony pending the logging run." Cold successor reconstructed all six briefing facts correctly and independently classified the no-evidence claims as unverifiable testimony.
- **Fleet coordination**: orchestrator created the scoreboard, self-verified `set` syntax before dictating it, wrote three briefs with fully-spelled commands, and reported the correct post-setup cursor. All three workers followed dictated grammar with zero friction; concurrent CAS writes all landed. Monitor drained all six events from the seeded cursor in one watch call, exercised the timeout contract (exit 2, cursor intact), and verified files against evidence before declaring the fleet done.

Doctrine validation: the ~10-rule quickstart was absorbed and correctly applied by every agent — including resume-and-verify and testimony-not-commands, the two rules that matter most.

## Friction findings → design items

1. **`--help` is a write hazard (the headline finding).** Five of six cold agents probed `--help`. The spike has no help handling: bare `--help` silently succeeds empty; `create --help` **created a real ledger named `--help`** (twice, independently); `watch --help` dumped the entire event stream as if it were an answer; unknown verb `log` silently succeeded; bare invocation raw-tracebacked. Spec consequences (mostly already present, now field-validated): the slug grammar `[a-z0-9][a-z0-9-]*` is a *safety* mechanism (it rejects flag-shaped slugs), `--help`/`-h` must work on every verb, unknown verbs must error, `--` must be respected, and exploratory invocations must never have write side-effects.
2. **Empty results must say so.** Empty `ls` printed nothing — indistinguishable from a hang. Zero-open implicit resolution errored `ambiguous_ledger: open ledgers: none` — wrong name for the situation; needs a distinct `no_open_ledger` error pointing at `create`.
3. **`create` must report its event SHA** (orchestrator had to recover the cursor via `tail`). Spec rev 9 already mandates this; validated.
4. **Verb-addressing consistency matters** — `since worker-fleet` swallowed the slug as a cursor and errored `reset_required`. Spec rev 9's addressing rule (positional for lifecycle verbs, `--ledger` for data verbs) exists precisely for this; the spike predates it; field-validated.
5. **JSON contract must cover every verb** (`close`/`vocab` plain-printed while everything else emitted JSON).
6. **Spine rows should render their `-m` annotation.** The cold investigator's sharpest observation: `status` alone is thin — the substance lived in notes. Expected under the two-register design, but the one-line `-m` on a status event is exactly what belongs in the spine row, and the spike's renderer dropped it.
7. **Quickstart additions**: one-place verb list; "empty output means empty result"; `notes` flags documented; explicit statement that `--ledger` is required when multiple ledgers are open.

## Verdict

The design's load-bearing ideas — two registers, evidence-backed claims, vocab hard-errors with self-service extension, dictated grammar for fleets, cursor-based watch, resume-and-verify doctrine — all work at Sonnet level, cold. Every friction item is CLI surface (help, errors, output consistency), not architecture. The generalized enum-field model produced zero confusion.

Spike + scenario repos live in the session scratchpad (throwaway); this report is the durable record.
