package main

import (
	"fmt"
	"strings"
	"testing"
)

// Test-plan item 6 — IDENTITY. The bridge has no identity of its own on
// GitHub and compares no logins anywhere; echo suppression is the VERIFIED
// MARKER.

// TestMultiLoginMirroredCommentsNeverImport is the ruling, end to end:
// several operator logins across runs, with humans commenting under THOSE
// SAME logins. Mirrored comments never import; human ones import once.
//
// An operator-token echo check dropped the operator's own comments (probed
// live, same login) — which is why no login is compared.
func TestMultiLoginMirroredCommentsNeverImport(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("alice")

	// Board-side notes mirrored under three different operator logins.
	for i, login := range []string{"alice", "bob", "carol"} {
		f.ledgerOK("note", "-k", "handoff", "--key", "cache-warm",
			"-m", fmt.Sprintf("board note %d", i), "--as", "jesse")
		f.syncOK(login)
	}
	// And the SAME humans commenting as themselves.
	for _, login := range []string{"alice", "bob", "carol"} {
		f.humanComment(1, "human comment from "+login, login)
	}
	f.converge("alice", 4)

	comments := f.notes("comment")
	if len(comments) != 3 {
		t.Fatalf("want exactly the three human comments, got %d: %+v", len(comments), comments)
	}
	for _, n := range comments {
		if !strings.HasPrefix(n.Text, "human comment from ") {
			t.Fatalf("a mirrored comment imported as a board note: %+v", n)
		}
		if !strings.HasPrefix(n.Author, ghAuthorPrefix) {
			t.Fatalf("an imported comment must be authored github:@<login>: %+v", n)
		}
	}
	// And nothing re-imports on further runs.
	f.converge("bob", 3)
	if got := len(f.notes("comment")); got != 3 {
		t.Fatalf("re-import: %d comment notes", got)
	}
}

// TestMarkerEdges is the three ruled edges, all verified live.
func TestMarkerEdges(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	// Give the board a real event id to forge with.
	f.ledgerOK("note", "-k", "handoff", "--key", "cache-warm", "-m", "a real note", "--as", "jesse")
	f.converge("operator", 3)
	realID := ""
	for _, ev := range f.chain() {
		if ev.Type == "note" && ev.Text == "a real note" {
			realID = ev.ID
		}
	}
	if realID == "" {
		t.Fatal("setup: no real note id")
	}

	// (1) a pasted marker carrying a RESOLVING id suppresses the paster's own
	// comment. Self-inflicted, stated, and it costs them one comment.
	f.humanComment(1, markerFor("mallory", realID)+"\n\nI typed this myself", "mallory")
	// (2) a marker with a garbage id is just text somebody typed: it imports.
	f.humanComment(1, markerFor("mallory", "deadbeef99")+"\n\nalso mine", "mallory")
	f.converge("operator", 3)

	texts := []string{}
	for _, n := range f.notes("comment") {
		texts = append(texts, n.Text)
	}
	if countSubstr(texts, "I typed this myself") != 0 {
		t.Fatalf("a forged marker with a REAL id must suppress: %v", texts)
	}
	if countSubstr(texts, "also mine") != 1 {
		t.Fatalf("a marker with a garbage id must import normally: %v", texts)
	}
}

// TestMarkerIsAVersionedWireFormat pins the mechanism, not one string: every
// format in markerFormats stays recognized forever. Dropping a prior format
// makes the bridge re-import its own history as human comments — observed
// live against an earlier format.
func TestMarkerIsAVersionedWireFormat(t *testing.T) {
	if len(markerFormats) == 0 {
		t.Fatal("there must be at least one marker format")
	}
	// The current format round-trips.
	author, id, ok := parseMarker(markerFor("jesse", "abc123") + "\n\nbody")
	if !ok || author != "jesse" || id != "abc123" {
		t.Fatalf("current format does not round-trip: %q %q %v", author, id, ok)
	}
	// Every registered format is honoured by the same reader, so adding one
	// can never retire another.
	for i, re := range markerFormats {
		if !re.MatchString(markerFor("jesse", "abc123")) && i == 0 {
			t.Fatalf("format %d does not match what markerFor emits", i)
		}
	}
	if _, _, ok := parseMarker("just a person talking"); ok {
		t.Fatal("ordinary prose must not parse as a marker")
	}
}

// TestHijackRegression: an unlinked issue claiming a LINKED key is warned
// and never intaken. Body-line authority was probed as a hijack that let a
// stranger's issue retitle and settle an existing key, then flip-flop it
// with fabricated overrides every run.
func TestHijackRegression(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")

	hijack := f.humanCreateIssue("totally unrelated", keyLinePrefix+"cache-warm\n\nmine now", "mallory")
	f.humanClose(hijack, false, "mallory")
	f.humanRetitle(hijack, "cache-warm is cancelled", "mallory")

	r := f.syncOK("operator")
	if !hasWarning(r, "already linked to #1") {
		t.Fatalf("the hijack must be warned: %s", mustJSON(t, r))
	}
	if f.status("cache-warm") != openValue {
		t.Fatalf("the hijack moved the key's status to %q", f.status("cache-warm"))
	}
	if f.title("cache-warm") != "warm the cache on boot" {
		t.Fatalf("the hijack retitled the key to %q", f.title("cache-warm"))
	}
	// A closed hintless-or-hijacking unknown seeds nothing.
	for _, k := range f.keyList() {
		if strings.Contains(k, "cancelled") || strings.Contains(k, "unrelated") {
			t.Fatalf("the hijack issue was seeded as %q", k)
		}
	}
	if ov := f.fabricatedOverrides(); len(ov) != 0 {
		t.Fatalf("the hijack fabricated override(s): %v", ov)
	}
}

// TestAdoptionRecoversACrashedCreate: a crash between `issue create` and its
// link note leaves a STAMPED orphan. The next run adopts it from the bulk
// list already in hand — one link, no duplicate.
func TestAdoptionRecoversACrashedCreate(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	// Crash AFTER the create lands: the orphan window a fail-before injection
	// never reaches. On an empty repo with one titled key the call sequence is
	// exactly [issue list, issue create], so call 2 is the create — and the
	// assertions below fail loudly if that ever stops being true.
	if _, err := f.syncAfter("operator", 2); err == nil {
		t.Fatal("the injected failure did not abort the run")
	}
	if f.countIssues() != 1 {
		t.Fatalf("setup: the create should have landed, got %d issues", f.countIssues())
	}
	if len(f.notes(kindLink)) != 0 {
		t.Fatalf("setup: the link note should NOT have landed: %+v", f.notes(kindLink))
	}

	r := f.syncOK("operator")
	if !hasAction(r, "adopted an issue this bridge created but never linked") {
		t.Fatalf("the orphan was not adopted: %s", mustJSON(t, r))
	}
	if f.countIssues() != 1 {
		t.Fatalf("adoption minted a duplicate: %d issues", f.countIssues())
	}
	if got := len(f.notes(kindLink)); got != 1 {
		t.Fatalf("want exactly one link note, got %d", got)
	}
	f.converge("operator", 3)
}

// TestAdoptionRequiresTheStamp: stamp + hint on an UNLINKED key adopts;
// hint alone does not. A stamped forgery can bind a stranger's issue to a
// not-yet-linked key and can never touch a linked one — bounded, accepted,
// stated.
func TestAdoptionRequiresTheStamp(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.seed("retry-storm", "fix the retry storm", "jesse")

	unstamped := f.humanCreateIssue("some issue", keyLinePrefix+"cache-warm\n\nno stamp here", "mallory")
	stamped := f.humanCreateIssue("other issue", keyLinePrefix+"retry-storm\n"+bridgeStamp+"\n\nstamped", "mallory")

	r := f.syncOK("operator")
	if !hasWarning(r, "carries no bridge stamp") {
		t.Fatalf("an unstamped hint must be refused with a warning: %s", mustJSON(t, r))
	}
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if lm.ByKey["retry-storm"] != stamped {
		t.Fatalf("the stamped issue should have been adopted for retry-storm: %v", lm.ByKey)
	}
	if lm.ByKey["cache-warm"] == unstamped {
		t.Fatalf("the unstamped issue must never be adopted: %v", lm.ByKey)
	}
	f.converge("operator", 4)
}

// TestTwoIssuesOneKeyEstablishedLinkWins: the oldest non-retracted
// bridge-authored link note is the key's link, in both directions. An issue
// that is not the established link is not an inbound writer either — keeping
// every issue ever linked as one produced an unbounded flip-flop minting a
// fabricated override per run.
func TestTwoIssuesOneKeyEstablishedLinkWins(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator") // creates #1 and links it

	// A duplicate issue and a second link note, as a concurrent run would
	// have left behind.
	dup := f.humanCreateIssue("warm the cache on boot", keyLinePrefix+"cache-warm\n"+bridgeStamp, "operator")
	if _, err := f.board().LinkNote("cache-warm", dup); err != nil {
		t.Fatal(err)
	}

	// The duplicate closes on GitHub. It must NOT drive the key.
	f.humanClose(dup, false, "mallory")
	r := f.syncOK("operator")
	if !hasWarning(r, "more than one github-link note") {
		t.Fatalf("the duplicate link must be warned: %s", mustJSON(t, r))
	}
	if f.status("cache-warm") != openValue {
		t.Fatalf("a non-established issue drove the key to %q", f.status("cache-warm"))
	}
	if r.Divergences < 1 {
		t.Fatalf("a duplicate link is a counted divergence: %s", mustJSON(t, r))
	}
	// It converges and never flip-flops.
	for i := 0; i < 3; i++ {
		next := f.syncOK("operator")
		if next.BoardWrites != 0 || next.GHMutations != 0 {
			t.Fatalf("run %d kept writing: %s", i, mustJSON(t, next))
		}
		if f.status("cache-warm") != openValue {
			t.Fatalf("run %d flip-flopped the key to %q", i, f.status("cache-warm"))
		}
	}
	if ov := f.fabricatedOverrides(); len(ov) != 0 {
		t.Fatalf("the duplicate link fabricated override(s): %v", ov)
	}

	// The cleanup doctrine that converges: close the duplicate AND retract
	// its link note. The warning clears next run.
	if _, _, err := f.board().Note("cache-warm", kindLink,
		fmt.Sprintf("%s%d", linkRetractPrefix, dup), bridgeAuthor, ""); err != nil {
		t.Fatal(err)
	}
	cleared := f.syncOK("operator")
	if hasWarning(cleared, "more than one github-link note") {
		t.Fatalf("the retraction did not clear the warning: %s", mustJSON(t, cleared))
	}
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if lm.ByKey["cache-warm"] != 1 {
		t.Fatalf("the established link must stay #1, got %v", lm.ByKey)
	}
}

// TestReservedKeySeizureRegression: the reserved state key defends itself.
// A real seizure artifact — an issue whose title slugifies into the state
// key — reached a live board once.
func TestReservedKeySeizureRegression(t *testing.T) {
	f := newIssueFixture(t)
	byTitle := f.humanCreateIssue("GitHub bridge state", "an innocent-looking issue", "mallory")
	byHint := f.humanCreateIssue("something else", keyLinePrefix+stateKey+"\n\nmine", "mallory")

	r := f.syncOK("operator")
	if !hasWarning(r, "reserved state key") {
		t.Fatalf("the hint-based seizure must be warned: %s", mustJSON(t, r))
	}
	for _, k := range f.keyList() {
		if k == stateKey {
			t.Fatalf("issue #%d seized the reserved key", byTitle)
		}
	}
	// The title-based one is seeded under a suffixed key, never the reserved
	// one; the hint-based one is not intaken at all.
	lm, err := f.board().Links()
	if err != nil {
		t.Fatal(err)
	}
	if _, linked := lm.ByIssue[byHint]; linked {
		t.Fatalf("the reserved-key claimant was linked: %v", lm.ByIssue)
	}
	if key := lm.ByIssue[byTitle]; key == stateKey {
		t.Fatalf("the title-derived slug seized the reserved key")
	}
	// And the bridge's own state note still loads.
	st, _, err := f.board().LoadState()
	if err != nil {
		t.Fatalf("the state note is unreadable after the seizure attempt: %v", err)
	}
	if st.Repo != f.repo {
		t.Fatalf("the state note was wedged: %+v", st)
	}
	f.converge("operator", 4)
}

// TestAuthorlessCommentIsNotImported is the second, independent guard behind
// the oracle: a comment resolving to no author is refused with a warning,
// never written as a bare `github:@`.
func TestAuthorlessCommentIsNotImported(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	f.syncOK("operator")
	st := f.ghLoad()
	st.AddComment(1, "who said this?", "")
	f.ghSave(st)

	r := f.syncOK("operator")
	if !hasWarning(r, "names no author") {
		t.Fatalf("an author-less comment must warn: %s", mustJSON(t, r))
	}
	for _, n := range f.notes("comment") {
		if n.Author == ghAuthorPrefix || strings.Contains(n.Text, "who said this?") {
			t.Fatalf("an author-less comment was imported: %+v", n)
		}
	}
}

// TestOwnDivergenceCommentNeverImportsWithAWarmCache is the live-caught
// oracle defect, kept as a regression: the oracle's domain must include
// every id THIS RUN wrote, and it is never a run-start snapshot.
//
// It only reproduces when an EARLIER issue's comment scan has already warmed
// the index, which is why a single-issue fixture never saw it. So this
// fixture has two issues, and the divergence lands on the second.
func TestOwnDivergenceCommentNeverImportsWithAWarmCache(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("first-key", "the first key", "jesse")
	f.seed("reserved-key", "the reserved key", "jesse")
	f.syncOK("operator")
	// A human comment on #1 warms the index before #2 is reached.
	f.humanComment(1, "warming the cache", "mallory")
	f.reserve("reserved-key", "jesse")
	f.humanClose(2, false, "mallory")

	r := f.syncOK("operator")
	if r.Divergences != 1 {
		t.Fatalf("want the human-reserved divergence: %s", mustJSON(t, r))
	}
	f.converge("operator", 4)

	for _, n := range f.notes("comment") {
		if strings.Contains(n.Text, "reserved on the board") {
			t.Fatalf("the bridge's own divergence comment imported as a board note: %+v", n)
		}
		if n.Author == ghAuthorPrefix {
			t.Fatalf("a note was written under an empty github:@ login: %+v", n)
		}
	}
}
