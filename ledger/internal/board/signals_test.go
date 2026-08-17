package board

import (
	"testing"
	"time"

	"ledger/internal/model"
)

// plainMeta guards status but never opts into ready-capable semantics (no
// --terminal) — rule 5 must never apply here, per spec "rule 5 exists only
// on ready-capable boards".
func plainMeta() model.Meta {
	return model.Meta{
		Fields: map[string][]string{"status": {"open", "done", "failed"}},
	}
}

// TestSignalClaimFiresCrossAuthorNonStale: an isolated claim signal names
// the claimant and the exact claim age.
func TestSignalClaimFiresCrossAuthorNonStale(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "k1", "status", "in-progress", func(e *model.Event) {
			e.Author = "alice"
			e.TS = "2026-08-16T00:00:00.000"
		}),
	}
	b := Build(readyMeta(), evs)
	now, _ := model.ParseTS("2026-08-16T00:05:00.000")
	signals := b.Signals(b.Keys["k1"], true, "bob", now)
	if len(signals) != 1 || signals[0].Name != "claim" {
		t.Fatalf("want single claim signal, got %+v", signals)
	}
	if signals[0].Facts != "alice, 5m0s" {
		t.Fatalf("claim facts: got %q, want %q", signals[0].Facts, "alice, 5m0s")
	}
}

// TestSignalOwnClaimNotASignal: your own claim is never a signal against you.
func TestSignalOwnClaimNotASignal(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "k1", "status", "in-progress", func(e *model.Event) { e.Author = "alice" }),
	}
	b := Build(readyMeta(), evs)
	if signals := b.Signals(b.Keys["k1"], true, "alice", time.Now()); len(signals) != 0 {
		t.Fatalf("own claim must never be a signal, got %+v", signals)
	}
}

// TestSignalStaleClaimNotASignal: a stale claim is freely reclaimable, so
// it must never surface as a signal.
func TestSignalStaleClaimNotASignal(t *testing.T) {
	meta := readyMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("1a", "k1", "status", "in-progress", func(e *model.Event) {
			e.Author = "alice"
			e.TS = "2026-08-16T00:00:00.000"
		}),
	}
	b := Build(meta, evs)
	now, _ := model.ParseTS("2026-08-16T02:00:00.000") // 2h later: stale
	if signals := b.Signals(b.Keys["k1"], true, "bob", now); len(signals) != 0 {
		t.Fatalf("a stale claim must never be a signal, got %+v", signals)
	}
}

// TestSignalHumanFiresRegardlessOfAuthorOrTouchesStatus: human applies to
// everyone, including on a write that doesn't touch status.
func TestSignalHumanFiresRegardlessOfAuthorOrTouchesStatus(t *testing.T) {
	evs := []model.Event{setEv("1a", "k1", "labels", "human", nil)}
	b := Build(readyMeta(), evs)
	signals := b.Signals(b.Keys["k1"], false, "anyone", time.Now())
	if len(signals) != 1 || signals[0].Name != "human" || signals[0].Facts != "labeled 'human'" {
		t.Fatalf("want single human signal, got %+v", signals)
	}
}

// TestSignalSettledFiresForCloseAuthorTooWithEvidence: settled applies even
// to the author of the close itself, and reports the evidence state.
func TestSignalSettledFiresForCloseAuthorTooWithEvidence(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "k1", "status", "closed", func(e *model.Event) {
			e.Author = "alice"
			e.Evidence = []string{"commit:abc"}
		}),
	}
	b := Build(readyMeta(), evs)
	signals := b.Signals(b.Keys["k1"], true, "alice", time.Now())
	if len(signals) != 1 || signals[0].Name != "settled" || signals[0].Facts != "closed, evidence: yes" {
		t.Fatalf("want single settled signal with evidence, got %+v", signals)
	}
}

// TestSignalSettledFactsNoEvidence: the settled fact string reports the
// absence of evidence too.
func TestSignalSettledFactsNoEvidence(t *testing.T) {
	evs := []model.Event{setEv("1a", "k1", "status", "closed", nil)}
	b := Build(readyMeta(), evs)
	signals := b.Signals(b.Keys["k1"], true, "anyone", time.Now())
	if len(signals) != 1 || signals[0].Facts != "closed, evidence: no" {
		t.Fatalf("want evidence: no, got %+v", signals)
	}
}

// TestSignalsComposedClaimHumanFixedOrder: multiple standing signals compose
// in the spec's fixed order (claim, human, settled).
func TestSignalsComposedClaimHumanFixedOrder(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "k1", "status", "in-progress", func(e *model.Event) { e.Author = "alice" }),
		setEv("1b", "k1", "labels", "human", nil),
	}
	b := Build(readyMeta(), evs)
	signals := b.Signals(b.Keys["k1"], true, "bob", time.Now())
	if len(signals) != 2 || signals[0].Name != "claim" || signals[1].Name != "human" {
		t.Fatalf("want [claim, human] in fixed order, got %+v", signals)
	}
}

// TestSignalHumanGatesBlockedByWrites: human still gates a write that
// doesn't touch status (e.g. a blocked-by edge edit).
func TestSignalHumanGatesBlockedByWrites(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "k1", "status", "open", nil),
		setEv("1b", "k1", "labels", "human", nil),
	}
	b := Build(readyMeta(), evs)
	signals := b.Signals(b.Keys["k1"], false, "anyone", time.Now())
	if len(signals) != 1 || signals[0].Name != "human" {
		t.Fatalf("human must gate a blocked-by write too, got %+v", signals)
	}
}

// TestSignalsClaimAndSettledNeverFireWhenTouchesStatusFalse: claim and
// settled are status-write-only signals — an edge edit never triggers them,
// even when their underlying condition (live cross-author claim / terminal
// status) holds.
func TestSignalsClaimAndSettledNeverFireWhenTouchesStatusFalse(t *testing.T) {
	evs1 := []model.Event{
		setEv("1a", "k1", "status", "in-progress", func(e *model.Event) { e.Author = "alice" }),
	}
	b1 := Build(readyMeta(), evs1)
	if signals := b1.Signals(b1.Keys["k1"], false, "bob", time.Now()); len(signals) != 0 {
		t.Fatalf("claim must never fire on a non-status write, got %+v", signals)
	}

	evs2 := []model.Event{setEv("1a", "k2", "status", "closed", nil)}
	b2 := Build(readyMeta(), evs2)
	if signals := b2.Signals(b2.Keys["k2"], false, "anyone", time.Now()); len(signals) != 0 {
		t.Fatalf("settled must never fire on a non-status write, got %+v", signals)
	}
}

// TestSignalsNeverFireOnPlainBoard: rule 5 exists only on ready-capable
// boards — a plain board must return no signals ever, no matter the key's
// state.
func TestSignalsNeverFireOnPlainBoard(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "k1", "status", "in-progress", func(e *model.Event) { e.Author = "alice" }),
		setEv("1b", "k1", "labels", "human", nil),
	}
	b := Build(plainMeta(), evs)
	if signals := b.Signals(b.Keys["k1"], true, "bob", time.Now()); len(signals) != 0 {
		t.Fatalf("a plain (non-ready-capable) board must never produce signals, got %+v", signals)
	}
}

// TestSignalsNilKeyReturnsNil: a brand-new key (never touched by any event)
// carries no signals — there is nothing to check against.
func TestSignalsNilKeyReturnsNil(t *testing.T) {
	b := Build(readyMeta(), nil)
	if signals := b.Signals(nil, true, "anyone", time.Now()); signals != nil {
		t.Fatalf("a nil key must never carry signals, got %+v", signals)
	}
}
