// Package fakegh is the bridge's fixture transport: a stateful stand-in for
// the `gh` CLI, driven by a JSON file, with crash injection at every call
// site.
//
// It exists because the bridge's hard promises — crash anywhere and re-run
// safely, never double-post, never mint a duplicate issue — are promises
// about what happens BETWEEN two transport calls, and a live repo cannot be
// failed on demand at the tenth call, thirty times in a row. The board side
// of those tests is the real `ledger` binary against a real store: only
// GitHub is faked.
//
// FIXTURE FAITHFULNESS is a test-plan law, not a nicety. Four real defects
// hid behind an unfaithful fixture at various points in this design's
// history:
//
//   - not honouring --limit let the saturation refusal be proved to FIRE
//     while proving nothing about what it protects against;
//   - not honouring --state left the entire suite green under an open-only
//     listing, which disables close intake, comment dedupe and adoption;
//   - not modelling the per-issue 100-comment cap hid a busy issue that
//     stopped importing forever with a clean 0/0 report;
//   - not serializing calls would have made the concurrency probe measure
//     the fixture's own read-modify-write race instead of the bridge's.
//
// So: --limit and --state are honoured, the bulk listing caps comments at
// GITHUB'S OWN 100 (oldest first, newest silently missing) while the
// per-issue read is complete, and every invocation holds an exclusive lock
// on the state file.
package fakegh

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const (
	// EnvState names the JSON file holding the fixture repo.
	EnvState = "FAKEGH_STATE"
	// EnvLogin is the GitHub login this invocation acts as — the fixture's
	// multi-operator lever. The bridge never reads it; the fake uses it to
	// author the comments and timeline events the bridge produces, so a test
	// can run consecutive syncs under different logins and let humans
	// comment under those same logins.
	EnvLogin = "FAKEGH_LOGIN"
	// EnvFailAt crashes the nth call BEFORE its effect reaches GitHub.
	EnvFailAt = "FAKEGH_FAIL_AT"
	// EnvFailAfter is the OTHER crash: the call lands, GitHub changes, and
	// the bridge dies before it can record that it happened. That is the
	// window duplicate issues and double-posted comments come from, and a
	// fail-BEFORE injection never reaches it.
	EnvFailAfter = "FAKEGH_FAIL_AFTER"
	// EnvFlakeRate/EnvFlakeSeed model TRANSIENT API failure: a percentage of
	// calls answer with a 502-shaped error instead of doing their job. Half
	// of those fail before the effect and half after, since a real gateway
	// error tells the caller nothing about whether the write landed. The
	// seed makes a run reproducible; the failure decision is a pure function
	// of (seed, call number), never of wall-clock or goroutine order.
	EnvFlakeRate = "FAKEGH_FLAKE_RATE"
	EnvFlakeSeed = "FAKEGH_FLAKE_SEED"
)

// BulkCommentCap is GitHub's own per-issue comment ceiling in the bulk
// listing: `gh issue list --json comments` returns the OLDEST 100 comments
// per issue and silently omits the rest. Probed live. The bridge's
// saturation re-read exists for exactly this.
const BulkCommentCap = 100

type Author struct {
	Login string `json:"login"`
}

type Comment struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Body      string `json:"body"`
	Author    Author `json:"author"`
	CreatedAt string `json:"createdAt"`
}

type TimelineEvent struct {
	Event string `json:"event"`
	Actor Author `json:"actor"`
}

type Issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`       // OPEN | CLOSED
	StateReason string    `json:"stateReason"` // COMPLETED | NOT_PLANNED | REOPENED | ""
	Author      Author    `json:"author"`
	URL         string    `json:"url"`
	Comments    []Comment `json:"comments"`
}

// State is the whole fixture repo, on disk.
type State struct {
	Repo        string                     `json:"repo"`
	Issues      []*Issue                   `json:"issues"`
	Timelines   map[string][]TimelineEvent `json:"timelines"`
	NextComment int                        `json:"next_comment"`
	// Calls counts invocations since the test last reset it; FAKEGH_FAIL_AT
	// names the one that fails.
	Calls int `json:"calls"`
	// Log is every invocation's argv, for assertions about call shape and
	// ordering — the crash sweep asserts the shapes it covers before it
	// begins, so it cannot silently drift off the sequence it proves safe.
	Log []string `json:"log"`
}

func Load(path string) (*State, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	st := &State{}
	if err := json.Unmarshal(blob, st); err != nil {
		return nil, err
	}
	if st.Timelines == nil {
		st.Timelines = map[string][]TimelineEvent{}
	}
	if st.NextComment == 0 {
		st.NextComment = 100000
	}
	return st, nil
}

func (s *State) Save(path string) error {
	blob, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o644)
}

func (s *State) Issue(n int) *Issue {
	for _, i := range s.Issues {
		if i.Number == n {
			return i
		}
	}
	return nil
}

// AddTimeline records an actor's action on an issue. The bridge's Law 4
// attribution reads these, paginated.
func (s *State) AddTimeline(n int, event, actor string) {
	k := strconv.Itoa(n)
	s.Timelines[k] = append(s.Timelines[k], TimelineEvent{Event: event, Actor: Author{Login: actor}})
}

// AddComment appends a comment as a given login would have written it, and
// returns its url. Tests use it for human comments; the fake uses it for the
// bridge's own.
func (s *State) AddComment(n int, body, login string) string {
	is := s.Issue(n)
	if is == nil {
		return ""
	}
	s.NextComment++
	url := fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-%d", s.Repo, n, s.NextComment)
	is.Comments = append(is.Comments, Comment{
		ID:     fmt.Sprintf("IC_kw%d", s.NextComment),
		URL:    url,
		Body:   body,
		Author: Author{Login: login},
	})
	s.AddTimeline(n, "commented", login)
	return url
}

// Run is one `gh` invocation. Exit code out, stdout/stderr in.
//
// The whole invocation holds an exclusive lock on the state file, so two
// concurrent `gh` processes serialize exactly the way two API calls against
// one repo do. Without it the read-modify-write of the state file loses one
// process's mutation whenever the two overlap.
func Run(args []string, stdout, stderr io.Writer) int {
	path := os.Getenv(EnvState)
	unlock, err := lockState(path)
	if err != nil {
		fmt.Fprintf(stderr, "fakegh: %v\n", err)
		return 1
	}
	defer unlock()

	st, err := Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "fakegh: %v\n", err)
		return 1
	}
	login := os.Getenv(EnvLogin)
	if login == "" {
		login = "operator"
	}
	// --repo faithfulness: the real `gh` talks to the repo it is told to,
	// and a test that points a bridge at the wrong fixture repo must fail
	// the way a real mis-pointed run would, not silently answer with some
	// other repo's issues. `gh api` addresses the repo inside its path
	// instead, so it is checked there.
	if repo := flagOf(args, "--repo"); repo != "" && repo != st.Repo {
		fmt.Fprintf(stderr, "fakegh: this repo is %q, not %q\n", st.Repo, repo)
		return 1
	}
	st.Calls++
	st.Log = append(st.Log, strings.Join(args, " "))
	flakeBefore, flakeAfter := flakeAt(st.Calls)
	if at := os.Getenv(EnvFailAt); at != "" {
		if n, _ := strconv.Atoi(at); n == st.Calls {
			// Persist the call count and the log, but NONE of this call's
			// effect: the crash lands before the mutation reaches GitHub.
			_ = st.Save(path)
			fmt.Fprintf(stderr, "fakegh: injected failure at call %d (%s)\n", n, strings.Join(args, " "))
			return 1
		}
	}
	if flakeBefore {
		_ = st.Save(path)
		fmt.Fprintln(stderr, "HTTP 502: Bad Gateway (transient)")
		return 1
	}
	code := dispatch(st, args, login, stdout, stderr)
	if err := st.Save(path); err != nil {
		fmt.Fprintf(stderr, "fakegh: %v\n", err)
		return 1
	}
	if at := os.Getenv(EnvFailAfter); at != "" && code == 0 {
		if n, _ := strconv.Atoi(at); n == st.Calls {
			fmt.Fprintf(stderr, "fakegh: injected failure AFTER call %d (%s)\n", n, strings.Join(args, " "))
			return 1
		}
	}
	if flakeAfter && code == 0 {
		fmt.Fprintln(stderr, "HTTP 502: Bad Gateway (transient, after the write landed)")
		return 1
	}
	return code
}

// lockState takes an exclusive advisory lock for the whole invocation.
func lockState(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// flakeAt decides, deterministically, whether call n answers 502 — and if
// so whether the write landed first. A gateway error carries no information
// about that, which is exactly what makes transient failure interesting.
func flakeAt(n int) (before, after bool) {
	rate, _ := strconv.Atoi(os.Getenv(EnvFlakeRate))
	if rate <= 0 {
		return false, false
	}
	seed, _ := strconv.Atoi(os.Getenv(EnvFlakeSeed))
	// A small deterministic mix; nothing here needs to be a good hash, only
	// reproducible and uncorrelated with call ordering.
	h := uint32(seed)*2654435761 + uint32(n)*40503
	h ^= h >> 13
	if int(h%100) >= rate {
		return false, false
	}
	if h&(1<<7) != 0 {
		return false, true
	}
	return true, false
}

func flagOf(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func dispatch(st *State, args []string, login string, stdout, stderr io.Writer) int {
	flagVal := func(name string) string { return flagOf(args, name) }
	has := func(name string) bool { return hasFlag(args, name) }
	if len(args) < 2 {
		fmt.Fprintf(stderr, "fakegh: not enough arguments: %s\n", strings.Join(args, " "))
		return 1
	}
	switch {
	case args[0] == "issue" && args[1] == "list":
		return listIssues(st, flagVal, stdout, stderr)
	case args[0] == "issue" && args[1] == "view":
		return viewIssue(st, args, stdout, stderr)
	case args[0] == "issue" && args[1] == "create":
		n := 1
		for _, i := range st.Issues {
			if i.Number >= n {
				n = i.Number + 1
			}
		}
		st.Issues = append(st.Issues, &Issue{
			Number: n, Title: flagVal("--title"), Body: flagVal("--body"),
			State: "OPEN", Author: Author{Login: login},
			URL: fmt.Sprintf("https://github.com/%s/issues/%d", st.Repo, n),
		})
		fmt.Fprintf(stdout, "https://github.com/%s/issues/%d\n", st.Repo, n)
		return 0
	case args[0] == "issue" && args[1] == "edit":
		is := issueArg(st, args[2], stderr)
		if is == nil {
			return 1
		}
		if has("--title") {
			is.Title = flagVal("--title")
			st.AddTimeline(is.Number, "renamed", login)
		}
		if has("--body") {
			is.Body = flagVal("--body")
		}
		return 0
	case args[0] == "issue" && args[1] == "comment":
		is := issueArg(st, args[2], stderr)
		if is == nil {
			return 1
		}
		url := st.AddComment(is.Number, flagVal("--body"), login)
		fmt.Fprintln(stdout, url)
		return 0
	case args[0] == "api":
		return apiCall(st, args, login, flagVal, stdout, stderr)
	}
	fmt.Fprintf(stderr, "fakegh: unsupported call: %s\n", strings.Join(args, " "))
	return 1
}

// listIssues is the bulk read, faithful in all three ways that have bitten:
// --state selects (gh's own default is open-only), --limit truncates, and
// each issue carries at most the OLDEST 100 comments.
func listIssues(st *State, flagVal func(string) string, stdout, stderr io.Writer) int {
	state := flagVal("--state")
	if state == "" {
		state = "open" // gh's own default — the bug that left a whole suite green
	}
	issues := []*Issue{}
	for _, is := range st.Issues {
		switch strings.ToLower(state) {
		case "all":
		case "open":
			if is.State != "OPEN" {
				continue
			}
		case "closed":
			if is.State != "CLOSED" {
				continue
			}
		default:
			fmt.Fprintf(stderr, "fakegh: unsupported --state %q\n", state)
			return 1
		}
		issues = append(issues, is)
	}
	if lim := flagVal("--limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil && n > 0 && len(issues) > n {
			issues = issues[len(issues)-n:] // newest first is gh's own order
		}
	}
	// The per-issue comment cap. Copy, so the truncation never reaches the
	// stored state.
	capped := make([]Issue, 0, len(issues))
	for _, is := range issues {
		c := *is
		if len(c.Comments) > BulkCommentCap {
			c.Comments = append([]Comment(nil), c.Comments[:BulkCommentCap]...)
		}
		capped = append(capped, c)
	}
	blob, _ := json.Marshal(capped)
	fmt.Fprintln(stdout, string(blob))
	return 0
}

// viewIssue is the per-issue read the saturation rule uses: COMPLETE
// comments, no cap. `gh issue view --json comments` paginates internally.
func viewIssue(st *State, args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "fakegh: issue view needs a number")
		return 1
	}
	is := issueArg(st, args[2], stderr)
	if is == nil {
		return 1
	}
	blob, _ := json.Marshal(is)
	fmt.Fprintln(stdout, string(blob))
	return 0
}

func issueArg(st *State, arg string, stderr io.Writer) *Issue {
	n, err := strconv.Atoi(arg)
	if err != nil {
		fmt.Fprintf(stderr, "fakegh: bad issue number %q\n", arg)
		return nil
	}
	is := st.Issue(n)
	if is == nil {
		fmt.Fprintf(stderr, "fakegh: no such issue #%d\n", n)
	}
	return is
}

// apiCall serves the two REST endpoints the bridge uses directly:
//
//   - the timeline, the way `gh api --paginate` does: oldest first, one JSON
//     document per page, concatenated on stdout, 30 per page by default —
//     GitHub's own default, and the number that made a single-call
//     attribution find NOTHING on a busy issue;
//   - PATCH /issues/<n>, the ONLY call that can express a
//     done<->not-planned reclassification. `gh issue close --reason` on an
//     already-closed issue is a no-op with exit 0 (probed live), so the
//     bridge's state mutations all go through this shape.
func apiCall(st *State, args []string, login string, flagVal func(string) string, stdout, stderr io.Writer) int {
	path := ""
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			path = a
			break
		}
	}
	method := flagVal("-X")
	if method == "PATCH" {
		return patchIssue(st, path, login, args, stdout, stderr)
	}
	if !strings.Contains(path, "/timeline") {
		fmt.Fprintf(stderr, "fakegh: unsupported api path %q\n", path)
		return 1
	}
	if !strings.Contains(path, "repos/"+st.Repo+"/") {
		fmt.Fprintf(stderr, "fakegh: this repo is %q, but the path is %q\n", st.Repo, path)
		return 1
	}
	trimmed := strings.SplitN(path, "?", 2)[0]
	parts := strings.Split(strings.TrimSuffix(trimmed, "/timeline"), "/")
	n := parts[len(parts)-1]
	events := st.Timelines[n]
	const page = 30
	paginate := hasFlag(args, "--paginate")
	if !paginate && len(events) > page {
		events = events[:page]
	}
	for start := 0; start < len(events) || start == 0; start += page {
		end := start + page
		if end > len(events) {
			end = len(events)
		}
		blob, _ := json.Marshal(events[start:end])
		fmt.Fprintln(stdout, string(blob))
		if !paginate {
			break
		}
	}
	return 0
}

// patchIssue applies `-f state=...` / `-f state_reason=...`, which is how
// the bridge closes, reclassifies and reopens.
func patchIssue(st *State, path, login string, args []string, stdout, stderr io.Writer) int {
	if !strings.Contains(path, "repos/"+st.Repo+"/") {
		fmt.Fprintf(stderr, "fakegh: this repo is %q, but the path is %q\n", st.Repo, path)
		return 1
	}
	parts := strings.Split(strings.SplitN(path, "?", 2)[0], "/")
	is := issueArg(st, parts[len(parts)-1], stderr)
	if is == nil {
		return 1
	}
	fields := map[string]string{}
	for i, a := range args {
		if a != "-f" || i+1 >= len(args) {
			continue
		}
		name, value, ok := strings.Cut(args[i+1], "=")
		if ok {
			fields[name] = value
		}
	}
	was := is.State
	switch fields["state"] {
	case "closed":
		is.State = "CLOSED"
		switch fields["state_reason"] {
		case "not_planned":
			is.StateReason = "NOT_PLANNED"
		default:
			is.StateReason = "COMPLETED"
		}
		// A reclassification of an already-closed issue is still a `closed`
		// timeline event on the real API (probed live).
		st.AddTimeline(is.Number, "closed", login)
	case "open":
		is.State = "OPEN"
		is.StateReason = "REOPENED"
		if was == "OPEN" {
			is.StateReason = ""
		} else {
			st.AddTimeline(is.Number, "reopened", login)
		}
	default:
		fmt.Fprintf(stderr, "fakegh: PATCH with no usable state field (%v)\n", fields)
		return 1
	}
	blob, _ := json.Marshal(map[string]any{"state": strings.ToLower(is.State), "state_reason": strings.ToLower(is.StateReason)})
	fmt.Fprintln(stdout, string(blob))
	return 0
}
