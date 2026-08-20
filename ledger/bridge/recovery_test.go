package main

import (
	"strings"
	"testing"
)

// Fix-round regressions: the windows and failure modes review found open
// after the first build.

// TestTimelineTransportFailureAbortsAndWritesNothing pins the OTHER side of
// Law 4's fallback. "No matching event found" falls back to the issue author
// with a warning; a TRANSPORT FAILURE must abort, because attributing a close
// to the wrong person is a permanent board write made on the strength of a
// hiccup.
//
// The crash sweep cannot pin this on its own: while the error was swallowed,
// the sweep's injection at that call simply skipped as "not reached", which
// is exactly how the original bug hid.
func TestTimelineTransportFailureAbortsAndWritesNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		stage func(f *fixture)
	}{
		{"a close, whose actor comes from the timeline", func(f *fixture) { f.humanClose(1, false, "mallory") }},
		{"a retitle, likewise", func(f *fixture) { f.humanRetitle(1, "retitled on GitHub", "mallory") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newIssueFixture(t)
			f.seed("cache-warm", "warm the cache on boot", "jesse")
			f.converge("operator", 3)
			c.stage(f)

			before := f.eventCount()
			status, title := f.status("cache-warm"), f.title("cache-warm")
			// The run's calls on this board are [issue list, api timeline, …],
			// so call 2 is the timeline read. The assertions below fail loudly
			// if that ever stops being true.
			_, err := f.sync("operator", 2)
			if err == nil {
				t.Fatal("a failed timeline read must abort the run, never fall back")
			}
			if !strings.Contains(err.Error(), "timeline") {
				t.Fatalf("the error must name the failing call: %v", err)
			}
			if got := f.eventCount(); got != before {
				t.Fatalf("the aborted run wrote %d board events", got-before)
			}
			if f.status("cache-warm") != status || f.title("cache-warm") != title {
				t.Fatalf("the aborted run moved the key: status %q->%q title %q->%q",
					status, f.status("cache-warm"), title, f.title("cache-warm"))
			}
			// And the next clean run does the work properly.
			f.converge("operator", 4)
		})
	}
}

// TestAuthorlessIssueIsSkippedNotAborted: a deleted GitHub account leaves an
// author-less issue. There is nobody to attribute a seed to, so it is warned
// and SKIPPED — the same rule its comments already follow.
//
// Propagating the error instead aborts the run, and since the issue never
// goes away that bricks every future run: one deleted account takes the whole
// bridge down, permanently.
func TestAuthorlessIssueIsSkippedNotAborted(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	orphan := f.humanCreateIssue("filed by a since-deleted account", "body", "")
	live := f.humanCreateIssue("filed by a live account", "body", "mallory")

	r := f.syncOK("operator")
	if !hasWarning(r, "names no author") {
		t.Fatalf("the author-less issue must warn: %s", mustJSON(t, r))
	}
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if _, linked := lm.ByIssue[orphan]; linked {
		t.Fatalf("the author-less issue must not be seeded: %v", lm.ByIssue)
	}
	// The rest of the run must have happened anyway — that is the whole point.
	if _, linked := lm.ByIssue[live]; !linked {
		t.Fatalf("the run must continue past the author-less issue: %v", lm.ByIssue)
	}
	if f.countIssues() < 3 {
		t.Fatalf("the board key should still have gained its issue: %d issues", f.countIssues())
	}
	// And it stays skipped, run after run, without ever bricking one.
	for i := 0; i < 3; i++ {
		if _, err := f.sync("operator", 0); err != nil {
			t.Fatalf("run %d aborted on the author-less issue: %v", i, err)
		}
	}
	f.converge("operator", 4)
}

// TestSeedToLinkCrashWindow is the probed window rev 8.2 closes. A crash
// between an inbound seed and its link note used to mint a SECOND, suffixed
// key on the next run and a second GitHub issue for the first one —
// permanently, since neither run could see what the other had done.
//
// The seed now carries `--idempotency-key gh-issue-<n>`, and the shared
// derived index maps that spent key back to the board key it minted. Issue
// resolution consults the mapping BEFORE minting anything.
func TestSeedToLinkCrashWindow(t *testing.T) {
	f := newIssueFixture(t)
	n := f.humanCreateIssue("a person filed this", "no hints", "mallory")

	// The run's calls are [issue list, issue edit --body]: the seed and the
	// link note are BOARD writes between them, so failing after the listing
	// and killing the process mid-board-write is not reachable through the
	// transport. Do it the honest way: write the seed by hand exactly as the
	// bridge would, and leave the link note unwritten.
	if _, err := f.board().Seed("a-person-filed-this", "a person filed this", ghAuthorPrefix+"mallory", n); err != nil {
		t.Fatal(err)
	}
	if got := len(f.notes(kindLink)); got != 0 {
		t.Fatalf("setup: the link note must be missing, got %+v", f.notes(kindLink))
	}

	r := f.syncOK("operator")
	if !hasAction(r, "recovered a seed this bridge wrote but never linked") {
		t.Fatalf("the orphaned seed must be recovered, not re-seeded: %s", mustJSON(t, r))
	}
	keys := f.keyList()
	if len(keys) != 1 || keys[0] != "a-person-filed-this" {
		t.Fatalf("want exactly the one key, got %v", keys)
	}
	if f.countIssues() != 1 {
		t.Fatalf("want exactly the one issue, got %d", f.countIssues())
	}
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if lm.ByKey["a-person-filed-this"] != n {
		t.Fatalf("the recovered key must link to #%d, got %v", n, lm.ByKey)
	}
	f.converge("operator", 4)
	if got := len(f.keyList()); got != 1 {
		t.Fatalf("converging minted keys: %v", f.keyList())
	}
	if got := f.countIssues(); got != 1 {
		t.Fatalf("converging minted issues: %d", got)
	}
}

// TestSeedMapIsScopedToTheBridgesWriteShape: the inbound-seed map is a
// binding primitive, so a decoy must not be able to point an issue at a key
// of somebody else's choosing. Same lesson the derived idempotency index
// learned from the censorship probe — the map is scoped to the bridge's own
// write shape (a `set` on a board key authored `github:@<login>`).
func TestSeedMapIsScopedToTheBridgesWriteShape(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("victim-key", "a key somebody else owns", "jesse")
	n := f.humanCreateIssue("a person filed this", "no hints", "mallory")

	// The decoy: the bridge's seed idempotency key, on a NOTE, under a human
	// author, pointing at an existing key.
	f.ledgerOK("note", "-k", "comment", "--key", "victim-key", "-m", "poison",
		"--as", "mallory", "--idempotency-key", issueIdem(n))

	f.syncOK("operator")
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if lm.ByKey["victim-key"] == n {
		t.Fatalf("a decoy bound issue #%d to an existing key: %v", n, lm.ByKey)
	}
	if lm.ByIssue[n] == "victim-key" {
		t.Fatalf("a decoy hijacked the issue: %v", lm.ByIssue)
	}
	f.converge("operator", 4)
}

// TestDedupedLinkNoteIsNotABoardWrite is the reviewer's forever-persisting
// state-note loop, built. A key whose sole link note has been RETRACTED, plus
// a stamped issue still carrying its `ledger-key:` line, sent the adoption
// path down a re-link whose idempotency key was already spent: the board
// deduped it, the bridge counted it as a write anyway, and every run
// thereafter persisted a fresh state note forever.
//
// Two independent fixes meet here: a deduped link note is not a write at any
// caller, and a retracted issue number takes the retraction path silently.
func TestDedupedLinkNoteIsNotABoardWrite(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.converge("operator", 4)
	if f.countIssues() != 1 {
		t.Fatalf("setup: %d issues", f.countIssues())
	}
	// The cleanup doctrine: close the issue on GitHub AND retract its link.
	f.humanClose(1, false, "mallory")
	if _, _, err := f.board().Note("cache-warm", kindLink, linkRetractPrefix+"1", bridgeAuthor, ""); err != nil {
		t.Fatal(err)
	}
	f.converge("operator", 5)

	before := f.eventCount()
	for i := 0; i < 3; i++ {
		r := f.syncOK("operator")
		if r.BoardWrites != 0 || r.GHMutations != 0 {
			t.Fatalf("run %d must be a fixed point, got %s", i, mustJSON(t, r))
		}
	}
	if got := f.eventCount(); got != before {
		t.Fatalf("the chain grew by %d events across three no-op runs — the state note is being "+
			"re-persisted forever", got-before)
	}
	// A deduped link note, directly: not a new event.
	id, deduped, err := f.board().LinkNote("cache-warm", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !deduped {
		t.Fatalf("the link note's idempotency key should already be spent (id %s)", id)
	}
}

// TestRetractedIssueNeverHijackWarnsAgain: after the cleanup doctrine runs,
// the duplicate issue still carries the `ledger-key:` line the BRIDGE wrote
// into it. Warning about that forever is the opposite of a cleanup that
// converges — the operator is told to fix something they already fixed.
func TestRetractedIssueNeverHijackWarnsAgain(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	// A duplicate, as a concurrent run would have left it: same stamp, same
	// hint, its own link note.
	dup := f.humanCreateIssue("warm the cache on boot", keyLinePrefix+"cache-warm\n"+bridgeStamp, "operator")
	if _, _, err := f.board().LinkNote("cache-warm", dup); err != nil {
		t.Fatal(err)
	}
	noisy := f.syncOK("operator")
	if !hasWarning(noisy, "more than one github-link note") {
		t.Fatalf("before cleanup the duplicate must be warned: %s", mustJSON(t, noisy))
	}

	// The cleanup doctrine: close the duplicate AND retract its link note.
	f.humanClose(dup, false, "mallory")
	if _, _, err := f.board().Note("cache-warm", kindLink,
		linkRetractPrefix+itoa(dup), bridgeAuthor, ""); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 3; run++ {
		r := f.syncOK("operator")
		for _, w := range r.Warnings {
			if strings.Contains(w, "more than one github-link note") ||
				strings.Contains(w, "already linked to") ||
				strings.Contains(w, "carries no bridge stamp") {
				t.Fatalf("run %d still complains about the cleaned-up duplicate: %q", run, w)
			}
		}
		if r.Divergences != 0 {
			t.Fatalf("run %d still counts the cleared divergence: %s", run, mustJSON(t, r))
		}
	}
	// And the retracted issue is never seeded as a key of its own either.
	for _, k := range f.keyList() {
		if k != "cache-warm" {
			t.Fatalf("the retracted duplicate was seeded as %q", k)
		}
	}
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if lm.ByKey["cache-warm"] != 1 {
		t.Fatalf("the established link must stay #1, got %v", lm.ByKey)
	}
	f.converge("operator", 3)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestClaimOnlyDrainCreatesNothing is rev 8.2's sharpened issue-creation
// rule. A claim has NO GitHub representation at all, so minting an issue for
// one shows a reader nothing — "a claim-only drain pushes nothing and creates
// nothing".
//
// The rule is about the DRAIN, not the level: a drain that also carries the
// key's `open` seed does have something to push, and does create.
func TestClaimOnlyDrainCreatesNothing(t *testing.T) {
	t.Run("a claim-only drain creates no issue and warns nothing", func(t *testing.T) {
		f := newIssueFixture(t)
		f.seed("cache-warm", "warm the cache on boot", "jesse")
		f.converge("operator", 3) // the seed is mirrored and behind the cursor
		// Start over with an empty repo, so the key exists with no issue.
		st := f.ghLoad()
		st.Issues = nil
		f.ghSave(st)
		// Retract the link so the key is genuinely issue-less rather than
		// broken-linked.
		if _, _, err := f.board().Note("cache-warm", kindLink, linkRetractPrefix+"1", bridgeAuthor, ""); err != nil {
			t.Fatal(err)
		}
		f.converge("operator", 4)

		f.setStatus("cache-warm", "in-progress", "picking this up", "ash")
		r := f.syncOK("operator")
		if f.countIssues() != 0 {
			t.Fatalf("a claim-only drain must create no issue, got %d: %s", f.countIssues(), mustJSON(t, r))
		}
		if r.GHMutations != 0 {
			t.Fatalf("a claim-only drain pushes nothing: %s", mustJSON(t, r))
		}
		for _, w := range r.Warnings {
			if strings.Contains(w, "dropped") {
				t.Fatalf("nothing was dropped — there was nothing to drop: %q", w)
			}
		}
	})

	t.Run("a drain carrying the seed does create", func(t *testing.T) {
		f := newIssueFixture(t)
		f.seed("cache-warm", "warm the cache on boot", "jesse")
		f.setStatus("cache-warm", "in-progress", "and claiming it immediately", "ash")
		r := f.syncOK("operator")
		if f.countIssues() != 1 {
			t.Fatalf("a drain carrying the key's seed must create its issue: %s", mustJSON(t, r))
		}
		if state, _ := f.issueState(1); state != "OPEN" {
			t.Fatalf("and leave it open, got %s", state)
		}
		if len(f.commentBodies(1)) != 0 {
			t.Fatalf("the claim message must not reach GitHub: %v", f.commentBodies(1))
		}
		f.converge("operator", 3)
	})

	t.Run("a terminal drain creates and closes", func(t *testing.T) {
		f := newIssueFixture(t)
		f.seed("cache-warm", "warm the cache on boot", "jesse")
		f.converge("operator", 3)
		st := f.ghLoad()
		st.Issues = nil
		f.ghSave(st)
		if _, _, err := f.board().Note("cache-warm", kindLink, linkRetractPrefix+"1", bridgeAuthor, ""); err != nil {
			t.Fatal(err)
		}
		f.converge("operator", 4)

		f.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")
		f.syncOK("operator")
		if f.countIssues() != 1 {
			t.Fatalf("a terminal drain must create its issue, got %d", f.countIssues())
		}
		fresh := f.ghLoad().Issues[0].Number
		if state, _ := f.issueState(fresh); state != "CLOSED" {
			t.Fatalf("and close it, got %s", state)
		}
		f.converge("operator", 3)
	})
}

// TestDroppedStateMirrorWarnsNamingTheEvent: a close that mirrors to nothing
// while the report stays clean is exactly the silent-failure shape the
// saturation rules exist to prevent.
func TestDroppedStateMirrorWarnsNamingTheEvent(t *testing.T) {
	f := newIssueFixture(t)
	// A statusless key with a fold-total rename has a title but no seed, and
	// a rename mirror for it has nowhere to land until it does.
	doc := f.ledgerOK("note", "-k", "handoff", "--key", "titleless", "-m", "no title anywhere", "--as", "jesse")
	noteID, _ := doc["id"].(string)

	r := f.syncOK("operator")
	if !hasWarning(r, noteID) {
		t.Fatalf("a dropped note must name its event: %s", mustJSON(t, r))
	}

	// And the state half: a key whose issue was deleted out from under it.
	g := newIssueFixture(t)
	g.seed("cache-warm", "warm the cache on boot", "jesse")
	g.converge("operator", 3)
	st := g.ghLoad()
	st.Issues = nil
	g.ghSave(st)
	g.setStatus("cache-warm", "closed", "fixed", "jesse", "commit:a1b2c3")

	r = g.syncOK("operator")
	closeID := ""
	for _, ev := range g.chain() {
		if ev.Type == "set" && ev.Fields["status"] == "closed" {
			closeID = ev.ID
		}
	}
	if closeID == "" {
		t.Fatal("setup: no close event")
	}
	if !hasWarning(r, closeID) {
		t.Fatalf("a dropped STATUS mirror must name its event id too: %s", mustJSON(t, r))
	}
	if !hasWarning(r, "no GitHub issue to land on") {
		t.Fatalf("and say why: %s", mustJSON(t, r))
	}
}
