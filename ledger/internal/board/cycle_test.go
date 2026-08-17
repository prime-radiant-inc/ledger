package board

import (
	"encoding/json"
	"testing"

	"ledger/internal/model"
)

// --- Rev 17: holder-blind cycle detection + the self-service break object ---
//
// These tests cover the trial-5 redesign (spec "Cycle detection is
// holder-blind" and the attention bullet's break-object paragraph): cycles
// are detected through a live claim or a human-labeled key, not just open
// unlabeled ones, and every cycle attention entry carries a paste-ready
// {key, drop, keep, expect, human} fix. envelope_test.go's existing cycle
// tests (true 2-cycle, diamond-never-false-flagged, filter composition)
// stay green unchanged — this file adds the NEW rev-17 behavior only.

// cycleEntries filters an envelope's attention list down to "cycle" reason
// entries, in list order.
func cycleEntries(env Envelope) []AttentionEntry {
	var out []AttentionEntry
	for _, a := range env.Attention {
		if a.Reason == "cycle" {
			out = append(out, a)
		}
	}
	return out
}

// TestDetectCyclesHolderBlindThroughLiveClaim: a blocked-by b, b IN-PROGRESS
// (a live claim) blocked-by a — the earlier open-keys-only walk would have
// stopped at b's claim and called this all-handled; holder-blind detection
// must catch it. A mutually-blocked open/claimed pair must never read
// all-handled.
func TestDetectCyclesHolderBlindThroughLiveClaim(t *testing.T) {
	evs := []model.Event{
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", nil),
		setEv("b1", "b", "status", "in-progress", func(e *model.Event) { e.Author = "worker" }),
		setEv("b2", "b", "blocked-by", "a", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	if env.Frontier != "attention-needed" {
		t.Fatalf("a cycle through a live claim must drive attention-needed, never all-handled, got %q", env.Frontier)
	}
	cycles := cycleEntries(env)
	if len(cycles) != 1 || len(cycles[0].Keys) != 2 || !model.Contains(cycles[0].Keys, "a") || !model.Contains(cycles[0].Keys, "b") {
		t.Fatalf("expected exactly one cycle entry naming a and b, got %+v", cycles)
	}
}

// TestDetectCyclesHolderBlindThroughHumanLabel: a blocked-by b, b
// human-labeled and open (non-terminal) blocked-by a. Must be detected, and
// the suggested break must carry human:true when the break target is the
// human-labeled member.
func TestDetectCyclesHolderBlindThroughHumanLabel(t *testing.T) {
	evs := []model.Event{
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", func(e *model.Event) { e.TS = "2026-08-16T00:00:00.000" }),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("bl", "b", "labels", "human", nil),
		// b's blocked-by write lands after a's — the youngest edge, so the
		// break targets b, the human-labeled member.
		setEv("b2", "b", "blocked-by", "a", func(e *model.Event) { e.TS = "2026-08-16T00:00:01.000" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	if env.Frontier != "attention-needed" {
		t.Fatalf("a cycle through a human-labeled key must drive attention-needed, got %q", env.Frontier)
	}
	cycles := cycleEntries(env)
	if len(cycles) != 1 {
		t.Fatalf("expected exactly one cycle entry, got %+v", cycles)
	}
	if cycles[0].Break == nil || !cycles[0].Break.Human {
		t.Fatalf("break against the human-labeled member must carry human:true, got %+v", cycles[0].Break)
	}
}

// TestCycleBreakHumanFalseWhenYoungestMemberIsThePlainOne: a cycle where a
// human-labeled member EXISTS but is NOT the suggested break target — b is
// human-labeled, but a's blocked-by write is the younger (closing) edge, so
// the break must target a with human:false. This is the discriminating
// case TestDetectCyclesHolderBlindThroughHumanLabel can't catch on its
// own: that test's board has no plain member whose edge is younger, so an
// implementation that (wrongly) set Human whenever ANY cycle member is
// human-labeled — rather than only when the suggested break target is —
// would still pass it. Here it wouldn't.
func TestCycleBreakHumanFalseWhenYoungestMemberIsThePlainOne(t *testing.T) {
	evs := []model.Event{
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("bl", "b", "labels", "human", nil),
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		// b's blocked-by write lands FIRST — the older edge.
		setEv("b2", "b", "blocked-by", "a", func(e *model.Event) { e.TS = "2026-08-16T00:00:00.000" }),
		// a's blocked-by write lands SECOND — the younger (closing) edge, so
		// the break targets a, the plain (non-human) member.
		setEv("a2", "a", "blocked-by", "b", func(e *model.Event) { e.TS = "2026-08-16T00:00:01.000" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	cycles := cycleEntries(env)
	if len(cycles) != 1 {
		t.Fatalf("expected exactly one cycle entry, got %+v", cycles)
	}
	brk := cycles[0].Break
	if brk == nil || brk.Key != "a" {
		t.Fatalf("the younger edge (a's) must be suggested even though b is human-labeled, got %+v", brk)
	}
	if brk.Human {
		t.Fatalf("break.human must be false — the suggested target (a) is not the human-labeled member, got %+v", brk)
	}
}

// TestDetectCyclesTerminalKeyBreaksChain: a blocked-by b, b CLOSED (terminal)
// blocked-by a. A terminal status's edges are moot — the walk must stop at
// b and never re-discover a "cycle" back through its own (irrelevant)
// blocked-by value. Only terminal keys are excluded from the holder-blind
// walk; this pins that exclusion still holds.
func TestDetectCyclesTerminalKeyBreaksChain(t *testing.T) {
	evs := []model.Event{
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", nil),
		setEv("b1", "b", "status", "closed", func(e *model.Event) { e.Text = "b"; e.Evidence = []string{"commit:x"} }),
		setEv("b2", "b", "blocked-by", "a", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	if len(cycleEntries(env)) != 0 {
		t.Fatalf("a terminal key's edges are moot — must never be walked into as a cycle member: %+v", env.Attention)
	}
	if env.Frontier != "work-available" {
		t.Fatalf("a resolved via b's terminal status must be ready, got %q", env.Frontier)
	}
}

// TestDetectCyclesDiamondThroughClaimedSinkNeverFalseFlagged: b and c both
// depend on d, a live (non-stale) claim with no edges of its own — a
// diamond reached via two paths, never via itself. The holder-blind walk
// (which now recurses into claimed keys too) must still never false-flag
// this as a cycle; d's claim is a valid terminus.
func TestDetectCyclesDiamondThroughClaimedSinkNeverFalseFlagged(t *testing.T) {
	evs := []model.Event{
		setEv("d1", "d", "status", "in-progress", func(e *model.Event) { e.Author = "worker" }),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("b2", "b", "blocked-by", "d", nil),
		setEv("c1", "c", "status", "open", func(e *model.Event) { e.Text = "c" }),
		setEv("c2", "c", "blocked-by", "d", nil),
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b,c", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	if len(cycleEntries(env)) != 0 {
		t.Fatalf("a diamond behind a claimed sink must never be flagged a cycle: %+v", env.Attention)
	}
	if env.Frontier != "all-handled" {
		t.Fatalf("every chain here ends at d's live claim, got %q", env.Frontier)
	}
}

// TestCycleBreakSuggestsYoungestEdgeAndKeepsOtherTokens: a<->b, b's edge
// write is the younger of the two (distinct BlockedByTS), and b's
// blocked-by also carries an unrelated, already-terminal token — the break
// must target b, drop a (b's successor in the cycle), and Keep the
// unrelated token.
func TestCycleBreakSuggestsYoungestEdgeAndKeepsOtherTokens(t *testing.T) {
	evs := []model.Event{
		setEv("x1", "extra", "status", "closed", func(e *model.Event) { e.Text = "extra"; e.Evidence = []string{"commit:x"} }),
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", func(e *model.Event) { e.TS = "2026-08-16T00:00:00.000" }),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("b2", "b", "blocked-by", "a,extra", func(e *model.Event) { e.TS = "2026-08-16T00:00:01.000" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	cycles := cycleEntries(env)
	if len(cycles) != 1 {
		t.Fatalf("expected exactly one cycle entry, got %+v", cycles)
	}
	brk := cycles[0].Break
	if brk == nil {
		t.Fatal("cycle entry must carry a break object")
	}
	if brk.Key != "b" {
		t.Fatalf("the youngest edge (b's) must be suggested, got key=%q", brk.Key)
	}
	if brk.Drop != "a" {
		t.Fatalf("drop must name b's successor in the cycle (a), got %q", brk.Drop)
	}
	if brk.Keep != "extra" {
		t.Fatalf("keep must retain b's other, unrelated token, got %q", brk.Keep)
	}
	if brk.Expect != "b2" {
		t.Fatalf("expect must be b's blocked-by field's latest event id, got %q", brk.Expect)
	}
	if brk.Human {
		t.Fatalf("b is not human-labeled, human must be false")
	}
}

// TestCycleBreakKeepDropsAllOccurrencesOfDroppedToken: b's blocked-by
// carries the cycle-closing token TWICE among other tokens (a malformed but
// representable multi-field value) — Keep must drop every occurrence, not
// just the first.
func TestCycleBreakKeepDropsAllOccurrencesOfDroppedToken(t *testing.T) {
	evs := []model.Event{
		setEv("x1", "other1", "status", "closed", func(e *model.Event) { e.Text = "other1"; e.Evidence = []string{"commit:x"} }),
		setEv("x2", "other2", "status", "closed", func(e *model.Event) { e.Text = "other2"; e.Evidence = []string{"commit:x"} }),
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", func(e *model.Event) { e.TS = "2026-08-16T00:00:00.000" }),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("b2", "b", "blocked-by", "other1,a,a,other2", func(e *model.Event) { e.TS = "2026-08-16T00:00:01.000" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	cycles := cycleEntries(env)
	if len(cycles) != 1 {
		t.Fatalf("expected exactly one cycle entry, got %+v", cycles)
	}
	if cycles[0].Break.Keep != "other1,other2" {
		t.Fatalf("keep must drop every occurrence of the dropped token, got %q", cycles[0].Break.Keep)
	}
}

// TestCycleEntryDedupOnDoubledEdge: b's blocked-by names a TWICE
// ("a,a") — the doubled edge must not produce two identical cycle attention
// entries (the spike's known gap; rev 17 requires dedup on the member set).
func TestCycleEntryDedupOnDoubledEdge(t *testing.T) {
	evs := []model.Event{
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", nil),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("b2", "b", "blocked-by", "a,a", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	cycles := cycleEntries(env)
	if len(cycles) != 1 {
		t.Fatalf("a doubled edge must dedupe to exactly one cycle entry, got %d: %+v", len(cycles), cycles)
	}
}

// TestCycleBreakSelfEdge: k's blocked-by names itself — a 1-member "cycle".
// The break must target k, drop k, and keep the empty remainder.
func TestCycleBreakSelfEdge(t *testing.T) {
	evs := []model.Event{
		setEv("k1", "k", "status", "open", func(e *model.Event) { e.Text = "k" }),
		setEv("k2", "k", "blocked-by", "k", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	cycles := cycleEntries(env)
	if len(cycles) != 1 || len(cycles[0].Keys) != 1 || cycles[0].Keys[0] != "k" {
		t.Fatalf("expected a single-member self-edge cycle entry, got %+v", cycles)
	}
	brk := cycles[0].Break
	if brk == nil || brk.Key != "k" || brk.Drop != "k" || brk.Keep != "" || brk.Expect != "k2" {
		t.Fatalf("self-edge break must be {key:k drop:k keep:\"\" expect:k2}, got %+v", brk)
	}
}

// TestCycleBreakTieBreakFallsBackToChainOrderWhenTimestampUnparseable: both
// members' BlockedByTS are malformed (unparseable). Per the defined
// fallback, the tie is broken by chain order — the member whose
// blocked-by event lands LATER in the chain (higher blockedBySeq) is
// treated as younger. The status events are deliberately ordered OPPOSITE
// of the blocked-by events (b's status write comes first, but a's
// blocked-by write comes first) — a fallback that mistakenly used status
// chain position instead of blocked-by chain position would pick the
// wrong member (a) here; only the correct one (b, whose blocked-by event
// b2 is the later of the two edge writes) satisfies this test.
func TestCycleBreakTieBreakFallsBackToChainOrderWhenTimestampUnparseable(t *testing.T) {
	evs := []model.Event{
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", func(e *model.Event) { e.TS = "not-a-timestamp" }),
		setEv("b2", "b", "blocked-by", "a", func(e *model.Event) { e.TS = "also-not-a-timestamp" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	cycles := cycleEntries(env)
	if len(cycles) != 1 {
		t.Fatalf("expected exactly one cycle entry, got %+v", cycles)
	}
	if cycles[0].Break.Key != "b" {
		t.Fatalf("unparseable timestamps must fall back to blocked-by chain order (later blockedBySeq = younger); expected b, got %q", cycles[0].Break.Key)
	}
}

// TestCycleBreakTieBreakFallsBackToChainOrderOnEqualTimestamps: same
// fallback path, exercised via two members with the identical (valid)
// timestamp rather than a parse failure. Status/blocked-by chain order is
// again deliberately opposite (see the unparseable-timestamp test above
// for why) so the test can only pass if the fallback keys off blocked-by
// chain position, not status chain position.
func TestCycleBreakTieBreakFallsBackToChainOrderOnEqualTimestamps(t *testing.T) {
	evs := []model.Event{
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", func(e *model.Event) { e.TS = "2026-08-16T00:00:00.000" }),
		setEv("b2", "b", "blocked-by", "a", func(e *model.Event) { e.TS = "2026-08-16T00:00:00.000" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	cycles := cycleEntries(env)
	if len(cycles) != 1 {
		t.Fatalf("expected exactly one cycle entry, got %+v", cycles)
	}
	if cycles[0].Break.Key != "b" {
		t.Fatalf("an exact timestamp tie must fall back to blocked-by chain order (later blockedBySeq = younger); expected b, got %q", cycles[0].Break.Key)
	}
}

// TestCycleBreakJSONAlwaysRendersHumanAndKeep: the spec's pinned envelope
// example shows "human": false and "keep": "" explicitly, not omitted —
// mirroring HeldEntry.Stale's own false-must-render precedent. A cycle
// entry's break must round-trip the same way.
func TestCycleBreakJSONAlwaysRendersHumanAndKeep(t *testing.T) {
	entry := AttentionEntry{Reason: "cycle", Keys: []string{"a", "b"},
		Break: &CycleBreak{Key: "b", Drop: "a", Keep: "", Expect: "abc123", Human: false}}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	brk, ok := doc["break"].(map[string]any)
	if !ok {
		t.Fatalf("break must serialize as an object: %s", raw)
	}
	if v, present := brk["human"]; !present || v != false {
		t.Fatalf("break.human=false must render explicitly, not be omitted: %s", raw)
	}
	if v, present := brk["keep"]; !present || v != "" {
		t.Fatalf("break.keep=\"\" must render explicitly, not be omitted: %s", raw)
	}
}

// TestEnvelopeCycleResidualAfterBreakingRing: a ring-plus-chord graph
// (a<-b<-c<-a is the ring; b is also blocked-by d, d<-a is the chord,
// sharing members a and b with the ring). b carries the latest BlockedByTS
// of every member in both cycles, so it's the suggested break target in
// both. Applying the RING's suggested break (b drops c, keeps d) must
// clear the ring cycle while leaving the chord cycle (a, b, d) intact —
// visible on the next Envelope built from the updated event chain.
func TestEnvelopeCycleResidualAfterBreakingRing(t *testing.T) {
	evs := []model.Event{
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("c1", "c", "status", "open", func(e *model.Event) { e.Text = "c" }),
		setEv("d1", "d", "status", "open", func(e *model.Event) { e.Text = "d" }),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("a2", "a", "blocked-by", "b", func(e *model.Event) { e.TS = "2026-08-16T00:00:00.000" }),
		setEv("c2", "c", "blocked-by", "a", func(e *model.Event) { e.TS = "2026-08-16T00:00:01.000" }),
		setEv("d2", "d", "blocked-by", "a", func(e *model.Event) { e.TS = "2026-08-16T00:00:01.000" }),
		// b is blocked by both c (closes the ring a-b-c) and d (closes the
		// chord a-b-d); its single BlockedByTS write is the youngest of
		// every member in both cycles.
		setEv("b2", "b", "blocked-by", "c,d", func(e *model.Event) { e.TS = "2026-08-16T00:00:02.000" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, alwaysTrue)
	cycles := cycleEntries(env)
	if len(cycles) != 2 {
		t.Fatalf("expected both the ring and the chord cycle, got %d: %+v", len(cycles), cycles)
	}

	var ring *AttentionEntry
	for i := range cycles {
		if len(cycles[i].Keys) == 3 && model.Contains(cycles[i].Keys, "c") {
			ring = &cycles[i]
		}
	}
	if ring == nil {
		t.Fatalf("expected a ring cycle entry naming c among its members: %+v", cycles)
	}
	if ring.Break.Key != "b" || ring.Break.Drop != "c" || ring.Break.Keep != "d" {
		t.Fatalf("ring break must be {key:b drop:c keep:d}, got %+v", ring.Break)
	}

	// Apply the ring's suggested break: b blocked-by=d (dropping c).
	evs2 := append(append([]model.Event(nil), evs...),
		setEv("b3", "b", "blocked-by", ring.Break.Keep, func(e *model.Event) { e.TS = "2026-08-16T00:00:03.000" }))
	b2 := Build(envelopeMeta(), evs2)
	env2 := b2.Envelope(envNow, 50, alwaysTrue)
	cycles2 := cycleEntries(env2)
	if len(cycles2) != 1 {
		t.Fatalf("expected only the residual chord cycle after breaking the ring, got %d: %+v", len(cycles2), cycles2)
	}
	if model.Contains(cycles2[0].Keys, "c") {
		t.Fatalf("the ring cycle must be gone after the break: %+v", cycles2[0])
	}
	if !model.Contains(cycles2[0].Keys, "d") {
		t.Fatalf("the residual chord cycle must still name d: %+v", cycles2[0])
	}
}
