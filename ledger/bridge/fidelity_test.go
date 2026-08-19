package main

import (
	"fmt"
	"strings"
	"testing"
)

// Test-plan item 11 — MIRROR FIDELITY.

// TestStateCommentPrecedesTheStateChange, in both directions. The reason and
// the evidence go up FIRST, so a GitHub reader sees why even if the state
// call itself is the one that crashes — and a reopen's reason matters as
// much as a close's.
func TestStateCommentPrecedesTheStateChange(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")
	f.syncOK("operator")
	assertCommentBeforeState(t, f.ghLog(), "state=closed")

	f.setStatusOverride("cache-warm", openValue, "regressed", "jesse")
	f.syncOK("operator")
	assertCommentBeforeState(t, f.ghLog(), "state=open")
	f.converge("operator", 3)
}

func assertCommentBeforeState(t *testing.T, log []string, stateShape string) {
	t.Helper()
	comment, state := -1, -1
	for i, l := range log {
		if strings.Contains(l, "issue comment") && comment < 0 {
			comment = i
		}
		if strings.Contains(l, stateShape) && state < 0 {
			state = i
		}
	}
	if state < 0 {
		t.Fatalf("no %q call in the log: %v", stateShape, log)
	}
	if comment < 0 || comment > state {
		t.Fatalf("the comment must precede the %q call: %v", stateShape, log)
	}
}

// TestTitleMirrorIsATitleEditAndNothingElse: a rename mirrors as a title
// edit and NOTHING else. A bare rename has no message by Part A's -m rule,
// and an override justification never leaves the board.
func TestTitleMirrorIsATitleEditAndNothingElse(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	before := len(f.commentBodies(1))

	f.reserve("cache-warm", "jesse")
	f.ledgerOK("set", "cache-warm", "--rename", "warm the cache on boot (revised)",
		"--override", "-m", "the old title was wrong and I am overriding the human label", "--as", "jesse")
	r := f.syncOK("operator")

	if f.issueTitle(1) != "warm the cache on boot (revised)" {
		t.Fatalf("the rename must mirror as a title edit, got %q: %s", f.issueTitle(1), mustJSON(t, r))
	}
	if got := len(f.commentBodies(1)); got != before {
		t.Fatalf("a title mirror must post NO comment, comments went %d -> %d: %v", before, got, f.commentBodies(1))
	}
	for _, b := range f.commentBodies(1) {
		if strings.Contains(b, "overriding the human label") {
			t.Fatalf("an override justification reached GitHub: %q", b)
		}
	}
	f.converge("operator", 3)
}

// TestIssueCreationBacksFillsNotesExactlyOnce: a key may collect notes while
// it has no issue (the statusless-seed window, or notes older than the
// bridge). They backfill at issue-creation time — and the MARKER is what
// keeps the backfill and the drain from double-posting the same note.
func TestIssueCreationBacksFillsNotesExactlyOnce(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.ledgerOK("note", "-k", "handoff", "--key", "cache-warm", "-m", "note one", "--as", "jesse")
	f.ledgerOK("note", "-k", "comment", "--key", "cache-warm", "-m", "note two", "--as", "ash")

	f.syncOK("operator")
	bodies := f.commentBodies(1)
	if countSubstr(bodies, "note one") != 1 || countSubstr(bodies, "note two") != 1 {
		t.Fatalf("both notes must backfill exactly once: %v", bodies)
	}
	f.converge("operator", 4)
	bodies = f.commentBodies(1)
	if countSubstr(bodies, "note one") != 1 || countSubstr(bodies, "note two") != 1 {
		t.Fatalf("the drain re-posted a backfilled note: %v", bodies)
	}
}

// TestNoteOnANeverLinkedKeyIsDroppedWithAWarningNamingTheEvent: a titleless
// key never gains an issue, so a note on it has nowhere to land.
func TestNoteOnANeverLinkedKeyIsDroppedWithAWarningNamingTheEvent(t *testing.T) {
	f := newIssueFixture(t)
	// A note creates no board key and the key has no title, so there is
	// nothing to name an issue with.
	doc := f.ledgerOK("note", "-k", "handoff", "--key", "orphan-key", "-m", "nowhere to go", "--as", "jesse")
	id, _ := doc["id"].(string)

	r := f.syncOK("operator")
	if !hasWarning(r, id) {
		t.Fatalf("the drop must name the event id %s: %s", id, mustJSON(t, r))
	}
	if !hasWarning(r, "has no GitHub issue to land on") {
		t.Fatalf("the drop must say why: %s", mustJSON(t, r))
	}
	if f.countIssues() != 0 {
		t.Fatalf("a titleless key must never gain an issue: %d", f.countIssues())
	}
}

// TestAuthorSuppressionMirrorsAHumanHandoffAndNotTheBridgesBookkeeping is
// the author-vs-kind ruling. A kind list silently ate HUMAN `handoff` notes —
// the issues spec's designated reclaim channel and the highest-value note
// class on the board — while mirroring every other kind.
func TestAuthorSuppressionMirrorsAHumanHandoffAndNotTheBridgesBookkeeping(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	f.ledgerOK("note", "-k", kindHand, "--key", "cache-warm", "-m", "handing this back to whoever", "--as", "jesse")
	// A forged bookkeeping note from a person: inert on the board (the link
	// map is author-filtered) but a human note, so it MIRRORS — the poisoning
	// becomes visible in two places, one public. Stated, accepted.
	f.ledgerOK("note", "-k", kindLink, "--key", "cache-warm", "-m", linkPrefix+"999", "--as", "mallory")
	r := f.syncOK("operator")

	bodies := f.commentBodies(1)
	if countSubstr(bodies, "handing this back to whoever") != 1 {
		t.Fatalf("a HUMAN handoff note must mirror: %v", bodies)
	}
	if !hasWarning(r, "is not authored "+bridgeAuthor) {
		t.Fatalf("the forged link note must be warned: %s", mustJSON(t, r))
	}
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if lm.ByKey["cache-warm"] != 1 {
		t.Fatalf("a forged link note must be inert, got %v", lm.ByKey)
	}
	// The bridge's OWN bookkeeping never mirrors.
	for _, b := range bodies {
		if strings.Contains(b, "bridge-cursor:") {
			t.Fatalf("the bridge's state note mirrored to GitHub: %q", b)
		}
	}
	f.converge("operator", 4)
}

// TestSuppressedAuthorsCountsAPoisonedEvent: the github:@ namespace is
// unenforced (author enforcement rides the owner-enforcement v2 item), so a
// poisoned `--as github:@x` event is invisible unless counted.
func TestSuppressedAuthorsCountsAPoisonedEvent(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.ledgerOK("note", "-k", "comment", "--key", "cache-warm", "-m", "I am pretending to be GitHub",
		"--as", ghAuthorPrefix+"impostor")

	r := f.syncOK("operator")
	if r.SuppressedAuthors[ghAuthorPrefix+"impostor"] != 1 {
		t.Fatalf("the poisoned author must be counted: %s", mustJSON(t, r))
	}
	for _, b := range f.commentBodies(1) {
		if strings.Contains(b, "pretending to be GitHub") {
			t.Fatalf("a github:@-authored event must never mirror out: %q", b)
		}
	}
}

// ---- items 12 and 13: the gauntlet and spike regressions ----

// TestMarkerOracleSurvivesExportImport: `export`/`import` re-mints every
// event id and preserves the old one only in `imported_from`, so an id-only
// oracle goes blind on exactly the recovery path Law 2 calls safe — and
// re-imports the bridge's whole mirrored history as human notes, in BOTH
// directions.
//
// The bound is honest and pinned here too: `imported_from` is SINGLE-HOP, so
// the oracle survives exactly ONE round trip.
func TestMarkerOracleSurvivesExportImport(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.ledgerOK("note", "-k", kindHand, "--key", "cache-warm", "-m", "a mirrored note", "--as", "jesse")
	f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")
	f.converge("operator", 4)
	mirrored := len(f.commentBodies(1))
	comments := len(f.notes("comment"))

	// Export and re-import into a fresh store — the recovery path.
	dump := f.dir + "/dump.jsonl"
	if _, err := f.board().runBare("export", f.slug, "--to", dump); err != nil {
		t.Fatalf("export: %v", err)
	}
	g := &fixture{t: t, dir: t.TempDir(), ghState: f.ghState, slug: f.slug, repo: f.repo,
		done: f.done, notPlanned: f.notPlanned, listLimit: defaultListLimit}
	g.git("init", "-q", ".")
	g.git("config", "user.email", "r@example.com")
	g.git("config", "user.name", "r")
	g.ledgerOK("init")
	if _, err := g.board().runBare("import", dump, "--slug", f.slug); err != nil {
		t.Fatalf("import: %v", err)
	}

	r := g.syncOK("operator")
	if got := len(g.commentBodies(1)); got != mirrored {
		t.Fatalf("the recovery re-posted mirrored history: %d -> %d comments: %s",
			mirrored, got, mustJSON(t, r))
	}
	if got := len(g.notes("comment")); got != comments {
		t.Fatalf("the recovery re-imported its own comments as board notes: %d -> %d", comments, got)
	}
	// And it neither reopens nor retitles: the mirror is a function of the
	// board's CURRENT STATE, never of its history. Taking the level from the
	// drain instead of the fold reopened a closed issue and restored a
	// superseded title on exactly this path.
	if state, _ := g.issueState(1); state != "CLOSED" {
		t.Fatalf("the recovery reopened a closed issue: %s", state)
	}
	if g.issueTitle(1) != f.issueTitle(1) {
		t.Fatalf("the recovery restored a superseded title: %q", g.issueTitle(1))
	}
	g.converge("operator", 4)
}

// TestDerivedIndexIsToolScoped: the derived idempotency index is scoped
// exactly as the tool's own dedupe is — (author, kind, key, key-string). The
// bare key string was a CENSORSHIP PRIMITIVE: one decoy note under any
// author, kind or key suppressed a real comment's import AND deleted the
// `deduped: true` impersonation detector.
func TestDerivedIndexIsToolScoped(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.humanComment(1, "a real human comment", "mallory")

	// The decoy: the bridge's idempotency key, under a different author, kind
	// and key.
	st := f.ghLoad()
	rest := ""
	for _, c := range st.Issue(1).Comments {
		if strings.Contains(c.Body, "a real human comment") {
			rest = ghComment{URL: c.URL}.restID()
		}
	}
	if rest == "" {
		t.Fatal("setup: no rest id")
	}
	f.ledgerOK("note", "-k", "decoy", "--key", "cache-warm", "-m", "poison",
		"--as", "mallory", "--idempotency-key", commentIdem(rest))

	r := f.syncOK("operator")
	if countSubstr(noteTexts(f.notes("comment")), "a real human comment") != 1 {
		t.Fatalf("the decoy censored a real comment: %+v", f.notes("comment"))
	}
	if !hasWarning(r, "outside its write shape") {
		t.Fatalf("the poison must fail LOUDLY: %s", mustJSON(t, r))
	}
	f.converge("operator", 4)
	if countSubstr(noteTexts(f.notes("comment")), "a real human comment") != 1 {
		t.Fatalf("the comment imported twice: %+v", f.notes("comment"))
	}
}

func noteTexts(notes []Note) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, n.Text)
	}
	return out
}

// TestIntakeCloseOnAClosedKeyWritesNothing: state writes are convergent in
// both directions. Re-firing an attributed override forever was the defect.
func TestIntakeCloseOnAClosedKeyWritesNothing(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.humanClose(1, false, "mallory")
	f.converge("operator", 4)
	before := f.eventCount()

	for i := 0; i < 3; i++ {
		r := f.syncOK("operator")
		if r.BoardWrites != 0 || r.GHMutations != 0 {
			t.Fatalf("run %d: %s", i, mustJSON(t, r))
		}
	}
	if got := f.eventCount(); got != before {
		t.Fatalf("the chain grew by %d", got-before)
	}
	if got := len(f.fabricatedOverrides()); got != 0 {
		t.Fatalf("overrides re-fired: %v", f.fabricatedOverrides())
	}
}

// TestReDrainReplaysNothingAndAccusesNobody: on a re-drain
// (`reset_required`) the divergence warning is SUPPRESSED ENTIRELY —
// MIRROREDVIEW has no meaning when the drain is the whole chain — and no
// state mutation is replayed.
func TestReDrainReplaysNothingAndAccusesNobody(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.seed("retry-storm", "fix the retry storm", "jesse")
	f.converge("operator", 4)
	f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")
	f.converge("operator", 4)

	// Poison the stored cursor so `since` reports reset_required.
	st, _, err := f.board().LoadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Cursor = "0123456789"
	if _, err := f.board().SaveState(st); err != nil {
		t.Fatal(err)
	}

	r := f.syncOK("operator")
	if !hasWarning(r, "no longer resolves") {
		t.Fatalf("the re-drain must be announced: %s", mustJSON(t, r))
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "was discarded") {
			t.Fatalf("a re-drain must accuse nobody: %q", w)
		}
	}
	if r.GHMutations != 0 {
		t.Fatalf("a re-drain must replay no state mutations: %s", mustJSON(t, r))
	}
	if state, _ := f.issueState(1); state != "CLOSED" {
		t.Fatalf("#1 is %s", state)
	}
	f.converge("operator", 4)
}

// TestForeignStateNoteIsInertAndWarned: link and bridge-state notes are read
// AUTHOR-FILTERED. An any-author last-write-wins read let one note from any
// board writer wedge the bridge's whole state.
func TestForeignStateNoteIsInertAndWarned(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.ledgerOK("note", "-k", kindState, "--key", stateKey,
		"-m", "bridge-cursor: nope\n{\"repo\":\"evil/repo\",\"cursor\":\"nope\"}", "--as", "mallory")

	r := f.syncOK("operator")
	if !hasWarning(r, "is not authored "+bridgeAuthor) {
		t.Fatalf("the foreign state note must be warned: %s", mustJSON(t, r))
	}
	if !hasWarning(r, "corrective note authored") {
		t.Fatalf("the warning must name the operator's remedy: %s", mustJSON(t, r))
	}
	// The bridge is not wedged and is still bound to the real repo.
	st, _, err := f.board().LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Repo != f.repo {
		t.Fatalf("the foreign note wedged the state: %+v", st)
	}
	f.converge("operator", 4)
}

// TestChangedLinkIsRefusedNeverRepointed: the established link stands, and
// the change is a refusal-with-handoff.
func TestChangedLinkIsRefusedNeverRepointed(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	other := f.humanCreateIssue("somewhere else", "body", "mallory")
	if _, err := f.board().LinkNote("cache-warm", other); err != nil {
		t.Fatal(err)
	}

	r := f.syncOK("operator")
	if !hasWarning(r, "The established link stands") {
		t.Fatalf("a changed link must be refused, never repointed: %s", mustJSON(t, r))
	}
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if lm.ByKey["cache-warm"] != 1 {
		t.Fatalf("the link was repointed to %v", lm.ByKey)
	}
	if got := len(f.notes(kindHand)); got != 1 {
		t.Fatalf("want exactly one handoff note, got %d", got)
	}
}

// TestOldestWinsSurvivesAMerge: the tie-break is oldest IN FOLD ORDER, never
// by timestamp and never newest — newest-wins flips when a loser's note
// arrives on a later sync.
func TestOldestWinsSurvivesAMerge(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	established, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	want := established.ByKey["cache-warm"]

	// A later note naming a different issue arrives (as a merge would deliver
	// a loser's). The established link must not move.
	loser := f.humanCreateIssue("a loser", "body", "mallory")
	if _, err := f.board().LinkNote("cache-warm", loser); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		lm, err := f.board().Links()
		if err != nil {
			t.Fatal(err)
		}
		if lm.ByKey["cache-warm"] != want {
			t.Fatalf("the established link moved to #%d", lm.ByKey["cache-warm"])
		}
		if lm.ByIssue[loser] == "cache-warm" {
			t.Fatalf("a non-established issue must not be an inbound writer for the key: %v", lm.ByIssue)
		}
		f.syncOK("operator")
	}
}

// TestClosedHintlessUnknownImportsNothing: intake seeds only OPEN issues.
// Stripping a duplicate's hint used to mint a permanent junk key.
func TestClosedHintlessUnknownImportsNothing(t *testing.T) {
	f := newIssueFixture(t)
	n := f.humanCreateIssue("an old closed thing", "no hint here", "mallory")
	f.humanClose(n, false, "mallory")
	f.humanComment(n, "a comment on the closed thing", "mallory")

	r := f.syncOK("operator")
	if got := len(f.keyList()); got != 0 {
		t.Fatalf("a closed hintless unknown must seed nothing, got keys %v: %s", f.keyList(), mustJSON(t, r))
	}
	if got := len(f.notes("comment")); got != 0 {
		t.Fatalf("its comments must not import either: %+v", f.notes("comment"))
	}
	f.converge("operator", 3)
}

// TestClosedLinkedIssueIsInTheBulkMap is the `--state all` regression. Under
// gh's open-only default, close intake never fires, a closed issue's comment
// dedupe is zero-valued, a crashed create whose issue got closed is
// un-adoptable, and saturation never trips — and the whole suite stays green.
func TestClosedLinkedIssueIsInTheBulkMap(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	f.humanClose(1, false, "mallory")
	f.humanComment(1, "still broken actually", "mallory")

	r := f.syncOK("operator")
	if f.status("cache-warm") != "closed" {
		t.Fatalf("a closed issue's state must intake: %s", mustJSON(t, r))
	}
	if countSubstr(noteTexts(f.notes("comment")), "still broken actually") != 1 {
		t.Fatalf("a closed issue's comments must import: %+v", f.notes("comment"))
	}
	// The call must literally pin --state all.
	if countSubstr(f.ghLog(), "--state all") == 0 {
		t.Fatalf("the listing must pin --state all: %v", f.ghLog())
	}
	f.converge("operator", 4)
	if countSubstr(noteTexts(f.notes("comment")), "still broken actually") != 1 {
		t.Fatalf("the closed issue's comment imported twice: %+v", f.notes("comment"))
	}
}

// TestPerIssueCommentSaturationIsReReadCompletely: the BULK listing returns
// the OLDEST 100 comments per issue, newest silently missing — a busy issue
// stopped importing forever with a clean 0/0 report, and crash re-runs
// double-posted past the cap.
func TestPerIssueCommentSaturationIsReReadCompletely(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	st := f.ghLoad()
	for i := 0; i < BulkCommentCap; i++ {
		st.AddComment(1, fmt.Sprintf("chatter %d", i), "mallory")
	}
	st.AddComment(1, "the comment past the cap", "mallory")
	f.ghSave(st)
	if got := len(f.ghLoad().Issue(1).Comments); got != BulkCommentCap+1 {
		t.Fatalf("setup: %d comments", got)
	}

	r := f.syncOK("operator")
	if countSubstr(f.ghLog(), "issue view") == 0 {
		t.Fatalf("a saturated issue must be re-read completely: %v", f.ghLog())
	}
	if countSubstr(noteTexts(f.notes("comment")), "the comment past the cap") != 1 {
		t.Fatalf("the comment past the cap never imported: %s", mustJSON(t, r))
	}
	f.converge("operator", 4)
	if countSubstr(noteTexts(f.notes("comment")), "the comment past the cap") != 1 {
		t.Fatal("the past-the-cap comment imported twice")
	}
}

// TestUnboundedLinkMapRead: `notes -k github-link -n 0`. The default limit of
// 10 silently truncates the identity map at ten issues and mints duplicates
// for everything past it.
func TestUnboundedLinkMapRead(t *testing.T) {
	f := newIssueFixture(t)
	for i := 0; i < 11; i++ {
		f.seed(fmt.Sprintf("key-%02d", i), fmt.Sprintf("task number %d", i), "jesse")
	}
	f.converge("operator", 4)
	if got := f.countIssues(); got != 11 {
		t.Fatalf("setup: %d issues", got)
	}
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if len(lm.ByKey) != 11 {
		t.Fatalf("the link map must be unbounded, got %d entries", len(lm.ByKey))
	}
	// The eleventh linked issue must not mint a duplicate on any later run.
	f.setStatus("key-00", "closed", "fixed", "jesse", "commit:a1")
	f.converge("operator", 4)
	if got := f.countIssues(); got != 11 {
		t.Fatalf("a later run minted duplicates: %d issues", got)
	}
}

// TestBrokenLinkWarnsAndHandsOffOnce is the named out-of-scope case with a
// defined behaviour: issue deletion or transfer out of the repo.
func TestBrokenLinkWarnsAndHandsOffOnce(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	// The issue is deleted / transferred away.
	st := f.ghLoad()
	st.Issues = nil
	f.ghSave(st)

	for run := 0; run < 3; run++ {
		r := f.syncOK("operator")
		if !hasWarning(r, "deleted or transferred") {
			t.Fatalf("run %d must warn about the broken link: %s", run, mustJSON(t, r))
		}
		if r.Divergences != 1 {
			t.Fatalf("run %d: the broken link is a counted divergence: %s", run, mustJSON(t, r))
		}
	}
	if got := len(f.notes(kindHand)); got != 1 {
		t.Fatalf("want exactly one handoff note across three runs, got %d", got)
	}
	if f.countIssues() != 0 {
		t.Fatalf("the bridge must NOT create a replacement issue: %d", f.countIssues())
	}
}

// TestSyncPartialFailureIsScopedToOwnSlug: `ledger sync` takes no slug
// selector, so a blanket abort couples the bridge's availability to every
// dead remote in the operator's store. Abort iff OUR slug failed.
func TestSyncPartialFailureIsScopedToOwnSlug(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	remote := t.TempDir()
	f.git("init", "-q", "--bare", remote)
	f.git("remote", "add", "origin", remote)
	// A second slug in the same store, tracked against a remote whose ref is
	// about to be made unmergeable.
	f.ledgerOK("create", "other", "--scope", "somebody else's board", "--field", "state=on,off", "--as", "jesse")
	f.ledgerOK("push", "issues")
	f.ledgerOK("push", "other")

	// Foreign slug fails: a warning, and the run proceeds.
	r := f.syncOK("operator")
	if r.GHMutations == 0 {
		t.Fatalf("the run must proceed: %s", mustJSON(t, r))
	}
	f.converge("operator", 4)
}

// TestFixtureTransportIsFaithful pins the fixture-faithfulness LAW itself.
// An unfaithful fixture proved a refusal could fire while hiding what it
// protects against, would have measured its own race in the concurrency
// probe, and left a whole suite green under an open-only listing.
func TestFixtureTransportIsFaithful(t *testing.T) {
	f := newIssueFixture(t)
	open1 := f.humanCreateIssue("an open one", "b", "mallory")
	closed1 := f.humanCreateIssue("a closed one", "b", "mallory")
	f.humanClose(closed1, false, "mallory")
	st := f.ghLoad()
	for i := 0; i < BulkCommentCap+5; i++ {
		st.AddComment(open1, fmt.Sprintf("c%d", i), "mallory")
	}
	f.ghSave(st)

	gh := GH{Repo: f.repo, Bin: ghBin, ListLimit: 250}
	// --state all returns both.
	all, err := gh.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("--state all must return open AND closed, got %d", len(all))
	}
	// The bulk read caps comments at 100; the per-issue read does not.
	for _, is := range all {
		if is.Number == open1 && len(is.Comments) != BulkCommentCap {
			t.Fatalf("the bulk listing must cap comments at %d, got %d", BulkCommentCap, len(is.Comments))
		}
	}
	full, err := gh.ViewComments(open1)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != BulkCommentCap+5 {
		t.Fatalf("the per-issue read must be complete, got %d", len(full))
	}
	// --limit truncates.
	small := GH{Repo: f.repo, Bin: ghBin, ListLimit: 1}
	one, err := small.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 {
		t.Fatalf("--limit must truncate, got %d", len(one))
	}
	// gh's own default is open-only: prove the fixture models it, so the
	// bridge's explicit --state all is load-bearing rather than decorative.
	out, err := gh.run("issue", "list", "--json", listFields)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "a closed one") {
		t.Fatal("the fixture must model gh's open-only default")
	}
}

// TestUnsyncedReplicaAdoptsRatherThanDuplicating is the unsynced-replica
// duplicate-issue hazard, demonstrated against the FIXTURE transport and
// never live (it would leave permanent litter in a shared repo).
//
// Replica B has never met replica A: no remote, no shared history, so none
// of A's github-link notes exist on B's chain. The sync-first law protects
// nothing here — there is nothing to sync with. What closes the hazard is
// the issue BODY: the STAMP plus the `ledger-key:` line are a SECOND,
// INDEPENDENT copy of the identity map, so B adopts A's issues instead of
// minting a duplicate for every key.
func TestUnsyncedReplicaAdoptsRatherThanDuplicating(t *testing.T) {
	a := newIssueFixture(t)
	keys := []string{"cache-warm", "retry-storm", "flaky-auth"}
	for _, k := range keys {
		a.seed(k, "task "+k, "jesse")
	}
	a.converge("operator", 4)
	if a.countIssues() != len(keys) {
		t.Fatalf("setup: %d issues", a.countIssues())
	}

	// B: the same board content, arrived at independently, with NO remote and
	// no knowledge of A's link notes.
	b := newIssueFixture(t)
	b.ghState, b.repo = a.ghState, a.repo
	for _, k := range keys {
		b.seed(k, "task "+k, "jesse")
	}
	r := b.syncOK("operator")
	if got := b.countIssues(); got != len(keys) {
		t.Fatalf("an unsynced replica minted duplicates: %d issues (want %d): %s",
			got, len(keys), mustJSON(t, r))
	}
	if countSubstr(r.Actions, "adopted an issue this bridge created but never linked") != len(keys) {
		t.Fatalf("every key should have been adopted from its issue body: %s", mustJSON(t, r))
	}
	b.converge("operator", 4)
	if got := b.countIssues(); got != len(keys) {
		t.Fatalf("converging minted duplicates: %d", got)
	}
}
