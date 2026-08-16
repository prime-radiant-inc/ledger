# Issues-tracker spike trial — two concurrent workers, one board

2026-08-15. Spike (branch `wip/issues-spike`, commit 137910c, no tests by
design): `--multi-field`, `--terminal`, `show --where`, and the `ready` verb
on the real Go tool. Board: seven issues, a dependency diamond
(setup-schema → {write-parser, write-emitter} → integration → docs) plus two
immediately-ready items to force a first-pick race. Two Sonnet workers
("rex", "blue") ran the picking loop concurrently from a one-page doctrine
file. Ground truth audited from the chain, not worker prose.

## What worked

- **`ready` is correct and agents drive it well.** Dependency order held
  exactly: setup-schema closed before parser/emitter claims, integration
  after both, docs last. Zero tool errors across both workers. `blocked` +
  `waiting_on` earned its place: blue used the transitive chain to decide
  the remaining work belonged to rex's in-progress claim and exited cleanly;
  rex used it to sanity-check the DAG as dependencies resolved.
- **One claim race resolved exactly per doctrine** (setup-schema, claims one
  second apart): the loser saw the winner's event, yielded, never touched
  the key again.
- **The chain is a complete forensic record.** The failure below was
  diagnosed entirely from `tail --raw` — every claim, close, author, and
  second-by-second ordering. No tracker log could say more.

## What failed: the claim-verify doctrine is a snapshot, not a lock

On `lint-cleanup`, BOTH workers did the work and closed it:

```
02:28:45  lint-cleanup  in-progress  rex     ← rex claims, verifies: clean
02:28:48  lint-cleanup  in-progress  blue    ← blue claims, verifies: sees itself latest — also "clean"
02:28:51  lint-cleanup  closed       rex
02:28:53  lint-cleanup  closed       blue
```

Each verification was correct *at the moment it ran*. The doctrine's
verify-once idiom only catches a race that landed before your check; a claim
landing after your check but before your close is invisible to both sides.
Result: duplicate work (done.log carries both lines), duplicate closes, and
no signal to either worker — rex found it only by idle curiosity afterward.
Spike open question 2 is answered: **doctrine alone is insufficient;
claiming needs a mechanism.**

The right primitive is a **conditional write**: `set <key> … --expect
<event-id>` fails (`claim_lost`, exit 4) unless the key's latest event still
is the one the claimant read. The store's CAS retry loop can re-validate the
precondition on every retry, making it genuinely atomic — first claim wins
mechanically, the loser gets a clean error instead of a stale snapshot. This
is generally useful beyond claiming (any read-modify-write on a key).

## Smaller findings

- **Last-write-wins claiming inverts intuition**: in the setup-schema race
  the *later* claimant won (the fold shows the latest event). Consistent and
  therefore safe, but a late claimer silently "steals" from an earlier one
  who hasn't verified yet. `--expect` fixes this too (the second claim fails
  its precondition), making claiming first-wins.
- **Stop-condition doctrine needs a sentence**: blue had to infer that
  "blocked on another worker's in-progress key" counts as "someone else is
  finishing." Reasonable inference, but a different agent might poll forever.
- **`ready`'s oldest-first ordering ties** at same-second timestamps; chain
  position already breaks the tie deterministically — document that.
- **Evidence path ambiguity** (`--evidence file:done.log` vs the actual
  `../done.log`): evidence refs are unvalidated strings by design, so both
  "worked"; a doctrine nit, not a tool defect.

## Verdict

The spike validates the design: multi-fields, `--where`, `--terminal`, and
`ready`'s two-list output are right, and agents run the picking loop
correctly from one page of doctrine. The one falsified spec claim is
"claim discipline: deliberately no mechanism" — the trial produced the exact
duplicate-work failure that rule was betting against, inside a two-worker,
seven-issue board, within 90 seconds. Spec rev 2 adds `--expect` and demotes
verify-after-claim to a fallback for tools without it.
