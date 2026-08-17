// Package dag orders a ledger's event DAG (sync design, Addition 1): Kahn's
// topological sort with a min-heap keyed on (parsed event timestamp, full
// 40-char commit SHA), run over a graph from which every sentinel commit
// has first been CONTRACTED OUT — its parents spliced straight to its
// children.
//
// The contraction is the load-bearing half. A sync sentinel is minted by
// whichever host happened to run `ledger sync`, carrying that host's clock;
// leaving it in the heap would let the syncing host's clock decide
// last-write-wins outcomes between two OTHER hosts' writes. Contracted out,
// a sentinel can neither delay nor reorder a real event — it only carries
// the ancestry its parents already had. Callers mark torn or foreign
// commits (no readable event.json) as sentinels too, for the same reason
// reads have always skipped them: they must never crash or reorder a read.
//
// Pure: no git; the only outside dependency is model.ParseTS for timestamp
// parsing. Callers hand in nodes — git's traversal order is irrelevant to
// the result (both --topo-order and --date-order are merge-parent-
// dependent, which is exactly why this sort exists).
package dag

import (
	"container/heap"
	"sort"
	"time"

	"ledger/internal/model"
)

// Node is one commit of a ledger's chain.
type Node struct {
	// SHA is the full 40-char commit sha — the fold order's final tiebreak,
	// so it is never the abbreviated form.
	SHA string
	// Parents are full 40-char parent shas. Parents naming commits outside
	// this node set (there are none in a well-formed ledger ref) are ignored.
	Parents []string
	// TS is the event's own timestamp, in model.TSLayout or
	// model.TSLayoutLegacy. A missing or unparseable TS sorts after every
	// timestamped peer, by SHA (spec: "an event with a missing or
	// unparseable ts sorts after all timestamped peers, by SHA").
	TS string
	// IsSentinel marks a commit to be contracted out before the sort: sync
	// sentinels and torn/foreign commits alike — neither may delay or
	// reorder a real event.
	IsSentinel bool
}

// Result is the pinned fold order plus the sentinel-contracted DAG's
// adjacency.
type Result struct {
	// Order holds the non-sentinel nodes' full SHAs in fold order.
	Order []string
	// Children is the child adjacency of the sentinel-contracted DAG
	// (non-sentinel nodes only — a sentinel's SHA never appears as a key
	// or a value).
	Children map[string][]string
	// Roots are the contracted DAG's parentless nodes. More than one means
	// a multi-root ledger, which callers use to detect divergent history.
	Roots []string
}

// Sort computes the fold order.
func Sort(nodes []Node) Result {
	byS := make(map[string]int, len(nodes))
	for i := range nodes {
		byS[nodes[i].SHA] = i
	}

	eff := effectiveParents(nodes, byS)

	kept := make([]int, 0, len(nodes))
	for i := range nodes {
		if !nodes[i].IsSentinel {
			kept = append(kept, i)
		}
	}

	ts := make(map[string]parsedTS, len(kept))
	indeg := make(map[string]int, len(kept))
	children := make(map[string][]string, len(kept))
	for _, i := range kept {
		sha := nodes[i].SHA
		ts[sha] = parseTS(nodes[i].TS)
		indeg[sha] = len(eff[sha])
		for _, p := range eff[sha] {
			children[p] = append(children[p], sha)
		}
	}

	var roots []string
	h := &shaHeap{ts: ts}
	for _, i := range kept {
		sha := nodes[i].SHA
		if indeg[sha] == 0 {
			roots = append(roots, sha)
			h.shas = append(h.shas, sha)
		}
	}
	heap.Init(h)

	order := make([]string, 0, len(kept))
	for h.Len() > 0 {
		sha := heap.Pop(h).(string)
		order = append(order, sha)
		for _, c := range children[sha] {
			indeg[c]--
			if indeg[c] == 0 {
				heap.Push(h, c)
			}
		}
	}

	// Defensive: a git DAG is acyclic, so nothing should be left. If a
	// malformed graph ever strands nodes, append them in heap-key order
	// rather than silently dropping events from every read.
	if len(order) < len(kept) {
		done := make(map[string]bool, len(order))
		for _, s := range order {
			done[s] = true
		}
		var left []string
		for _, i := range kept {
			if s := nodes[i].SHA; !done[s] {
				left = append(left, s)
			}
		}
		sort.Slice(left, func(a, b int) bool { return tsLess(left[a], left[b], ts) })
		order = append(order, left...)
	}

	return Result{Order: order, Children: children, Roots: roots}
}

// effectiveParents maps every non-sentinel node's SHA to its nearest
// non-sentinel ancestors — the contraction. Walking through a sentinel
// yields whatever kept commits sit above it, so a sync merge hands its two
// branch tips straight to the events that followed it, and the ancestry
// those events gained by being merged is preserved exactly.
func effectiveParents(nodes []Node, byS map[string]int) map[string][]string {
	memo := map[string][]string{}
	var lift func(sha string) []string
	lift = func(sha string) []string {
		if v, ok := memo[sha]; ok {
			return v
		}
		i, known := byS[sha]
		if !known {
			return nil // a parent outside this ref's history
		}
		if !nodes[i].IsSentinel {
			return []string{sha}
		}
		memo[sha] = nil // guard against a malformed cyclic graph
		var out []string
		for _, p := range nodes[i].Parents {
			out = append(out, lift(p)...)
		}
		out = dedupe(out)
		memo[sha] = out
		return out
	}

	eff := make(map[string][]string, len(nodes))
	for i := range nodes {
		if nodes[i].IsSentinel {
			continue
		}
		var ps []string
		for _, p := range nodes[i].Parents {
			ps = append(ps, lift(p)...)
		}
		eff[nodes[i].SHA] = dedupe(ps)
	}
	return eff
}

func dedupe(xs []string) []string {
	if len(xs) < 2 {
		return xs
	}
	seen := make(map[string]bool, len(xs))
	out := xs[:0]
	for _, x := range xs {
		if seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// parsedTS is a node's heap-key half: the parsed timestamp, or the
// undated-sorts-last marker.
type parsedTS struct {
	t     time.Time
	dated bool
}

func parseTS(s string) parsedTS {
	if s == "" {
		return parsedTS{}
	}
	t, err := model.ParseTS(s)
	if err != nil {
		return parsedTS{}
	}
	return parsedTS{t: t, dated: true}
}

// tsLess is the heap key: dated events first, ordered by parsed time (never
// by string — the millisecond and legacy layouts must compare as times),
// then by full SHA. Undated events sort after every dated peer, among
// themselves by SHA.
func tsLess(a, b string, ts map[string]parsedTS) bool {
	pa, pb := ts[a], ts[b]
	if pa.dated != pb.dated {
		return pa.dated
	}
	if pa.dated && !pa.t.Equal(pb.t) {
		return pa.t.Before(pb.t)
	}
	return a < b
}

// shaHeap is a min-heap of ready SHAs, ordered by tsLess.
type shaHeap struct {
	shas []string
	ts   map[string]parsedTS
}

func (h shaHeap) Len() int           { return len(h.shas) }
func (h shaHeap) Less(i, j int) bool { return tsLess(h.shas[i], h.shas[j], h.ts) }
func (h shaHeap) Swap(i, j int)      { h.shas[i], h.shas[j] = h.shas[j], h.shas[i] }
func (h *shaHeap) Push(x any)        { h.shas = append(h.shas, x.(string)) }
func (h *shaHeap) Pop() any {
	old := h.shas
	n := old[len(old)-1]
	h.shas = old[:len(old)-1]
	return n
}
