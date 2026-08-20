package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ledger/bridge/fakegh"
)

// The bridge's tests run against a FIXTURE transport for GitHub and the REAL
// ledger binary for the board. Faking the board would mean asserting against
// a mock of the very machinery the bridge's correctness depends on — CAS,
// standing signals, idempotency-key dedupe, the cursor contract — so the
// board here is a real store, real git, real subprocesses. Only GitHub is
// faked, because crash injection at the tenth call, thirty times over, is
// not something a live repo can be asked for.

var (
	ledgerBin string
	ghBin     string
	// bridgeBin is the bridge itself, built as a real binary so the
	// concurrency regression can launch two genuinely separate PROCESSES
	// (with their own environments and no shared memory) rather than two
	// goroutines pretending to be them.
	bridgeBin string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bridge-bins")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ledgerBin = filepath.Join(dir, "ledger")
	ghBin = filepath.Join(dir, "gh")
	bridgeBin = filepath.Join(dir, "ledger-gh")
	for _, b := range [][2]string{{ledgerBin, "."}, {ghBin, "./bridge/fakegh/cmd"}, {bridgeBin, "./bridge"}} {
		cmd := exec.Command("go", "build", "-o", b[0], b[1])
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building %s: %v\n%s", b[1], err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const defaultListLimit = 250

type fixture struct {
	t       *testing.T
	dir     string // the board's git repo
	ghState string
	slug    string
	repo    string
	// the vocabulary flags the Syncer is configured with
	done, notPlanned string
	listLimit        int
}

// newFixture builds a board and an empty fixture repo. vocab is the status
// vocabulary to declare; done/notPlanned are the bridge's two MIRRORED
// TERMINAL flags, and are also what the board declares terminal.
func newFixture(t *testing.T, vocab, done, notPlanned string) *fixture {
	t.Helper()
	return newFixtureTerminals(t, vocab, done, notPlanned, done+","+notPlanned)
}

// newFixtureTerminals lets a test declare a terminal set that does NOT match
// the flags — the three-terminal refusal's fixture.
func newFixtureTerminals(t *testing.T, vocab, done, notPlanned, terminals string) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), slug: "issues", repo: "prime-radiant-inc/fixture",
		done: done, notPlanned: notPlanned, listLimit: defaultListLimit}
	f.ghState = filepath.Join(t.TempDir(), "gh.json")
	st := &fakegh.State{Repo: f.repo, Timelines: map[string][]fakegh.TimelineEvent{}, NextComment: 100000}
	if err := st.Save(f.ghState); err != nil {
		t.Fatal(err)
	}
	f.git("init", "-q", ".")
	f.git("config", "user.email", "bridge@example.com")
	f.git("config", "user.name", "bridge")
	f.ledgerOK("init")
	args := []string{"create", f.slug, "--scope", "fixture board",
		"--field", "status=" + vocab, "--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by", "--stale-after", "2h", "--as", "jesse"}
	if terminals != "" {
		args = append(args, "--terminal", "status="+terminals, "--require-evidence", "status="+done)
	}
	f.ledgerOK(args...)
	t.Setenv(fakegh.EnvState, f.ghState)
	t.Setenv(fakegh.EnvLogin, "operator")
	return f
}

// newIssueFixture is the common case: the canonical issue-board vocabulary.
func newIssueFixture(t *testing.T) *fixture {
	return newFixture(t, "open,in-progress,closed,wontfix", "closed", "wontfix")
}

func (f *fixture) git(args ...string) {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (f *fixture) board() Board {
	return Board{Bin: ledgerBin, Slug: f.slug, Store: f.dir}
}

// ledger runs one ledger verb against this fixture's store and returns its
// JSON envelope plus the error (a *BoardErr when the CLI reported one).
func (f *fixture) ledger(args ...string) (map[string]any, error) {
	return f.board().run(args...)
}

func (f *fixture) ledgerOK(args ...string) map[string]any {
	f.t.Helper()
	// create/init address the store, not a ledger, so they must not carry
	// --ledger; everything else goes through the normal path.
	var doc map[string]any
	var err error
	switch args[0] {
	case "init", "create", "push", "sync", "export", "import":
		doc, err = f.board().runBare(args...)
	default:
		doc, err = f.ledger(args...)
	}
	if err != nil {
		f.t.Fatalf("ledger %s: %v", strings.Join(args, " "), err)
	}
	return doc
}

// statusID is the CAS ticket a test needs to write a key's status by hand.
func (f *fixture) statusID(key string) string {
	f.t.Helper()
	keys, _, err := f.board().Snapshot()
	if err != nil {
		f.t.Fatal(err)
	}
	ks := keys[key]
	if ks == nil {
		f.t.Fatalf("no such key %q", key)
	}
	return ks.StatusID
}

func (f *fixture) seed(key, title, as string) {
	f.t.Helper()
	f.ledgerOK("set", key, "status="+openValue, "--expect", "none", "-m", title, "--as", as)
}

// setStatus writes a key's status by hand, CAS'd, as a board-side actor.
func (f *fixture) setStatus(key, value, msg, as string, evidence ...string) {
	f.t.Helper()
	args := []string{"set", key, "status=" + value, "--expect", f.statusID(key), "-m", msg, "--as", as}
	for _, e := range evidence {
		args = append(args, "--evidence", e)
	}
	f.ledgerOK(args...)
}

// setStatusOverride is the same write past a standing signal — what a human
// reclassifying a settled key actually types. The override it records is a
// PERSON's, authored by that person; the property the bridge must never
// violate is a fabricated one, authored github:@* or github-bridge.
func (f *fixture) setStatusOverride(key, value, msg, as string, evidence ...string) {
	f.t.Helper()
	args := []string{"set", key, "status=" + value, "--expect", f.statusID(key), "-m", msg,
		"--as", as, "--override"}
	for _, e := range evidence {
		args = append(args, "--evidence", e)
	}
	f.ledgerOK(args...)
}

// reserve puts the `human` label on a key — the standing signal the bridge
// must NEVER override, and so the trigger for Law 3's refusal path.
func (f *fixture) reserve(key, as string) {
	f.t.Helper()
	f.ledgerOK("set", key, "labels=human", "-m", "reserving this for a person", "--as", as)
}

// ---- the fixture GitHub repo ----

func (f *fixture) ghLoad() *fakegh.State {
	f.t.Helper()
	st, err := fakegh.Load(f.ghState)
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

func (f *fixture) ghSave(st *fakegh.State) {
	f.t.Helper()
	if err := st.Save(f.ghState); err != nil {
		f.t.Fatal(err)
	}
}

// humanCreateIssue is a person opening an issue on GitHub.
func (f *fixture) humanCreateIssue(title, body, login string) int {
	f.t.Helper()
	st := f.ghLoad()
	n := 1
	for _, i := range st.Issues {
		if i.Number >= n {
			n = i.Number + 1
		}
	}
	st.Issues = append(st.Issues, &fakegh.Issue{Number: n, Title: title, Body: body, State: "OPEN",
		Author: fakegh.Author{Login: login},
		URL:    fmt.Sprintf("https://github.com/%s/issues/%d", f.repo, n)})
	f.ghSave(st)
	return n
}

func (f *fixture) humanComment(n int, body, login string) {
	f.t.Helper()
	st := f.ghLoad()
	st.AddComment(n, body, login)
	f.ghSave(st)
}

func (f *fixture) humanClose(n int, notPlanned bool, login string) {
	f.t.Helper()
	st := f.ghLoad()
	is := st.Issue(n)
	is.State, is.StateReason = "CLOSED", "COMPLETED"
	if notPlanned {
		is.StateReason = "NOT_PLANNED"
	}
	st.AddTimeline(n, "closed", login)
	f.ghSave(st)
}

func (f *fixture) humanReopen(n int, login string) {
	f.t.Helper()
	st := f.ghLoad()
	is := st.Issue(n)
	is.State, is.StateReason = "OPEN", "REOPENED"
	st.AddTimeline(n, "reopened", login)
	f.ghSave(st)
}

func (f *fixture) humanRetitle(n int, title, login string) {
	f.t.Helper()
	st := f.ghLoad()
	st.Issue(n).Title = title
	st.AddTimeline(n, "renamed", login)
	f.ghSave(st)
}

func (f *fixture) issueState(n int) (state, reason string) {
	f.t.Helper()
	is := f.ghLoad().Issue(n)
	if is == nil {
		f.t.Fatalf("no such issue #%d", n)
	}
	return is.State, is.StateReason
}

func (f *fixture) issueTitle(n int) string {
	f.t.Helper()
	is := f.ghLoad().Issue(n)
	if is == nil {
		f.t.Fatalf("no such issue #%d", n)
	}
	return is.Title
}

// ---- running the bridge ----

func (f *fixture) syncer() *Syncer {
	return &Syncer{
		Board: f.board(), GH: GH{Repo: f.repo, Bin: ghBin, ListLimit: f.listLimit},
		Done: f.done, NotPlanned: f.notPlanned,
	}
}

// sync runs one bridge pass as a given GitHub login. failAt injects a
// transport failure at the nth `gh` call of this run (0 = no failure); the
// call counter is reset first, so the number is per-run.
func (f *fixture) sync(login string, failAt int) (*Report, error) {
	return f.syncMode(login, failAt, 0)
}

// syncAfter crashes the run AFTER the nth call took effect: the GitHub
// mutation landed and the bridge never learned it did. A fail-BEFORE
// injection never reaches that window, which is where duplicate issues and
// double-posted comments come from.
func (f *fixture) syncAfter(login string, failAfter int) (*Report, error) {
	return f.syncMode(login, 0, failAfter)
}

func (f *fixture) syncMode(login string, failAt, failAfter int) (*Report, error) {
	f.t.Helper()
	st := f.ghLoad()
	st.Calls, st.Log = 0, nil
	f.ghSave(st)
	// Set the state path per run, not once at construction: a test that
	// builds two fixtures must not have the second one's repo answer the
	// first one's calls.
	f.t.Setenv(fakegh.EnvState, f.ghState)
	f.t.Setenv(fakegh.EnvLogin, login)
	set := func(env string, n int) {
		if n > 0 {
			f.t.Setenv(env, fmt.Sprint(n))
		} else {
			f.t.Setenv(env, "")
		}
	}
	set(fakegh.EnvFailAt, failAt)
	set(fakegh.EnvFailAfter, failAfter)
	return f.syncer().Run()
}

// runBridgeBinary runs the real `ledger-gh` binary as a separate PROCESS
// with its own environment — what the concurrency regression needs, since
// two goroutines sharing this test's memory would not be two operators.
func (f *fixture) runBridgeBinary() (string, error) {
	cmd := exec.Command(bridgeBin, "sync", "--repo", f.repo, "--ledger", f.slug,
		"--store", f.dir, "--ledger-bin", ledgerBin, "--gh-bin", ghBin,
		"--done", f.done, "--not-planned", f.notPlanned, "--list-limit", fmt.Sprint(f.listLimit))
	cmd.Env = append(os.Environ(),
		fakegh.EnvState+"="+f.ghState, fakegh.EnvLogin+"=operator",
		fakegh.EnvFailAt+"=", fakegh.EnvFailAfter+"=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *fixture) syncOK(login string) *Report {
	f.t.Helper()
	r, err := f.sync(login, 0)
	if err != nil {
		f.t.Fatalf("sync as %s: %v", login, err)
	}
	return r
}

// converge runs until the bridge reports a 0/0 fixed point, up to a bound.
// Recovery bookkeeping is itself events, so a converged state may take two
// or three runs to reach — the spec forbids promising "the next run is a
// no-op".
func (f *fixture) converge(login string, max int) *Report {
	f.t.Helper()
	var last *Report
	for i := 0; i < max; i++ {
		last = f.syncOK(login)
		if last.GHMutations == 0 && last.BoardWrites == 0 {
			return last
		}
	}
	f.t.Fatalf("did not converge in %d runs: last report %s", max, mustJSON(f.t, last))
	return nil
}

// ghCalls is the number of transport calls the last run made — the sweep
// bound for crash injection.
func (f *fixture) ghCalls() int { return f.ghLoad().Calls }

// ghLog is every argv the last run issued, for call-shape assertions.
func (f *fixture) ghLog() []string { return f.ghLoad().Log }

// ---- assertions ----

func (f *fixture) countIssues() int { return len(f.ghLoad().Issues) }

// commentBodies returns every comment on an issue, in order.
func (f *fixture) commentBodies(n int) []string {
	st := f.ghLoad()
	is := st.Issue(n)
	if is == nil {
		return nil
	}
	out := make([]string, 0, len(is.Comments))
	for _, c := range is.Comments {
		out = append(out, c.Body)
	}
	return out
}

// notes returns the board's notes of a kind (all keys), oldest first.
func (f *fixture) notes(kind string) []Note {
	f.t.Helper()
	doc, err := f.ledger("notes", "-k", kind, "-n", "0")
	if err != nil {
		f.t.Fatal(err)
	}
	raw, _ := doc["notes"].([]any)
	out := []Note{}
	for _, r := range raw {
		m, _ := r.(map[string]any)
		n := Note{}
		n.ID, _ = m["id"].(string)
		n.Kind, _ = m["kind"].(string)
		n.Key, _ = m["key"].(string)
		n.Author, _ = m["by"].(string)
		n.Text, _ = m["text"].(string)
		out = append(out, n)
	}
	return out
}

func (f *fixture) status(key string) string {
	f.t.Helper()
	keys, _, err := f.board().Snapshot()
	if err != nil {
		f.t.Fatal(err)
	}
	if keys[key] == nil {
		return ""
	}
	return keys[key].Status
}

func (f *fixture) title(key string) string {
	f.t.Helper()
	keys, _, err := f.board().Snapshot()
	if err != nil {
		f.t.Fatal(err)
	}
	if keys[key] == nil {
		return ""
	}
	return keys[key].Title
}

// keyList is every board key, sorted, for "nothing was seeded" assertions.
func (f *fixture) keyList() []string {
	f.t.Helper()
	keys, _, err := f.board().Snapshot()
	if err != nil {
		f.t.Fatal(err)
	}
	out := []string{}
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// chain is the whole board chain, oldest first.
func (f *fixture) chain() []Event {
	f.t.Helper()
	evs, _, err := f.board().Since("")
	if err != nil {
		f.t.Fatal(err)
	}
	return evs
}

func (f *fixture) eventCount() int { return len(f.chain()) }

// fabricatedOverrides lists every override on the chain the BRIDGE recorded
// — an event authored github:@* or github-bridge carrying an `override:`.
//
// This is the assertion several probed defects violated once per run,
// forever: the bridge inventing a person's decision to force a board write.
// A human's own `--override` is legitimate and is deliberately not counted.
func (f *fixture) fabricatedOverrides() []string {
	f.t.Helper()
	doc, err := f.ledger("tail", "-n", "0", "--raw")
	if err != nil {
		f.t.Fatal(err)
	}
	raw, _ := doc["events"].([]any)
	out := []string{}
	for _, r := range raw {
		m, _ := r.(map[string]any)
		ov, _ := m["override"].(string)
		author, _ := m["author"].(string)
		if ov == "" {
			continue
		}
		if !strings.HasPrefix(author, ghAuthorPrefix) && author != bridgeAuthor {
			continue
		}
		id, _ := m["id"].(string)
		out = append(out, fmt.Sprintf("%s by %s: %s", id, author, ov))
	}
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	blob, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(blob)
}

func hasWarning(r *Report, substr string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func hasAction(r *Report, substr string) bool {
	for _, a := range r.Actions {
		if strings.Contains(a, substr) {
			return true
		}
	}
	return false
}

func countSubstr(list []string, substr string) int {
	n := 0
	for _, s := range list {
		if strings.Contains(s, substr) {
			n++
		}
	}
	return n
}
