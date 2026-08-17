package dag

import (
	"strings"
	"testing"
)

// sha pads a readable label out to a full 40-char sha, since the fold's
// final tiebreak is the FULL sha and a test using short ids would compare
// something the real code never compares.
func sha(label string) string {
	return label + strings.Repeat("0", 40-len(label))
}

func ev(t *testing.T, label, when string, parents ...string) Node {
	t.Helper()
	n := Node{SHA: sha(label), TS: when}
	for _, p := range parents {
		n.Parents = append(n.Parents, sha(p))
	}
	return n
}

func sentinel(label string, parents ...string) Node {
	n := Node{SHA: sha(label), IsSentinel: true}
	for _, p := range parents {
		n.Parents = append(n.Parents, sha(p))
	}
	return n
}

func labels(order []string) []string {
	out := make([]string, len(order))
	for i, s := range order {
		out[i] = strings.TrimRight(s, "0")
	}
	return out
}

// TestSkewedClocksNeverBreakAncestry: ancestry is structural and immune to
// skew. A late-dated ROOT (a host whose clock runs hours fast wrote the
// creation commit) must still sort first, because every other event
// descends from it — the heap only ever chooses among events that are
// genuinely concurrent.
func TestSkewedClocksNeverBreakAncestry(t *testing.T) {
	nodes := []Node{
		ev(t, "root", "2026-08-17T23:00:00.000"),      // clock 12h fast
		ev(t, "a", "2026-08-17T11:00:00.000", "root"), // real time
		ev(t, "b", "2026-08-17T12:00:00.000", "a"),
	}
	got := labels(Sort(nodes).Order)
	want := []string{"root", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ancestry must dominate timestamps: got %v want %v", got, want)
		}
	}
}

// TestConcurrentEventsSortByTimestampThenSHA: among genuinely concurrent
// events the heap key decides — parsed timestamp first, full sha as the
// tiebreak.
func TestConcurrentEventsSortByTimestampThenSHA(t *testing.T) {
	nodes := []Node{
		ev(t, "root", "2026-08-17T10:00:00.000"),
		ev(t, "zz", "2026-08-17T11:00:00.000", "root"), // later sha, earlier ts
		ev(t, "aa", "2026-08-17T12:00:00.000", "root"),
	}
	if got := labels(Sort(nodes).Order); got[1] != "zz" || got[2] != "aa" {
		t.Fatalf("timestamp beats sha among concurrent events: %v", got)
	}

	// Same timestamp: the full sha breaks the tie, deterministically.
	nodes[1].TS, nodes[2].TS = "2026-08-17T11:00:00.000", "2026-08-17T11:00:00.000"
	if got := labels(Sort(nodes).Order); got[1] != "aa" || got[2] != "zz" {
		t.Fatalf("exact ts tie must break by full sha ascending: %v", got)
	}
}

// TestMixedTimestampLayoutsCompareAsTimes: the millisecond and legacy
// layouts must compare as TIMES, never as strings — string order would put
// "…T11:00:00" after "…T11:00:00.000" and reorder the fold.
func TestMixedTimestampLayoutsCompareAsTimes(t *testing.T) {
	legacy := Node{SHA: sha("zlegacy"), Parents: []string{sha("root")}, TS: "2026-08-17T11:00:00"}
	nodes := []Node{
		ev(t, "root", "2026-08-17T10:00:00.000"),
		ev(t, "amilli", "2026-08-17T11:00:00.500", "root"),
		legacy,
	}
	// 11:00:00.000 (legacy) is genuinely earlier than 11:00:00.500, despite
	// sorting later as a string and having the later sha.
	if got := labels(Sort(nodes).Order); got[1] != "zlegacy" {
		t.Fatalf("layouts must compare as times: %v", got)
	}
}

// TestUndatedEventsSortAfterEveryDatedPeer pins the spec's rule for a
// missing or unparseable ts.
func TestUndatedEventsSortAfterEveryDatedPeer(t *testing.T) {
	undated := Node{SHA: sha("aundated"), Parents: []string{sha("root")}}
	nodes := []Node{
		ev(t, "root", "2026-08-17T10:00:00.000"),
		undated,
		ev(t, "zdated", "2026-08-17T23:00:00.000", "root"),
	}
	got := labels(Sort(nodes).Order)
	if got[1] != "zdated" || got[2] != "aundated" {
		t.Fatalf("undated sorts after every dated peer regardless of sha: %v", got)
	}
}

// TestSentinelContractionIsTotal is the design's load-bearing claim: a sync
// sentinel's own clock must never reorder real events. Same event set, same
// merge structure, sentinel timestamp varied wildly — the fold is unchanged
// (and the sentinel never appears in it).
func TestSentinelContractionIsTotal(t *testing.T) {
	build := func() []Node {
		return []Node{
			ev(t, "root", "2026-08-17T10:00:00.000"),
			ev(t, "alice", "2026-08-17T11:00:00.000", "root"),
			ev(t, "bob", "2026-08-17T12:00:00.000", "root"),
			sentinel("merge", "alice", "bob"),
			ev(t, "after", "2026-08-17T13:00:00.000", "merge"),
		}
	}
	base := labels(Sort(build()).Order)
	want := []string{"root", "alice", "bob", "after"}
	for i := range want {
		if base[i] != want[i] {
			t.Fatalf("fold = %v, want %v (sentinel contracted out)", base, want)
		}
	}
	// The sentinel is not an event and is never emitted.
	for _, l := range base {
		if l == "merge" {
			t.Fatal("a sync sentinel must never appear in the fold")
		}
	}
	// Assert the fold is byte-identical when a second, differently-shaped
	// sentinel chain joins the same two branches.
	nested := []Node{
		ev(t, "root", "2026-08-17T10:00:00.000"),
		ev(t, "alice", "2026-08-17T11:00:00.000", "root"),
		ev(t, "bob", "2026-08-17T12:00:00.000", "root"),
		sentinel("m1", "alice", "bob"),
		sentinel("m2", "m1"), // a sentinel whose parent is a sentinel
		ev(t, "after", "2026-08-17T13:00:00.000", "m2"),
	}
	got := labels(Sort(nested).Order)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chained sentinels must contract transitively: %v want %v", got, want)
		}
	}
}

// TestReplicasWithDifferentMergeOrdersFoldIdentically is the convergence
// property the whole trial rests on: two replicas that merged the same
// events in different orders, with the merge parents swapped, render the
// same fold.
func TestReplicasWithDifferentMergeOrdersFoldIdentically(t *testing.T) {
	replicaA := []Node{
		ev(t, "root", "2026-08-17T10:00:00.000"),
		ev(t, "x", "2026-08-17T11:00:00.000", "root"),
		ev(t, "y", "2026-08-17T12:00:00.000", "root"),
		sentinel("m", "x", "y"), // A merged x-first
	}
	replicaB := []Node{
		ev(t, "root", "2026-08-17T10:00:00.000"),
		ev(t, "x", "2026-08-17T11:00:00.000", "root"),
		ev(t, "y", "2026-08-17T12:00:00.000", "root"),
		sentinel("m", "y", "x"), // B merged y-first
	}
	a, b := labels(Sort(replicaA).Order), labels(Sort(replicaB).Order)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("merge parent order must not affect the fold: %v vs %v", a, b)
	}
}

// TestCrissCrossMergesFold: two replicas that each merged the other, then
// merged again — the shape git produces when both sides sync before either
// pushes. Every real event must appear exactly once, ancestors first.
func TestCrissCrossMergesFold(t *testing.T) {
	nodes := []Node{
		ev(t, "root", "2026-08-17T10:00:00.000"),
		ev(t, "a1", "2026-08-17T11:00:00.000", "root"),
		ev(t, "b1", "2026-08-17T11:30:00.000", "root"),
		sentinel("ma", "a1", "b1"),
		sentinel("mb", "b1", "a1"),
		ev(t, "a2", "2026-08-17T12:00:00.000", "ma"),
		ev(t, "b2", "2026-08-17T12:30:00.000", "mb"),
		sentinel("mc", "a2", "b2"),
	}
	got := labels(Sort(nodes).Order)
	want := []string{"root", "a1", "b1", "a2", "b2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("criss-cross fold = %v, want %v", got, want)
	}
}

// TestTornCommitsAreContractedNotDropped: a commit with no readable event
// (marked IsSentinel, like a foreign or torn object) must be spliced out,
// never left to strand its children out of the fold.
func TestTornCommitsAreContractedNotDropped(t *testing.T) {
	nodes := []Node{
		ev(t, "root", "2026-08-17T10:00:00.000"),
		sentinel("torn", "root"),
		ev(t, "after", "2026-08-17T11:00:00.000", "torn"),
	}
	got := labels(Sort(nodes).Order)
	if len(got) != 2 || got[0] != "root" || got[1] != "after" {
		t.Fatalf("a torn commit must not strand its children: %v", got)
	}
}

func TestChildrenIsContractedAdjacency(t *testing.T) {
	// A <- S(sentinel) <- B : contracted children of A must be [B]; S absent everywhere.
	r := Sort([]Node{
		{SHA: sha("a"), TS: "2026-08-17T01:00:00.000"},
		{SHA: sha("s"), Parents: []string{sha("a")}, TS: "2026-08-17T02:00:00.000", IsSentinel: true},
		{SHA: sha("b"), Parents: []string{sha("s")}, TS: "2026-08-17T03:00:00.000"},
	})
	if len(r.Children[sha("a")]) != 1 || r.Children[sha("a")][0] != sha("b") {
		t.Fatalf("contracted child: %v", r.Children)
	}
	if _, ok := r.Children[sha("s")]; ok {
		t.Fatal("sentinel present in adjacency")
	}
}

func TestRootsMultiRootDetected(t *testing.T) {
	// two parentless non-sentinel nodes joined by a sentinel merge -> 2 roots
	r := Sort([]Node{
		{SHA: sha("a"), TS: "2026-08-17T01:00:00.000"},
		{SHA: sha("x"), TS: "2026-08-17T01:30:00.000"},
		{SHA: sha("m"), Parents: []string{sha("a"), sha("x")}, TS: "2026-08-17T02:00:00.000", IsSentinel: true},
	})
	if len(r.Roots) != 2 {
		t.Fatalf("roots = %v", r.Roots)
	}
}
