# Roll-up records for the ledger: OptMem study + research synthesis

2026-08-14. Inputs: a close read of [VictorTaelin/OptMem](https://github.com/VictorTaelin/OptMem)
(clone studied at HEAD, one ~860-line Python file), the prior-art survey
(`rollup-prior-art.md`), and the academic survey (`rollup-academic.md`).

## What OptMem actually is

An append-only log of one-line memories (≤280 bytes each, fixed-width 320-byte
records so position is identity and every lookup is one seek), plus a **binary
merge tree of one-line summaries** stored as a rebuildable cache. Block
`[lo,hi)` — always an aligned power-of-two range — is the compression of its
two halves. The parts that matter:

- **The agent is the compressor.** The tool never calls a model. When a block
  becomes summarizable, `note`'s output appends a prompt: "Compress memories
  #16–31 into one line… Run: `memo nap 16-31 "<your line>"`". The agent pays
  one summary before its next action. Nothing runs in the background.
- **Read-time detail decay.** `wake` covers the whole log with at most
  `WAKE_LINES` (~96) blocks, chosen by binary-searching a threshold α where a
  block stays whole iff `size ≤ α·age`. Recent memories print verbatim;
  ancient ranges collapse to one summary line; leftover budget is spent
  splitting the newest blocks. The whole life always fits one read, and
  **everything is covered at some resolution** — nothing is ever invisible,
  only coarse.
- **Drill-down.** Every printed `#a-b` line is a tree node; `zoom a-b` opens
  it into its two halves, down to raw records. `recall <regex>` greps the raw
  log. Summaries route; the log answers.
- **Summaries are a disposable cache.** `TREE/<size>` level files hold a dense
  prefix of fixed-width records; `forget` truncates a level (and everything
  built on it) and the next `nap` rebuilds. The log is never touched.
- **Merge debt is metered and lazy.** One nap prompt per note; `wake` refuses
  only when the *document it must print* needs a missing summary — debt that
  wake doesn't need is paid after the read, costing no round trip.

The craftsmanship detail worth stealing regardless of the rest: every prompt
the tool prints is **self-addressed and copy-pasteable** (`ME` = the tool's own
path, so the printed command always runs), the same discipline our
`vocab_unknown` hint already follows.

## What the research says (both surveys, compressed)

- **The shape is convergent and near-optimal.** Aligned power-of-two roll-ups
  = Merkle Mountain Ranges = Bentley–Saxe merging = DGIM exponential
  histograms (SODA 2002), which prove a logarithmic block budget covers
  exponential history with uniform-relative-error recency weighting, and that
  you can't asymptotically beat it. Alignment makes every summary
  **write-once**: appends never invalidate an existing node, and which ranges
  need summaries is computable from the event count alone.
- **Write-once merge trees beat rolling rewrites.** BooookScore (ICLR 2024)
  measured it: hierarchical merging is more coherent than incrementally
  rewriting one running summary, which drifts. MemGPT's recursive chain and
  LangChain's summary memory are the saturating alternative; Claude Code's
  own compaction is the pain everyone already knows.
- **The raw log must stay authoritative.** Every system that aged well
  (event-store snapshots, MMR, Thanos, Zep, RAPTOR, Generative Agents) keeps
  derived layers as caches over intact raw data; every system users curse
  (RRDtool, mem0's in-place UPDATE/DELETE, LangChain, compaction) destroys it.
- **Known failure modes with mitigations:** detail loss compounds with tree
  height (OpenAI book work) — mitigate by grounding merges in sampled raw
  lines, not just the two child summaries (arXiv:2502.00977), and by making
  summaries **cite concrete anchors** (event ids, filenames, numbers) the way
  Generative Agents' reflections cite evidence records. Drill-down routing is
  fragile with weak models (MemWalker) — summaries are signposts first, gists
  second. And RAPTOR's ablation: collapsed-tree (search all levels at once)
  beats top-down traversal — **navigation must never be the only read path**;
  flat grep over raw events stays first-class.
- **Purely positional decay is the weakest assumption.** Importance scoring
  (Generative Agents), access-refreshed retention (MemoryBank, ACT-R) all
  argue big events should resist compression. Cheapest fix in our setting:
  pinned/important events stay verbatim in ancestor renderings.

## Does the ledger need this?

Honest YAGNI check first. Our phase-1 corpus ledgers are small — SDD spines
and scoreboards run dozens of events, and the round-3 eval ledgers held 6–15.
`show` and `status` fold to *current state*, which doesn't grow with history;
the things that grow are `tail`/`notes` (narrative register) and the cold-read
"story so far." Today, none of that hurts at observed sizes.

Where it will hurt, on the trajectory we already designed for: long-lived
investigation ledgers (weeks of repro/hypothesis events), fleet scoreboards
that outlive many dispatch waves, and any ledger that becomes the durable
memory of a project rather than one plan. The corpus's handoff-note pattern is
already a manual, unstructured roll-up: agents write "the story so far" notes
because no tool does it. OptMem shows the tool-shaped version.

So: **not a v1.x must; a strong v2 candidate with a clear trigger** — the
moment we observe real ledgers where cold agents page through `tail` output or
handoff notes stack up (say, >100 events with multiple resumes), the roll-up
pays for itself.

## Design sketch, if/when we build it

Everything below falls out of marrying OptMem's mechanism to our storage and
trust model; noted here so the thinking isn't lost.

1. **Out-of-band cache, not events.** Summaries live in a parallel ref
   (`refs/ledger-rollup/<slug>`), one commit per block keyed `(lo,hi)` against
   the event chain's total order — never as events in the fold. Precedent:
   event-sourcing snapshots are kept outside the stream, keyed to a revision;
   rebuildable when the summary prompt changes; invisible to `since`/`watch`
   cursors and sync semantics. (Generative Agents' reflections-as-events is
   the counter-precedent, but it costs rebuildability and pollutes the fold.)
2. **Summaries are testimony with provenance.** Unlike OptMem's anonymous
   lines, ours carry author + committer marker like any write. A summary of
   other agents' events is *second-order* testimony: it must cite the event
   ids it compresses and must never launder evidence — "3 tasks done
   (evidence: commits)" not restating the claims as facts. This is our
   doctrine extended one level, and it's a genuinely novel corner: none of the
   surveyed systems have multi-writer, provenance-tracked summaries.
3. **Multi-writer merge debt needs CAS, and reads must never block.** OptMem
   is single-identity; its `wake` can refuse until the agent pays a summary.
   In a fleet, a monitor must never be blocked by another agent's summary
   debt. So: reads degrade gracefully (print the raw range, or the finest
   available cover) and merely *suggest* the nap; block summaries are claimed
   by CAS on the rollup ref (two agents racing to summarize the same block —
   first wins, loser discards). The write-once/aligned property makes the race
   benign.
4. **Ground the merge and cite anchors.** The nap prompt hands the agent the
   two child summaries *plus* the raw events of the range (they're one
   `cat-file --batch` away — we own the log), and instructs it to keep event
   ids/evidence refs as anchors. Both are measured mitigations for compounding
   loss.
5. **Coverage as an invariant, search stays flat.** The rendered "story so
   far" covers the entire event range at some resolution (the differentiator
   vs. retrieval memories, worth stating and testing), while grep-style reads
   over raw events remain first-class — RAPTOR's collapsed-tree lesson.
6. **Meter the debt.** One nap suggestion per write, never a catch-up batch
   (Lucene/Anki/OptMem all converge here). Amortized cost is O(log n)
   summary-writes per event — paid in agent attention, so the schedule is the
   product.

Open questions to settle before any build: whether the roll-up unit is the
event or the *note* register only (state events fold to nothing — maybe only
narrative needs compression); whether block boundaries should snap to
session/handoff boundaries instead of strict powers of two (ReadAgent's
episode-boundary result vs. MMR's write-once alignment — alignment probably
wins, with handoff notes as pinned events); and where the read surfaces it
(`show --story`? a new `story` verb? the renderer?).

## Bottom line

OptMem is the best small design we've seen in this space: it independently
reinvented a provably near-optimal structure (DGIM/MMR), avoids every failure
mode the literature documents (rolling rewrite, destroyed raw, retrieval
holes), and its agent-as-compressor-on-a-tool-schedule move has almost no
shipped precedent (Generative Agents and Anki are the closest). Its ideas
transfer to the ledger cleanly, with two genuinely new problems our setting
adds — multi-writer summary debt and provenance-preserving second-order
testimony — both of which our existing machinery (CAS, committer markers,
evidence discipline) already covers. Recommended disposition: hold as a
designed-but-unbuilt v2 feature with an observed-need trigger; steal the
copy-pasteable self-addressed prompt discipline now.
