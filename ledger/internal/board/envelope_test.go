package board

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"ledger/internal/model"
)

// envelopeMeta is the canonical ready-capable shape for envelope tests: two
// terminal values (closed, wontfix) so unblocked_without_evidence can be
// exercised "regardless of which terminal value" per spec.
func envelopeMeta() model.Meta {
	return model.Meta{
		Fields:      map[string][]string{"status": {"open", "in-progress", "closed", "wontfix"}},
		Terminal:    map[string][]string{"status": {"closed", "wontfix"}},
		MultiFields: []string{"labels", "blocked-by"},
	}
}

// allowAll is the "no --where clause" filter: every key matches, and (per
// matchWhere's own nil-key rule, which this constant mirrors) so does the
// filter(nil) orphan check.
func allowAll(*Key) bool { return true }

// envNow is the fixed "now" every envelope test measures ages against.
var envNow = mustParseTS("2026-08-16T12:00:00.000")

func mustParseTS(s string) time.Time {
	t, err := model.ParseTS(s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestEnvelopeReadyEntryShape: the full ready entry shape, no annotation
// when the (sole) blocker's terminal event carries evidence.
func TestEnvelopeReadyEntryShape(t *testing.T) {
	evs := []model.Event{
		setEv("b1", "spike-probe", "status", "closed", func(e *model.Event) {
			e.Text = "probe done"
			e.Evidence = []string{"commit:abc123"}
		}),
		setEv("r1", "fix-retry", "status", "open", func(e *model.Event) {
			e.Text = "fix the retry loop"
			e.Author = "alice"
			e.TS = "2026-08-16T10:00:00.000"
		}),
		setEv("r2", "fix-retry", "blocked-by", "spike-probe", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Ready) != 1 {
		t.Fatalf("expected 1 ready entry, got %+v", env.Ready)
	}
	want := ReadyEntry{Key: "fix-retry", Title: "fix the retry loop", Note: "fix the retry loop",
		TS: "2026-08-16T10:00:00.000", By: "alice", ID: "r1"}
	if !reflect.DeepEqual(env.Ready[0], want) {
		t.Fatalf("ready entry shape:\n got  %+v\n want %+v", env.Ready[0], want)
	}
}

// TestEnvelopeReadyUnblockedWithoutEvidenceFiresOnEvidenceFreeWontfix:
// the annotation fires regardless of which terminal value the blocker
// landed on, as long as its terminal event carries no evidence.
func TestEnvelopeReadyUnblockedWithoutEvidenceFiresOnEvidenceFreeWontfix(t *testing.T) {
	evs := []model.Event{
		setEv("b1", "spike-probe", "status", "wontfix", func(e *model.Event) { e.Text = "not doing it" }),
		setEv("r1", "fix-retry", "status", "open", func(e *model.Event) { e.Text = "fix it"; e.Author = "a" }),
		setEv("r2", "fix-retry", "blocked-by", "spike-probe", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Ready) != 1 {
		t.Fatalf("expected 1 ready entry: %+v", env.Ready)
	}
	if got := env.Ready[0].UnblockedWithoutEvidence; len(got) != 1 || got[0] != "spike-probe" {
		t.Fatalf("expected annotation naming spike-probe, got %v", got)
	}
}

// TestEnvelopeReadyUnblockedWithoutEvidenceNotOnEvidencedClose: the
// annotation must NOT fire when the terminal blocker's event carries
// evidence.
func TestEnvelopeReadyUnblockedWithoutEvidenceNotOnEvidencedClose(t *testing.T) {
	evs := []model.Event{
		setEv("b1", "spike-probe", "status", "closed", func(e *model.Event) {
			e.Text = "done"
			e.Evidence = []string{"commit:abc123"}
		}),
		setEv("r1", "fix-retry", "status", "open", func(e *model.Event) { e.Text = "fix it"; e.Author = "a" }),
		setEv("r2", "fix-retry", "blocked-by", "spike-probe", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Ready) != 1 {
		t.Fatalf("expected 1 ready entry: %+v", env.Ready)
	}
	if got := env.Ready[0].UnblockedWithoutEvidence; len(got) != 0 {
		t.Fatalf("expected no annotation on an evidenced terminal blocker, got %v", got)
	}
}

// TestEnvelopeHeldClaimEntryClaimedButBlocked: a claim entry's exact shape,
// including waiting_on for a claimed-but-blocked key (legal and visible).
// Mirrors the spec's own pinned "big-task"/"dep-x" example.
func TestEnvelopeHeldClaimEntryClaimedButBlocked(t *testing.T) {
	meta := envelopeMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("d1", "dep-x", "status", "open", func(e *model.Event) { e.TS = "2026-08-16T11:00:00.000" }),
		setEv("s1", "big-task", "status", "open", func(e *model.Event) { e.Text = "big task title"; e.Author = "seeder" }),
		setEv("c1", "big-task", "status", "in-progress", func(e *model.Event) {
			e.Text = "claiming"
			e.Author = "worker-2"
			e.TS = "2026-08-16T11:46:00.000" // 14m before envNow
		}),
		setEv("c2", "big-task", "blocked-by", "dep-x", nil),
	}
	b := Build(meta, evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Held) != 1 {
		t.Fatalf("expected 1 held entry, got %+v", env.Held)
	}
	stale := false
	want := HeldEntry{Key: "big-task", Title: "big task title", Kind: "claim", By: "worker-2", ID: "c1",
		Age: "14m0s", Stale: &stale, WaitingOn: []WaitingOn{{Key: "dep-x", State: "open"}}}
	if !reflect.DeepEqual(env.Held[0], want) {
		t.Fatalf("held claim entry:\n got  %+v (stale=%v)\n want %+v (stale=%v)",
			env.Held[0], derefBool(env.Held[0].Stale), want, derefBool(want.Stale))
	}
}

// TestEnvelopeHeldHumanUnclaimedEntry: a human-labeled, non-terminal,
// unclaimed key's held shape. Mirrors the spec's own pinned "sign-off"
// example.
func TestEnvelopeHeldHumanUnclaimedEntry(t *testing.T) {
	evs := []model.Event{
		setEv("h1", "sign-off", "status", "open", func(e *model.Event) {
			e.Text = "needs a human sign-off"
			e.Author = "alice"
			e.TS = "2026-08-16T09:00:00.000"
		}),
		setEv("h2", "sign-off", "labels", "human", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Held) != 1 {
		t.Fatalf("expected 1 held entry, got %+v", env.Held)
	}
	want := HeldEntry{Key: "sign-off", Title: "needs a human sign-off", Kind: "human",
		Status: "open", By: "alice", TS: "2026-08-16T09:00:00.000", ID: "h1"}
	if !reflect.DeepEqual(env.Held[0], want) {
		t.Fatalf("held human entry:\n got  %+v\n want %+v", env.Held[0], want)
	}
}

// TestEnvelopeHeldHumanClaimedComposite: a human-labeled key that is ALSO
// actively claimed renders kind "human" (label dominates placement) AND
// carries the claim fields alongside its base status fields.
func TestEnvelopeHeldHumanClaimedComposite(t *testing.T) {
	meta := envelopeMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("h1", "sign-off", "status", "open", func(e *model.Event) {
			e.Text = "needs sign-off"
			e.Author = "alice"
			e.TS = "2026-08-16T08:00:00.000"
		}),
		setEv("h2", "sign-off", "labels", "human", nil),
		setEv("h3", "sign-off", "status", "in-progress", func(e *model.Event) {
			e.Text = "taking it"
			e.Author = "bob"
			e.TS = "2026-08-16T11:50:00.000" // 10m before envNow
		}),
	}
	b := Build(meta, evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Held) != 1 {
		t.Fatalf("expected 1 held entry, got %+v", env.Held)
	}
	stale := false
	want := HeldEntry{Key: "sign-off", Title: "needs sign-off", Kind: "human",
		Status: "in-progress", By: "bob", TS: "2026-08-16T11:50:00.000", ID: "h3",
		Age: "10m0s", Stale: &stale}
	got := env.Held[0]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("held human+claimed composite:\n got  %+v\n want %+v", got, want)
	}
	if got.Kind != "human" {
		t.Fatal("label must dominate placement: kind must stay human even though claimed")
	}
}

// TestEnvelopeBlockedEntryShape: the full blocked entry shape. Mirrors the
// spec's own pinned "deploy"/"sign-off" example.
func TestEnvelopeBlockedEntryShape(t *testing.T) {
	evs := []model.Event{
		setEv("h1", "sign-off", "status", "open", func(e *model.Event) { e.Text = "sign-off"; e.Author = "carol" }),
		setEv("h2", "sign-off", "labels", "human", nil),
		setEv("d1", "deploy", "status", "open", func(e *model.Event) {
			e.Text = "ship it"
			e.Author = "alice"
			e.TS = "2026-08-16T09:00:00.000"
		}),
		setEv("d2", "deploy", "blocked-by", "sign-off", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Blocked) != 1 {
		t.Fatalf("expected 1 blocked entry, got %+v", env.Blocked)
	}
	want := BlockedEntry{Key: "deploy", Title: "ship it", Note: "ship it", TS: "2026-08-16T09:00:00.000",
		By: "alice", ID: "d1", WaitingOn: []WaitingOn{{Key: "sign-off", State: "human"}}}
	if !reflect.DeepEqual(env.Blocked[0], want) {
		t.Fatalf("blocked entry shape:\n got  %+v\n want %+v", env.Blocked[0], want)
	}
	if len(env.Ready) != 0 {
		t.Fatalf("deploy must not appear in ready while blocked: %+v", env.Ready)
	}
}

// TestEnvelopeHeldHumanWithUnresolvedEdgesNeverBlocked: a human-labeled,
// non-terminal key with its OWN unresolved blocked-by edges is a distinct
// spec-named case from the human+claimed (no edges) composite tested above
// — the label still excludes it from blocked (quarantine is mechanism), and
// held must carry its waiting_on exactly like a claim's claimed-but-blocked
// case does. Unclaimed here (status=open) since the claimed+edges
// combination is already implied by the same allEdgesTerminal/waitingOnList
// code path the claim test exercises directly.
func TestEnvelopeHeldHumanWithUnresolvedEdgesNeverBlocked(t *testing.T) {
	evs := []model.Event{
		setEv("d1", "dep-y", "status", "open", nil),
		setEv("h1", "sign-off2", "status", "open", func(e *model.Event) {
			e.Text = "needs sign-off, has a dependency"
			e.Author = "alice"
			e.TS = "2026-08-16T09:00:00.000"
		}),
		setEv("h2", "sign-off2", "labels", "human", nil),
		setEv("h3", "sign-off2", "blocked-by", "dep-y", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Held) != 1 {
		t.Fatalf("expected 1 held entry, got %+v", env.Held)
	}
	want := HeldEntry{Key: "sign-off2", Title: "needs sign-off, has a dependency", Kind: "human",
		Status: "open", By: "alice", TS: "2026-08-16T09:00:00.000", ID: "h1",
		WaitingOn: []WaitingOn{{Key: "dep-y", State: "open"}}}
	if !reflect.DeepEqual(env.Held[0], want) {
		t.Fatalf("held human-with-unresolved-edges entry:\n got  %+v\n want %+v", env.Held[0], want)
	}
	if len(env.Blocked) != 0 {
		t.Fatalf("a human-labeled key must never appear in blocked, even with unresolved edges: %+v", env.Blocked)
	}
}

// TestEnvelopeBlockedWaitingOnAllStates: every waiting_on state the spec
// enumerates, on one blocked key with six blockers — terminal wins over
// everything (including a human+claimed blocker, the accepted flattening),
// a missing blocker (orphan) reports statusless same as a present-but-
// statusless one would.
func TestEnvelopeBlockedWaitingOnAllStates(t *testing.T) {
	meta := envelopeMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("t1", "done-dep", "status", "closed", func(e *model.Event) { e.Evidence = []string{"commit:x"} }),
		setEv("o1", "open-dep", "status", "open", nil),
		setEv("p1", "live-dep", "status", "in-progress", func(e *model.Event) { e.TS = "2026-08-16T11:50:00.000" }),
		setEv("s1", "stale-dep", "status", "in-progress", func(e *model.Event) { e.TS = "2026-08-16T10:00:00.000" }),
		setEv("hu1", "human-dep", "status", "open", nil),
		setEv("hu2", "human-dep", "labels", "human", nil),
		setEv("hc1", "human-claimed-dep", "status", "open", nil),
		setEv("hc2", "human-claimed-dep", "labels", "human", nil),
		setEv("hc3", "human-claimed-dep", "status", "in-progress", nil),
		setEv("k1", "deploy", "status", "open", func(e *model.Event) {
			e.Text = "ship it"
			e.Author = "alice"
			e.TS = "2026-08-16T09:00:00.000"
		}),
		setEv("k2", "deploy", "blocked-by",
			"done-dep,open-dep,live-dep,stale-dep,human-dep,human-claimed-dep,orphan-dep", nil),
	}
	b := Build(meta, evs)
	env := b.Envelope(envNow, 50, allowAll)
	if len(env.Blocked) != 1 {
		t.Fatalf("expected 1 blocked entry, got %+v", env.Blocked)
	}
	want := []WaitingOn{
		{Key: "done-dep", State: "terminal"},
		{Key: "open-dep", State: "open"},
		{Key: "live-dep", State: "in-progress"},
		{Key: "stale-dep", State: "in-progress-stale"},
		{Key: "human-dep", State: "human"},
		{Key: "human-claimed-dep", State: "human"},
		{Key: "orphan-dep", State: "statusless"},
	}
	if !reflect.DeepEqual(env.Blocked[0].WaitingOn, want) {
		t.Fatalf("waiting_on states:\n got  %+v\n want %+v", env.Blocked[0].WaitingOn, want)
	}
}

// TestEnvelopeBlockedSortKeyAscending: blocked sorts key-ascending — three
// entries seeded in a deliberately non-alphabetical, non-map-iteration-safe
// order, asserted against the envelope's actual returned order (no
// re-sorting the result before comparing).
func TestEnvelopeBlockedSortKeyAscending(t *testing.T) {
	evs := []model.Event{
		setEv("z1", "zebra-blocked", "status", "open", func(e *model.Event) { e.Text = "z" }),
		setEv("z2", "zebra-blocked", "blocked-by", "some-dep", nil),
		setEv("a1", "alpha-blocked", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "alpha-blocked", "blocked-by", "some-dep", nil),
		setEv("m1", "mid-blocked", "status", "open", func(e *model.Event) { e.Text = "m" }),
		setEv("m2", "mid-blocked", "blocked-by", "some-dep", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	got := make([]string, 0, len(env.Blocked))
	for _, e := range env.Blocked {
		got = append(got, e.Key)
	}
	want := []string{"alpha-blocked", "mid-blocked", "zebra-blocked"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocked sort key-ascending: got %v want %v", got, want)
	}
}

// TestEnvelopeAttentionStaleClaimIncludesHumanLabeled: stale-claim fires
// for both a plain claim and a human-labeled one — placement in attention
// does not carve out human keys (only the frontier verdict does).
func TestEnvelopeAttentionStaleClaimIncludesHumanLabeled(t *testing.T) {
	meta := envelopeMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("s1", "orphaned-task", "status", "open", func(e *model.Event) { e.Text = "orphaned task"; e.Author = "seeder" }),
		setEv("s2", "orphaned-task", "status", "in-progress", func(e *model.Event) {
			e.Text = "claiming"
			e.Author = "dead-worker"
			e.TS = "2026-08-16T09:00:00.000" // 3h before envNow
		}),
		setEv("hu1", "human-stale", "status", "open", func(e *model.Event) { e.Text = "human stale"; e.Author = "seeder" }),
		setEv("hu2", "human-stale", "labels", "human", nil),
		setEv("hu3", "human-stale", "status", "in-progress", func(e *model.Event) {
			e.Text = "claiming"
			e.Author = "worker"
			e.TS = "2026-08-16T09:00:00.000"
		}),
	}
	b := Build(meta, evs)
	env := b.Envelope(envNow, 50, allowAll)
	var got []string
	for _, a := range env.Attention {
		if a.Reason == "stale-claim" {
			got = append(got, a.Key)
		}
	}
	// Order asserted as-returned (no sorting here) — attention sorts
	// key-ascending, and "human-stale" < "orphaned-task" lexically, so this
	// also exercises that ordering, not just membership.
	want := []string{"human-stale", "orphaned-task"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale-claim attention keys (order matters): got %v want %v", got, want)
	}
	for _, a := range env.Attention {
		if a.Key == "orphaned-task" {
			want := AttentionEntry{Reason: "stale-claim", Key: "orphaned-task", Title: "orphaned task",
				By: "dead-worker", Age: "3h0m0s", ID: "s2"}
			if !reflect.DeepEqual(a, want) {
				t.Fatalf("stale-claim entry shape:\n got  %+v\n want %+v", a, want)
			}
		}
	}
}

// TestEnvelopeAttentionStatuslessHalfSeedAndOrphan: a half-seed (a board
// key touched only via a non-status field) and an orphan (a name only ever
// referenced as someone else's blocker) both surface as statusless,
// deduplicated across multiple referencing keys, with no title.
func TestEnvelopeAttentionStatuslessHalfSeedAndOrphan(t *testing.T) {
	evs := []model.Event{
		setEv("hs1", "half-seeded", "labels", "urgent", nil), // touched, but never got a status write
		setEv("k1", "consumer-a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("k2", "consumer-a", "blocked-by", "ghost-dep", nil),
		setEv("k3", "consumer-b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("k4", "consumer-b", "blocked-by", "ghost-dep", nil), // same orphan — must dedupe
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	var got []string
	for _, a := range env.Attention {
		if a.Reason == "statusless" {
			got = append(got, a.Key)
		}
	}
	// Order asserted as-returned (no sorting here): "ghost-dep" <
	// "half-seeded" lexically, matching attention's key-ascending sort.
	want := []string{"ghost-dep", "half-seeded"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statusless attention keys (order matters): got %v want %v", got, want)
	}
	for _, a := range env.Attention {
		if a.Reason == "statusless" && a.Title != "" {
			t.Fatalf("statusless entry must not carry a title: %+v", a)
		}
	}
}

// TestEnvelopeLimitTruncatesButTotalsHonest: --limit caps a list, but
// totals report the true (pre-truncation) count.
func TestEnvelopeLimitTruncatesButTotalsHonest(t *testing.T) {
	var evs []model.Event
	for i, name := range []string{"a1", "a2", "a3", "a4"} {
		name := name
		evs = append(evs, setEv(fmt.Sprintf("id%d", i), name, "status", "open", func(e *model.Event) {
			e.Text = name
			e.TS = fmt.Sprintf("2026-08-16T%02d:00:00.000", 8+i)
		}))
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 2, allowAll)
	if len(env.Ready) != 2 {
		t.Fatalf("expected list truncated to 2, got %d", len(env.Ready))
	}
	if env.Totals.Ready != 4 {
		t.Fatalf("totals must count the full filtered set (4), got %d", env.Totals.Ready)
	}
}

// TestEnvelopeFilterMayEmptyHeld: a filter (standing in for --where) may
// legitimately empty a list — no error, just fewer entries.
func TestEnvelopeFilterMayEmptyHeld(t *testing.T) {
	evs := []model.Event{
		setEv("c1", "big-task", "status", "in-progress", func(e *model.Event) { e.Author = "worker-2" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, func(*Key) bool { return false })
	if len(env.Held) != 0 {
		t.Fatalf("filter must be able to legitimately empty held, got %+v", env.Held)
	}
	if env.Totals.Held != 0 {
		t.Fatal("totals must reflect the filtered (empty) set, not the unfiltered one")
	}
}

// TestEnvelopeFrontierWorkAvailableFromReady: non-empty ready alone drives
// work-available.
func TestEnvelopeFrontierWorkAvailableFromReady(t *testing.T) {
	evs := []model.Event{
		setEv("r1", "fix-retry", "status", "open", func(e *model.Event) { e.Text = "fix it" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if env.Frontier != "work-available" {
		t.Fatalf("non-empty ready must drive work-available, got %q", env.Frontier)
	}
}

// TestEnvelopeFrontierWorkAvailableFromStaleNonHumanClaim: a reclaimable
// stale claim (not human-labeled) also drives work-available, even with an
// empty ready list.
func TestEnvelopeFrontierWorkAvailableFromStaleNonHumanClaim(t *testing.T) {
	meta := envelopeMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("c1", "orphaned-task", "status", "in-progress", func(e *model.Event) {
			e.Author = "dead-worker"
			e.TS = "2026-08-16T09:00:00.000" // 3h stale
		}),
	}
	b := Build(meta, evs)
	env := b.Envelope(envNow, 50, allowAll)
	if env.Frontier != "work-available" {
		t.Fatalf("a stale claim on a non-human key must drive work-available (reclaimable), got %q", env.Frontier)
	}
}

// TestEnvelopeFrontierAttentionNeededOnStaleHumanClaimOnly: a stale claim
// on a HUMAN-labeled key needs a person's override — it must drive
// attention-needed, never work-available, even though it is technically a
// stale claim.
func TestEnvelopeFrontierAttentionNeededOnStaleHumanClaimOnly(t *testing.T) {
	meta := envelopeMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("h1", "human-stale", "status", "open", func(e *model.Event) { e.Text = "x"; e.Author = "seeder" }),
		setEv("h2", "human-stale", "labels", "human", nil),
		setEv("h3", "human-stale", "status", "in-progress", func(e *model.Event) {
			e.Author = "worker"
			e.TS = "2026-08-16T09:00:00.000" // 3h stale
		}),
	}
	b := Build(meta, evs)
	env := b.Envelope(envNow, 50, allowAll)
	if env.Frontier != "attention-needed" {
		t.Fatalf("a stale claim ONLY on a human-labeled key must be attention-needed, never work-available, got %q", env.Frontier)
	}
}

// TestEnvelopeReadySortOldestFirstChainPositionTie: ready sorts oldest
// status-ts first; same-ts entries break the tie by chain position (the
// order their status event appears in the fold's input).
func TestEnvelopeReadySortOldestFirstChainPositionTie(t *testing.T) {
	evs := []model.Event{
		setEv("r1", "written-first", "status", "open", func(e *model.Event) { e.TS = "2026-08-16T08:00:00.000" }),
		setEv("r2", "written-second", "status", "open", func(e *model.Event) { e.TS = "2026-08-16T08:00:00.000" }),
		setEv("r3", "oldest-ts", "status", "open", func(e *model.Event) { e.TS = "2026-08-16T07:00:00.000" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	var got []string
	for _, e := range env.Ready {
		got = append(got, e.Key)
	}
	want := []string{"oldest-ts", "written-first", "written-second"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ready sort (oldest-first, chain-position ties): got %v want %v", got, want)
	}
}

// TestEnvelopeHeldSortKeyAscending: held sorts key-ascending, not insertion
// order. blocked's own ordering is covered separately by
// TestEnvelopeBlockedSortKeyAscending, and attention's by the order
// assertions in the stale-claim/statusless tests above — held/blocked/
// attention share one sort call each in Envelope, but that's an
// implementation detail this test doesn't get to lean on.
func TestEnvelopeHeldSortKeyAscending(t *testing.T) {
	evs := []model.Event{
		setEv("c1", "zebra-task", "status", "in-progress", nil),
		setEv("c2", "alpha-task", "status", "in-progress", nil),
		setEv("c3", "mid-task", "status", "in-progress", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	var got []string
	for _, e := range env.Held {
		got = append(got, e.Key)
	}
	want := []string{"alpha-task", "mid-task", "zebra-task"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("held sort key-ascending: got %v want %v", got, want)
	}
}

// --- Task 11: frontier verdict with the correct DFS (spec rule 9) ---
//
// TestEnvelopeFrontierWorkAvailableFromReady/FromStaleNonHumanClaim and
// TestEnvelopeFrontierAttentionNeededOnStaleHumanClaimOnly above already
// cover two of the required table's eight rows (non-empty ready; stale
// non-human claim only; stale HUMAN claim only). The tests below cover the
// remaining rows: linear chain all-live, diamond behind one open key, a
// true 2-cycle, a statusless reference, a closed human blocker resolving
// its dependents, and full-board computation under both --limit and a
// --where-style filter.

// TestEnvelopeFrontierLinearChainAllLiveAllHandled: C blocked-by B blocked-by
// A, A a live (non-stale) claim — no ready, no attention, no cycle. The
// chain terminates in a live claim, so all-handled.
func TestEnvelopeFrontierLinearChainAllLiveAllHandled(t *testing.T) {
	evs := []model.Event{
		setEv("a1", "a", "status", "in-progress", func(e *model.Event) { e.Author = "worker"; e.TS = "2026-08-16T11:00:00.000" }),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("b2", "b", "blocked-by", "a", nil),
		setEv("c1", "c", "status", "open", func(e *model.Event) { e.Text = "c" }),
		setEv("c2", "c", "blocked-by", "b", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if env.Frontier != "all-handled" {
		t.Fatalf("linear chain terminating in a live claim must be all-handled, got %q (attention=%+v)", env.Frontier, env.Attention)
	}
}

// TestEnvelopeFrontierDiamondBehindOneOpenKeyWorkAvailable: b and c both
// depend on d (a diamond, not a cycle — d is reached via two paths but never
// via itself); d itself has no edges, so it's ready. The DFS must never
// false-flag the diamond as a cycle.
func TestEnvelopeFrontierDiamondBehindOneOpenKeyWorkAvailable(t *testing.T) {
	evs := []model.Event{
		setEv("d1", "d", "status", "open", func(e *model.Event) { e.Text = "d" }),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("b2", "b", "blocked-by", "d", nil),
		setEv("c1", "c", "status", "open", func(e *model.Event) { e.Text = "c" }),
		setEv("c2", "c", "blocked-by", "d", nil),
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b,c", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if env.Frontier != "work-available" {
		t.Fatalf("d ready via the diamond must drive work-available, got %q", env.Frontier)
	}
	for _, a := range env.Attention {
		if a.Reason == "cycle" {
			t.Fatalf("a diamond (shared dependency reached via two paths) must never be flagged a cycle: %+v", env.Attention)
		}
	}
}

// TestEnvelopeFrontierTrueTwoCycleAttentionNeeded: a blocked-by b blocked-by
// a — a true cycle, neither ready nor a live claim. Must surface as
// attention-needed with a cycle entry naming both keys.
func TestEnvelopeFrontierTrueTwoCycleAttentionNeeded(t *testing.T) {
	evs := []model.Event{
		setEv("a1", "a", "status", "open", func(e *model.Event) { e.Text = "a" }),
		setEv("a2", "a", "blocked-by", "b", nil),
		setEv("b1", "b", "status", "open", func(e *model.Event) { e.Text = "b" }),
		setEv("b2", "b", "blocked-by", "a", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if env.Frontier != "attention-needed" {
		t.Fatalf("a true cycle must drive attention-needed, got %q", env.Frontier)
	}
	var cycles []AttentionEntry
	for _, a := range env.Attention {
		if a.Reason == "cycle" {
			cycles = append(cycles, a)
		}
	}
	if len(cycles) != 1 {
		t.Fatalf("expected exactly 1 cycle entry, got %+v", cycles)
	}
	if len(cycles[0].Keys) != 2 || !contains(cycles[0].Keys, "a") || !contains(cycles[0].Keys, "b") {
		t.Fatalf("cycle entry must name both a and b, got %+v", cycles[0].Keys)
	}
}

// TestEnvelopeFrontierStatuslessReferenceAttentionNeeded: c depends on a
// name no set event ever touched (an orphan) — statusless, no ready, no
// cycle. Must surface as attention-needed.
func TestEnvelopeFrontierStatuslessReferenceAttentionNeeded(t *testing.T) {
	evs := []model.Event{
		setEv("c1", "c", "status", "open", func(e *model.Event) { e.Text = "c" }),
		setEv("c2", "c", "blocked-by", "ghost", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if env.Frontier != "attention-needed" {
		t.Fatalf("a statusless (orphan) reference must drive attention-needed, got %q", env.Frontier)
	}
}

// TestEnvelopeFrontierClosedHumanBlockerResolvesDependentsWorkAvailable: x is
// human-labeled AND terminal (closed) — a terminal status resolves an edge
// regardless of label. y, blocked-by x, must land in ready.
func TestEnvelopeFrontierClosedHumanBlockerResolvesDependentsWorkAvailable(t *testing.T) {
	evs := []model.Event{
		setEv("x1", "x", "status", "open", func(e *model.Event) { e.Text = "x" }),
		setEv("x2", "x", "labels", "human", nil),
		setEv("x3", "x", "status", "closed", func(e *model.Event) { e.Evidence = []string{"commit:x"} }),
		setEv("y1", "y", "status", "open", func(e *model.Event) { e.Text = "y" }),
		setEv("y2", "y", "blocked-by", "x", nil),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 50, allowAll)
	if env.Frontier != "work-available" {
		t.Fatalf("a closed human-labeled blocker must resolve its dependent's edge, got %q", env.Frontier)
	}
	if len(env.Ready) != 1 || env.Ready[0].Key != "y" {
		t.Fatalf("y must land in ready once its human blocker closes: %+v", env.Ready)
	}
}

// TestEnvelopeFrontierFullBoardIgnoresLimitTruncation: three ready keys,
// --limit 1 — frontier must still be work-available, and totals honest.
func TestEnvelopeFrontierFullBoardIgnoresLimitTruncation(t *testing.T) {
	evs := []model.Event{
		setEv("r1", "r1", "status", "open", func(e *model.Event) { e.Text = "r1" }),
		setEv("r2", "r2", "status", "open", func(e *model.Event) { e.Text = "r2" }),
		setEv("r3", "r3", "status", "open", func(e *model.Event) { e.Text = "r3" }),
	}
	b := Build(envelopeMeta(), evs)
	env := b.Envelope(envNow, 1, allowAll)
	if len(env.Ready) != 1 {
		t.Fatalf("--limit 1 must truncate the returned list, got %d entries", len(env.Ready))
	}
	if env.Totals.Ready != 3 {
		t.Fatalf("totals must report the true count, got %d", env.Totals.Ready)
	}
	if env.Frontier != "work-available" {
		t.Fatalf("frontier must be computed on the full pre-truncation board, got %q", env.Frontier)
	}
}

// TestEnvelopeFrontierFullBoardIgnoresWhereFilter: r1 is ready on the full
// board, but a filter (standing in for --where) excludes it from every
// returned list. The spec pins frontier to "the FULL board regardless of
// --limit" — read together with "Extra --where clauses apply uniformly to
// all lists" (which names the four *lists*, not the verdict), the verdict
// is the board's own truth, not the view's: it must still report
// work-available even though the filtered ready list is empty.
func TestEnvelopeFrontierFullBoardIgnoresWhereFilter(t *testing.T) {
	evs := []model.Event{
		setEv("r1", "r1", "status", "open", func(e *model.Event) { e.Text = "r1" }),
	}
	b := Build(envelopeMeta(), evs)
	excludeR1 := func(k *Key) bool { return k == nil || k.Name != "r1" }
	env := b.Envelope(envNow, 50, excludeR1)
	if len(env.Ready) != 0 {
		t.Fatalf("the filter must empty the returned ready list, got %+v", env.Ready)
	}
	if env.Frontier != "work-available" {
		t.Fatalf("frontier must be computed on the full unfiltered board, got %q", env.Frontier)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func derefBool(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}
