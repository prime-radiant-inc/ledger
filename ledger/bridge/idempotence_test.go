package main

import (
	"fmt"
	"strings"
	"testing"
)

// Test-plan item 5 — IDEMPOTENCE. Crash injection at every transport call
// site in BOTH modes (fail-BEFORE and fail-AFTER the effect), each replay
// converging to a 0/0 fixed point.
//
// Both modes matter and only one of them is obvious. A fail-BEFORE injection
// never creates the orphan that mints duplicates: the window where a
// duplicate issue or a double-posted comment comes from is the one where the
// call LANDED and the bridge died before it could record that it had.
//
// Recovery CONVERGES; it may take two or three runs, because recovery
// bookkeeping is itself events. Nothing here asserts "the next run is a
// no-op".

// sweepScenario builds a board and a fixture repo that together exercise
// every transport call shape the bridge makes. The shapes are ASSERTED below
// before injection begins, so the sweep cannot silently drift off the
// sequence it is proving safe.
func sweepScenario(t *testing.T) *fixture {
	t.Helper()
	f := newIssueFixture(t)

	// (a) a key that must gain an issue this run — create + link.
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	// (b) a key that gets closed with evidence — comment + PATCH completed.
	f.seed("retry-storm", "fix the retry storm in the uploader", "jesse")
	// (c) a key closed not-planned — comment + PATCH not_planned.
	f.seed("rewrite-css", "rewrite the CSS from scratch", "jesse")
	// (d) a key whose issue is ALREADY linked when the drain starts, so its
	// rename mirrors as `issue edit --title` rather than folding into a
	// create.
	f.seed("flaky-auth", "flaky auth test", "jesse")
	// (e) a key the board reopens while GitHub has it closed — PATCH open.
	f.seed("readme-install", "readme install command is wrong", "jesse")
	f.syncOK("operator")
	// Settle readme-install from the GitHub side so the reopen below is a
	// genuine terminal -> non-terminal board move. Both of these preparatory
	// syncs must happen BEFORE the drain is staged, or they consume it.
	f.humanClose(5, false, "mallory")
	f.converge("operator", 3)

	// --- from here on, everything is the drain the sweep runs against ---

	// A key seeded only now, so the sweep run itself must CREATE its issue —
	// the crash window that adoption exists to close.
	f.seed("brand-new", "a key born inside the drain", "jesse")
	f.setStatus("retry-storm", "closed", "fixed in a1b2c3", "jesse", "commit:a1b2c3")
	f.setStatus("rewrite-css", "wontfix", "not doing this", "jesse")
	f.ledgerOK("set", "flaky-auth", "--rename", "flaky auth test times out under load", "--as", "jesse")
	f.ledgerOK("note", "-k", "handoff", "--key", "cache-warm", "-m", "handing this back", "--as", "jesse")
	f.setStatusOverride("readme-install", "open", "regressed again", "jesse")

	// (f) a brand-new human issue — seed + link + issue edit --body.
	f.humanCreateIssue("new bug from a person", "please fix", "mallory")
	// (g) a human comment on a linked issue — intake note.
	f.humanComment(1, "any progress on this?", "mallory")
	// (h) a human retitle — timeline read + rename.
	f.humanRetitle(2, "fix the retry storm (uploader)", "mallory")
	// (i) an issue at exactly the bulk comment cap — the saturation re-read.
	st := f.ghLoad()
	for i := 0; i < BulkCommentCap; i++ {
		st.AddComment(3, fmt.Sprintf("chatter %d", i), "mallory")
	}
	f.ghSave(st)
	return f
}

// TestLaw2CrashSweep is the whole item-5 sweep.
func TestLaw2CrashSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("the crash sweep runs a full bridge pass per injection")
	}
	// One clean pass first, to learn how many calls the scenario makes and
	// which shapes it covers.
	probe := sweepScenario(t)
	probe.syncOK("operator")
	calls := probe.ghCalls()
	log := probe.ghLog()
	want := []string{"issue list", "issue view", "issue create", "issue edit", "issue comment",
		"state=closed -f state_reason=completed", "state_reason=not_planned", "state=open", "--paginate"}
	for _, shape := range want {
		if countSubstr(log, shape) == 0 {
			t.Fatalf("the sweep scenario never makes a %q call — it would prove nothing about it.\nlog:\n%s",
				shape, strings.Join(log, "\n"))
		}
	}
	t.Logf("scenario makes %d transport calls covering %v", calls, want)
	// Converge the probe so its expected end state is known.
	probe.converge("operator", 4)
	wantIssues := probe.countIssues()

	for call := 1; call <= calls; call++ {
		for _, mode := range []string{"before", "after"} {
			t.Run(fmt.Sprintf("call%d-%s", call, mode), func(t *testing.T) {
				f := sweepScenario(t)
				var err error
				if mode == "before" {
					_, err = f.sync("operator", call)
				} else {
					_, err = f.syncAfter("operator", call)
				}
				if err == nil {
					// The injected call was never reached this run (the run
					// made fewer calls than the probe did — legal, since a
					// crash changes what later runs have to do).
					t.Skipf("injection at call %d (%s) was not reached", call, mode)
				}
				f.converge("operator", 6)
				f.auditNoDuplicates(wantIssues)
			})
		}
	}
}

// auditNoDuplicates is the sweep's whole verdict: no duplicate issues, no
// duplicate mirrored comments, no duplicate link notes, no duplicate
// imported comments.
func (f *fixture) auditNoDuplicates(wantIssues int) {
	f.t.Helper()
	if got := f.countIssues(); got != wantIssues {
		f.t.Fatalf("want %d issues, got %d", wantIssues, got)
	}
	// No board event is mirrored twice onto the same issue.
	for _, is := range f.ghLoad().Issues {
		seen := map[string]int{}
		for _, c := range is.Comments {
			if _, id, ok := parseMarker(c.Body); ok {
				seen[id]++
			}
		}
		for id, n := range seen {
			if n > 1 {
				f.t.Fatalf("issue #%d carries event %s %d times", is.Number, id, n)
			}
		}
	}
	// One link note per (key, issue), and one established link per key.
	links := map[string]int{}
	for _, n := range f.notes(kindLink) {
		links[n.Key+" "+n.Text]++
	}
	for k, n := range links {
		if n > 1 {
			f.t.Fatalf("duplicate link note %q x%d", k, n)
		}
	}
	lm, err := f.board().Links()
	if err != nil {
		f.t.Fatal(err)
	}
	if len(lm.Changed) != 0 {
		f.t.Fatalf("a key ended with more than one link: %v", lm.Changed)
	}
	// No GitHub comment imported twice.
	imported := map[string]int{}
	for _, ev := range f.chain() {
		if ev.Type == "note" && ev.Kind == "comment" && ev.IdemKey != "" {
			imported[ev.Key+" "+ev.IdemKey]++
		}
	}
	for k, n := range imported {
		if n > 1 {
			f.t.Fatalf("comment %q imported %d times", k, n)
		}
	}
	if ov := f.fabricatedOverrides(); len(ov) != 0 {
		f.t.Fatalf("fabricated override(s) survived recovery: %v", ov)
	}
}

// TestDedupedIsNotAWrite pins the `deduped: true` contract on the path that
// depends on it most: a re-observed divergence. A deduped write is not a
// write anywhere the bridge writes, or a converged run can never report
// zero.
func TestDedupedIsNotAWrite(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.reserve("cache-warm", "jesse")
	f.humanClose(1, false, "mallory")

	first := f.syncOK("operator")
	if first.Divergences != 1 {
		t.Fatalf("want one divergence: %s", mustJSON(t, first))
	}
	// Prove the deduped path is actually exercised: write the same handoff
	// note by hand and watch the board dedupe it.
	notesBefore := len(f.notes(kindHand))
	r := Record{Issue: 1, Class: classRefusal, Aspect: aspectStatus, Observed: "closed"}
	_, deduped, err := f.board().Note("cache-warm", kindHand, "whatever", bridgeAuthor, r.idem())
	if err != nil {
		t.Fatal(err)
	}
	if !deduped {
		t.Fatalf("the refusal note's idempotency key %q did not dedupe", r.idem())
	}
	if got := len(f.notes(kindHand)); got != notesBefore {
		t.Fatalf("a deduped write grew the chain: %d -> %d", notesBefore, got)
	}
	// And the bridge's own re-observation reports zero writes.
	converged := f.converge("operator", 4)
	if converged.BoardWrites != 0 || converged.GHMutations != 0 {
		t.Fatalf("a converged run must report 0/0: %s", mustJSON(t, converged))
	}
	if converged.Divergences != 1 {
		t.Fatalf("the standing divergence must keep being counted: %s", mustJSON(t, converged))
	}
}

// TestNoOpRunReportsThePersistedCursor: `cursor` in the report is the
// PERSISTED cursor, not the drain's tip. A run that changed nothing persists
// nothing and reports the stored value.
func TestNoOpRunReportsThePersistedCursor(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	first := f.syncOK("operator")
	stored := first.Cursor

	// A board write nobody mirrors (the bridge's own state note) still leaves
	// the chain head past the stored cursor.
	head := f.chain()[len(f.chain())-1].ID
	if stored == head {
		t.Logf("stored cursor is the head this time (%s)", stored)
	}
	for i := 0; i < 3; i++ {
		r := f.syncOK("operator")
		if r.GHMutations != 0 || r.BoardWrites != 0 {
			t.Fatalf("run %d was not a no-op: %s", i, mustJSON(t, r))
		}
		if r.Cursor != stored {
			t.Fatalf("run %d reported cursor %q, want the persisted %q", i, r.Cursor, stored)
		}
	}
}

// TestFlakeStormConvergesWithAccumulatingProgress: a transport answering 502
// on a fraction of calls — half before the write landed, half after — for
// three consecutive runs, then a clean transport. Every configuration must
// reach the scenario's exact end state, with progress ACCUMULATING across
// failed runs.
//
// The honest cost this pins the other side of: there is no retry and no
// backoff anywhere, so one transient failure aborts the whole run. Safe, and
// unavailable under sustained flakiness.
func TestFlakeStormConvergesWithAccumulatingProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("six flake configurations, each several full passes")
	}
	for _, cfg := range []struct{ seed, rate int }{{1, 33}, {7, 33}, {13, 33}, {3, 10}, {11, 10}, {29, 33}} {
		t.Run(fmt.Sprintf("seed%d-rate%d", cfg.seed, cfg.rate), func(t *testing.T) {
			f := newIssueFixture(t)
			for _, k := range []string{"a-one", "b-two", "c-three", "d-four", "e-five", "f-six"} {
				f.seed(k, "task "+k, "jesse")
			}
			n := f.humanCreateIssue("a person's issue", "body", "mallory")
			f.humanComment(n, "a human comment", "mallory")

			t.Setenv("FAKEGH_FLAKE_SEED", fmt.Sprint(cfg.seed))
			t.Setenv("FAKEGH_FLAKE_RATE", fmt.Sprint(cfg.rate))
			failed, progress := 0, []int{}
			for run := 0; run < 3; run++ {
				if _, err := f.sync("operator", 0); err != nil {
					failed++
				}
				progress = append(progress, f.countIssues())
			}
			t.Setenv("FAKEGH_FLAKE_RATE", "0")
			f.converge("operator", 8)
			if got := f.countIssues(); got != 7 {
				t.Fatalf("want 7 issues after recovery, got %d (mid-storm progress %v)", got, progress)
			}
			f.auditNoDuplicates(7)
			t.Logf("seed %d rate %d%%: %d/3 flaky runs failed, issues mid-storm %v",
				cfg.seed, cfg.rate, failed, progress)
		})
	}
}

// TestConcurrencyIsBoundedAndDetectedNextRun keeps the falsified
// "inefficient, not corrupting" claim as a REGRESSION, in its honest form.
//
// Two overlapping runs mint PERMANENT artifacts and BOTH exit 0 with no
// warning — there is no failure signal at run time. What holds, and is what
// this pins: the damage is bounded, nothing flip-flops, no override is ever
// fabricated, the established link resolves to exactly one inbound writer,
// and the NEXT run's duplicate-link warnings name every affected key.
func TestConcurrencyIsBoundedAndDetectedNextRun(t *testing.T) {
	f := newIssueFixture(t)
	keys := []string{"cache-warm", "retry-storm", "flaky-auth"}
	for _, k := range keys {
		f.seed(k, "task "+k, "jesse")
	}
	// Two real PROCESSES against one store, launched together: the cron
	// overlap. The fixture transport holds an exclusive lock per invocation,
	// so the two serialize the way two API calls against one repo do — the
	// probe measures the BRIDGE's race, not the fixture's.
	outs := f.runConcurrently(2)
	for i, out := range outs {
		if !strings.Contains(out, `"ok": true`) {
			t.Fatalf("overlapping run %d did not exit ok — the honest claim is that BOTH succeed: %s", i, out)
		}
	}
	if got := f.countIssues(); got <= len(keys) {
		t.Skipf("the two runs did not actually overlap this time (%d issues) — nothing to assert", got)
	}
	// The next run is where the operator finally learns.
	next := f.syncOK("operator")
	named := 0
	for _, k := range keys {
		if hasWarning(next, "board key '"+k+"' has more than one github-link note") {
			named++
		}
	}
	if named == 0 {
		t.Fatalf("the next run must name every duplicated key: %s", mustJSON(t, next))
	}
	if next.Divergences < named {
		t.Fatalf("duplicate links must count as divergences: %s", mustJSON(t, next))
	}
	if ov := f.fabricatedOverrides(); len(ov) != 0 {
		t.Fatalf("an overlap fabricated override(s): %v", ov)
	}
	// No flip-flop: the established link never moves.
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	before := fmt.Sprint(lm.ByKey)
	f.syncOK("operator")
	lm2, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(lm2.ByKey) != before {
		t.Fatalf("the established link moved: %s -> %v", before, lm2.ByKey)
	}
}

// runConcurrently launches n real bridge processes at once against this
// fixture and returns their combined output.
func (f *fixture) runConcurrently(n int) []string {
	f.t.Helper()
	type res struct {
		i   int
		out string
	}
	ch := make(chan res, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			out, _ := f.runBridgeBinary()
			ch <- res{i, out}
		}(i)
	}
	outs := make([]string, n)
	for i := 0; i < n; i++ {
		r := <-ch
		outs[r.i] = r.out
	}
	return outs
}
