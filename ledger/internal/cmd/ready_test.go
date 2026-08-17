package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"ledger/internal/gitx"
	"ledger/internal/store"
)

// seedReadyEnvelope builds a ready-capable board (setupReady's shape, no
// --stale-after — nothing in it must ever go stale) mirroring the spec's
// own pinned `ready` example: one entry for each list, plus the statusless
// attention reason.
//
//   - spike-probe: status=wontfix, no evidence — fix-retry's blocker.
//   - fix-retry:   status=open, blocked-by=spike-probe → ready, annotated
//     unblocked_without_evidence (spike-probe's terminal event has none).
//   - dep-x:       status=open, no edges → also ready (an unblocked leaf).
//   - sign-off:    labels=human, status=open, unclaimed → held/human.
//   - big-task:    status=in-progress (claim by worker-2), blocked-by=dep-x
//     → held/claim, claimed-but-blocked, waiting_on=[{dep-x, open}].
//   - deploy:      status=open, blocked-by=sign-off (human, non-terminal)
//     → blocked, waiting_on=[{sign-off, human}].
//   - half-seeded: touched only via labels, no status write → attention/
//     statusless.
//
// Staleness (attention/stale-claim, and the frontier's stale-driven
// work-available arm) is exercised separately by
// TestReadyAttentionStaleClaimAndFrontier — real wall-clock timing doesn't
// mix well with this board's several-seconds-to-seed CLI setup, so keeping
// it in its own minimal board avoids every claim here going stale by the
// time `ready` finally runs.
func seedReadyEnvelope(t *testing.T) string {
	dir := setupReady(t)
	run(t, dir, "set", "spike-probe", "status=wontfix", "--expect", "none", "-m", "not doing it", "--as", "a")

	run(t, dir, "set", "fix-retry", "blocked-by=spike-probe", "--expect", "none", "--as", "a")
	run(t, dir, "set", "fix-retry", "status=open", "--expect", "none", "-m", "fix the retry loop", "--as", "a")

	run(t, dir, "set", "dep-x", "status=open", "--expect", "none", "-m", "a dependency", "--as", "a")

	run(t, dir, "set", "sign-off", "labels=human", "--as", "a")
	run(t, dir, "set", "sign-off", "status=open", "--expect", "none", "--override", "-m", "needs a human sign-off", "--as", "a")

	run(t, dir, "set", "big-task", "blocked-by=dep-x", "--expect", "none", "--as", "a")
	so, _, code := run(t, dir, "set", "big-task", "status=open", "--expect", "none", "-m", "big task title", "--as", "seeder")
	if code != 0 {
		t.Fatal(so)
	}
	run(t, dir, "set", "big-task", "status=in-progress", "--expect", mustEventID(t, dir, "big-task"), "-m", "claiming", "--as", "worker-2")

	run(t, dir, "set", "deploy", "blocked-by=sign-off", "--expect", "none", "--as", "a")
	run(t, dir, "set", "deploy", "status=open", "--expect", "none", "-m", "ship it", "--as", "a")

	run(t, dir, "set", "half-seeded", "labels=urgent", "--as", "a")
	return dir
}

// entryByKey finds the entry whose "key" field matches k inside a list
// decoded from ready's JSON payload.
func entryByKey(list []any, k string) map[string]any {
	for _, e := range list {
		m := e.(map[string]any)
		if m["key"] == k {
			return m
		}
	}
	return nil
}

// keysOf collects every "key" field from a decoded list.
func keysOf(list []any) []string {
	ks := make([]string, 0, len(list))
	for _, e := range list {
		ks = append(ks, e.(map[string]any)["key"].(string))
	}
	return ks
}

// TestReadyEnvelopeMembersAndTitles: the full envelope shape against the
// trial-shaped board — every list's membership, titles, and the annotation/
// waiting_on fields the spec's pinned example illustrates.
func TestReadyEnvelopeMembersAndTitles(t *testing.T) {
	dir := seedReadyEnvelope(t)
	so, se, code := run(t, dir, "ready")
	if code != 0 {
		t.Fatalf("ready: %d %s", code, se)
	}
	doc := mustJSON(t, so)

	if doc["ledger"] != "issues" {
		t.Fatalf("ledger: got %v", doc["ledger"])
	}
	if doc["ok"] != true {
		t.Fatalf("ok: got %v", doc["ok"])
	}
	if doc["frontier"] != "work-available" {
		t.Fatalf("frontier: got %v (non-empty ready must drive work-available)", doc["frontier"])
	}

	ready := doc["ready"].([]any)
	if got := keysOf(ready); len(got) != 2 || !contains2(got, "fix-retry") || !contains2(got, "dep-x") {
		t.Fatalf("ready members: got %v want [fix-retry dep-x]", got)
	}
	fr := entryByKey(ready, "fix-retry")
	if fr["title"] != "fix the retry loop" {
		t.Fatalf("fix-retry title: got %v", fr["title"])
	}
	uwe := fr["unblocked_without_evidence"].([]any)
	if len(uwe) != 1 || uwe[0] != "spike-probe" {
		t.Fatalf("fix-retry unblocked_without_evidence: got %v", uwe)
	}

	held := doc["held"].([]any)
	if got := keysOf(held); len(got) != 2 || !contains2(got, "big-task") || !contains2(got, "sign-off") {
		t.Fatalf("held members: got %v want [big-task sign-off]", got)
	}
	bt := entryByKey(held, "big-task")
	if bt["title"] != "big task title" || bt["kind"] != "claim" || bt["by"] != "worker-2" {
		t.Fatalf("big-task held entry: %+v", bt)
	}
	btWO := bt["waiting_on"].([]any)[0].(map[string]any)
	if btWO["key"] != "dep-x" || btWO["state"] != "open" {
		t.Fatalf("big-task waiting_on: %v", bt["waiting_on"])
	}
	so2 := entryByKey(held, "sign-off")
	if so2["title"] != "needs a human sign-off" || so2["kind"] != "human" || so2["status"] != "open" {
		t.Fatalf("sign-off held entry: %+v", so2)
	}

	blocked := doc["blocked"].([]any)
	if got := keysOf(blocked); len(got) != 1 || got[0] != "deploy" {
		t.Fatalf("blocked members: got %v want [deploy]", got)
	}
	dep := entryByKey(blocked, "deploy")
	if dep["title"] != "ship it" {
		t.Fatalf("deploy title: got %v", dep["title"])
	}
	depWO := dep["waiting_on"].([]any)[0].(map[string]any)
	if depWO["key"] != "sign-off" || depWO["state"] != "human" {
		t.Fatalf("deploy waiting_on: %v", dep["waiting_on"])
	}

	attention := doc["attention"].([]any)
	statusless := entryByKey(attention, "half-seeded")
	if statusless["reason"] != "statusless" {
		t.Fatalf("half-seeded attention entry: %+v", statusless)
	}
	if _, hasTitle := statusless["title"]; hasTitle {
		t.Fatalf("statusless entry must not carry a title: %+v", statusless)
	}

	totals := doc["totals"].(map[string]any)
	if totals["ready"] != float64(2) || totals["held"] != float64(2) ||
		totals["blocked"] != float64(1) || totals["attention"] != float64(1) {
		t.Fatalf("totals: %+v", totals)
	}
}

// TestReadyWhereFilterLegitimatelyEmptiesAllLists: --where status=closed
// matches none of the seeded keys (none is closed) — every list empties,
// exit 0, no error. Mirrors the spec's own example clause. The fixture adds
// its own cycle (cycle-a <-> cycle-b, both status=open) on top of
// seedReadyEnvelope's shape — without it, this test would pass for the
// wrong reason: an acyclic fixture can't prove a cycle attention entry
// composes with --where at all, only that ordinary reasons do. Neither
// cycle member is status=closed, so per the composition rule (a cycle
// entry survives iff ANY member matches) the entry must be excluded here
// too, keeping attention legitimately empty.
func TestReadyWhereFilterLegitimatelyEmptiesAllLists(t *testing.T) {
	dir := seedReadyEnvelope(t)
	// blocked-by existence validation rejects a forward reference to a key
	// that doesn't exist yet, so both keys must exist before either edge can
	// name the other.
	run(t, dir, "set", "cycle-a", "status=open", "--expect", "none", "-m", "cycle a", "--as", "a")
	run(t, dir, "set", "cycle-b", "status=open", "--expect", "none", "-m", "cycle b", "--as", "a")
	if _, se, code := run(t, dir, "set", "cycle-a", "blocked-by=cycle-b", "--expect", "none", "--as", "a"); code != 0 {
		t.Fatalf("cycle-a blocked-by=cycle-b: %s", se)
	}
	if _, se, code := run(t, dir, "set", "cycle-b", "blocked-by=cycle-a", "--expect", "none", "--as", "a"); code != 0 {
		t.Fatalf("cycle-b blocked-by=cycle-a: %s", se)
	}

	so, se, code := run(t, dir, "ready", "--where", "status=closed")
	if code != 0 {
		t.Fatalf("a filtering --where must not error: %d %s", code, se)
	}
	doc := mustJSON(t, so)
	for _, list := range []string{"ready", "held", "blocked", "attention"} {
		if got := doc[list].([]any); len(got) != 0 {
			t.Fatalf("%s must be legitimately empty under --where status=closed, got %v", list, got)
		}
	}
}

// TestReadyOnNonReadyCapableBoardBadUsage: a plain (non-ready-capable)
// board's `ready` is bad_usage, with the create-time fix in the hint.
func TestReadyOnNonReadyCapableBoardBadUsage(t *testing.T) {
	dir := setupPlainGuarded(t)
	_, se, code := run(t, dir, "ready")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_usage" {
		t.Fatalf("error: got %v", doc["error"])
	}
	hint := doc["hint"].(string)
	if !strings.Contains(hint, "create time") || !strings.Contains(hint, "--terminal") || !strings.Contains(hint, "--guard status") {
		t.Fatalf("hint must name the create-time fix: %q", hint)
	}
}

// TestReadyAttentionStaleClaimAndFrontier: a single stale claim, in its own
// minimal board so the tight --stale-after horizon can't be crossed by
// anything but the deliberate sleep — proves staleness flows end-to-end
// through the real wall clock (board.Envelope's board-level tests already
// cover the classification logic against an injected `now`). A stale claim
// on a non-human key also drives frontier to work-available (reclaimable).
func TestReadyAttentionStaleClaimAndFrontier(t *testing.T) {
	dir := setupReadyStale(t, "10ms")
	so, _, code := run(t, dir, "set", "orphaned-task", "status=open", "--expect", "none", "-m", "orphaned task", "--as", "seeder")
	if code != 0 {
		t.Fatal(so)
	}
	run(t, dir, "set", "orphaned-task", "status=in-progress", "--expect", mustEventID(t, dir, "orphaned-task"), "-m", "claiming", "--as", "dead-worker")
	time.Sleep(30 * time.Millisecond) // ages the claim past the 10ms horizon

	so2, se, code := run(t, dir, "ready")
	if code != 0 {
		t.Fatalf("ready: %d %s", code, se)
	}
	doc := mustJSON(t, so2)
	if doc["frontier"] != "work-available" {
		t.Fatalf("a stale claim on a non-human key must drive work-available, got %v", doc["frontier"])
	}
	attention := doc["attention"].([]any)
	staleClaim := entryByKey(attention, "orphaned-task")
	if staleClaim["reason"] != "stale-claim" || staleClaim["title"] != "orphaned task" || staleClaim["by"] != "dead-worker" {
		t.Fatalf("orphaned-task attention entry: %+v", staleClaim)
	}
}

// TestReadyTTYRendersListsAndStaleFlag: readyLines' TTY rendering, bypassing
// run() (which never sets TTY, per TestShowTTYNoteSummaryOneLine's own
// established pattern) to invoke runReady directly with an explicit TTY
// Ctx. Covers a ready entry, a held claim with stale:true, and the
// stale-claim/statusless attention reasons; the cycle reason's own TTY
// rendering (the "break:" suggestion line) isn't exercised by this
// fixture — see the rev-17 cycle-break tests below for that.
func TestReadyTTYRendersListsAndStaleFlag(t *testing.T) {
	dir := setupReadyStale(t, "10ms")
	run(t, dir, "set", "fix-retry", "status=open", "--expect", "none", "-m", "fix the retry loop", "--as", "alice")

	so, _, code := run(t, dir, "set", "big-task", "status=open", "--expect", "none", "-m", "big task title", "--as", "seeder")
	if code != 0 {
		t.Fatal(so)
	}
	run(t, dir, "set", "big-task", "status=in-progress", "--expect", mustEventID(t, dir, "big-task"), "-m", "claiming", "--as", "worker-2")

	run(t, dir, "set", "half-seeded", "labels=urgent", "--as", "a")

	time.Sleep(30 * time.Millisecond) // ages big-task's claim past the 10ms horizon

	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	if err := runReady(c, "", nil, 50); err != nil {
		t.Fatal(err)
	}
	rendered := buf.String()

	if !strings.Contains(rendered, "ready") || !strings.Contains(rendered, "fix-retry") || !strings.Contains(rendered, `"fix the retry loop"`) {
		t.Fatalf("TTY output must render the ready entry (key + title): %q", rendered)
	}
	if !strings.Contains(rendered, "held") || !strings.Contains(rendered, "big-task") ||
		!strings.Contains(rendered, "claim") || !strings.Contains(rendered, "stale=true") {
		t.Fatalf("TTY output must render the held claim with stale=true: %q", rendered)
	}
	if !strings.Contains(rendered, "attn") || !strings.Contains(rendered, "stale-claim") || !strings.Contains(rendered, "big-task") {
		t.Fatalf("TTY output must render a stale-claim attention line: %q", rendered)
	}
	if !strings.Contains(rendered, "statusless") || !strings.Contains(rendered, "half-seeded") {
		t.Fatalf("TTY output must render a statusless attention line: %q", rendered)
	}
}

// --- Rev 17: CLI-level self-service cycle breaking ---

// TestReadyTTYRendersCycleBreakLine: readyLines' cycle rendering must
// include the paste-ready "break: set ... --expect ..." suggestion, with
// --override appended when the break target is human-labeled.
func TestReadyTTYRendersCycleBreakLine(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "tty-cycle-p", "status=open", "--expect", "none", "-m", "tty cycle p", "--as", "a")
	run(t, dir, "set", "tty-cycle-q", "labels=human", "--expect", "none", "-m", "reserving", "--as", "a")
	run(t, dir, "set", "tty-cycle-q", "status=open", "--expect", "none", "--override", "-m", "tty cycle q -- reserved", "--as", "a")
	if _, se, code := run(t, dir, "set", "tty-cycle-p", "blocked-by=tty-cycle-q", "--expect", "none", "--as", "a"); code != 0 {
		t.Fatalf("tty-cycle-p blocked-by=tty-cycle-q: %s", se)
	}
	time.Sleep(5 * time.Millisecond)
	if _, se, code := run(t, dir, "set", "tty-cycle-q", "blocked-by=tty-cycle-p", "--expect", "none", "--override", "-m", "closing", "--as", "a"); code != 0 {
		t.Fatalf("tty-cycle-q blocked-by=tty-cycle-p: %s", se)
	}

	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	if err := runReady(c, "", nil, 50); err != nil {
		t.Fatal(err)
	}
	rendered := buf.String()
	if !strings.Contains(rendered, "cycle") || !strings.Contains(rendered, "tty-cycle-p") || !strings.Contains(rendered, "tty-cycle-q") {
		t.Fatalf("TTY output must render the cycle attention line: %q", rendered)
	}
	if !strings.Contains(rendered, "break: set tty-cycle-q blocked-by=\"\" --expect ") {
		t.Fatalf("TTY output must render the paste-ready break suggestion: %q", rendered)
	}
	// The rendered line must be pastable as-is: --override with no -m is
	// bad_usage by the tool's own rule, so the load-bearing message
	// convention must be present too.
	if !strings.Contains(rendered, `-m "breaking cycle [tty-cycle-p tty-cycle-q]: dropping tty-cycle-p"`) {
		t.Fatalf("TTY output must render the -m message naming the cycle and the dropped key: %q", rendered)
	}
	if !strings.Contains(rendered, "--override") {
		t.Fatalf("TTY output must append --override for a human-labeled break target: %q", rendered)
	}
}

// TestReadyAttentionCycleBreakObjectExecutesAndClears: a staged 2-cycle's
// ready output must carry the break object naming the youngest (closing)
// edge, and executing the suggested paste-ready fix through the real CLI
// must clear the cycle and recover the frontier on the next ready.
func TestReadyAttentionCycleBreakObjectExecutesAndClears(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "cycle-p", "status=open", "--expect", "none", "-m", "cycle p", "--as", "a")
	run(t, dir, "set", "cycle-q", "status=open", "--expect", "none", "-m", "cycle q", "--as", "a")
	if _, se, code := run(t, dir, "set", "cycle-p", "blocked-by=cycle-q", "--expect", "none", "--as", "a"); code != 0 {
		t.Fatalf("cycle-p blocked-by=cycle-q: %s", se)
	}
	time.Sleep(5 * time.Millisecond) // gives the closing edge a distinct, later timestamp
	if _, se, code := run(t, dir, "set", "cycle-q", "blocked-by=cycle-p", "--expect", "none", "--as", "a"); code != 0 {
		t.Fatalf("cycle-q blocked-by=cycle-p: %s", se)
	}

	so, se, code := run(t, dir, "ready")
	if code != 0 {
		t.Fatalf("ready: %d %s", code, se)
	}
	doc := mustJSON(t, so)
	if doc["frontier"] != "attention-needed" {
		t.Fatalf("a true cycle must drive attention-needed, got %v", doc["frontier"])
	}
	cycle := findCycleEntry(t, doc["attention"].([]any))
	brk := cycle["break"].(map[string]any)
	// cycle-q's edge write is the younger (closing) one — it's the
	// suggested break target.
	if brk["key"] != "cycle-q" || brk["drop"] != "cycle-p" || brk["keep"] != "" || brk["human"] != false {
		t.Fatalf("break object: %+v", brk)
	}
	expect, _ := brk["expect"].(string)
	if expect == "" {
		t.Fatalf("break.expect must be a real event id: %+v", brk)
	}

	// Execute the paste-ready fix.
	msg := "breaking cycle [cycle-p cycle-q]: dropping cycle-p"
	if _, se, code := run(t, dir, "set", "cycle-q", "blocked-by=", "--expect", expect, "-m", msg, "--as", "kit"); code != 0 {
		t.Fatalf("break write: %s", se)
	}

	so2, se2, code2 := run(t, dir, "ready")
	if code2 != 0 {
		t.Fatalf("ready after break: %d %s", code2, se2)
	}
	doc2 := mustJSON(t, so2)
	assertNoCycleEntry(t, doc2["attention"].([]any))
	if doc2["frontier"] != "work-available" {
		t.Fatalf("frontier must recover once the cycle is broken (cycle-q is now ready), got %v", doc2["frontier"])
	}
}

// TestReadyAttentionCycleBreakHumanVariantRequiresOverride: a cycle whose
// suggested break target is human-labeled needs --override to execute;
// without it the CLI rejects with needs_override, and with it the cycle
// clears and the frontier recovers to all-handled (the human key resolves
// the chain, but stays unpickable).
func TestReadyAttentionCycleBreakHumanVariantRequiresOverride(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "hcycle-p", "status=open", "--expect", "none", "-m", "hcycle p", "--as", "a")
	run(t, dir, "set", "hcycle-q", "labels=human", "--expect", "none", "-m", "reserving", "--as", "a")
	if _, se, code := run(t, dir, "set", "hcycle-q", "status=open", "--expect", "none", "--override", "-m", "hcycle q -- reserved for jesse", "--as", "a"); code != 0 {
		t.Fatalf("seed hcycle-q: %s", se)
	}
	if _, se, code := run(t, dir, "set", "hcycle-p", "blocked-by=hcycle-q", "--expect", "none", "--as", "a"); code != 0 {
		t.Fatalf("hcycle-p blocked-by=hcycle-q: %s", se)
	}
	time.Sleep(5 * time.Millisecond)
	if _, se, code := run(t, dir, "set", "hcycle-q", "blocked-by=hcycle-p", "--expect", "none", "--override", "-m", "closing the demo cycle", "--as", "a"); code != 0 {
		t.Fatalf("hcycle-q blocked-by=hcycle-p: %s", se)
	}

	so, se, code := run(t, dir, "ready")
	if code != 0 {
		t.Fatalf("ready: %d %s", code, se)
	}
	doc := mustJSON(t, so)
	cycle := findCycleEntry(t, doc["attention"].([]any))
	brk := cycle["break"].(map[string]any)
	if brk["key"] != "hcycle-q" || brk["human"] != true {
		t.Fatalf("break must target the human-labeled member (hcycle-q's edge write closed the loop): %+v", brk)
	}
	expect, _ := brk["expect"].(string)
	msg := "breaking cycle [hcycle-p hcycle-q]: dropping hcycle-p"

	if _, se, code := run(t, dir, "set", "hcycle-q", "blocked-by=", "--expect", expect, "-m", msg, "--as", "kit"); code != 4 {
		t.Fatalf("break write without --override must be rejected: %d %s", code, se)
	} else {
		errDoc := mustJSON(t, se)
		if errDoc["error"] != "needs_override" {
			t.Fatalf("expected needs_override, got %v: %s", errDoc["error"], se)
		}
	}

	if _, se, code := run(t, dir, "set", "hcycle-q", "blocked-by=", "--expect", expect, "--override", "-m", msg, "--as", "kit"); code != 0 {
		t.Fatalf("break write with --override: %s", se)
	}

	so2, se2, code2 := run(t, dir, "ready")
	if code2 != 0 {
		t.Fatalf("ready after break: %d %s", code2, se2)
	}
	doc2 := mustJSON(t, so2)
	assertNoCycleEntry(t, doc2["attention"].([]any))
	if doc2["frontier"] != "all-handled" {
		t.Fatalf("frontier must recover to all-handled — hcycle-p's chain now ends at the non-terminal human key, got %v", doc2["frontier"])
	}
}

// findCycleEntry locates the (sole) "cycle" attention entry in a decoded
// ready envelope's attention list, failing the test if none is present.
func findCycleEntry(t *testing.T, attention []any) map[string]any {
	t.Helper()
	for _, a := range attention {
		m := a.(map[string]any)
		if m["reason"] == "cycle" {
			return m
		}
	}
	t.Fatalf("expected a cycle attention entry: %+v", attention)
	return nil
}

// assertNoCycleEntry fails the test if attention carries any "cycle" entry.
func assertNoCycleEntry(t *testing.T, attention []any) {
	t.Helper()
	for _, a := range attention {
		if a.(map[string]any)["reason"] == "cycle" {
			t.Fatalf("expected no cycle attention entry, got %+v", attention)
		}
	}
}
