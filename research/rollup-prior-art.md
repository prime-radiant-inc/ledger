# Prior Art: Roll-Up Records over Append-Only Logs

Survey of shipped systems and tools relevant to the OptMem "roll-up record" idea:
a binary merge tree of one-line LLM-written summaries over an append-only log,
where read output covers the log with power-of-two blocks whose allowed size
grows with age (recent = verbatim, ancient = one line), summaries are a
rebuildable cache, and the agent itself is the compressor on a tool-managed
schedule.

Researched 2026-08-14 for the git-backed ledger CLI design.

**The distinction we care most about:** does the system keep the raw log
authoritative (derived layer is a cache) or does compaction/summarization
destroy raw data? Flagged per item as **RAW KEPT** or **RAW DESTROYED**.

---

## 0. The reference design: OptMem itself

[VictorTaelin/OptMem](https://github.com/VictorTaelin/OptMem) — `LOG.txt` is
"append-only, never edited"; the `TREE` directory of one-line summaries
(`#0-1`, `#2-3`, pairs of those as `#0-3`, and so on) is "a cache, rebuildable
from the log alone." `memo wake` prints one line per tree node covering the
whole log; `zoom` opens a node into its two halves, down to raw memories. When
a merge is due, the tool "asks a compression: do it before your next action" —
the agent writes the summary inline; "nothing ever runs in the background."
**RAW KEPT.** Structurally this is a Fenwick-tree/Merkle-mountain-range layout
with LLM summaries at internal nodes, plus a synchronous, tool-scheduled,
agent-executed compression step. As far as this survey found, no other shipped
system combines all three of: power-of-two aligned roll-ups, raw-log-as-truth,
and agent-as-compressor on the read path.

---

## 1. Log compaction and snapshotting in event/stream systems

### Kafka compacted topics — RAW DESTROYED

[Kafka log compaction](https://docs.confluent.io/kafka/design/log_compaction.html)
retains at least the last value per message key and garbage-collects superseded
records; a null-value "tombstone" deletes a key entirely, and tombstones
themselves are purged after a window
([Confluent course](https://developer.confluent.io/courses/architecture/compaction/),
[Conduktor glossary](https://www.conduktor.io/glossary/kafka-log-compaction-explained)).
Compaction runs in the background on the *tail* of the log; the head (recent
segments) stays verbatim — so Kafka independently arrived at "recent = raw,
old = condensed," with the boundary set by dirty-ratio and min-lag configs
rather than power-of-two ages. What it gets right: compaction is
*key-semantic*, not positional — it knows which old records are superseded
because keys make redundancy explicit. Sharpest lesson: Kafka can only do
lossy compaction safely because "latest value per key wins" is a mechanical
rule; a ledger of prose events has no such rule, which is exactly why the
compressor must be an agent (judgment) and why the raw log must survive
(judgment is fallible). Also cautionary: Kafka compaction rewrites segments
in place and the pre-compaction history is unrecoverable — the opposite of a
rebuildable cache.

### Event-sourcing snapshots (EventStoreDB/Kurrent, CQRS practice) — RAW KEPT

A snapshot records aggregate state at a specific stream revision; reads load
the newest snapshot plus events after it
([Kurrent: Snapshots in Event Sourcing](https://www.kurrent.io/blog/snapshots-in-event-sourcing/),
[Domain Centric](https://domaincentric.net/blog/event-sourcing-snapshotting),
[CodeOpinion](https://codeopinion.com/snapshots-in-event-sourcing-for-rehydrating-aggregates/)).
Triggers in the wild: every N events, on a replay-latency metric (community
rule of thumb: consider snapshots when replay exceeds ~100ms), synchronously
after write, or asynchronously in the background. Doctrine is emphatic on two
points: the event log remains the source of truth, and snapshots are a
disposable optimization you can delete and rebuild — the community explicitly
warns against snapshotting before metrics prove you need it
([thinkbeforecoding](https://thinkbeforecoding.com/post/2010/02/25/Event-Sourcing-and-CQRS-Snapshots)).
This is the closest philosophical match to the roll-up record: derived state
keyed to an immutable log position, invalidated by rebuild rather than edit.
Sharpest lesson: snapshots version poorly — when the folding logic changes,
old snapshots are silently wrong, and the standard fix is "throw them all
away and rebuild from the log." A roll-up record has the same property
(summary style/prompt changes ⇒ rebuild), and keying each summary to an exact
event range makes that rebuild cheap and safe. Note the difference: a
snapshot is *state* (fold result), a roll-up is *history compressed*; event
sourcing keeps both concepts separate, which is worth copying.

---

## 2. Age-based resolution decay in time-series storage

### RRDtool / round-robin databases — RAW DESTROYED

RRDtool defines multiple round-robin archives (RRAs) over one data stream,
each with a consolidation function (AVERAGE, MIN, MAX, LAST) and a resolution:
e.g. 5-minute points for 2 days, 30-minute for 2 weeks, 2-hour for 2 months,
daily for 2 years
([RRDtool manual](https://linux.die.net/man/1/rrdtool),
[Wikipedia](https://en.wikipedia.org/wiki/RRDtool)). Consolidation happens
*on write*, fixed-size files never grow, and old high-resolution data is
overwritten forever. This is the canonical shipped embodiment of "resolution
decays with age" — 25+ years in production (MRTG, Cacti, Munin, collectd).
What it gets right: the decay schedule is declared once, up front, as data
policy, and reads transparently pick the best archive covering the requested
range — exactly the shape of `wake` output. Sharpest lesson (cautionary):
everyone who has run RRDtool has hit the wall of "I need last year's data at
5-minute resolution and it's gone." Consolidation functions also only work
because numeric aggregation is lossless *in a chosen dimension* (the average
really is the average); a prose summary has no such guarantee, so destroying
the raw text under it is far more dangerous than RRDtool's trade-off.

### Prometheus / Thanos downsampling — RAW KEPT (until retention says otherwise)

[Thanos Compactor](https://thanos.io/tip/components/compact.md/) downsamples
raw blocks older than 40h to 5-minute resolution and 5m blocks older than 10
days to 1-hour resolution, keeping raw, 5m, and 1h as *separate coexisting
blocks* with independent retention flags. Crucially, downsampling is framed as
a query-speed feature, not a space saver — storing all tiers costs ~3x space
([Thanos docs](https://thanos.io/v0.8/components/compact/)). Two sharp
lessons. Positive: derived-resolution data as *additional* blocks alongside
authoritative raw is precisely "summaries are a cache," and Thanos shows the
overhead is acceptable in production at scale. Cautionary: Thanos documents a
real footgun — if retention at one resolution is shorter than the age at which
the next downsampling pass runs, data is deleted before its roll-up exists
([GitHub issue #5435](https://github.com/thanos-io/thanos/issues/5435)). For
a ledger: never let any future pruning/archiving step run ahead of roll-up
creation; the invariant "a range may only be hidden once its summary commit
exists" must be enforced, not assumed.

---

## 3. Hierarchical merge structures over append-only data

### LSM trees: size-tiered / leveled compaction (RocksDB, Cassandra) — RAW DESTROYED (but content-preserving)

LSM stores accumulate immutable sorted runs and merge them in the background:
size-tiered compaction merges ~4 similar-sized runs into one bigger run
(producing an exponential size hierarchy — effectively power-of-multiple-of-K
blocks that grow with age); leveled compaction maintains levels each ~10x
larger than the last. Old runs are deleted after merge, but the merge is
*content-preserving* (same key-value data, reorganized), so nothing is lost —
only obsolete versions and tombstones are dropped. What it gets right for us:
merges are scheduled by simple local size/count triggers, are idempotent, and
the read path transparently overlays newest-to-oldest runs — recent data lives
in small fresh runs, ancient data in huge consolidated ones. Sharpest lesson:
write amplification is the tax on any merge hierarchy — each event gets
re-processed O(log n) times as its block is folded upward. For an
agent-as-compressor that means each event's content is *re-summarized* O(log n)
times over its lifetime; the schedule should make merges rare and aligned
(power-of-two does this) because every merge costs an LLM invocation of agent
attention, not just CPU.

Cassandra's [TimeWindowCompactionStrategy](https://cassandra.apache.org/doc/stable/cassandra/managing/operating/compaction/twcs.html)
is a notable variant: data is bucketed by time window, active windows compact
normally, and once a window closes it is major-compacted *once* into a single
SSTable and never touched again. That "freeze completed windows, never rewrite
them" property is exactly what aligned power-of-two blocks buy: a summary over
a completed block is write-once.

### Lucene TieredMergePolicy (Elasticsearch/Solr/OpenSearch) — RAW DESTROYED (content-preserving)

[TieredMergePolicy](https://lucene.apache.org/core/8_1_0/core/org/apache/lucene/index/TieredMergePolicy.html)
computes a budget of allowed segments per tier, and when over budget selects
the least-cost merge, deliberately avoiding cascading over-merges. Lesson:
Lucene separates "how many segments may exist" (read-time cost cap) from "how
aggressively to merge" (write-time cost), and never merges more than needed to
get under budget. A roll-up record has the same knob: the invariant is "wake
output ≤ K lines," and the tool should request only the merges needed to
restore that invariant — one summary per turn, as OptMem does, rather than a
big catch-up batch that stalls the agent.

### Merkle Mountain Ranges (OpenTimestamps, Grin, Herodotus) — RAW KEPT

An [MMR](https://github.com/opentimestamps/opentimestamps-server/blob/master/doc/merkle-mountain-range.md)
maintains, over an append-only list, a forest of perfect binary trees ("mountains")
of strictly decreasing power-of-two sizes; appending a leaf creates parents as
soon as two children exist, existing nodes are never modified, and the peaks
are "bagged" into one root
([Grin docs](https://docs.grin.mw/wiki/chain-state/merkle-mountain-range/),
[commonware writeup](https://commonware.xyz/blogs/mmr)). This is the *exact*
structural skeleton of the roll-up record — the set of peaks at any moment is
the set of maximal completed power-of-two blocks, i.e. the coarsest valid
cover of the log. Substitute "hash of children" with "one-line summary of
children" and OptMem's tree falls out. Sharpest lesson (positive): because
blocks are aligned, every internal node is immutable once created — a summary
never needs revision when new events arrive, only new *parents* get added.
That is the property that makes summaries safe to store as commits/notes in
git: append-only derivation over append-only data, no rewrites, deterministic
addressing of which ranges exist (computable from the event count alone, just
like MMR peak positions).

### Honorable mention: hierarchical timing wheels — N/A (no raw data)

Kafka's and the Linux kernel's timer subsystems use cascading timing wheels
where timers further in the future sit in exponentially coarser buckets and
get re-sorted into finer wheels as they approach. Same intuition inverted in
time (far future = coarse, imminent = fine); useful only as evidence that
"resolution proportional to temporal distance" is a recurring systems design,
cheap to maintain with power-of-multiple bucket alignment.

---

## 4. AI agent memory tools with summarization/roll-ups

### MemGPT / Letta — RAW KEPT

[MemGPT](https://arxiv.org/pdf/2310.08560) treats context as virtual memory:
when prompt tokens cross a threshold, the queue manager evicts ~50% of context
messages and generates a *recursive summary* (new summary = f(old summary,
evicted messages)); evicted messages are stored verbatim and indefinitely in
recall storage, searchable via tool calls
([Letta memory overview](https://www.letta.com/blog/agent-memory/)).
Trigger: token pressure. Who summarizes: a system-driven LLM call inside the
tool, not the acting agent's deliberate authorship (though Letta agents can
also edit their own memory blocks via tools). Drill-down: search over recall
storage, not structured zoom. Sharpest lesson (cautionary): a single rolling
recursive summary is a *linear chain*, so old material passes through many
successive compressions and degrades — MemGPT's own docs note older messages
have progressively less influence. A binary tree bounds any event's
compression depth at O(log n) distinct summaries and keeps each summary keyed
to a fixed range instead of "everything so far," which is a genuine structural
improvement over the state of the art here. Letta's newer
[sleep-time compute](https://www.letta.com/blog/sleep-time-compute/) has a
background agent rewrite shared memory blocks while idle — the direct
antithesis of OptMem's "nothing runs in the background," and its cost is
operational complexity plus write-write races on memory; the synchronous
"pay one summary before your next action" model trades a little latency for
determinism and single-writer simplicity, which fits a git-backed CLI far
better.

### mem0 — RAW DESTROYED (memory layer is authoritative)

[mem0](https://github.com/mem0ai/mem0) runs an extraction pipeline: an LLM
extracts salient facts from each turn, compares each against the top-k similar
existing memories, and classifies ADD / UPDATE / DELETE / NOOP
([Dwarves breakdown](https://memo.d.foundation/breakdown/mem0)). The vector
store of facts *is* the memory; the conversation itself is not the recoverable
substrate of the system (the app may keep transcripts, but mem0's UPDATE and
DELETE mutate the record destructively). Trigger: every ingestion, in the
tool. Who summarizes: a model inside the tool. Sharpest lesson (cautionary):
in-place UPDATE/DELETE of derived memories makes errors unrecoverable and
undiagnosable — when the extractor mangles a fact, the original is gone and
there is no range key pointing back to source events. The roll-up design's
"summary is a cache over an immutable range" avoids this whole failure class;
keep it.

### Zep / Graphiti — RAW KEPT

[Graphiti](https://neo4j.com/blog/developer/graphiti-knowledge-graph-memory/)
builds a temporal knowledge graph in three tiers: episodic nodes (raw
messages, retained), extracted entities/facts with bi-temporal validity
ranges, and community summaries over clusters
([Zep paper](https://arxiv.org/pdf/2501.13956)). When new information
contradicts old, edges are *invalidated with timestamps, not deleted* —
history is preserved and queryable. Trigger: every episode ingestion; who
summarizes: LLM inside the tool. Drill-down: graph traversal plus hybrid
search from summaries to facts to raw episodes — real hierarchical
drill-down, closest to `zoom` among commercial memory products. Sharpest
lesson (positive): "invalidate, never discard" applied to *derived* records —
if a roll-up summary is later found wrong or is superseded by a re-roll, the
old summary can remain in git history with the new one taking precedence,
giving an audit trail of what the agent believed when. Git gives this for
free; keep summaries as commits rather than mutable files-in-working-tree
semantics.

### LangChain ConversationSummary(Buffer)Memory — RAW DESTROYED

[ConversationSummaryMemory](https://python.langchain.com/api_reference/langchain/memory/langchain.memory.summary.ConversationSummaryMemory.html)
maintains one progressively-updated running summary of the whole conversation;
ConversationSummaryBufferMemory keeps the last N tokens verbatim and folds
overflow into the summary. Trigger: every turn / token threshold. Who
summarizes: an LLM call inside the memory class. Raw messages fall out of the
memory object (persistence is the app's problem); no drill-down exists —
once folded, detail is gone. This is "recent verbatim + one ancient summary
line," i.e. the degenerate two-level version of the roll-up record. Sharpest
lesson (cautionary): a single flat summary saturates — it has constant size
for unboundedly growing history, so it must continually evict detail, and
users reported exactly the failure of mid-history information vanishing. The
multi-resolution tree exists precisely to fix this: output grows O(log n),
not O(1), so detail decays gracefully instead of falling off a cliff.

### Claude Code auto-compact — RAW DESTROYED (in context; transcript on disk)

Claude Code monitors context usage and at a threshold runs a summarization
pass over the conversation, replacing earlier turns with the summary; `/compact`
does it on demand
([Anthropic cookbook: automatic context compaction](https://platform.claude.com/cookbook/tool-use-automatic-context-compaction),
[compaction explained](https://okhlopkov.com/claude-code-compaction-explained/)).
Trigger: token pressure. Who summarizes: a tool-driven model call with a fixed
compression prompt; the working agent does not author it deliberately. The
JSONL transcript survives on disk, but the live session cannot drill back into
pre-compact detail — practitioners' consistent complaint is losing exact
error messages, variable names, and early decisions. Sharpest lesson: this is
the pain the ledger exists to fix, and it demonstrates that *when* compression
happens matters as much as *how* — compression under memory pressure at an
arbitrary moment is worst-case (no natural boundary, everything compressed at
once). A schedule tied to log position (block boundaries) instead of context
pressure compresses at semantically stable points, in small increments, with
the raw events still one `zoom` away.

### ChatGPT memory — RAW KEPT (but opaque)

ChatGPT combines discrete "saved memories" (facts extracted by classifier or
on user request) with "reference chat history" (RAG over past conversations)
([OpenAI Memory FAQ](https://help.openai.com/en/articles/8590148-memory-faq),
[Embrace The Red deep dive](https://embracethered.com/blog/posts/2025/chatgpt-how-does-chat-history-memory-preferences-work/)).
Raw conversations persist and are retrieved associatively; there is no
user-visible hierarchy and no deterministic coverage — retrieval may simply
miss things. Sharpest lesson: retrieval-based memory gives no *completeness
guarantee*; the roll-up record's distinctive promise is that wake output
**covers the entire log** at some resolution — nothing is ever entirely
outside the agent's view, only coarsened. That coverage guarantee is rare in
this whole category and is worth stating explicitly as a design invariant.

### Stanford Generative Agents (Smallville) — RAW KEPT

The [generative agents paper](https://arxiv.org/pdf/2304.03442) stores every
observation in an append-only memory stream, retrieves by
recency × importance × relevance, and periodically has the agent generate
*reflections* — higher-level insights synthesized from recent memories, added
back to the same stream (reflections can cite other reflections, forming
trees). Trigger: sum of importance scores of recent events exceeds a
threshold (150), roughly 2–3 reflections per simulated day. Who summarizes:
the agent itself, prompted by the framework — this is the clearest shipped
precedent for "agent as compressor, tool-managed schedule." Raw observations
are never deleted; recency decay handles their fading. Sharpest lessons:
(1) importance-weighted triggering is a plausible alternative to purely
positional scheduling — big events could earn earlier roll-up; (2) reflections
stored as first-class events *in the same log* (rather than a side-cache)
worked well for them, which supports modeling roll-ups as ledger events (of a
distinct type) rather than an out-of-band structure — while OptMem's
cache-directory approach keeps rebuild semantics cleaner. That's the live
design tension for a git ledger: roll-up-as-event (in history, foldable) vs.
roll-up-as-note/cache (rebuildable, out-of-band).

### RAPTOR — RAW KEPT

[RAPTOR](https://arxiv.org/pdf/2401.18059) (ICLR 2024) recursively embeds,
clusters, and LLM-summarizes text chunks bottom-up into a tree; retrieval
draws from all levels, and leaf chunks remain in the tree. Shipped in
[RAGFlow](https://ragflow.io/docs/enable_raptor) among others. It differs from
the roll-up record in grouping by *semantic cluster* rather than by *position/age*,
and it's batch-built over a corpus rather than incrementally maintained.
Sharpest lesson: multi-level summaries measurably beat flat retrieval on
multi-step reasoning (+20% on QuALITY with GPT-4) — empirical evidence that
hierarchical summaries genuinely help LLM consumers, not just save tokens.
Cautionary: semantic clustering is unstable under append (new events can
re-cluster everything), which is exactly why positional power-of-two blocks —
stable, append-friendly, MMR-like — are the right choice for a live ledger
even though topic-coherent blocks would summarize more cleanly.

---

## 5. Agent-as-compressor with tool-managed schedule; scheduler analogs

### Spaced repetition schedulers (SuperMemo SM-2, Anki, FSRS) — RAW KEPT

Anki/SM-2/FSRS are shipped software where the *tool owns the schedule* and an
*intelligent agent (the human) does the cognitive work* on demand: the
algorithm computes expanding review intervals per item; the human performs
recall; the tool records the grade and reschedules. Cards (raw data) are never
destroyed. The structural rhyme with OptMem is exact: deterministic scheduler +
external intelligence invoked synchronously at the scheduled moment + append-only
review log (Anki's revlog). Sharpest lesson: expanding-interval scheduling is
the time-domain twin of power-of-two blocks in the position domain — both
encode "attention owed to old material decays geometrically." Also practical:
Anki caps daily reviews to bound the human's load; a ledger tool should
likewise cap roll-up requests per session (OptMem's one-merge-per-note does
this) so an agent returning after long absence isn't buried in summary debt.

### MemoryBank (Ebbinghaus forgetting curve) — RAW DESTROYED (decayed memories go away)

[MemoryBank](https://arxiv.org/abs/2305.10250) (AAAI 2024) gives an LLM
companion a memory store where each item has a strength; recall reinforces it
and unrecalled items decay per an Ebbinghaus-style exponential and are
eventually forgotten. It's the software embodiment of forgetting curves for
LLM memory. Sharpest lesson (cautionary): access-based decay deletes what was
merely *unneeded recently*, which is wrong for a ledger — an unread event may
be the crucial one during an audit. Age-based *resolution* decay (coarsen, keep)
strictly dominates age- or access-based *retention* decay (delete) when the
substrate is cheap, and a git repo of one-line events is about as cheap as
substrates get.

### Bullet-journal-style roll-ups in software

The bullet journal's monthly "migration" (re-copy forward only what still
matters, condense the rest) is the human-practice analog. No widely-shipped
software embodiment with genuine LLM/agent roll-up conventions was found beyond
the agent tools above — Logseq/Obsidian daily-notes plugins do periodic
*aggregation* (collect, not compress). The closest software convention is the
changelog/release-notes discipline (many commits → one release entry, raw
history kept in git), which is arguably the oldest "roll-up over an
append-only log with raw kept authoritative" practice in software engineering,
and it's already git-native. Lesson: humans doing migration re-copy *forward*
(the summary is judged by what the future needs, not what the past contained) —
a good framing for the roll-up prompt given to the agent.

---

## Summary table

| System | Raw log | Compressor | Trigger/schedule | Drill-down |
|---|---|---|---|---|
| OptMem | **Kept** (authoritative) | Acting agent, sync | Power-of-two block completion, one merge per note | `zoom`, to raw |
| Kafka compaction | Destroyed | Broker (mechanical, per-key) | Dirty ratio / lag configs | None |
| Event-store snapshots | **Kept** (authoritative) | App code (fold) | Every N events / latency metric | Replay events past snapshot |
| RRDtool | Destroyed | Numeric CF on write | Declared RRA resolutions | None (coarse tiers only) |
| Thanos | **Kept** (per retention) | Compactor (numeric) | Age thresholds (40h, 10d) | Query any resolution tier |
| LSM / Lucene merges | Destroyed (content-preserving) | Merge (mechanical) | Size/count budgets | N/A |
| Merkle Mountain Range | **Kept** (authoritative) | Hash | Block completion (structural) | Proof paths to leaves |
| MemGPT/Letta | **Kept** (recall storage) | Tool-driven LLM call | Token pressure | Search, not structured |
| mem0 | Destroyed (in memory layer) | Tool-driven LLM call | Every ingestion | None to source |
| Zep/Graphiti | **Kept** (episodes) | Tool-driven LLM call | Every episode | Graph traversal to episodes |
| LangChain summary memory | Destroyed | Tool-driven LLM call | Every turn / token cap | None |
| Claude Code compact | Destroyed (in context) | Tool-driven LLM call | Context pressure | None in-session |
| ChatGPT memory | **Kept** (opaque) | Classifier + RAG | Continuous | Associative retrieval only |
| Generative Agents | **Kept** (memory stream) | **Acting agent, prompted** | Importance-sum threshold | Retrieval over stream |
| RAPTOR | **Kept** (leaves in tree) | Pipeline LLM | Batch build | Tree traversal |
| Anki/SM-2/FSRS | **Kept** (cards + revlog) | **Human, on schedule** | Expanding intervals | Open the card |
| MemoryBank | Destroyed (decay deletes) | Tool-driven LLM call | Forgetting-curve decay | Until forgotten |

---

## Lessons for the git-backed ledger

1. **Raw log stays authoritative; summaries are a rebuildable cache.** The
   systems that aged best (event sourcing, MMR, Thanos, Zep) all treat derived
   layers as disposable; the ones users curse (RRDtool's vanished history,
   LangChain's saturating summary, Claude Code's lost error messages) destroy
   raw data. Git makes keeping raw free — there is no excuse to compact
   destructively.
2. **Aligned power-of-two blocks make summaries write-once.** MMR proves the
   structural property: with aligned blocks, appending never invalidates an
   existing node, so each summary is created exactly once and which ranges
   need summaries is computable from the event count alone. Cassandra TWCS
   independently converged on "freeze completed windows."
3. **Enforce roll-up-before-hiding as an invariant.** Thanos's
   retention-vs-downsampling footgun: never let any pruning, archiving, or
   display-truncation step outrun summary creation.
4. **Bound the merge debt per interaction.** Lucene merges only enough to get
   under budget; Anki caps daily reviews; OptMem asks one merge per note.
   Each event is re-summarized O(log n) times over its life (LSM write
   amplification, paid in agent attention) — amortize it, never batch it.
5. **A tree beats a rolling summary.** MemGPT's recursive chain compresses old
   material unboundedly many times; a binary tree bounds compression depth at
   O(log n) and keys each summary to a fixed range. RAPTOR shows multi-level
   summaries measurably improve LLM task performance.
6. **Coverage is the differentiator.** Retrieval memories (ChatGPT, Letta
   recall, mem0) can silently miss; the roll-up record guarantees the whole
   log is visible at some resolution. State that as an invariant and test it.
7. **Synchronous agent-as-compressor is nearly novel — precedent is Generative
   Agents and Anki.** Generative Agents shipped agent-authored reflections on
   a tool-managed (importance-threshold) schedule; spaced repetition shipped
   tool-scheduled human cognition. Letta's sleep-time compute is the
   background-agent alternative; its costs (async complexity, memory races)
   argue for OptMem's synchronous single-writer model in a git CLI.
8. **Open design tension:** roll-ups as first-class ledger events (Generative
   Agents style — in history, part of the fold) vs. out-of-band cache
   (OptMem's TREE, git notes/refs — cleanly rebuildable when the summary
   prompt changes). Event sourcing's practice of keeping snapshots *outside*
   the event stream, keyed to a stream revision, is the strongest precedent
   for out-of-band.
