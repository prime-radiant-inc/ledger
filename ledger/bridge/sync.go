package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// stateKey is the reserved board key the bridge parks its own state
	// under. It is a NOTE key, never a `set` key: notes create no board key
	// (a key exists once a set event names it), so the bridge's bookkeeping
	// can never show up in `ready`'s statusless attention list. Intake
	// defends it: no GitHub issue title may slugify into it, and a link hint
	// naming it is refused.
	stateKey  = "github-bridge-state"
	kindState = "bridge-state"
	kindLink  = "github-link"
	kindHand  = "handoff"
	// bridgeAuthor signs the bridge's own bookkeeping. Deliberately NOT in
	// the github:@ namespace: those events are echoes of GitHub actors.
	bridgeAuthor = "github-bridge"
	// ghAuthorPrefix is the namespace every GitHub-originated board write is
	// authored under; mirroring out skips exactly these.
	ghAuthorPrefix    = "github:@"
	linkPrefix        = "github: issues/"
	linkRetractPrefix = "github: retracted issues/"
	keyLinePrefix     = "ledger-key: "
	// bridgeStamp is the ADOPTION CREDENTIAL: the line the bridge writes
	// into every issue body it creates, and the second, independent copy of
	// the identity map. It is what lets a re-run adopt an issue it created
	// just before crashing instead of creating a second one.
	bridgeStamp = "<!-- ledger-bridge -->"
	// reopenText is Law 2's FIXED text for a convergence reopen. Never a
	// board message: the only messages a non-terminal board level carries
	// are claim and touch-base messages, and those never reach GitHub.
	reopenText = "reopened to match the board: this key is active there again."
	// claimValue is the one non-terminal transition with no GitHub
	// representation at all. `ledger create` pins the non-terminal vocabulary
	// to {open, in-progress}, so this is the whole of it.
	claimValue = "in-progress"
)

// Syncer holds one run's whole working set.
type Syncer struct {
	Board Board
	GH    GH
	// The two MIRRORED TERMINALS, configured rather than assumed: a legal
	// ready-capable board can call these done/dropped.
	Done       string // outbound: close (completed);  inbound: GitHub close COMPLETED
	NotPlanned string // outbound: close (not planned); inbound: close NOT_PLANNED

	state *State
	keys  map[string]*KeyState
	links *LinkMap
	// byKey/byIssue are links.ByKey/ByIssue, kept as fields because this run
	// extends them as it creates and adopts issues.
	byKey   map[string]int
	byIssue map[int]string
	// ghTitle/ghState/ghComments model GitHub's state as this run leaves it,
	// so the mirror never issues a mutation that would change nothing and
	// never posts a comment it already posted.
	ghTitle    map[int]string
	ghState    map[int]ghStateBits
	ghComments map[int][]ghComment
	// claimOnly names the keys whose drain carries status events that are ALL
	// `in-progress` — a CLAIM-ONLY drain. It pushes nothing and creates
	// nothing: a claim has no GitHub representation at all, so minting an
	// issue for one shows a reader nothing they did not already know. A drain
	// that also carries an `open` seed or a terminal close is not claim-only,
	// and does create.
	claimOnly map[string]bool
	// pendingStatus/pendingRename are the outbound half of echo safety, per
	// aspect, carrying the value the mirror is ABOUT to push. Intake runs
	// after the drain is READ: at intake time GitHub still shows the state
	// from BEFORE the board changes this run is about to mirror, so a board
	// close that has not reached GitHub yet reads to intake as "the issue is
	// still open" and would be reverted with a fabricated --override.
	pendingStatus map[string]string
	pendingRename map[string]string
	events        []Event
	nextCursor    string
	// chain is the ONE whole-chain read every per-run question shares: the
	// marker ORACLE's domain (every event id UNION every imported_from UNION
	// every id THIS RUN wrote), the derived idempotency index scoped exactly
	// as the tool's own dedupe is, and the inbound-seed map.
	chain *chainIndex
	// reDrained is set when the stored cursor no longer resolved and the run
	// re-drained from empty. MIRROREDVIEW has no meaning then (the drain IS
	// the whole chain), so the divergence warning is suppressed entirely
	// rather than accusing a human of every edit the bridge itself made.
	reDrained bool
	// mirroredStatus/Title are MIRROREDVIEW: the fold over the chain minus
	// this run's drain's NON-SUPPRESSED events.
	mirroredStatus map[string]string
	mirroredTitle  map[string]string
	// drainHadWork is true when the drain carried any event outbound
	// suppression does not skip.
	drainHadWork bool
	// knownIssues is every issue number the bulk listing returned. An
	// ESTABLISHED link naming an issue outside it is a BROKEN link — the
	// issue was deleted or transferred out of the repo — which is warned and
	// handed off once, and takes the key out of play for the run rather than
	// aborting on a 404 or minting a second issue for it.
	knownIssues map[int]bool
	// newRecords is this run's re-observed divergence set; state keeps only
	// these, so a divergence a human resolved stops being remembered.
	newRecords []Record
	report     *Report
}

// ghStateBits is one issue's state as the convergence axis reads it.
type ghStateBits struct {
	closed     bool
	notPlanned bool
}

// Run is one sync, in Law 1's order:
//
//	1  ledger sync             (always first; a stale replica mints duplicates)
//	1a post-sync preflight     (vocabulary, repo binding, link map, listing)
//	2  read the outbound drain (before any write — echo safety)
//	3  intake GitHub -> board  (per-aspect suppression, divergences noted)
//	4  mirror board -> GitHub
//	5  persist state if ANY of the four disjuncts holds
//	6  ledger push <slug>      (always, selective, last)
func (s *Syncer) Run() (*Report, error) {
	// Actions is never nil: a converged run must marshal it as [], not JSON
	// null, exactly as the parent tool's own empty lists do. A consumer that
	// iterates the report should not have to special-case the fixed point.
	s.report = &Report{OK: true, Repo: s.GH.Repo, Ledger: s.Board.Slug, Actions: []string{}}

	// (1) sync first. A failure on THIS board's slug aborts: acting on a
	// replica that could not merge the others is how duplicate issues get
	// minted. A failure on somebody else's slug is a warning — `ledger sync`
	// takes no slug selector, so a blanket abort would couple the bridge's
	// availability to every remote in the operator's store.
	mine, others := s.Board.Sync()
	if mine != nil {
		return nil, fmt.Errorf("ledger sync failed for '%s', refusing to run: %w", s.Board.Slug, mine)
	}
	for _, o := range others {
		s.report.warn("ledger sync: slug '%s' %s (%s) — not this bridge's board, continuing", o.Slug, o.Result, o.Detail)
	}

	// (1a) post-sync preflight. Every one of these reads board or GitHub
	// state, which is why none of them may run before the sync: a pre-sync
	// read of the bridge-state note on a fresh replica sees NOTHING and
	// waves through the exact mismatch the repo-binding check exists to
	// catch, and the saturation check needs a transport call Law 1 forbids
	// before sync.
	issues, err := s.preflight()
	if err != nil {
		return nil, err
	}

	// (2) read the drain before writing anything.
	loadedCursor := s.state.Cursor
	loadedRecords := s.state.Records
	if s.events, s.nextCursor, err = s.Board.Since(loadedCursor); err != nil {
		if code(err) != "reset_required" {
			return nil, err
		}
		// The stored cursor is not on this chain any more (export/import
		// re-mint, ref surgery). Re-draining from empty is safe precisely
		// because every mirror action is idempotent (Law 2): comments and
		// notes are marker/key-idempotent and state writes are convergent.
		s.report.warn("stored cursor %s no longer resolves (%v) — re-draining from the start of the chain", loadedCursor, err)
		s.reDrained = true
		if s.events, s.nextCursor, err = s.Board.Since(""); err != nil {
			return nil, err
		}
	}
	s.scanDrain()
	s.reportLinkConflicts()
	s.reportBrokenLinks()

	// (3) intake, (4) mirror.
	if err := s.intake(issues); err != nil {
		return nil, err
	}
	if err := s.mirror(); err != nil {
		return nil, err
	}

	// (5) persist state when ANY of the four disjuncts holds. A record no
	// longer observed is PRUNED, which keeps the state note bounded — and
	// pruning is itself a state change, or a run whose only change is a
	// RESOLVED divergence never lands the pruned record and the divergence's
	// next real recurrence is silently swallowed.
	s.report.Divergences = len(s.newRecords)
	changed := s.report.GHMutations > 0 || s.report.BoardWrites > 0 || s.drainHadWork ||
		!sameRecords(loadedRecords, s.newRecords)
	s.state.Records = s.newRecords
	s.report.Cursor = loadedCursor
	if changed {
		s.state.Cursor = s.nextCursor
		if _, err := s.Board.SaveState(s.state); err != nil {
			return nil, err
		}
		s.report.BoardWrites++
		s.report.Actions = append(s.report.Actions, "board: bridge-state cursor="+s.state.Cursor)
		s.report.Cursor = s.state.Cursor
	}

	// (6) push, always, selectively, last: link notes and bridge state that
	// never leave this replica make the sync-first law protect nothing.
	if err := s.Board.Push(); err != nil {
		s.report.warn("ledger push %s failed (%v) — retrying next run", s.Board.Slug, err)
	}
	return s.report, nil
}

// preflight is Law 1 step (1a): every check that reads board or GitHub
// state. It returns the bulk issue listing, which the rest of the run works
// from.
func (s *Syncer) preflight() ([]ghIssue, error) {
	if err := s.Board.CheckReadyCapable(); err != nil {
		return nil, err
	}
	var err error
	if s.keys, err = s.checkVocabulary(); err != nil {
		return nil, err
	}
	var foreignState []string
	if s.state, foreignState, err = s.Board.LoadState(); err != nil {
		return nil, err
	}
	for _, f := range foreignState {
		s.report.warn("bridge-state note %s is not authored %s — ignored; a maintainer should retract it "+
			"with a corrective note authored %s", f, bridgeAuthor, bridgeAuthor)
	}
	// One board, one repo. Multi-repo bridging is v2, and re-binding a board
	// to a second repo would re-import the first repo's mirrored history as
	// human comments (the marker is not board-scoped).
	if s.state.Repo != "" && s.state.Repo != s.GH.Repo {
		return nil, fmt.Errorf("board '%s' is bridged to %s, not %s — one board binds to one repo, permanently. "+
			"Point --repo at %s, or bridge %s from a different board",
			s.Board.Slug, s.state.Repo, s.GH.Repo, s.state.Repo, s.GH.Repo)
	}
	s.state.Repo = s.GH.Repo

	if s.links, err = s.Board.Links(); err != nil {
		return nil, err
	}
	s.byKey, s.byIssue = s.links.ByKey, s.links.ByIssue
	for _, f := range s.links.Foreign {
		s.report.warn("github-link note %s is not authored %s — ignored; the link map is bridge-authored only", f, bridgeAuthor)
	}

	issues, err := s.GH.List()
	if err != nil {
		return nil, err
	}
	if len(issues) >= s.GH.ListLimit {
		// Outside the window every bulk map is zero-valued, which silently
		// disables the comment dedupe, the state diff and adoption — so a
		// saturated run mints duplicate comments and un-adoptable orphans. A
		// loud stop beats both. The fix is a CONSTANT, not a project: `gh`
		// paginates internally, so a bigger --list-limit just works.
		return nil, fmt.Errorf("gh issue list returned %d issues against a --list-limit of %d: "+
			"the listing saturates its window, and outside it the bridge cannot dedupe comments, "+
			"diff state, or adopt its own issues — refusing to run blind. "+
			"Re-run with --list-limit %d (gh paginates internally; the flag is the escape hatch)",
			len(issues), s.GH.ListLimit, s.GH.ListLimit*2)
	}

	s.ghTitle, s.ghState, s.ghComments = map[int]string{}, map[int]ghStateBits{}, map[int][]ghComment{}
	s.knownIssues = map[int]bool{}
	for _, is := range issues {
		s.knownIssues[is.Number] = true
	}
	for i := range issues {
		is := &issues[i]
		// Per-issue comment saturation. The BULK listing returns the OLDEST
		// 100 comments per issue and silently omits the rest, so an issue at
		// exactly the cap is unread, not read — a busy issue stops importing
		// forever with a clean 0/0 report, and crash re-runs double-post past
		// the cap. Re-read it COMPLETELY before any dedupe, intake or posting
		// decision touches it.
		if len(is.Comments) == BulkCommentCap {
			full, err := s.GH.ViewComments(is.Number)
			if err != nil {
				return nil, err
			}
			is.Comments = full
		}
		s.ghTitle[is.Number] = is.Title
		s.ghState[is.Number] = ghStateBits{closed: is.State == "CLOSED", notPlanned: is.StateReason == "NOT_PLANNED"}
		s.ghComments[is.Number] = is.Comments
	}
	return issues, nil
}

// pinnedNonTerminal is the non-terminal status vocabulary `ledger create`
// pins on every ready-capable board — verified: a third non-terminal value
// is refused at create, and so is removing either of these two. It is the
// whole TERMINALITY ORACLE: declared-and-outside-this-set is terminal, and
// there is no read that says so more directly.
var pinnedNonTerminal = []string{openValue, "in-progress"}

func isTerminalValue(v string) bool {
	for _, nt := range pinnedNonTerminal {
		if v == nt {
			return false
		}
	}
	return v != ""
}

// checkVocabulary reads the board once and applies the three vocabulary
// refusals. Vocabulary is CONFIGURED, never assumed: done/dropped is as
// legal a ready-capable board as open/closed/wontfix.
//
// The remedy tells the truth. A ready-capable board's status vocabulary is
// IMMUTABLE — `vocab add` is refused by the tool itself — so the fix is
// always the board's own values as flags, or export/import to a re-declared
// board. Never `vocab add`.
func (s *Syncer) checkVocabulary() (map[string]*KeyState, error) {
	keys, schema, err := s.Board.Snapshot()
	if err != nil {
		return nil, err
	}
	declared := schema["status"]
	if len(declared) == 0 {
		return nil, fmt.Errorf("board '%s' declares no status vocabulary — the bridge needs a ready-capable board", s.Board.Slug)
	}
	isDeclared := map[string]bool{}
	for _, v := range declared {
		isDeclared[v] = true
	}
	remedy := fmt.Sprintf("pass the board's own values (--done <value> --not-planned <value>); "+
		"a ready-capable board's status vocabulary is immutable, so `vocab add` is refused by the tool — "+
		"the only other fix is export/import to a re-declared board. Declared on '%s': %s",
		s.Board.Slug, strings.Join(declared, ", "))

	// (1) a board lacking a flag's value.
	var missing []string
	for _, fv := range []struct{ flag, value string }{{"--done", s.Done}, {"--not-planned", s.NotPlanned}} {
		if !isDeclared[fv.value] {
			missing = append(missing, fmt.Sprintf("%s %s", fv.flag, fv.value))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("board '%s' does not declare %s — %s",
			s.Board.Slug, strings.Join(missing, ", "), remedy)
	}

	// (2) a flag naming a NON-TERMINAL value by the oracle. Membership alone
	// is not enough: `--done in-progress` passes a membership check
	// (in-progress IS declared) and then CLOSES GitHub issues on a
	// non-terminal board state.
	var wrong []string
	for _, fv := range []struct{ flag, value string }{{"--done", s.Done}, {"--not-planned", s.NotPlanned}} {
		if !isTerminalValue(fv.value) {
			wrong = append(wrong, fmt.Sprintf("%s %s is non-terminal", fv.flag, fv.value))
		}
	}
	if s.Done == s.NotPlanned {
		wrong = append(wrong, fmt.Sprintf("--done and --not-planned both name %s", s.Done))
	}
	if len(wrong) > 0 {
		sort.Strings(wrong)
		return nil, fmt.Errorf("board '%s': %s — a ready-capable board's non-terminal status vocabulary is "+
			"pinned to %s at create, so every other declared value is terminal; --done and --not-planned "+
			"must name two DISTINCT terminal values. %s",
			s.Board.Slug, strings.Join(wrong, ", "), strings.Join(pinnedNonTerminal, "/"), remedy)
	}

	// (3) a board whose TERMINAL SET exceeds the two MIRRORED TERMINALS.
	// v1 maps exactly two. A third terminal otherwise mirrors to nothing
	// forever with zero signal, and intake then overwrites the maintainer's
	// value on the next human close (probed end to end, exit 0 throughout).
	var unmapped []string
	for _, v := range declared {
		if isTerminalValue(v) && v != s.Done && v != s.NotPlanned {
			unmapped = append(unmapped, v)
		}
	}
	if len(unmapped) > 0 {
		sort.Strings(unmapped)
		return nil, fmt.Errorf("board '%s' declares terminal status value(s) %s that no flag maps: "+
			"v1 mirrors exactly two terminals (--done %s, --not-planned %s), so %s would mirror to nothing "+
			"forever and intake would overwrite it on the next GitHub close. Multi-terminal mapping is v2. %s",
			s.Board.Slug, strings.Join(unmapped, ", "), s.Done, s.NotPlanned,
			strings.Join(unmapped, ", "), remedy)
	}
	return keys, nil
}

// isTerminal answers the bridge's own question: is this board value one of
// the two MIRRORED TERMINALS? The vocabulary refusals make the board's whole
// terminal set exactly {Done, NotPlanned}, so this and the oracle agree.
func (s *Syncer) isTerminal(value string) bool {
	return value == s.Done || value == s.NotPlanned
}

// ghValue is the GitHub-side CURRENT VALUE for Law 2's convergence: the
// board value this issue's (state, stateReason) maps to under the Status
// mapping — never the bare open/closed bit.
//
// The bare-bit reading is what let a done<->not-planned reclassification
// never reach GitHub, after which intake reverted the maintainer's
// reclassification with a fabricated `override: settled` and fabricated
// evidence, once per human attempt, unbounded.
//
// An OPEN issue maps to "" — the two non-terminal board values are
// indistinguishable from GitHub, and that indistinguishability IS Law 6's
// mirrors-to-nothing rule.
func (s *Syncer) ghValue(bits ghStateBits) string {
	if !bits.closed {
		return ""
	}
	if bits.notPlanned {
		return s.NotPlanned
	}
	return s.Done
}

// boardValueForMirror is the board level as the convergence axis sees it:
// the value itself when terminal, "" when not.
func (s *Syncer) boardValueForMirror(level string) string {
	if s.isTerminal(level) {
		return level
	}
	return ""
}

// scanDrain walks the drain once: it records which keys carry un-mirrored
// board changes (per aspect, with the value about to be pushed), counts the
// suppressed github:@ authors, and answers whether the drain held anything
// outbound suppression does not skip.
//
// That last answer is Law 1's third persistence disjunct, and it matters
// more than it looks. The cursor advances only on a run that changed
// something — otherwise the state note the cursor rides in lands after the
// cursor it records and the bridge never reaches a fixed point. But a board
// event that mirrors to NOTHING (status=in-progress, a labels edit) changes
// nothing either, so without this disjunct it would sit in the drain forever
// — and, being un-mirrored, would suppress that key's intake aspect forever
// with it: a claimed key that stops accepting GitHub closes, permanently.
func (s *Syncer) scanDrain() {
	s.pendingStatus, s.pendingRename = map[string]string{}, map[string]string{}
	s.claimOnly = map[string]bool{}
	sawNonClaim := map[string]bool{}
	for _, ev := range s.events {
		if s.suppressedAuthor(ev) {
			if strings.HasPrefix(ev.Author, ghAuthorPrefix) {
				if s.report.SuppressedAuthors == nil {
					s.report.SuppressedAuthors = map[string]int{}
				}
				s.report.SuppressedAuthors[ev.Author]++
			}
			continue
		}
		s.drainHadWork = true
		if ev.Type != "set" || ev.Key == "" {
			continue
		}
		if ev.Rename != "" {
			s.pendingRename[ev.Key] = ev.Rename
		}
		if v := ev.Fields["status"]; v != "" {
			s.pendingStatus[ev.Key] = v
			if v == claimValue {
				if !sawNonClaim[ev.Key] {
					s.claimOnly[ev.Key] = true
				}
			} else {
				sawNonClaim[ev.Key] = true
				delete(s.claimOnly, ev.Key)
			}
		}
	}
}

// suppressedAuthor is the outbound echo rule, and it is ONE rule: skip
// events whose AUTHOR is the github:@ namespace (everything intake wrote) or
// the bridge itself (all of its bookkeeping) — full stop.
//
// By AUTHOR, never by kind. A kind list silently ate HUMAN `handoff` notes —
// the issues spec's designated reclaim channel and the highest-value note
// class on the board — while mirroring every other kind. Author suppression
// is strictly simpler and mirrors a human's handoff like any other note.
//
// Consequence, stated: a FORGED bookkeeping note (a human-authored
// github-link or bridge-state note) is inert on the board but mirrors to
// GitHub as an ordinary comment. The poisoning becomes visible in two
// places, one public; accepted.
func (s *Syncer) suppressedAuthor(ev Event) bool {
	return strings.HasPrefix(ev.Author, ghAuthorPrefix) || ev.Author == bridgeAuthor
}

// mirrorable narrows the drain to the event types the mirror acts on at all.
func (s *Syncer) mirrorable(ev Event) bool {
	return !s.suppressedAuthor(ev) && (ev.Type == "set" || ev.Type == "note")
}

// reportLinkConflicts makes the one-link-per-key rule visible. A key whose
// chain carries another link note naming a DIFFERENT issue is never
// repointed — the ESTABLISHED link stands — and the conflict is warned and
// handed off once, then counted, like every other standing divergence.
//
// The cleanup doctrine that actually converges: close the duplicate issue on
// GitHub AND write its retraction note. The warning clears next run.
func (s *Syncer) reportLinkConflicts() {
	for _, key := range sortedKeys(s.links.Changed) {
		others := s.links.Changed[key]
		explain := fmt.Sprintf("board key '%s' has more than one github-link note: it is linked to #%d, "+
			"and the chain also names %s. The established link stands; the bridge never repoints a key. "+
			"To clear this, close the duplicate issue on GitHub and write its retraction note: "+
			"ledger note -k %s --key %s -m '%s<n>' --as %s",
			key, s.byKey[key], issueList(others), kindLink, key, linkRetractPrefix, bridgeAuthor)
		s.report.warn("%s", explain)
		// Recorded against the ESTABLISHED issue, so the record is stable
		// however many competing notes arrive. Aspect-less: a link conflict
		// is not about status or title.
		s.refuse(Record{Issue: s.byKey[key], Class: classLink, Observed: issueList(others)}, key, explain)
	}
}

// reportBrokenLinks is the named out-of-scope case with a defined behaviour:
// issue deletion or transfer out of the repo. The key's ESTABLISHED link
// names an issue the listing does not contain, so there is nothing to mirror
// onto and nothing to intake from.
//
// Warning plus a ONE-TIME handoff note, and the key stands down for the run
// — never a 404 abort (which would take the whole board down for one deleted
// issue) and never a fresh create (which would silently re-mint the issue
// under a second number while the link still names the first).
func (s *Syncer) reportBrokenLinks() {
	for _, key := range sortedKeys(s.byKey) {
		n := s.byKey[key]
		if s.knownIssues[n] {
			continue
		}
		explain := fmt.Sprintf("board key '%s' is linked to #%d, which this repo's issue listing does not "+
			"contain — the issue was deleted or transferred. The bridge will neither mirror to it nor "+
			"create a replacement. To re-home the key, open a new issue and write the retraction and the "+
			"new link: ledger note -k %s --key %s -m '%s%d' --as %s",
			key, n, kindLink, key, linkRetractPrefix, n, bridgeAuthor)
		s.report.warn("%s", explain)
		s.refuse(Record{Issue: n, Class: classLink, Observed: "missing"}, key, explain)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func issueList(ns []int) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("#%d", n))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// ---- Level 2: GitHub -> ledger, additive only ----

func (s *Syncer) intake(issues []ghIssue) error {
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	for _, is := range issues {
		key, ok, err := s.resolveKey(is)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ks := s.keys[key]
		if ks == nil {
			s.report.warn("issue #%d resolved to key '%s', which is not on this board — skipped", is.Number, key)
			continue
		}
		if err := s.intakeTitle(is, ks); err != nil {
			return err
		}
		if err := s.intakeState(is, ks); err != nil {
			return err
		}
		if err := s.intakeComments(is, ks); err != nil {
			return err
		}
	}
	return nil
}

// resolveKey answers "which board key is this issue?".
//
// The board's github-link note is THE authority. The body's `ledger-key:`
// line is only a HINT, honored only when the link note agrees or when the
// bridge's own STAMP proves the bridge created the issue: anyone with a
// GitHub account can type a key line, and body-line authority was probed as
// a hijack that let a stranger's issue drive an existing key.
func (s *Syncer) resolveKey(is ghIssue) (string, bool, error) {
	if key, ok := s.byIssue[is.Number]; ok {
		return key, true, nil
	}
	// A RETRACTED issue number takes the retraction path silently. It is the
	// bridge's own cleaned-up artifact — its body still carries the stamp and
	// the `ledger-key:` line the bridge itself wrote — so every other branch
	// below would fire on it forever, which is the opposite of a cleanup
	// doctrine that converges.
	if s.links.Retracted[is.Number] {
		return "", false, nil
	}
	// The bridge's OWN prior seed for this issue, recovered from the chain.
	// This is consulted BEFORE any key is minted: a crash between the seed
	// and its link note otherwise mints a second, suffixed key on the next
	// run and a second GitHub issue for the first, permanently.
	if key, err := s.seededKeyFor(is.Number); err != nil {
		return "", false, err
	} else if key != "" && s.keys[key] != nil {
		if !s.byIssueFree(is.Number, key) {
			return "", false, nil
		}
		id, deduped, err := s.Board.LinkNote(key, is.Number)
		if err != nil {
			return "", false, err
		}
		if !deduped {
			s.report.board("link %s <-> #%d (recovered a seed this bridge wrote but never linked)", key, is.Number)
		}
		_ = id
		s.byKey[key], s.byIssue[is.Number] = is.Number, key
		return key, true, nil
	}
	hint := ledgerKeyFromBody(is.Body)
	stamped := s.adoptable(is)
	switch {
	case hint == stateKey:
		// The reserved state key defends itself: a real seizure artifact
		// (an issue titled so that it slugified into the state key) reached
		// a live board once.
		s.report.warn("issue #%d claims the bridge's reserved state key '%s' — refused", is.Number, stateKey)
		return "", false, nil
	case hint != "" && s.byKey[hint] != 0:
		// The probed hijack: an unlinked issue claiming a LINKED key.
		s.report.warn("issue #%d claims key '%s', which is already linked to #%d — the linked issue wins; "+
			"a human must resolve this", is.Number, hint, s.byKey[hint])
		return "", false, nil
	case hint != "" && s.keys[hint] != nil && stamped:
		// ADOPTION: the stamp AND the key hint are present and the key has
		// no linked issue, so this is an issue the bridge created and then
		// crashed before linking. Recovered from the bulk list already in
		// hand — no search call.
		_, deduped, err := s.Board.LinkNote(hint, is.Number)
		if err != nil {
			return "", false, err
		}
		if !deduped {
			s.report.board("link %s <-> #%d (adopted an issue this bridge created but never linked)", hint, is.Number)
		}
		s.byKey[hint], s.byIssue[is.Number] = is.Number, hint
		return hint, true, nil
	case hint != "" && s.keys[hint] != nil:
		s.report.warn("issue #%d claims existing key '%s' but carries no bridge stamp and the board has no "+
			"link note for it — not intaken, not seeded over", is.Number, hint)
		return "", false, nil
	case hint != "" && stamped:
		// The STAMPED-FOREIGN-HINT refusal, and the repo-side half of
		// one-repo-one-board. The stamp is proof some bridge created this
		// issue, and the hint names a key that is not on THIS board — so it
		// provably belongs to another board's bridge. Seeding it is how a
		// second board was observed live rewriting a bound repo's
		// `ledger-key:` lines, which destroys the stamp's crash-recovery
		// guarantee for every orphan still in the adoption window.
		//
		// This is the hijack rule's conservatism applied to the stamp: a
		// refusal, granting the body no new authority.
		s.report.warn("issue #%d carries the bridge stamp but names ledger-key '%s', which is not on this "+
			"board — it belongs to another board's bridge; skipped, not seeded and not rewritten. "+
			"One repo binds to one board: bridge %s from the board that owns it, or retract the link there",
			is.Number, hint, s.GH.Repo)
		return "", false, nil
	}
	// An unlinked, unclaimed issue (or one naming a key this board does not
	// have): additive intake, a new board key — but only while it is OPEN.
	// A CLOSED, hintless unknown imports NOTHING: stripping a duplicate's
	// hint used to mint a permanent junk key.
	if is.State != "OPEN" {
		return "", false, nil
	}
	if hint != "" {
		s.report.warn("issue #%d names ledger-key '%s', which is not on this board — seeding it as a new key instead",
			is.Number, hint)
	}
	key, ok, err := s.seedFromIssue(is)
	return key, ok, err
}

// byIssueFree guards the recovered-seed path against binding an issue that
// some OTHER key has already established a link to.
func (s *Syncer) byIssueFree(n int, key string) bool {
	if have, ok := s.byIssue[n]; ok && have != key {
		s.report.warn("issue #%d was seeded as key '%s' but is established to '%s' — leaving the established "+
			"link alone; a human must resolve this", n, key, have)
		return false
	}
	if have, ok := s.byKey[key]; ok && have != n {
		s.report.warn("key '%s' was seeded from issue #%d but is established to #%d — leaving the established "+
			"link alone; a human must resolve this", key, n, have)
		return false
	}
	return true
}

// seededKeyFor answers "did an earlier run of this bridge seed a board key
// for this issue?" off the shared whole-chain read.
func (s *Syncer) seededKeyFor(n int) (string, error) {
	if err := s.loadChainIndex(); err != nil {
		return "", err
	}
	return s.chain.seededFrom[n], nil
}

// adoptable is the crash-window recogniser. A human who copies the stamp
// into their own issue binds that issue to an UNLINKED key — a forged
// credential, self-inflicted, exactly the shape of the comment-marker
// ruling. It can never touch a key that already has an issue. Bounded,
// accepted, stated.
func (s *Syncer) adoptable(is ghIssue) bool {
	return strings.Contains(is.Body, bridgeStamp)
}

// seedFromIssue is the additive intake of a brand-new GitHub issue: a board
// key seeded under the GitHub author's namespaced identity, linked in both
// directions. The seeded key's TITLE is the GitHub issue title (it is the
// seed `-m`), and the SLUG is derived from that title.
func (s *Syncer) seedFromIssue(is ghIssue) (string, bool, error) {
	if is.Author.Login == "" {
		// A deleted GitHub account leaves an author-less issue. There is
		// nobody to attribute the seed to, and `--as github:@` is a
		// meaningless identity to put on a board — so warn and SKIP, exactly
		// as this issue's comments already do. Propagating the error instead
		// aborts the run, and since the issue never goes away that bricks
		// every future run too.
		s.report.warn("issue #%d names no author (a deleted account?) — not seeded; "+
			"a maintainer can open a replacement issue if the work still matters", is.Number)
		return "", false, nil
	}
	key := s.uniqueKey(slugify(is.Title), is.Number)
	as := ghAuthorPrefix + is.Author.Login
	id, err := s.Board.Seed(key, is.Title, as, is.Number)
	if err != nil {
		return "", false, fmt.Errorf("seeding issue #%d as '%s': %w", is.Number, key, err)
	}
	s.report.board("seed %s status=%s by %s (from #%d)", key, openValue, as, is.Number)
	s.keys[key] = &KeyState{Key: key, Title: is.Title, Status: openValue, StatusID: id}
	// The seed is now on the chain under gh-issue-<n>, so a crash before the
	// link note lands is recoverable on the next run. Register it in this
	// run's view too, since the chain index is loaded once.
	if s.chain != nil {
		if _, seen := s.chain.seededFrom[is.Number]; !seen {
			s.chain.seededFrom[is.Number] = key
		}
	}

	_, deduped, err := s.Board.LinkNote(key, is.Number)
	if err != nil {
		return "", false, err
	}
	if !deduped {
		s.report.board("link %s <-> #%d", key, is.Number)
	}
	s.byKey[key], s.byIssue[is.Number] = is.Number, key

	if err := s.GH.EditBody(is.Number, s.issueBody(key, is.Body)); err != nil {
		return "", false, err
	}
	s.report.gh("#%d body carries ledger-key: %s", is.Number, key)
	return key, true, nil
}

// intakeTitle is a GitHub title edit becoming a rename event on the board,
// attributed via Law 4's timeline and CAS'd against the key's own rename
// stream (Law 5). Law 2's convergence applies: it fires only when the two
// sides' current titles differ.
func (s *Syncer) intakeTitle(is ghIssue, ks *KeyState) error {
	if is.Title == ks.Title {
		return nil
	}
	if pending, ok := s.pendingRename[ks.Key]; ok {
		// The board renamed this key and the mirror has not pushed it yet,
		// so GitHub's title is stale, not a competing edit — UNLESS GitHub
		// is showing something the bridge never put there, which is a person
		// retitling concurrently and about to be overwritten.
		_, mirroredTitle, err := s.mirroredView()
		if err != nil {
			return err
		}
		// An empty mirrored title means the issue was created inside this
		// drain, so GitHub is showing whatever title the creating call used
		// — nothing a person could have overwritten yet.
		if is.Title != pending && mirroredTitle[ks.Key] != "" && is.Title != mirroredTitle[ks.Key] {
			s.suppressionNote(is.Number, ks.Key, aspectTitle, is.Title,
				fmt.Sprintf("GitHub title %q was discarded: the board is pushing %q for '%s' this run",
					is.Title, pending, ks.Key))
		}
		return nil
	}
	actor, ok, err := s.GH.LastActor(is.Number, "renamed")
	if err != nil {
		return err
	}
	if !ok {
		actor = is.Author.Login
		s.report.warn("issue #%d: no 'renamed' timeline event found — attributing the retitle to the issue author @%s", is.Number, actor)
	}
	as := ghAuthorPrefix + actor
	if _, err := s.Board.Rename(ks.Key, is.Title, as, ks.RenameID); err != nil {
		switch code(err) {
		case "claim_lost":
			s.report.warn("issue #%d: rename of '%s' lost its CAS (%v) — the board was renamed concurrently; not retried",
				is.Number, ks.Key, err)
			return nil
		case "needs_override":
			s.refuse(Record{Issue: is.Number, Class: classRefusal, Aspect: aspectTitle, Observed: is.Title}, ks.Key,
				fmt.Sprintf("GitHub retitled this to %q; the board key '%s' is reserved and the bridge will not override",
					is.Title, ks.Key))
			return nil
		}
		return fmt.Errorf("renaming '%s' from #%d: %w", ks.Key, is.Number, err)
	}
	s.report.board("rename %s -> %q by %s (from #%d)", ks.Key, is.Title, as, is.Number)
	ks.Title = is.Title
	return nil
}

// intakeState turns a GitHub close/reopen into the matching guarded board
// write, CAS'd against the key's current status event.
//
// THE REOPEN TRIGGER, stated: a reopen is written only when the issue is
// OPEN **and** the board's current status is TERMINAL. An open issue over a
// non-terminal board status is the resting state of every live key, not a
// reopen, and is never written — the write-on-any-difference reading
// un-claimed every claimed key with a fabricated auto-override per run.
func (s *Syncer) intakeState(is ghIssue, ks *KeyState) error {
	bits := s.ghState[is.Number]
	want := ""
	switch {
	case bits.closed:
		want = s.ghValue(bits)
	case s.isTerminal(ks.Status):
		want = openValue // reopened on GitHub while the board thinks it is settled
	}
	if want == "" || want == ks.Status {
		return nil
	}
	if pending, ok := s.pendingStatus[ks.Key]; ok {
		// The board changed status and the mirror has not pushed it yet, so
		// GitHub's state is the stale one — UNLESS it is not what the bridge
		// last put there, which means a person acted on GitHub and is about
		// to be overwritten.
		mirroredStatus, _, err := s.mirroredView()
		if err != nil {
			return err
		}
		if want != pending && want != mirroredStatus[ks.Key] {
			s.suppressionNote(is.Number, ks.Key, aspectStatus, want,
				fmt.Sprintf("GitHub state %s was discarded: the board is pushing status=%s for '%s' this run",
					want, pending, ks.Key))
		}
		return nil
	}
	event := "closed"
	if want == openValue {
		event = "reopened"
	}
	actor, ok, err := s.GH.LastActor(is.Number, event)
	if err != nil {
		return err
	}
	if !ok {
		actor = is.Author.Login
		s.report.warn("issue #%d: no '%s' timeline event found — attributing to the issue author @%s", is.Number, event, actor)
	}
	as := ghAuthorPrefix + actor
	msg := fmt.Sprintf("%s on GitHub by @%s (#%d)", event, actor, is.Number)
	// Evidence rides a completed close and nothing else: the issues spec
	// calls evidence on a not-planned close "pasted-string theater" — a
	// decision not to do something has no artifact.
	var evidence []string
	if want == s.Done {
		evidence = []string{fmt.Sprintf("gh:%s#%d", s.GH.Repo, is.Number)}
	}
	id, how, err := s.Board.SetStatus(ks.Key, want, ks.StatusID, msg, as, evidence, s.isTerminal(want))
	if err != nil {
		r := Record{Issue: is.Number, Class: classRefusal, Aspect: aspectStatus, Observed: want}
		be, _ := err.(*BoardErr)
		switch {
		case be != nil && be.Code == "needs_override":
			s.refuse(r, ks.Key, fmt.Sprintf("GitHub %s this (status=%s); the board key '%s' is reserved for a person "+
				"and the bridge will not override", event, want, ks.Key))
		case be != nil && be.Code == "claim_lost":
			s.refuse(r, ks.Key, fmt.Sprintf("GitHub %s this (status=%s) but the board key '%s' moved first (%s) — "+
				"never re-close blind", event, want, ks.Key, be.Message))
		default:
			s.refuse(r, ks.Key, fmt.Sprintf("the bridge could not apply status=%s from #%d: %v", want, is.Number, err))
		}
		return nil
	}
	suffix := ""
	if how != "" {
		suffix = " [" + how + "]"
	}
	s.report.board("set %s status=%s by %s (from #%d)%s", ks.Key, want, as, is.Number, suffix)
	ks.Status, ks.StatusID = want, id
	return nil
}

// intakeComments imports GitHub comments as board notes. Law 2: the board
// write carries `gh-comment-<rest-id>` as its idempotency key — the REST id
// parsed from the comment url's `#issuecomment-<id>` fragment, NOT the
// `--json` id field, which is a GraphQL node id and is not ordered.
//
// The bridge's own mirrored comments are skipped by VERIFIED MARKER: the
// marker names the board event it echoes, and that event id must resolve on
// this chain. No login is compared — the bridge must work under several
// logins, all of whom also comment as people.
func (s *Syncer) intakeComments(is ghIssue, ks *KeyState) error {
	for _, c := range s.ghComments[is.Number] {
		echo, err := s.isBridgeEcho(c.Body)
		if err != nil {
			return err
		}
		if echo {
			continue
		}
		if c.Author.Login == "" {
			// An author-less comment has nobody to attribute the note to,
			// and `--as github:@` is a meaningless identity to put on the
			// board. The second, independent guard behind the oracle.
			s.report.warn("issue #%d: a comment at %s names no author — not imported", is.Number, c.URL)
			continue
		}
		rest := c.restID()
		if rest == "" {
			s.report.warn("issue #%d: a comment has no REST id in its url (%q) — importing it without an idempotency key",
				is.Number, c.URL)
		}
		as := ghAuthorPrefix + c.Author.Login
		idem := ""
		if rest != "" {
			idem = commentIdem(rest)
			// Scoped exactly as the tool's dedupe is: (author, kind, key,
			// idempotency-key). The bare key string was a censorship
			// primitive — a decoy note under ANY author, kind or key made
			// the bridge skip a real comment before the board ever saw it,
			// which also deleted the `deduped: true` impersonation detector.
			spent, err := s.idemSpent(idemScope{as, "comment", ks.Key, idem})
			if err != nil {
				return err
			}
			if spent {
				continue // already on the chain; the board would only dedupe it
			}
		}
		_, deduped, err := s.Board.Note(ks.Key, "comment", c.Body, as, idem)
		if err != nil {
			return fmt.Errorf("importing comment %s on #%d: %w", c.URL, is.Number, err)
		}
		if deduped {
			continue // the board recognised it: nothing changed, nothing to report
		}
		s.report.board("note %s kind=comment by %s (from #%d)", ks.Key, as, is.Number)
	}
	return nil
}

// ---- Level 1: ledger -> GitHub mirror ----

// mirror pushes the drain out to GitHub.
//
// Comments and notes are EVENT-driven (each is a thing that was said, and
// the marker makes each idempotent). STATE is a function of the board's
// CURRENT FOLD, never of its history: only the drain's final status and
// final title per key are candidates, the value pushed is the key's folded
// value after intake, and the push happens only when GitHub's current value
// differs on the Status mapping's axis.
//
// Both halves of that were live-caught. Replaying state per event means a
// drain carrying close → reopen → close pushes a reopen and two closes; on a
// re-drain run the drain IS the whole chain, so the bridge would replay
// every state change the board has ever made. And taking the level from the
// DRAIN rather than the fold picks the last BOARD-side write while the key's
// actual current value came from a GitHub-intaken write that outbound
// suppression skipped — which reopened a closed issue and restored a
// superseded title on a recovery run.
func (s *Syncer) mirror() error {
	lastStatus, lastRename := map[string]int{}, map[string]int{}
	for i, ev := range s.events {
		if !s.mirrorable(ev) || ev.Type != "set" || ev.Key == "" {
			continue
		}
		if ev.Rename != "" {
			lastRename[ev.Key] = i
		}
		if ev.Fields["status"] != "" {
			lastStatus[ev.Key] = i
		}
	}
	for i, ev := range s.events {
		if !s.mirrorable(ev) {
			continue
		}
		switch {
		case ev.Type == "set" && ev.Rename != "":
			if lastRename[ev.Key] != i {
				continue // superseded within this drain; only the level matters
			}
			if err := s.mirrorTitle(ev); err != nil {
				return err
			}
		case ev.Type == "set" && ev.Fields["status"] != "":
			if lastStatus[ev.Key] != i {
				continue
			}
			if err := s.mirrorStatus(ev); err != nil {
				return err
			}
		case ev.Type == "note" && ev.Key != "":
			n, err := s.ensureIssue(ev.Key, ev.ID)
			if err != nil {
				return err
			}
			if n == 0 {
				s.report.warn("note %s on '%s' has no GitHub issue to land on (the key has no title yet) — dropped",
					ev.ID, ev.Key)
				continue
			}
			if err := s.postMirrored(n, markerIDs(ev.ID, ev.ImportedFrom), ev.Author, ev.Text); err != nil {
				return err
			}
		}
	}
	return nil
}

// mirrorTitle is Law 6's TITLE mirror: a title edit and NOTHING else. No
// comment — a bare rename has no message by Part A's `-m` rule, and an
// override justification never leaves the board.
func (s *Syncer) mirrorTitle(ev Event) error {
	n, err := s.ensureIssue(ev.Key, ev.ID)
	if err != nil {
		return err
	}
	if n == 0 {
		s.dropped(ev, "title")
		return nil
	}
	title := s.currentTitle(ev.Key, ev.Rename)
	if s.ghTitle[n] == title {
		return nil // Law 2's convergence: a title write fires only on a difference
	}
	if err := s.GH.EditTitle(n, title); err != nil {
		return err
	}
	s.ghTitle[n] = title
	s.report.gh("#%d retitled %q", n, title)
	return nil
}

// mirrorStatus is Law 6's STATUS mirror, measured on the Status mapping's
// axis: terminality class plus, between terminals, the close reason.
//
// Under this axis open<->in-progress is never a difference, which makes Law
// 6's "transitions that do not change terminality mirror to NOTHING" a
// COROLLARY of Law 2's convergence rather than an exception to it. The raw
// reading made the two contradict, and its probed resolution reopened a
// human's close and published a claim message.
func (s *Syncer) mirrorStatus(ev Event) error {
	// The issue-creation rule, checked BEFORE anything can create: a
	// CLAIM-ONLY drain pushes nothing and creates nothing. A claim has no
	// GitHub representation at all, so minting an issue for one shows a
	// reader nothing they did not already know — and nothing is dropped
	// either, because there was nothing to drop.
	if _, linked := s.byKey[ev.Key]; !linked && s.claimOnly[ev.Key] {
		return nil
	}
	n, err := s.ensureIssue(ev.Key, ev.ID)
	if err != nil {
		return err
	}
	if n == 0 {
		s.dropped(ev, "status")
		return nil
	}
	level := s.currentStatus(ev.Key, ev.Fields["status"])
	want := s.boardValueForMirror(level)
	have := s.ghValue(s.ghState[n])
	if want == have {
		return nil
	}
	closed := want != ""
	// Law 6: EVERY status mirror carries its message, and the comment goes
	// up FIRST, so a GitHub reader sees why even if the state call itself is
	// the one that crashes.
	//
	// The message rides only when this event IS the level; carrying an older
	// event's reason for a different value would be a lie.
	isLevel := ev.Fields["status"] == level
	explain := ev
	if !isLevel {
		explain = Event{ID: ev.ID, ImportedFrom: ev.ImportedFrom, Author: ev.Author}
	}
	var text string
	switch {
	case closed:
		text = closeText(explain, level)
	case isLevel && level == openValue:
		// An EVENT-DRIVEN reopen: a person wrote `open` with a reason, and
		// that reason is exactly what a GitHub reader needs. Law 6's "a
		// REOPEN likewise comments its reason before reopening".
		text = reopenTextFor(explain)
	default:
		// A CONVERGENCE-driven reopen: either no board event stands behind
		// the difference, or the level is `in-progress`, whose only message
		// is a claim or touch-base message. Law 2's fixed text, and never a
		// board message — publishing a claim to a public issue is the probed
		// failure this guards.
		text = reopenText
	}
	if err := s.postMirrored(n, markerIDs(ev.ID, ev.ImportedFrom), ev.Author, text); err != nil {
		return err
	}
	if err := s.GH.SetState(n, closed, level == s.NotPlanned); err != nil {
		return err
	}
	s.ghState[n] = ghStateBits{closed: closed, notPlanned: closed && level == s.NotPlanned}
	if closed {
		s.report.gh("#%d closed (status=%s)", n, level)
	} else {
		s.report.gh("#%d reopened (status=%s)", n, level)
	}
	return nil
}

// dropped reports a mirror that had nowhere to land. A close that mirrors
// to nothing while the report stays clean is exactly the silent-failure
// shape the saturation rules exist to prevent, so a dropped state or title
// mirror warns naming its event id — the same treatment a dropped note gets.
func (s *Syncer) dropped(ev Event, aspect string) {
	reason := "the key has no title yet"
	if n, linked := s.byKey[ev.Key]; linked {
		reason = fmt.Sprintf("its issue #%d is not in this repo's listing", n)
	}
	s.report.warn("%s mirror %s on '%s' has no GitHub issue to land on (%s) — dropped",
		aspect, ev.ID, ev.Key, reason)
}

// currentStatus / currentTitle are the LEVEL the mirror pushes: the key's
// folded value after this run's intake, which is exactly the state GitHub
// should be showing. fallback covers a key the snapshot does not know (a
// drain event for a key `show` no longer lists).
func (s *Syncer) currentStatus(key, fallback string) string {
	if ks := s.keys[key]; ks != nil && ks.Status != "" {
		return ks.Status
	}
	return fallback
}

func (s *Syncer) currentTitle(key, fallback string) string {
	if ks := s.keys[key]; ks != nil && ks.Title != "" {
		return ks.Title
	}
	return fallback
}

// reopenTextFor is the body of an EVENT-DRIVEN reopen's comment: the board
// event's own reason. Only reachable when the event IS the level and the
// level is the open value — see mirrorStatus.
func reopenTextFor(ev Event) string {
	body := fmt.Sprintf("reopened on the board (status=%s)", openValue)
	if strings.TrimSpace(ev.Text) != "" {
		body += ": " + ev.Text
	}
	return body
}

// closeText is the body of the comment that precedes a mirrored close.
func closeText(ev Event, value string) string {
	body := fmt.Sprintf("closed on the board (status=%s)", value)
	if strings.TrimSpace(ev.Text) != "" {
		body += ": " + ev.Text
	}
	if len(ev.Evidence) > 0 {
		body += "\n\nevidence: " + strings.Join(ev.Evidence, ", ")
	}
	return body
}

// postMirrored posts one board event to GitHub as a comment carrying the
// verified MARKER — `**<author>** (via ledger, <event-id>):` — and never
// posts the same event twice, checking the issue's comments (already
// fetched, plus anything this run added) for that event id first. That check
// is what makes a mid-mirror crash safe to re-run.
//
// EVERY comment the bridge posts opens with the marker — mirrored notes,
// close/reopen explanations, divergence notices, all of them. An unmarked
// bridge comment does not exist: the one unmarked class in an earlier build
// echoed back as board state attributed to a person, live.
//
// The check runs over the SAME domain the inbound oracle uses: the event's
// own id and its `imported_from`. An id-only check is blind in this
// direction too — after an export/import the board's events carry new ids
// while GitHub still carries the old ones in its markers, so the recovery
// run re-posts the bridge's entire mirrored history as fresh comments.
func (s *Syncer) postMirrored(n int, ids []string, author, text string) error {
	if len(ids) == 0 || ids[0] == "" {
		return fmt.Errorf("refusing to post an unmarked comment on #%d", n)
	}
	for _, c := range s.ghComments[n] {
		_, posted, ok := parseMarker(c.Body)
		if !ok {
			continue
		}
		for _, id := range ids {
			if id != "" && id == posted {
				return nil
			}
		}
	}
	eventID := ids[0]
	body := markerFor(author, eventID) + "\n\n" + text
	c, err := s.GH.Comment(n, body)
	if err != nil {
		return err
	}
	c.Body = body
	s.ghComments[n] = append(s.ghComments[n], c)
	// The oracle's domain must include ids THIS RUN wrote, and it is never a
	// run-start snapshot. The chain index is loaded once; the board events a
	// run writes mid-flight (a handoff note, above all) are not in it — so
	// the bridge's own divergence comment, posted and then read back by the
	// very same run's comment intake, failed the marker test and imported as
	// a board note under an empty login. Registering the id here covers
	// every marker the bridge emits, because a marker id is by construction
	// an id the bridge wrote.
	if s.chain != nil {
		for _, id := range ids {
			if id != "" {
				s.chain.ids[id] = true
			}
		}
	}
	s.report.gh("#%d commented (event %s by %s)", n, eventID, author)
	return nil
}

// ensureIssue returns the GitHub issue mirroring a board key, creating it
// the FIRST time the mirror has something to push for that key. Returns 0
// when the key has no title (Part A's fold rule) — a titleless key never
// gains an issue, because there is nothing to name one with.
func (s *Syncer) ensureIssue(key, forEvent string) (int, error) {
	if n, ok := s.byKey[key]; ok {
		if !s.knownIssues[n] {
			// A broken link (deleted or transferred issue), already warned
			// and handed off by reportBrokenLinks. Nothing to land on, and
			// creating a replacement would silently re-mint the issue under a
			// second number while the link still names the first.
			return 0, nil
		}
		return n, nil
	}
	ks := s.keys[key]
	if ks == nil || ks.Title == "" {
		return 0, nil
	}
	n, err := s.GH.Create(ks.Title, s.issueBody(key, ""))
	if err != nil {
		return 0, err
	}
	s.report.gh("#%d created for %s (%q)", n, key, ks.Title)
	s.ghTitle[n], s.ghState[n] = ks.Title, ghStateBits{}
	// An issue this run created is not in the preflight listing, and the
	// broken-link guard above reads exactly that listing. Without this line
	// the key's own status mirror is skipped as "linked to a missing issue"
	// on the very run that created it — the close never lands, and the next
	// run's intake reads the still-open issue as a human reopen and writes
	// one, with a fabricated auto-override. Caught by the export/import
	// regression.
	s.knownIssues[n] = true

	// The link note is written IMMEDIATELY after the create. The crash
	// window between the two is closed by ADOPTION, not by search.
	_, deduped, err := s.Board.LinkNote(key, n)
	if err != nil {
		return 0, err
	}
	if !deduped {
		s.report.board("link %s <-> #%d", key, n)
	}
	s.byKey[key], s.byIssue[n] = n, key

	// Law 6's backfill: the key may have collected notes while it had no
	// issue (the statusless-seed window, or notes written before the bridge
	// existed). The filter is by AUTHOR, never by kind — a kind filter here
	// re-eats the human `handoff` note through the backfill door. Notes
	// already in this drain are backfilled here and then recognised by their
	// event-id marker when the drain reaches them, so nothing double-posts.
	notes, err := s.Board.NotesOnKey(key)
	if err != nil {
		return 0, err
	}
	for _, note := range notes {
		if note.Author == bridgeAuthor || strings.HasPrefix(note.Author, ghAuthorPrefix) {
			continue
		}
		if note.ID == forEvent {
			continue // the caller is about to post this one itself
		}
		if err := s.postMirrored(n, markerIDs(note.ID, s.importedFromOf(note.ID)), note.Author, note.Text); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// issueBody is the body of an issue the bridge owns: the identity hint, the
// STAMP (the adoption credential and the second, independent copy of the
// identity map) and a word to the humans.
func (s *Syncer) issueBody(key, existing string) string {
	body := keyLinePrefix + key + "\n" + bridgeStamp + "\n\n" +
		"Mirrored from the ledger board `" + s.Board.Slug + "`. " +
		"Comments and state here flow back to the board; do not edit the first line."
	if strings.TrimSpace(existing) != "" {
		body += "\n\n---\n\n" + existing
	}
	return body
}

// ---- Law 3: refusals converge ----

// refuse records a divergence the bridge will not resolve, writes the ONE
// handoff note and posts the ONE GitHub comment.
//
// Both writes are keyed on the record's full (issue, CLASS, aspect,
// observed-state) quadruple, so they happen once per distinct divergence
// EVER, not per episode: Law 2's key makes a recurrence dedupe by design,
// and un-keying it would duplicate the note on every crash between the note
// and the state persist. What recurs afresh on a re-observation is the
// COUNT, the report line, and the re-persisted record — the original note
// remains the greppable record of the divergence's content, which is
// identical by construction.
func (s *Syncer) refuse(r Record, key, explain string) {
	s.recordDivergence(r)
	if s.state.has(r) {
		return // standing and unchanged: silently skipped, merely counted
	}
	body := explain + "\n\nA maintainer must apply this on the board, or clear the signal that blocks it."
	id, deduped, err := s.Board.Note(key, kindHand, body, bridgeAuthor, r.idem())
	if err != nil {
		s.report.warn("could not write the handoff note for #%d/%s/%s: %v", r.Issue, r.Class, r.Aspect, err)
		return
	}
	// A deduped write is NOT a write. Counting it would make a run whose
	// only event was a re-observed divergence report board_writes > 0 for a
	// chain that never grew.
	if !deduped {
		s.report.board("handoff note on %s (#%d %s %s divergence)", key, r.Issue, r.Class, r.Aspect)
	}
	// The GitHub comment carries the handoff note's event id in its marker,
	// like every other comment the bridge posts. Without it the bridge's own
	// divergence comment reads to the next run's intake as a human's.
	//
	// An issue the listing does not contain (deleted, transferred) has
	// nowhere to put it; the board note is the whole record there.
	if !s.knownIssues[r.Issue] {
		return
	}
	if err := s.postMirrored(r.Issue, markerIDs(id, ""), bridgeAuthor, explain+
		"\n\nThis key is reserved on the board; a maintainer must apply this there. "+
		"The bridge will not repeat this comment."); err != nil {
		s.report.warn("could not comment the divergence on #%d: %v", r.Issue, err)
	}
}

// suppressionNote is Law 1 step 3's other half: intake is standing down on
// an aspect because the board is about to push a DIFFERENT value there, and
// the value being discarded is a real person's GitHub action. It gets the
// same convergence treatment as a refusal — once per distinct divergence,
// then counted — under its OWN class, so it can never consume a refusal's
// note or comment.
//
// On a RE-DRAIN run it is suppressed entirely. MIRROREDVIEW is "the fold
// over the chain minus this run's non-suppressed drain", and when the drain
// is the whole chain that difference is empty — so every value GitHub shows
// looks unaccounted-for, and the naive fallback accused a person of every
// edit the bridge itself had made.
func (s *Syncer) suppressionNote(issue int, key, aspect, observed, explain string) {
	if s.reDrained {
		return
	}
	s.report.warn("#%d: %s", issue, explain)
	r := Record{Issue: issue, Class: classSuppression, Aspect: aspect, Observed: observed}
	s.recordDivergence(r)
	if s.state.has(r) {
		return
	}
	_, deduped, err := s.Board.Note(key, kindHand, explain+
		"\n\nThe board's value wins this run; re-apply it on GitHub if it was wanted.", bridgeAuthor, r.idem())
	if err != nil {
		s.report.warn("could not write the suppression note for #%d/%s: %v", issue, aspect, err)
		return
	}
	if !deduped {
		s.report.board("handoff note on %s (#%d %s suppressed)", key, issue, aspect)
	}
}

func (s *Syncer) recordDivergence(r Record) {
	for _, have := range s.newRecords {
		if have == r {
			return
		}
	}
	s.newRecords = append(s.newRecords, r)
}

// ---- helpers ----

// uniqueKey resolves a slug collision by suffixing the ISSUE NUMBER, since
// two GitHub issues may legitimately share a title and a board key must
// never be reused for a different task. Two replicas intaking concurrently
// can still collide or diverge — the board's own two-root machinery is the
// net, stated.
//
// The reserved state key is treated as taken even though it is a NOTE key
// with no board key of its own: an issue titled so that it slugified into it
// once seized a live board's state.
func (s *Syncer) uniqueKey(base string, issue int) string {
	taken := func(k string) bool {
		if k == stateKey {
			return true
		}
		_, ok := s.keys[k]
		return ok
	}
	if base == "" {
		base = fmt.Sprintf("issue-%d", issue)
	}
	if !taken(base) {
		return base
	}
	if candidate := fmt.Sprintf("%s-%d", base, issue); !taken(candidate) {
		return candidate
	}
	for i := 2; ; i++ {
		if candidate := fmt.Sprintf("%s-%d-%d", base, issue, i); !taken(candidate) {
			return candidate
		}
	}
}

var nonKeyChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a GitHub issue title into a board key matching the
// ready-capable board's key grammar (^[a-z0-9][a-z0-9-]*$).
func slugify(title string) string {
	s := nonKeyChars.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}

// ledgerKeyFromBody reads the identity line at the top of an issue body. It
// is a HINT, never authority.
func ledgerKeyFromBody(body string) string {
	line := strings.TrimSpace(strings.SplitN(strings.ReplaceAll(body, "\r\n", "\n"), "\n", 2)[0])
	rest, ok := strings.CutPrefix(line, keyLinePrefix)
	if !ok {
		return ""
	}
	return strings.TrimSpace(rest)
}

// markerRE is the CURRENT marker format. The marker is a VERSIONED wire
// format: every prior format must stay recognized FOREVER, or the bridge
// re-imports its own history as human comments (observed live against an
// earlier format). When a v2 format is added, add its pattern here and keep
// this one.
var markerFormats = []*regexp.Regexp{
	regexp.MustCompile(`^\*\*(.+?)\*\* \(via ledger, ([0-9a-f]+)\):$`),
}

func markerFor(author, eventID string) string {
	return "**" + author + "** (via ledger, " + eventID + "):"
}

// parseMarker reads the marker every comment the bridge posts carries.
func parseMarker(body string) (author, eventID string, ok bool) {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(body), "\n", 2)[0])
	for _, re := range markerFormats {
		if m := re.FindStringSubmatch(first); m != nil {
			return m[1], m[2], true
		}
	}
	return "", "", false
}

// isBridgeEcho decides whether a GitHub comment is one the bridge posted.
//
// No login comparison, anywhere: the bridge works under several GitHub
// logins at different times, and those same people also comment as
// themselves. A comment is bridge-authored iff it carries the marker AND the
// event id in that marker RESOLVES on this board's chain.
//
// Stated edges, all verified live: a person who pastes a well-formed marker
// carrying a REAL event id suppresses their own comment (self-inflicted, and
// it costs them one comment); a marker with an id that resolves nowhere is
// just text somebody typed, and imports normally. And the marker is
// board-scoped only by luck — a well-formed marker from ANOTHER board's
// bridge does not resolve here and imports as if human, which is why one
// repo binds to one board permanently.
func (s *Syncer) isBridgeEcho(body string) (bool, error) {
	_, id, ok := parseMarker(body)
	if !ok {
		return false, nil
	}
	if err := s.loadChainIndex(); err != nil {
		return false, err
	}
	return s.chain.ids[id], nil
}

func commentIdem(restID string) string { return "gh-comment-" + restID }

// issueIdem is the idempotency key an inbound SEED carries, and the handle
// the derived seed map is built from.
func issueIdem(n int) string { return fmt.Sprintf("gh-issue-%d", n) }

func issueFromIdem(key string) (int, bool) {
	rest, ok := strings.CutPrefix(key, "gh-issue-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// markerIDs is the ORACLE's domain for ONE event: its current id and, if it
// crossed an export/import boundary, the id it carried before. BOTH
// directions use it — the inbound "is this comment mine" test and the
// outbound "have I posted this already" test — because an id-only domain
// goes blind on the recovery path in both.
func markerIDs(id, importedFrom string) []string {
	ids := []string{id}
	if importedFrom != "" && importedFrom != id {
		ids = append(ids, importedFrom)
	}
	return ids
}

// idemSpent answers "has this idempotency key already been used, in the
// scope the board's own dedupe would apply?" — the derived form of the
// import map Law 2 deleted.
func (s *Syncer) idemSpent(scope idemScope) (bool, error) {
	if err := s.loadChainIndex(); err != nil {
		return false, err
	}
	return s.chain.idem[scope], nil
}

func (s *Syncer) loadChainIndex() error {
	if s.chain != nil {
		return nil
	}
	idx, err := s.Board.ChainIndex()
	if err != nil {
		return err
	}
	s.chain = idx
	s.warnDecoyIdemKeys()
	return nil
}

// importedFromOf recovers an event's pre-import id from the whole-chain
// read. `ledger notes` does not carry the field, so the backfill — which
// posts through the same marker-dedupe path the drain does — would otherwise
// use a one-element domain and re-post after an export/import.
func (s *Syncer) importedFromOf(id string) string {
	if err := s.loadChainIndex(); err != nil {
		return ""
	}
	return s.chain.importedFrom[id]
}

// warnDecoyIdemKeys reports chain events carrying a `gh-comment-*`
// idempotency key OUTSIDE the bridge's own intake write shape (a note of
// kind `comment` on a board key, authored `github:@<login>`).
//
// The bridge does NOT act on the warning: the scoped index already makes
// such an event a non-match, so the import proceeds and the TOOL's scoped
// dedupe is the arbiter. That is the whole point — the poison fails LOUDLY
// instead of succeeding silently.
func (s *Syncer) warnDecoyIdemKeys() {
	for _, ev := range s.chain.events {
		if !strings.HasPrefix(ev.IdemKey, commentIdem("")) {
			continue
		}
		if ev.Type == "note" && ev.Kind == "comment" && ev.Key != "" &&
			strings.HasPrefix(ev.Author, ghAuthorPrefix) {
			continue // the bridge's own intake shape
		}
		s.report.warn("event %s carries the bridge's idempotency key %q outside its write shape "+
			"(type=%s kind=%s key=%s by %s) — the board's own scoped dedupe is the arbiter, so this "+
			"suppresses nothing; a maintainer should look at who wrote it",
			ev.ID, ev.IdemKey, ev.Type, ev.Kind, ev.Key, ev.Author)
	}
}

// mirroredView is MIRROREDVIEW: the board state the bridge last PUT on
// GitHub — fold(chain − this run's drain's NON-SUPPRESSED events).
//
// The exclusion is deliberately narrow. Intake writes stay IN the view
// because they describe what GitHub already shows; a view that excluded them
// too fired a false "a human's edit was discarded" accusation on every
// board-side reversal of an intaken close.
//
// It is also what makes the divergence test honest at all. "The suppressed
// remote value differs from the OUTGOING value" is not the question — on any
// ordinary board-side close the remote still shows open, differs from
// `closed`, and would be reported as a discarded human action on every
// single close. The question is whether the remote differs from what the
// bridge last put there. Derived, never stored: the chain carries it, so two
// replicas cannot disagree about it.
func (s *Syncer) mirroredView() (map[string]string, map[string]string, error) {
	if s.mirroredStatus != nil {
		return s.mirroredStatus, s.mirroredTitle, nil
	}
	if err := s.loadChainIndex(); err != nil {
		return nil, nil, err
	}
	excluded := map[string]bool{}
	for _, ev := range s.events {
		if !s.suppressedAuthor(ev) {
			excluded[ev.ID] = true
		}
	}
	status, title := map[string]string{}, map[string]string{}
	for _, ev := range s.chain.events {
		if excluded[ev.ID] || ev.Type != "set" || ev.Key == "" {
			continue
		}
		if v := ev.Fields["status"]; v != "" {
			status[ev.Key] = v
			if title[ev.Key] == "" && ev.Text != "" {
				title[ev.Key] = ev.Text
			}
		}
		if ev.Rename != "" {
			title[ev.Key] = ev.Rename
		}
	}
	// A key with NO pre-drain history at all was mirrored (or half-mirrored,
	// by a run that crashed) inside this very drain: what GitHub shows for it
	// is a freshly created issue, which is open. Without this the first run
	// on a board reports every key as a discarded human action.
	for key := range s.pendingStatus {
		if status[key] == "" {
			status[key] = openValue
		}
	}
	s.mirroredStatus, s.mirroredTitle = status, title
	return status, title, nil
}
