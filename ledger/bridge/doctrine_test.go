package main

import (
	"fmt"
	"strings"
	"testing"
)

// Test-plan items 7, 8 and 9 — refusal convergence, guarded intake, and
// Law 4's attribution.

// ---- item 7: refusals converge ----

// TestHumanRefusalWritesOneNoteAndOneCommentEver: a human-labeled key whose
// GitHub side diverges gets exactly ONE handoff note and ONE GitHub comment
// across N runs. The earlier reading spammed both on every run, forever.
func TestHumanRefusalWritesOneNoteAndOneCommentEver(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.reserve("cache-warm", "jesse")
	f.humanClose(1, false, "mallory")

	for run := 0; run < 4; run++ {
		r := f.syncOK("operator")
		if r.Divergences != 1 {
			t.Fatalf("run %d: want the standing divergence counted every run, got %d: %s",
				run, r.Divergences, mustJSON(t, r))
		}
		if f.status("cache-warm") != openValue {
			t.Fatalf("run %d: the bridge overrode a human-reserved key (status=%q)", run, f.status("cache-warm"))
		}
	}
	if got := len(f.notes(kindHand)); got != 1 {
		t.Fatalf("want exactly one handoff note across four runs, got %d: %+v", got, f.notes(kindHand))
	}
	if got := countSubstr(f.commentBodies(1), "reserved on the board"); got != 1 {
		t.Fatalf("want exactly one divergence comment across four runs, got %d: %v", got, f.commentBodies(1))
	}
	if ov := f.fabricatedOverrides(); len(ov) != 0 {
		t.Fatalf("the human gate was overridden: %v", ov)
	}
	// Law 5: `human` NEVER auto-overrides — that is the refusal path.
	if got := countSubstr(f.commentBodies(1), "**github-bridge** (via ledger,"); got != 1 {
		t.Fatalf("the divergence comment must carry the marker exactly once: %v", f.commentBodies(1))
	}
}

// TestRefusalRecordLifecycle: a record NOT re-observed this run is PRUNED,
// pruning is a state change, and a later re-observation re-counts and
// re-persists the record — while the NOTE does not recur, because Law 2 keys
// it on the same quadruple and wins on the ground.
func TestRefusalRecordLifecycle(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.reserve("cache-warm", "jesse")
	f.humanClose(1, false, "mallory")
	first := f.syncOK("operator")
	if first.Divergences != 1 || len(f.notes(kindHand)) != 1 {
		t.Fatalf("setup: %s", mustJSON(t, first))
	}

	// A maintainer clears the signal. The divergence resolves, the record is
	// pruned, and the pruning MUST persist — or the next recurrence is
	// silently swallowed.
	f.ledgerOK("set", "cache-warm", "labels=", "-m", "releasing this", "--as", "jesse")
	cleared := f.syncOK("operator")
	if cleared.Divergences != 0 {
		t.Fatalf("the divergence must clear: %s", mustJSON(t, cleared))
	}
	if f.status("cache-warm") != "closed" {
		t.Fatalf("with the signal cleared the close must land, got %q", f.status("cache-warm"))
	}
	f.converge("operator", 4)
	st, _, err := f.board().LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Records) != 0 {
		t.Fatalf("the pruned record did not land: %+v", st.Records)
	}

	// Now the same divergence recurs: the count and the record come back, the
	// note does not.
	f.reserve("cache-warm", "jesse")
	f.setStatusOverride("cache-warm", "open", "reopening", "jesse")
	f.converge("operator", 4)
	f.humanClose(1, false, "mallory")
	again := f.syncOK("operator")
	if again.Divergences != 1 {
		t.Fatalf("the recurrence must be counted afresh: %s", mustJSON(t, again))
	}
	st, _, err = f.board().LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Records) != 1 {
		t.Fatalf("the recurrence must be re-persisted: %+v", st.Records)
	}
	if got := len(f.notes(kindHand)); got != 1 {
		t.Fatalf("the note must NOT recur (Law 2 keys it), got %d: %+v", got, f.notes(kindHand))
	}
}

// TestSuppressionRecordNeverSwallowsARefusal is the ASPECT-collision probe,
// kept as a regression. Keying both classes on the bare (issue, aspect,
// observed-state) triple let a run-A SUPPRESSION record silently consume a
// run-B human-reserved REFUSAL's note AND its one GitHub comment, leaving
// only a stale suppression note asserting the opposite of what happened.
func TestSuppressionRecordNeverSwallowsARefusal(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	// Run A: a suppression on (issue 1, status, closed) — a person closed on
	// GitHub while the board was pushing its own close.
	f.humanClose(1, true, "mallory")
	f.setStatus("cache-warm", "closed", "board says done", "jesse", "commit:a1b2c3")
	runA := f.syncOK("operator")
	if runA.Divergences != 1 {
		t.Fatalf("run A: want the suppression: %s", mustJSON(t, runA))
	}
	suppression := f.notes(kindHand)
	if len(suppression) != 1 || !strings.Contains(suppression[0].Text, "was discarded") {
		t.Fatalf("run A must leave a SUPPRESSION note: %+v", suppression)
	}
	f.converge("operator", 4)

	// Run B: a REFUSAL on the very same (issue, aspect, observed-state) —
	// the key is human-reserved and GitHub closes it again.
	f.reserve("cache-warm", "jesse")
	f.setStatusOverride("cache-warm", "open", "reopening", "jesse")
	f.converge("operator", 4)
	f.humanClose(1, false, "mallory")
	runB := f.syncOK("operator")
	if runB.Divergences != 1 {
		t.Fatalf("run B: want the refusal counted: %s", mustJSON(t, runB))
	}
	notes := f.notes(kindHand)
	if len(notes) != 2 {
		t.Fatalf("the refusal's note was swallowed by the suppression record: %+v", notes)
	}
	if !strings.Contains(notes[1].Text, "reserved for a person") {
		t.Fatalf("the second note must be the REFUSAL's, got %q", notes[1].Text)
	}
	if got := countSubstr(f.commentBodies(1), "reserved on the board"); got != 1 {
		t.Fatalf("the refusal's one GitHub comment was swallowed: %v", f.commentBodies(1))
	}
	// And the two records coexist under distinct classes.
	st, _, err := f.board().LoadState()
	if err != nil {
		t.Fatal(err)
	}
	classes := map[string]bool{}
	for _, rec := range st.Records {
		classes[rec.Class] = true
	}
	if !classes[classRefusal] {
		t.Fatalf("the refusal record is missing: %+v", st.Records)
	}
}

// TestRecordComparisonIsOrderBlind: record-set comparison is SET comparison.
// Walk order is an artifact of which issue was visited first; a list
// comparison would persist a new state note every run, forever.
func TestRecordComparisonIsOrderBlind(t *testing.T) {
	a := []Record{{Issue: 2, Class: classRefusal, Aspect: aspectStatus, Observed: "closed"},
		{Issue: 1, Class: classLink, Observed: "#3"}}
	b := []Record{a[1], a[0]}
	if !sameRecords(a, b) {
		t.Fatal("record comparison must be order-blind")
	}
	if sameRecords(a, a[:1]) {
		t.Fatal("a shorter set is not the same set")
	}
	c := []Record{a[0], {Issue: 1, Class: classSuppression, Observed: "#3"}}
	if sameRecords(a, c) {
		t.Fatal("the CLASS coordinate must participate in the comparison")
	}
}

// ---- item 8: guarded intake ----

// TestClaimSignalAutoOverridesAttributed: a `claim` is a real person's
// decision, so intake auto-overrides it and the tool records the override
// against the GitHub actor for triage.
func TestClaimSignalAutoOverridesAttributed(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "in-progress", "picking this up", "ash")
	f.converge("operator", 3)

	f.humanClose(1, false, "mallory")
	r := f.syncOK("operator")
	if f.status("cache-warm") != "closed" {
		t.Fatalf("the claim should have been auto-overridden, status=%q: %s", f.status("cache-warm"), mustJSON(t, r))
	}
	if !hasAction(r, "[override]") {
		t.Fatalf("the override must be reported: %s", mustJSON(t, r))
	}
	ov := f.fabricatedOverrides()
	if len(ov) != 1 || !strings.Contains(ov[0], "github:@mallory") || !strings.Contains(ov[0], "claim") {
		t.Fatalf("the override must be recorded against the GitHub actor: %v", ov)
	}
}

// TestSettledSignalAutoOverridesOnReopen: the other auto-overridable signal.
// A GitHub reopen over a settled board key is a person deciding it is not
// done after all.
func TestSettledSignalAutoOverridesOnReopen(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")
	f.converge("operator", 3)

	f.humanReopen(1, "mallory")
	r := f.syncOK("operator")
	if f.status("cache-warm") != openValue {
		t.Fatalf("the reopen should have landed, status=%q: %s", f.status("cache-warm"), mustJSON(t, r))
	}
	ov := f.fabricatedOverrides()
	if len(ov) != 1 || !strings.Contains(ov[0], "settled") {
		t.Fatalf("want one settled override attributed to the actor: %v", ov)
	}
}

// TestNeedsOverrideFailsClosed is Law 5's guard that does not depend on the
// startup probe. A reader that failed OPEN auto-overrode the one write Law 3
// exists to prevent, against any pre-rev-16 binary.
func TestNeedsOverrideFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		signals []string
		want    bool
	}{
		{"no signals at all (a pre-rev-16 document)", nil, false},
		{"empty list", []string{}, false},
		{"human alone", []string{"human"}, false},
		{"claim and human", []string{"claim", "human"}, false},
		{"claim alone", []string{"claim"}, true},
		{"settled alone", []string{"settled"}, true},
		{"claim and settled", []string{"claim", "settled"}, true},
		{"a signal this bridge does not know", []string{"quarantined"}, false},
		{"a known signal plus an unknown one", []string{"claim", "quarantined"}, false},
	}
	for _, c := range cases {
		be := &BoardErr{Code: "needs_override", Signals: c.signals}
		if got := be.autoOverridable(); got != c.want {
			t.Errorf("%s: autoOverridable = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSignalsComeFromTheDocumentNotTheProse: the signal names come from the
// error document's machine-readable list. A consumer must never parse
// English.
func TestSignalsComeFromTheDocumentNotTheProse(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.reserve("cache-warm", "jesse")
	_, err := f.ledger("set", "cache-warm", "status=closed", "--expect", f.statusID("cache-warm"),
		"-m", "closing", "--as", "someone-else", "--evidence", "commit:a1")
	be, ok := err.(*BoardErr)
	if !ok || be.Code != "needs_override" {
		t.Fatalf("want needs_override, got %v", err)
	}
	if len(be.Signals) == 0 {
		t.Fatalf("the document must carry machine-readable signals: %+v", be)
	}
	if !be.hasSignal("human") {
		t.Fatalf("want the human signal in the document, got %v", be.Signals)
	}
	// A document whose PROSE names every signal but whose list is empty must
	// answer no — the prose must never creep back in as a fallback.
	prose := &BoardErr{Code: "needs_override",
		Message: "'cache-warm' has standing signal(s) that guard this write: claim, human, settled"}
	if prose.hasSignal("human") || prose.autoOverridable() {
		t.Fatal("the English message must never be read as the signal list")
	}
}

// TestClaimLostOnATerminalWriteNeverRetries is the doctrine's own terminal
// exception: a lost CAS on a terminal write means somebody else already
// decided this key's outcome. Never re-close blind.
func TestClaimLostOnATerminalWriteNeverRetries(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	stale := f.statusID("cache-warm")
	f.setStatus("cache-warm", "in-progress", "somebody else moved first", "ash")
	before := f.eventCount()

	_, _, err := f.board().SetStatus("cache-warm", "closed", stale, "closing from GitHub",
		ghAuthorPrefix+"mallory", []string{"gh:x#1"}, true)
	if code(err) != "claim_lost" {
		t.Fatalf("want claim_lost straight out, got %v", err)
	}
	if got := f.eventCount(); got != before {
		t.Fatalf("a terminal claim_lost must write NOTHING, chain grew by %d", got-before)
	}
	if f.status("cache-warm") != "in-progress" {
		t.Fatalf("the key moved to %q", f.status("cache-warm"))
	}
}

// TestClaimLostOnANonTerminalWriteRetriesOnce: one re-read, one retry — and
// the same rules apply to the retry, since a retry that hits a signal takes
// the signal's rule.
func TestClaimLostOnANonTerminalWriteRetriesOnce(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	stale := f.statusID("cache-warm")
	f.setStatus("cache-warm", openValue, "a second board write", "jesse")

	id, how, err := f.board().SetStatus("cache-warm", "in-progress", stale, "claiming from GitHub",
		ghAuthorPrefix+"mallory", nil, false)
	if err != nil {
		t.Fatalf("the retry should have landed: %v", err)
	}
	if id == "" || how != "retried" {
		t.Fatalf("want a retried write, got id=%q how=%q", id, how)
	}
	if f.status("cache-warm") != "in-progress" {
		t.Fatalf("status=%q", f.status("cache-warm"))
	}
}

// TestIntakeRenameRaceLosesLoudly: an intake rename carries `--expect` from
// the rename stream, so a board rename racing it loses LOUDLY, not silently.
func TestIntakeRenameRaceLosesLoudly(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	// A first rename, so the stream has a head to be stale against.
	f.ledgerOK("set", "cache-warm", "--rename", "first board title", "--as", "jesse")
	stale := ""
	keys, _, err := f.board().Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	stale = keys["cache-warm"].RenameID
	f.ledgerOK("set", "cache-warm", "--rename", "second board title", "--as", "jesse")

	_, err = f.board().Rename("cache-warm", "a GitHub title", ghAuthorPrefix+"mallory", stale)
	if code(err) != "claim_lost" {
		t.Fatalf("a racing rename must lose loudly with claim_lost, got %v", err)
	}
	if f.title("cache-warm") != "second board title" {
		t.Fatalf("the racing rename overwrote the board: %q", f.title("cache-warm"))
	}
}

// ---- item 9: attribution is paginated or absent ----

// TestLaw4FindsTheNewestActorAcrossPages: the actor comes from the FULL
// timeline. A single-call read finds NOTHING past 30 events, and a
// last-page-only read misses any state event followed by a page of comments.
func TestLaw4FindsTheNewestActorAcrossPages(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	st := f.ghLoad()
	// A stale close cycle first...
	st.AddTimeline(1, "closed", "alice")
	st.AddTimeline(1, "reopened", "alice")
	// ...then the close that actually counts...
	st.AddTimeline(1, "closed", "bob")
	// ...then a page and a half of comments after it, so neither the first
	// page nor the last page alone contains it.
	for i := 0; i < 45; i++ {
		st.AddTimeline(1, "commented", "carol")
	}
	is := st.Issue(1)
	is.State, is.StateReason = "CLOSED", "COMPLETED"
	f.ghSave(st)

	r := f.syncOK("operator")
	if f.status("cache-warm") != "closed" {
		t.Fatalf("the close did not intake: %s", mustJSON(t, r))
	}
	if !hasAction(r, "by "+ghAuthorPrefix+"bob") {
		t.Fatalf("want the NEWEST closer (bob), not the stale one: %s", mustJSON(t, r))
	}
	if countSubstr(f.ghLog(), "--paginate") == 0 {
		t.Fatalf("the timeline read must paginate: %v", f.ghLog())
	}
	// The timeline is >30 events, so an unpaginated read would have found
	// nothing at all — prove the fixture really is that long.
	if got := len(f.ghLoad().Timelines["1"]); got <= 30 {
		t.Fatalf("the fixture timeline must exceed one page, got %d events", got)
	}
}

// TestLaw4FallsBackToTheIssueAuthorWithAWarning: "no matching event found"
// is the ONLY fallback.
func TestLaw4FallsBackToTheIssueAuthorWithAWarning(t *testing.T) {
	f := newIssueFixture(t)
	n := f.humanCreateIssue("a person's issue", "please fix", "mallory")
	f.syncOK("operator")
	key := ""
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	key = lm.ByIssue[n]
	if key == "" {
		t.Fatalf("setup: issue #%d was not seeded", n)
	}
	// Close it with NO timeline event at all.
	st := f.ghLoad()
	is := st.Issue(n)
	is.State, is.StateReason = "CLOSED", "COMPLETED"
	st.Timelines[fmt.Sprint(n)] = nil
	f.ghSave(st)

	r := f.syncOK("operator")
	if !hasWarning(r, "no 'closed' timeline event found") {
		t.Fatalf("the fallback must warn: %s", mustJSON(t, r))
	}
	if !hasAction(r, "by "+ghAuthorPrefix+"mallory") {
		t.Fatalf("the fallback must attribute to the issue author: %s", mustJSON(t, r))
	}
	if f.status(key) != "closed" {
		t.Fatalf("status=%q", f.status(key))
	}
}
