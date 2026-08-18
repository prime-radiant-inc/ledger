package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ledger/internal/gitx"
	"ledger/internal/store"
)

// ttyCtx builds a Ctx that renders as if attached to a terminal — run()
// never sets TTY, so a TTY-render assertion invokes the verb's runX
// directly, the same bypass read_test.go's TTY tests use.
func ttyCtx(dir string) (*Ctx, *bytes.Buffer) {
	var buf bytes.Buffer
	return &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}, &buf
}

// contestedReplicas builds a real two-replica partition race against real
// git: both sides seed from the same board, then claim task-1 concurrently,
// then replica b merges a's side in. Returns the two replica dirs and the
// pre-merge ref of b (so a caller can build the mirror-image merge on a).
func contestedReplicas(t *testing.T) (a, b, bPreMerge string) {
	t.Helper()
	_, a, b = twoReplicas(t)
	seedBoard(t, a, "board")
	mustRun(t, a, "set", "task-1", "status=open", "--expect", "none", "-m", "the task", "--as", "seeder")
	pushLedgerRef(t, a, "board")
	mustRun(t, b, "sync")

	// The partition: both replicas claim the same open key off the same
	// status event, neither having seen the other.
	openID := statusID(t, a, "board", "task-1")
	mustRun(t, a, "set", "task-1", "status=in-progress", "--expect", openID, "-m", "alice claims", "--as", "alice")
	mustRun(t, b, "set", "task-1", "status=in-progress", "--expect", openID, "-m", "bob claims", "--as", "bob")

	bPreMerge = git(t, b, "rev-parse", "refs/ledger/board")
	pushLedgerRef(t, a, "board")
	mustRun(t, b, "sync")
	return a, b, bPreMerge
}

// contestedEntries pulls the contested attention entries out of a `ready`
// payload.
func contestedEntries(t *testing.T, payload string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range mustJSON(t, payload)["attention"].([]any) {
		m := e.(map[string]any)
		if m["reason"] == "contested" {
			out = append(out, m)
		}
	}
	return out
}

// TestContestedSurfacesAfterMerge is the whole feature end to end against
// real git: two replicas race a claim, sync merges them, and `ready` on the
// merged replica flags the race exactly once — with a valid CAS ticket, an
// unchanged list placement, and the flag riding on that placement.
func TestContestedSurfacesAfterMerge(t *testing.T) {
	_, b, _ := contestedReplicas(t)

	so := mustRun(t, b, "ready", "--ledger", "board")
	entries := contestedEntries(t, so)
	if len(entries) != 1 {
		t.Fatalf("want exactly one contested entry, got %d:\n%s", len(entries), so)
	}
	e := entries[0]
	if e["key"] != "task-1" || e["title"] != "the task" {
		t.Fatalf("contested entry key/title: %+v", e)
	}
	c, ok := e["contest"].(map[string]any)
	if !ok {
		t.Fatalf("contested entry must carry the nested contest ticket: %+v", e)
	}
	if c["field"] != "status" || c["human"] != false {
		t.Fatalf("contest ticket: %+v", c)
	}
	ids := c["ids"].([]any)
	authors := c["authors"].([]any)
	if len(ids) != 2 || len(authors) != 2 {
		t.Fatalf("want two racing heads: %+v", c)
	}
	if c["expect"] != ids[1] {
		t.Fatalf("expect must be the fold-order-last head: %+v", c)
	}
	// The ticket's expect IS the field's latest event — a valid CAS ticket
	// by construction, which is the property the whole definition buys.
	if want := statusID(t, b, "board", "task-1"); c["expect"] != want {
		t.Fatalf("expect = %v, want the field's latest event %s", c["expect"], want)
	}

	// Membership is unchanged: the key is still held (claimed), and the
	// held entry carries the flag.
	held := mustJSON(t, so)["held"].([]any)
	if len(held) != 1 {
		t.Fatalf("the contested key keeps its ordinary placement: %v", held)
	}
	if h := held[0].(map[string]any); h["key"] != "task-1" || h["contested"] != true {
		t.Fatalf("held entry must carry contested:true: %+v", h)
	}
}

// TestContestedCollapseRecordsAndEchoes is the durable record, both halves:
// the collapsing write records `contested_resolved` as a JSON ARRAY of the
// losing head ids on its own event, the `set` response ECHOES it, the TTY
// event render labels it, and the contest is gone from the next `ready` —
// the definition clearing itself, no separate rule.
func TestContestedCollapseRecordsAndEchoes(t *testing.T) {
	_, b, _ := contestedReplicas(t)

	entries := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board"))
	c := entries[0]["contest"].(map[string]any)
	expect := c["expect"].(string)
	ids := c["ids"].([]any)
	loser := ids[0].(string)

	// Resolving a live claim by another author trips rule 5's claim signal,
	// so the resolution path is --expect <the ticket's expect> --override
	// with the message naming the contest.
	so := mustRun(t, b, "set", "task-1", "status=in-progress", "--expect", expect, "--override",
		"-m", "resolving the contested claim: bob keeps it, alice's side folded in", "--as", "bob")
	doc := mustJSON(t, so)
	got, ok := doc["contested_resolved"].([]any)
	if !ok {
		t.Fatalf("the set response must ECHO contested_resolved: %s", so)
	}
	if len(got) != 1 || got[0] != loser {
		t.Fatalf("contested_resolved must be the ARRAY of losing head ids (want [%s]): %v", loser, got)
	}

	// Durable on the event itself, greppable forever.
	raw := mustRun(t, b, "tail", "--raw", "-n", "5", "--ledger", "board")
	if !strings.Contains(raw, `"contested_resolved"`) || !strings.Contains(raw, loser) {
		t.Fatalf("the chain must carry contested_resolved on the collapsing event:\n%s", raw)
	}
	var chain map[string]any
	if err := json.Unmarshal([]byte(raw), &chain); err != nil {
		t.Fatal(err)
	}
	events := chain["events"].([]any)
	last := events[len(events)-1].(map[string]any)
	if last["id"] != doc["id"] {
		t.Fatalf("the collapsing write must be the chain's last event: %+v", last)
	}
	arr, ok := last["contested_resolved"].([]any)
	if !ok || len(arr) != 1 || arr[0] != loser {
		t.Fatalf("contested_resolved on the event must be a JSON array of losers: %+v", last["contested_resolved"])
	}

	// And the contest is gone: any write to the field collapses the heads.
	if e := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board")); len(e) != 0 {
		t.Fatalf("the collapsing write must clear the contest: %+v", e)
	}
}

// TestContestedResolutionMarkerOnTTY: the record is labeled on a TTY, not
// left JSON-only — the same mandatory-labeling class as `override:`. Both
// the write's own response line and the chain render carry it.
func TestContestedResolutionMarkerOnTTY(t *testing.T) {
	_, b, _ := contestedReplicas(t)
	entries := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board"))
	c := entries[0]["contest"].(map[string]any)
	loser := c["ids"].([]any)[0].(string)

	ctx, buf := ttyCtx(b)
	opts := writeOpts{ledger: "board", as: "bob", m: "resolving the contested claim"}
	if err := runSet(ctx, "task-1", []string{"status=in-progress"}, opts, c["expect"].(string), true, true); err != nil {
		t.Fatalf("collapse must land: %v", err)
	}
	if !strings.Contains(buf.String(), "contested_resolved: "+loser) {
		t.Fatalf("the write's own TTY line must label the resolution:\n%s", buf.String())
	}

	ctx, buf = ttyCtx(b)
	if err := runTail(ctx, 5, true, "", "board"); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(buf.String(), "contested_resolved: "+loser) {
		t.Fatalf("the TTY event render must show the resolution marker:\n%s", buf.String())
	}
}

// TestUnwittingTouchBaseResolves is the spec's named case: a routine
// touch-base that happens to descend from both heads resolves the contest
// and records it, whether the writer knew of the contest or not. bob's own
// claim is not a signal against bob, so this write needs no --override at
// all — it is exactly the unwitting path.
func TestUnwittingTouchBaseResolves(t *testing.T) {
	_, b, _ := contestedReplicas(t)
	entries := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board"))
	c := entries[0]["contest"].(map[string]any)

	so := mustRun(t, b, "set", "task-1", "status=in-progress", "--expect", c["expect"].(string),
		"-m", "still on it", "--as", "bob")
	got, ok := mustJSON(t, so)["contested_resolved"].([]any)
	if !ok || len(got) != 1 || got[0] != c["ids"].([]any)[0] {
		t.Fatalf("an unwitting touch-base still records the resolution: %s", so)
	}
}

// TestSameValueCloseRaceGoesThroughTheSettledGate is the spec's stated
// trial finding: two replicas closing the same key with the SAME terminal
// value still contest (no same-value auto-clear), and resolving it
// re-asserts a settled value — so the corrective write trips
// `needs_override` on the `settled` signal, and the resolution path is
// `--expect <the ticket's expect> --override` with the message doing double
// duty as the resolution note.
func TestSameValueCloseRaceGoesThroughTheSettledGate(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoard(t, a, "board")
	mustRun(t, a, "set", "task-1", "status=open", "--expect", "none", "-m", "the task", "--as", "seeder")
	pushLedgerRef(t, a, "board")
	mustRun(t, b, "sync")

	openID := statusID(t, a, "board", "task-1")
	mustRun(t, a, "set", "task-1", "status=done", "--expect", openID, "-m", "alice closes", "--as", "alice")
	mustRun(t, b, "set", "task-1", "status=done", "--expect", openID, "-m", "bob closes", "--as", "bob")
	pushLedgerRef(t, a, "board")
	mustRun(t, b, "sync")

	entries := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board"))
	if len(entries) != 1 {
		t.Fatalf("two concurrent closes carrying the SAME value must still contest: %+v", entries)
	}
	c := entries[0]["contest"].(map[string]any)
	expect := c["expect"].(string)

	// Bare, no --override: the settled gate stops it.
	_, se, code := run(t, b, "set", "task-1", "status=done", "--expect", expect,
		"-m", "collapsing", "--as", "carol", "--ledger", "board")
	if code != 4 || !strings.Contains(se, "needs_override") || !strings.Contains(se, "settled") {
		t.Fatalf("resolving a contested terminal write must trip the settled gate: %d %s", code, se)
	}

	// The stated resolution path, message doing double duty.
	so := mustRun(t, b, "set", "task-1", "status=done", "--expect", expect, "--override",
		"-m", "resolving contested close: both sides closed done, folding alice's in", "--as", "carol", "--ledger", "board")
	doc := mustJSON(t, so)
	got, ok := doc["contested_resolved"].([]any)
	if !ok || len(got) != 1 || got[0] != c["ids"].([]any)[0] {
		t.Fatalf("the resolution must be recorded and echoed: %s", so)
	}
	if e := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board")); len(e) != 0 {
		t.Fatalf("the contest must be gone: %+v", e)
	}
}

// TestUncontestedWriteRecordsNothing: the overwhelmingly common case must
// stay silent — no key, no marker, no noise.
func TestUncontestedWriteRecordsNothing(t *testing.T) {
	dir := setupReady(t)
	so := mustRun(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "a task", "--as", "a")
	if _, present := mustJSON(t, so)["contested_resolved"]; present {
		t.Fatalf("an uncontested write must not carry contested_resolved: %s", so)
	}
	raw := mustRun(t, dir, "tail", "--raw", "--ledger", "issues")
	if strings.Contains(raw, "contested_resolved") {
		t.Fatalf("nothing to record, nothing recorded:\n%s", raw)
	}
}

// TestContestedEntriesByteIdenticalAcrossMergeOrders is the replica
// convergence property against real git: replica a merged b's side into its
// own, replica b merged a's into its own. The two hold DIFFERENT commit
// graphs — different sentinel commits, opposite merge-parent order — over
// the same chain, and the entries they render must be byte-identical.
func TestContestedEntriesByteIdenticalAcrossMergeOrders(t *testing.T) {
	a, b, bPreMerge := contestedReplicas(t)

	// b has already merged a's side. Republish b's PRE-merge ref so a builds
	// the mirror-image merge instead of fast-forwarding onto b's sentinel.
	git(t, b, "push", "-q", "-f", "origin", bPreMerge+":refs/ledger/board")
	mustRun(t, a, "sync")

	if git(t, a, "rev-parse", "refs/ledger/board") == git(t, b, "rev-parse", "refs/ledger/board") {
		t.Fatal("the two replicas must hold different commit graphs for this test to mean anything")
	}
	for _, dir := range []string{a, b} {
		if n := mergeCount(t, dir, "board"); n != 1 {
			t.Fatalf("%s: want exactly one sentinel merge, got %d", dir, n)
		}
	}

	// Compare the attention list, which is where the contested entries live
	// and which carries no wall-clock ages (held entries do, and two
	// invocations seconds apart legitimately differ there).
	attentionJSON := func(dir string) string {
		raw, err := json.Marshal(mustJSON(t, mustRun(t, dir, "ready", "--ledger", "board"))["attention"])
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	ja, jb := attentionJSON(a), attentionJSON(b)
	if ja != jb {
		t.Fatalf("replicas that merged in different orders must render identical entries:\nA: %s\nB: %s", ja, jb)
	}
	if !strings.Contains(ja, `"reason":"contested"`) {
		t.Fatalf("the fixture must actually be contested: %s", ja)
	}
}

// TestContestedResolvedResetsPerCASAttempt is the override-carryover lesson
// applied to the second tool-computed field on the event: *ev is reused
// across every CAS retry, so a losing attempt that computed a resolution
// must never leave it stuck on a later, winning attempt whose own fresh
// read found nothing to resolve. The collapse below lands on attempt 1 and
// is then re-run against a chain that already holds it.
func TestContestedResolvedResetsPerCASAttempt(t *testing.T) {
	_, b, _ := contestedReplicas(t)
	entries := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board"))
	c := entries[0]["contest"].(map[string]any)

	// First write collapses the contest and records it.
	first := mustRun(t, b, "set", "task-1", "status=in-progress", "--expect", c["expect"].(string),
		"-m", "collapsing", "--as", "bob")
	firstID := mustJSON(t, first)["id"].(string)

	// Second write, against the post-collapse state: there is nothing left
	// to resolve, so the record must be absent — not inherited.
	second := mustRun(t, b, "set", "task-1", "status=done", "--expect", firstID,
		"--override", "-m", "done", "--as", "bob")
	if _, present := mustJSON(t, second)["contested_resolved"]; present {
		t.Fatalf("a write with nothing to resolve must record nothing: %s", second)
	}
	raw := mustRun(t, b, "tail", "--raw", "-n", "2", "--ledger", "board")
	var chain map[string]any
	if err := json.Unmarshal([]byte(raw), &chain); err != nil {
		t.Fatal(err)
	}
	events := chain["events"].([]any)
	last := events[len(events)-1].(map[string]any)
	if _, present := last["contested_resolved"]; present {
		t.Fatalf("the second event must carry no resolution: %+v", last)
	}
}

// TestContestedTTYLines: the envelope's TTY render names the contest with a
// paste-able ticket and marks the contested key's own list line — the race
// has to be visible right where the entry is read and acted on.
func TestContestedTTYLines(t *testing.T) {
	_, b, _ := contestedReplicas(t)
	ctx, buf := ttyCtx(b)
	if err := runReady(ctx, "board", nil, 50); err != nil {
		t.Fatalf("ready: %v", err)
	}
	so := buf.String()
	if !strings.Contains(so, "attn     contested") {
		t.Fatalf("the attention list must render the contested entry:\n%s", so)
	}
	if !strings.Contains(so, "field=status") || !strings.Contains(so, "expect=") {
		t.Fatalf("the contested line must carry the ticket:\n%s", so)
	}
	held := ""
	for _, l := range strings.Split(so, "\n") {
		if strings.Contains(l, "held") && strings.Contains(l, "task-1") {
			held = l
		}
	}
	if !strings.Contains(held, "contested") {
		t.Fatalf("the contested key's held line must carry the flag: %q", held)
	}
}
