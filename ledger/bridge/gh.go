package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// GH is the GitHub side of the bridge, reached ONLY through the `gh` CLI as
// a subprocess (v1). Token plumbing is a production question `gh` already
// answers.
//
// The bridge NEVER asks who it is (`gh api user`). Any number of GitHub
// logins operate the bridge, each with their own `gh` auth, while the same
// logins participate as humans; nothing in the code path compares logins.
// Echo suppression is the verified MARKER, not an identity.
type GH struct {
	Repo string
	Bin  string
	// ListLimit is the issue window one run reads. A run whose listing
	// SATURATES it is refused rather than run blind: outside the window the
	// bulk maps are zero-valued, which silently disables the comment dedupe,
	// the state diff and adoption.
	ListLimit int
}

type ghAuthor struct {
	Login string `json:"login"`
}

type ghComment struct {
	// ID is the GraphQL node id. It is NOT ordered and is NOT used as an
	// identity anywhere: the REST id parsed out of URL is (Law 2).
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Body      string   `json:"body"`
	Author    ghAuthor `json:"author"`
	CreatedAt string   `json:"createdAt"`
}

// restID is the numeric comment id from the comment's url fragment
// (`…/issues/7#issuecomment-2394…`). It is the idempotency handle for
// comment intake — free in the bulk read, unlike a second API call, and
// ordered, unlike the GraphQL node id. Pinned here so nobody re-derives it
// from the `id` field.
func (c ghComment) restID() string {
	_, frag, ok := strings.Cut(c.URL, "#issuecomment-")
	if !ok {
		return ""
	}
	return strings.TrimSpace(frag)
}

type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	// State is OPEN | CLOSED; StateReason is COMPLETED | NOT_PLANNED |
	// REOPENED | "". An OPEN issue may carry REOPENED — verified live — so
	// no rule may read StateReason without first reading State.
	State       string      `json:"state"`
	StateReason string      `json:"stateReason"`
	Author      ghAuthor    `json:"author"`
	URL         string      `json:"url"`
	Comments    []ghComment `json:"comments"`
}

func (g GH) run(args ...string) (string, error) {
	return g.raw(append(args, "--repo", g.Repo)...)
}

func (g GH) raw(args ...string) (string, error) {
	cmd := exec.Command(g.Bin, args...)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(se.String()))
	}
	return so.String(), nil
}

// listFields is the bulk read's projection. Comments ride along because the
// run reconciles whole state anyway, and having every body in hand is what
// makes "search before create" free for adoption.
const listFields = "number,title,body,state,stateReason,author,url,comments"

// List reads the repo's issues, OPEN AND CLOSED, with their comments.
//
// `--state all` is load-bearing, not a default: `gh` defaults to open-only,
// under which close intake never fires, closed issues' comment dedupe is
// zero-valued, a crashed create whose issue got closed is un-adoptable, and
// saturation never trips.
func (g GH) List() ([]ghIssue, error) {
	out, err := g.run("issue", "list", "--state", "all", "--limit", fmt.Sprint(g.ListLimit),
		"--json", listFields)
	if err != nil {
		return nil, err
	}
	var issues []ghIssue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("gh issue list: undecodable output: %w", err)
	}
	return issues, nil
}

// BulkCommentCap is GitHub's per-issue comment ceiling in the BULK listing:
// it returns the OLDEST 100 comments per issue and silently omits the rest
// (probed live). An issue at exactly the cap is therefore unread, not read —
// and a busy issue that hits it stops importing forever with a clean 0/0
// report, while crash re-runs double-post past the cap.
const BulkCommentCap = 100

// ViewComments re-reads ONE issue's comments completely. The mirror calls it
// for every issue whose bulk listing came back at the cap, before any
// dedupe, intake or posting decision touches that issue.
func (g GH) ViewComments(n int) ([]ghComment, error) {
	out, err := g.run("issue", "view", fmt.Sprint(n), "--json", "comments")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Comments []ghComment `json:"comments"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("gh issue view %d: undecodable output: %w", n, err)
	}
	return doc.Comments, nil
}

func (g GH) Create(title, body string) (int, error) {
	out, err := g.run("issue", "create", "--title", title, "--body", body)
	if err != nil {
		return 0, err
	}
	// `gh issue create` prints the new issue's URL; its last path segment is
	// the number. There is no --json on create.
	url := strings.TrimSpace(out)
	parts := strings.Split(url, "/")
	var n int
	if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &n); err != nil {
		return 0, fmt.Errorf("gh issue create: cannot read the issue number from %q", url)
	}
	return n, nil
}

func (g GH) EditTitle(n int, title string) error {
	_, err := g.run("issue", "edit", fmt.Sprint(n), "--title", title)
	return err
}

func (g GH) EditBody(n int, body string) error {
	_, err := g.run("issue", "edit", fmt.Sprint(n), "--body", body)
	return err
}

// SetState is every state mutation the mirror makes: close completed, close
// not-planned, reopen.
//
// It goes through the REST PATCH rather than `gh issue close`/`gh issue
// reopen` for one ruled reason: a done<->not-planned RECLASSIFICATION on an
// already-closed issue must reach GitHub, and `gh issue close --reason "not
// planned"` on a closed issue is a NO-OP that exits 0 with a warning on
// stderr (probed live against the scratch repo — the state_reason does not
// move). The PATCH expresses the reclassification in one call and produces
// the same `closed` timeline event the CLI would, so Law 4's attribution is
// unaffected.
func (g GH) SetState(n int, closed, notPlanned bool) error {
	args := []string{"api", fmt.Sprintf("repos/%s/issues/%d", g.Repo, n), "-X", "PATCH"}
	if closed {
		reason := "completed"
		if notPlanned {
			reason = "not_planned"
		}
		args = append(args, "-f", "state=closed", "-f", "state_reason="+reason)
	} else {
		args = append(args, "-f", "state=open")
	}
	_, err := g.raw(args...)
	return err
}

// Comment posts a comment and returns it as the API would have listed it, so
// the caller can fold it into this run's view of the issue and never
// double-post after a mid-run failure.
func (g GH) Comment(n int, body string) (ghComment, error) {
	out, err := g.run("issue", "comment", fmt.Sprint(n), "--body", body)
	if err != nil {
		return ghComment{}, err
	}
	return ghComment{URL: strings.TrimSpace(out), Body: body}, nil
}

type timelineEvent struct {
	Event string   `json:"event"`
	Actor ghAuthor `json:"actor"`
}

// LastActor names who last performed one of an issue's timeline events
// ("renamed", "closed", "reopened"). GitHub's issue JSON carries the
// resulting state but not who caused it, and attributing a GH-side change to
// the issue's original author would be a lie.
//
// Law 4: the timeline is oldest-first, 30 per page by default, and includes
// comments — so a single unpaginated call finds NOTHING past the first page
// on a busy issue, and a last-page-only read misses any state event followed
// by a page of comments. This reads EVERY page (`--paginate`,
// `per_page=100`) and takes the NEWEST matching event. Cost, priced
// honestly: ceil(timeline/100) calls per changed ASPECT, uncached.
//
// `gh api --paginate` emits one JSON document per page, concatenated. A
// streaming decoder reads that AND the single-document case, so this works
// against both without asking which one it got.
// A TRANSPORT FAILURE is not a fallback. "No matching event found" is the
// only case that falls back to the issue author; a 502 or a killed process
// must abort the run, or a hiccup gets written to the board permanently as
// somebody's decision. The crash sweep found this: swallowing the error made
// injections at every timeline call site unreachable, which is precisely the
// shape of a silent wrong attribution.
func (g GH) LastActor(n int, event string) (login string, found bool, err error) {
	out, err := g.raw("api", "--paginate",
		fmt.Sprintf("repos/%s/issues/%d/timeline?per_page=100", g.Repo, n))
	if err != nil {
		return "", false, err
	}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var page []timelineEvent
		if e := dec.Decode(&page); e != nil {
			if e == io.EOF {
				break
			}
			return "", false, fmt.Errorf("gh api timeline for #%d: undecodable output: %w", n, e)
		}
		for _, te := range page {
			if te.Event == event && te.Actor.Login != "" {
				login = te.Actor.Login
			}
		}
	}
	if login == "" {
		return "", false, nil
	}
	return login, true, nil
}
