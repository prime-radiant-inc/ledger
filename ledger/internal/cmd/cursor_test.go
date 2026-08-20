package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/store"
)

// mergedChain is a real divergent ledger DAG, built through the store's own
// commit builder: one root, two branches of three events each, joined by a
// sync sentinel that IS the ref tip. Every historical falsification of the
// pager law (the rev-4 livelock, the sentinel-tip hole, the branch-local
// cursor oscillation) was found on this shape, so the fixtures below are
// regression traps first and unit tests second.
type mergedChain struct {
	dir      string
	s        store.Store
	root     string
	a, b     []string // full shas, oldest first
	merge    string   // the sentinel tip
	aIDs     []string
	bIDs     []string
	allOrder []string // global fold order, event ids
}

func (m mergedChain) id(sha string) string { return sha[:10] }

// buildChain lands one event commit; ts and parent are explicit so a fixture
// can stage the exact skew a fold rule turns on.
func buildChain(t *testing.T, s store.Store, parent, key, ts string, extra map[string]string) string {
	t.Helper()
	ev := model.Event{TS: ts, Type: "set", Key: key, Author: "t",
		Fields: map[string]string{"status": "open"}}
	sha, err := s.BuildCommit("demo", parent, ev, extra)
	if err != nil {
		t.Fatalf("BuildCommit(%s): %v", key, err)
	}
	return sha
}

// buildSentinel mints a sync-sentinel merge commit over N parents. Sentinels
// carry a sync event.json, which is exactly what makes the store contract
// them out of the fold — and what makes them invisible to any positional
// cursor scheme.
func buildSentinel(t *testing.T, s store.Store, ts string, parents ...string) string {
	t.Helper()
	blob, _, code := s.Repo.Git(`{"type":"sync","ts":"`+ts+`","author":"host"}`, "hash-object", "-w", "--stdin")
	if code != 0 {
		t.Fatal("hash-object for sentinel failed")
	}
	tree, _, code := s.Repo.Git("100644 blob "+blob+"\tevent.json\n", "mktree")
	if code != 0 {
		t.Fatal("mktree for sentinel failed")
	}
	args := append(gitx.IdentityArgs("host", "sync"), "commit-tree", tree, "-m", "sync")
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	sha, _, code := s.Repo.Git("", args...)
	if code != 0 {
		t.Fatal("commit-tree for sentinel failed")
	}
	return sha
}

func setRef(t *testing.T, s store.Store, sha string) {
	t.Helper()
	if _, se, code := s.Repo.Git("", "update-ref", "refs/ledger/demo", sha); code != 0 {
		t.Fatalf("update-ref: %s", se)
	}
}

const chainMeta = `{"slug":"demo","scope":"x","fields":{"status":["open","done"]}}`

// buildMergedChain: root, then branch A (a1,a2,a3) and branch B (b1,b2,b3)
// diverging from it with interleaved timestamps, joined by a sentinel tip.
// Global fold order is root,a1,b1,a2,b2,a3,b3.
func buildMergedChain(t *testing.T) mergedChain {
	t.Helper()
	dir := initRepo(t)
	s := store.Store{Repo: gitx.Repo{Dir: dir}}
	m := mergedChain{dir: dir, s: s}
	m.root = buildChain(t, s, "", "root", "2026-08-17T10:00:00.000", map[string]string{"meta.json": chainMeta})
	prev := m.root
	for i, ts := range []string{"2026-08-17T11:00:00.000", "2026-08-17T13:00:00.000", "2026-08-17T15:00:00.000"} {
		prev = buildChain(t, s, prev, fmt.Sprintf("a%d", i+1), ts, nil)
		m.a = append(m.a, prev)
		m.aIDs = append(m.aIDs, prev[:10])
	}
	prev = m.root
	for i, ts := range []string{"2026-08-17T12:00:00.000", "2026-08-17T14:00:00.000", "2026-08-17T16:00:00.000"} {
		prev = buildChain(t, s, prev, fmt.Sprintf("b%d", i+1), ts, nil)
		m.b = append(m.b, prev)
		m.bIDs = append(m.bIDs, prev[:10])
	}
	m.merge = buildSentinel(t, s, "2026-08-17T23:00:00.000", m.a[2], m.b[2])
	setRef(t, s, m.merge)
	m.allOrder = []string{m.root[:10], m.aIDs[0], m.bIDs[0], m.aIDs[1], m.bIDs[1], m.aIDs[2], m.bIDs[2]}
	return m
}

// sinceIDs runs `since` and returns the delivered event ids in delivery
// order plus the emitted cursor.
func sinceIDs(t *testing.T, dir string, args ...string) ([]string, string) {
	t.Helper()
	so, se, code := run(t, dir, append([]string{"since"}, args...)...)
	if code != 0 {
		t.Fatalf("since %v: code=%d %s", args, code, se)
	}
	doc := mustJSON(t, so)
	var ids []string
	for _, e := range doc["events"].([]any) {
		ids = append(ids, e.(map[string]any)["id"].(string))
	}
	cur, _ := doc["cursor"].(string)
	return ids, cur
}

// (a) The rev-4 livelock trap. Paging a merged chain from the root must
// terminate, deliver every event exactly once, and land on the tip — the
// falsified "emit the last delivered event" rule re-delivered the other
// branch on every page, forever.
func TestPagedDrainAcrossMergeIsExactlyOnceAndTerminates(t *testing.T) {
	m := buildMergedChain(t)
	drain, drainCur := sinceIDs(t, m.dir, m.id(m.root), "--ledger", "demo")
	if len(drain) != 6 {
		t.Fatalf("unpaged drain from root must deliver both branches: %v", drain)
	}

	seen := map[string]int{}
	var paged []string
	cur := m.id(m.root)
	for pages := 0; ; pages++ {
		if pages > 12 {
			t.Fatalf("pager did not terminate (livelock): seen=%v", seen)
		}
		ids, next := sinceIDs(t, m.dir, cur, "--limit", "2", "--ledger", "demo")
		for _, id := range ids {
			seen[id]++
			paged = append(paged, id)
		}
		if next == cur {
			break
		}
		cur = next
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("event %s delivered %d times — exactly-once is the whole contract: %v", id, n, paged)
		}
	}
	if len(paged) != len(drain) {
		t.Fatalf("paged union %v != unpaged drain %v", paged, drain)
	}
	for _, id := range drain {
		if seen[id] != 1 {
			t.Fatalf("drain event %s missing from the paged union %v", id, paged)
		}
	}
	if cur != m.id(m.merge) || drainCur != m.id(m.merge) {
		t.Fatalf("final cursor must be the tip: paged=%s unpaged=%s tip=%s", cur, drainCur, m.id(m.merge))
	}
}

// (b) The sentinel-tip hole. When the range exhausts before a single
// dominating event exists, the page emits the TIP — including when the tip
// is a sync sentinel, which is a legal cursor and no branch head is.
func TestPagedDrainEmitsSentinelTipOnExhaustion(t *testing.T) {
	m := buildMergedChain(t)
	ids, cur := sinceIDs(t, m.dir, m.id(m.root), "--limit", "2", "--ledger", "demo")
	if len(ids) != 6 {
		t.Fatalf("--limit is a floor: a page crossing the divergence delivers the whole region: %v", ids)
	}
	if cur != m.id(m.merge) {
		t.Fatalf("exhausted page must emit the sentinel TIP %s, got %s", m.id(m.merge), cur)
	}
	for _, head := range []string{m.id(m.a[2]), m.id(m.b[2])} {
		if cur == head {
			t.Fatalf("emitted cursor must be the tip, never a branch head: %s", cur)
		}
	}
	// and the sentinel must be a usable cursor on the next call
	next, cur2 := sinceIDs(t, m.dir, cur, "--ledger", "demo")
	if len(next) != 0 || cur2 != cur {
		t.Fatalf("a sentinel cursor at the tip must be valid and drain empty: %v %s", next, cur2)
	}
}

// The other half of the exhaustion clause: once real work lands ABOVE the
// merge, that event dominates both branches and the page stops there instead
// of running to the tip. This is also the trap for judging maximality on the
// RAW DAG: `after`'s only raw parent is the sentinel, so a raw-parent
// frontier would still be holding both branch heads and would never stop.
func TestPostMergeEventDominatesAndStopsThePage(t *testing.T) {
	m := buildMergedChain(t)
	after1 := buildChain(t, m.s, m.merge, "after1", "2026-08-18T09:00:00.000", nil)
	after2 := buildChain(t, m.s, after1, "after2", "2026-08-18T10:00:00.000", nil)
	setRef(t, m.s, after2)

	// after1 dominates both branch heads and is NOT the tip, so stopping
	// there is distinguishable from the exhaustion clause: a raw-DAG
	// frontier (whose only parent for after1 is the uncontracted sentinel)
	// would still hold a3 and b3, find no C, and run on to after2.
	ids, cur := sinceIDs(t, m.dir, m.id(m.root), "--limit", "2", "--ledger", "demo")
	if len(ids) != 7 || ids[len(ids)-1] != after1[:10] { // both branches (6) + after1; root is the cursor
		t.Fatalf("page must stop at the first dominating event after1: %v", ids)
	}
	if cur != after1[:10] {
		t.Fatalf("emitted cursor must be after1 %s, not the tip %s: got %s", after1[:10], after2[:10], cur)
	}
	rest, cur2 := sinceIDs(t, m.dir, cur, "--limit", "2", "--ledger", "demo")
	if len(rest) != 1 || rest[0] != after2[:10] || cur2 != after2[:10] {
		t.Fatalf("the remainder is exactly after2, cursor at the tip: %v %s", rest, cur2)
	}
}

// (c) The branch-local-cursor oscillation. A consumer whose cursor sits on
// one branch must never be re-delivered its own branch: nothing in the other
// branch descends from that cursor, so no C satisfies condition (i) and the
// page runs to exhaustion and emits the tip.
func TestBranchLocalCursorNeverRedeliversItsOwnBranch(t *testing.T) {
	m := buildMergedChain(t)
	ids, cur := sinceIDs(t, m.dir, m.id(m.a[2]), "--limit", "1", "--ledger", "demo")
	if len(ids) != 3 {
		t.Fatalf("branch-local cursor must deliver the other branch whole: %v", ids)
	}
	for _, id := range ids {
		for _, a := range m.aIDs {
			if id == a {
				t.Fatalf("re-delivered an own-branch event %s: %v", id, ids)
			}
		}
	}
	for _, want := range m.bIDs {
		if !model.Contains(ids, want) {
			t.Fatalf("missing other-branch event %s: %v", want, ids)
		}
	}
	if cur != m.id(m.merge) {
		t.Fatalf("exhausted page must emit the tip %s, got %s", m.id(m.merge), cur)
	}
}

// (d) Linear history: the pager stops at exactly --limit, emitting the last
// delivered event (which dominates the page and descends from the cursor),
// and the pages partition the unpaged drain.
func TestLinearPagingStopsAtLimitAndPartitionsTheDrain(t *testing.T) {
	dir := seed(t)
	drain, drainCur := sinceIDs(t, dir, "--ledger", "demo")
	if len(drain) != 6 {
		t.Fatalf("seeded chain drain: %v", drain)
	}

	var paged []string
	cur := ""
	for pages := 0; ; pages++ {
		if pages > 12 {
			t.Fatalf("linear pager did not terminate: %v", paged)
		}
		args := []string{"--limit", "2", "--ledger", "demo"}
		if cur != "" {
			args = append([]string{cur}, args...)
		}
		ids, next := sinceIDs(t, dir, args...)
		if len(ids) > 0 {
			if len(ids) != 2 {
				t.Fatalf("linear pages are exactly --limit: %v", ids)
			}
			if next != ids[len(ids)-1] {
				t.Fatalf("linear page cursor must be the last delivered event: %s vs %v", next, ids)
			}
			paged = append(paged, ids...)
		}
		if next == cur {
			break
		}
		cur = next
	}
	if strings.Join(paged, ",") != strings.Join(drain, ",") {
		t.Fatalf("paged pages must partition the drain: %v vs %v", paged, drain)
	}
	if cur != drainCur {
		t.Fatalf("paged end cursor %s must equal the unpaged drain cursor %s", cur, drainCur)
	}
}

// (e) Cursor validity is reachability, never fold position: the fold HEAD of
// a merged chain is a valid cursor and re-delivers the other branch (the
// documented consequence — those events sort fold-below it but are not its
// ancestors). A foreign sha is reset_required.
func TestFoldHeadCursorIsValidAndForeignShaResets(t *testing.T) {
	m := buildMergedChain(t)
	drain, _ := sinceIDs(t, m.dir, m.id(m.root), "--ledger", "demo")
	foldHead := drain[len(drain)-1]
	if foldHead != m.bIDs[2] {
		t.Fatalf("fixture assumption: fold head should be b3, got %s (%v)", foldHead, drain)
	}
	ids, cur := sinceIDs(t, m.dir, foldHead, "--ledger", "demo")
	if strings.Join(ids, ",") != strings.Join(m.aIDs, ",") {
		t.Fatalf("a fold-head cursor re-delivers the other branch (documented): %v want %v", ids, m.aIDs)
	}
	if cur != m.id(m.merge) {
		t.Fatalf("unpaged drain emits the tip: %s want %s", cur, m.id(m.merge))
	}

	_, se, code := run(t, m.dir, "since", "ffffffffff", "--ledger", "demo")
	if code != 4 || !strings.Contains(se, "reset_required") {
		t.Fatalf("a foreign sha must be reset_required: %d %s", code, se)
	}
	// a real commit that is not in this ledger's history is equally unreachable
	other, _, _ := m.s.Repo.Git("", "rev-parse", "HEAD")
	if other != "" {
		_, se, code = run(t, m.dir, "since", other[:10], "--ledger", "demo")
		if code != 4 || !strings.Contains(se, "reset_required") {
			t.Fatalf("an off-ledger commit must be reset_required: %d %s", code, se)
		}
	}
}

// (f) Batch order is the Kahn fold on the RANGE's contracted sub-DAG, not the
// global fold restricted to the range. a2 carries a timestamp earlier than
// its own out-of-range parent a1: globally ancestry pins it after b1, but
// inside the range a1 is gone and a2's timestamp wins.
func TestBatchOrderIsRangeLocalNotGlobalRestricted(t *testing.T) {
	dir := initRepo(t)
	s := store.Store{Repo: gitx.Repo{Dir: dir}}
	root := buildChain(t, s, "", "root", "2026-08-17T10:00:00.000", map[string]string{"meta.json": chainMeta})
	a1 := buildChain(t, s, root, "a1", "2026-08-17T10:50:00.000", nil)
	a2 := buildChain(t, s, a1, "a2", "2026-08-17T10:11:00.000", nil) // skewed: earlier than its parent
	b1 := buildChain(t, s, root, "b1", "2026-08-17T10:20:00.000", nil)
	merge := buildSentinel(t, s, "2026-08-17T23:00:00.000", a2, b1)
	setRef(t, s, merge)

	global, _ := sinceIDs(t, dir, "--ledger", "demo")
	want := []string{root[:10], b1[:10], a1[:10], a2[:10]}
	if strings.Join(global, ",") != strings.Join(want, ",") {
		t.Fatalf("global fold order = %v, want %v", global, want)
	}

	ranged, cur := sinceIDs(t, dir, a1[:10], "--ledger", "demo")
	wantRange := []string{a2[:10], b1[:10]} // range-local: a2's ts beats b1's, a1 is out of range
	if strings.Join(ranged, ",") != strings.Join(wantRange, ",") {
		t.Fatalf("range batch order = %v, want the range-local Kahn order %v", ranged, wantRange)
	}
	if cur != merge[:10] {
		t.Fatalf("unpaged drain emits the tip: %s want %s", cur, merge[:10])
	}
}

func TestSincePagingAndReset(t *testing.T) {
	dir := seed(t) // 7+ events
	so, _, _ := run(t, dir, "since", "--limit", "2")
	doc := mustJSON(t, so)
	if int(doc["count"].(float64)) != 2 {
		t.Fatalf("limit: %v", doc)
	}
	cur := doc["cursor"].(string)
	so, _, _ = run(t, dir, "since", cur)
	doc2 := mustJSON(t, so)
	if int(doc2["count"].(float64)) < 1 {
		t.Fatal("paging must resume after cursor")
	}
	_, se, code := run(t, dir, "since", "ffffffffff")
	if code != 4 || !strings.Contains(se, "reset_required") {
		t.Fatalf("%s", se)
	}
}

// TestSinceResetHintDropsRedrainClause: the old hint told the caller a
// cursorless `since` "re-drains from the start", which isn't true of
// since (it has no state) and conflicts with quickstart rule 6's recovery
// advice (status + tail, never a full re-drain).
func TestSinceResetHintDropsRedrainClause(t *testing.T) {
	dir := seed(t)
	_, se, code := run(t, dir, "since", "ffffffffff")
	if code != 4 || !strings.Contains(se, "reset_required") {
		t.Fatalf("%s", se)
	}
	if strings.Contains(se, "re-drains from the start") {
		t.Fatalf("hint must drop the re-drains clause: %s", se)
	}
	if !strings.Contains(se, "chit tail -n 50 shows recent events") {
		t.Fatalf("hint must point at tail -n 50: %s", se)
	}
}

func TestWatchDrainAndTimeout(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "tail", "-n", "1")
	first := mustJSON(t, so)["events"].([]any)[0].(map[string]any)["id"].(string)
	_ = first
	// drain: watch from the very first event id
	so, _, _ = run(t, dir, "since", "--limit", "1")
	c0 := mustJSON(t, so)["cursor"].(string)
	so, _, code := run(t, dir, "watch", "--since", c0, "--timeout", "5")
	if code != 0 {
		t.Fatalf("drain should match existing sets: %s", so)
	}
	doc := mustJSON(t, so)
	if doc["cursor"] == nil || len(doc["events"].([]any)) == 0 {
		t.Fatalf("watch payload: %v", doc)
	}
	// timeout with cursor intact
	head := doc["cursor"].(string)
	start := time.Now()
	so, _, code = run(t, dir, "watch", "--since", head, "--timeout", "1")
	if code != 2 || time.Since(start) < time.Second {
		t.Fatalf("timeout contract: code=%d", code)
	}
	doc = mustJSON(t, so)
	if doc["timeout"] != true || doc["cursor"] != head {
		t.Fatalf("timeout payload: %v", doc)
	}
}

func TestWatchCursorlessEmitsStart(t *testing.T) {
	dir := seed(t)
	so, _, code := run(t, dir, "watch", "--timeout", "1")
	if code != 2 {
		t.Fatal("no events after head: timeout expected")
	}
	if mustJSON(t, so)["starting_cursor"] == nil {
		t.Fatal("cursorless watch must emit its starting cursor (cold-start rule)")
	}
}

// TestWatchFollowCursorlessEmitsStartLine: --follow's per-event JSON stream
// has no enclosing envelope, so the cold-start announcement (the head a
// crashed/killed follow consumer must resume from) can't ride the final
// drain/timeout payload the way it does in non-follow watch — it needs its
// own leading line. --follow itself loops forever and isn't unit-testable
// (no process control here), so this exercises the pre-loop setup
// (resolveStartCursor) directly with follow=true and a non-TTY Ctx, which
// is the exact call runWatch makes before entering the stream loop.
func TestWatchFollowCursorlessEmitsStartLine(t *testing.T) {
	dir := seed(t)
	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: false, Stdout: &buf, Stderr: &buf}
	led, err := c.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	cur, start, err := resolveStartCursor(c, led, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if start["starting_cursor"] != cur {
		t.Fatalf("returned start map must carry the resolved cursor: %v (cur=%s)", start, cur)
	}
	doc := mustJSON(t, buf.String())
	if doc["starting_cursor"] != cur {
		t.Fatalf("follow's leading JSON line must carry starting_cursor so a killed consumer can resume: %q", buf.String())
	}
}

func TestWatchValueFilter(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "since", "--limit", "1")
	c0 := mustJSON(t, so)["cursor"].(string)
	so, _, _ = run(t, dir, "watch", "--since", c0, "--value", "done,failed", "--timeout", "5")
	for _, e := range mustJSON(t, so)["events"].([]any) {
		vals := e.(map[string]any)["fields"].(map[string]any)
		found := false
		for _, v := range vals {
			if v == "done" || v == "failed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("filter leak: %v", e)
		}
	}
}

// TestFollowDocIncludesKindAndTextForNotes: --follow's per-event stream
// previously reduced a note to {id,key,fields:null,by,ts} — indistinguishable
// from a set with no fields. Note events must additionally carry kind and a
// text preview, truncated to 200 runes; set events must carry neither.
func TestFollowDocIncludesKindAndTextForNotes(t *testing.T) {
	ev := model.Event{ID: "abc123", Type: "note", Kind: "gotcha", Key: "t1", Author: "x",
		TS: "2026-08-13T00:00:00", Text: strings.Repeat("a", 250)}
	doc := followDoc(ev)
	if doc["kind"] != "gotcha" {
		t.Fatalf("follow doc must carry kind for note events: %v", doc)
	}
	text, _ := doc["text"].(string)
	if r := []rune(text); len(r) != 203 { // 200 runes + "..." marker
		t.Fatalf("follow doc text must be truncated to 200 runes: %d runes: %q", len(r), text)
	}
	if !strings.HasSuffix(text, "...") {
		t.Fatalf("truncated text must carry the ellipsis marker: %q", text)
	}

	setEv := model.Event{ID: "def456", Type: "set", Key: "t1", Author: "x", TS: "2026-08-13T00:00:00",
		Fields: map[string]string{"status": "open"}}
	setDoc := followDoc(setEv)
	if _, ok := setDoc["kind"]; ok {
		t.Fatalf("set events must not carry kind: %v", setDoc)
	}
	if _, ok := setDoc["text"]; ok {
		t.Fatalf("set events must not carry text: %v", setDoc)
	}
}
