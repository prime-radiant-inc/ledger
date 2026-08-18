// Package model defines the on-disk event/meta shapes and identity resolution.
package model

import (
	"os"
	"regexp"
	"strings"
	"time"

	"ledger/internal/gitx"
)

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

const (
	TSLayout       = "2006-01-02T15:04:05.000"
	TSLayoutLegacy = "2006-01-02T15:04:05"
)

func ValidSlug(s string) bool { return slugRE.MatchString(s) }

func ParseTS(s string) (time.Time, error) {
	if t, err := time.Parse(TSLayout, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(TSLayoutLegacy, s)
	return t.UTC(), err
}

// Contains reports whether x is present in xs.
func Contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

type Origin struct {
	Host          string `json:"host"`
	CWD           string `json:"cwd"`
	PID           int    `json:"pid"`
	Branch        string `json:"branch"`
	Head          string `json:"head"`
	Session       string `json:"session,omitempty"`
	SessionSource string `json:"session_source,omitempty"`
}

type Event struct {
	TS                string            `json:"ts"`
	Type              string            `json:"type"`
	Key               string            `json:"key,omitempty"`
	Fields            map[string]string `json:"fields,omitempty"`
	Kind              string            `json:"kind,omitempty"`
	Text              string            `json:"text,omitempty"`
	Field             string            `json:"field,omitempty"`
	Value             string            `json:"value,omitempty"`
	Reason            string            `json:"reason,omitempty"`
	LifecycleKind     string            `json:"lifecycle_kind,omitempty"`
	Successor         string            `json:"successor,omitempty"`
	Evidence          []string          `json:"evidence,omitempty"`
	Children          []string          `json:"children,omitempty"`
	Author            string            `json:"author"`
	Origin            Origin            `json:"origin"`
	IdempotencyKey    string            `json:"idempotency_key,omitempty"`
	Override          string            `json:"override,omitempty"`
	ContestedResolved []string          `json:"contested_resolved,omitempty"`
	ImportedFrom      string            `json:"imported_from,omitempty"`
	ID                string            `json:"-"`
	CommitterOverride string            `json:"-"`
}

type Meta struct {
	Slug            string              `json:"slug"`
	Scope           string              `json:"scope"`
	Created         string              `json:"created"`
	CreatedBy       string              `json:"created_by"`
	Owner           string              `json:"owner,omitempty"`
	Supersedes      string              `json:"supersedes,omitempty"`
	Base            string              `json:"base,omitempty"`
	Fields          map[string][]string `json:"fields"`
	RequireEvidence map[string][]string `json:"require_evidence,omitempty"`
	FieldOrder      []string            `json:"field_order"`
	MultiFields     []string            `json:"multi_fields,omitempty"`
	Terminal        map[string][]string `json:"terminal,omitempty"`
	Guard           []string            `json:"guard,omitempty"`
	StaleAfter      string              `json:"stale_after,omitempty"` // Go time.ParseDuration input, verbatim
}

func HarnessMarker() string {
	if os.Getenv("CLAUDECODE") != "" {
		return "claude-code"
	}
	if os.Getenv("CODEX_THREAD_ID") != "" {
		return "codex"
	}
	return "terminal"
}

func ResolveAuthor(asFlag string) string {
	if asFlag != "" {
		return asFlag
	}
	if a := os.Getenv("LEDGER_AUTHOR"); a != "" {
		return a
	}
	if m := HarnessMarker(); m != "terminal" {
		return m // an agent omitting --as must never sign as the human (spec, Identity)
	}
	return os.Getenv("USER")
}

func CaptureOrigin(r gitx.Repo) Origin {
	host, _ := os.Hostname()
	cwd, _ := os.Getwd()
	o := Origin{Host: host, CWD: cwd, PID: os.Getpid()}
	br, _, code := r.Git("", "symbolic-ref", "--short", "-q", "HEAD")
	head, _, _ := r.Git("", "rev-parse", "--short", "HEAD")
	o.Head = head
	if code == 0 && br != "" {
		o.Branch = br
	} else if head != "" {
		o.Branch = "(detached@" + head + ")"
	}
	for _, src := range []string{"CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID"} {
		if v := os.Getenv(src); v != "" {
			o.Session, o.SessionSource = v, src
			break
		}
	}
	return o
}

func NewEvent(typ, author string, r gitx.Repo) Event {
	return Event{TS: Now().UTC().Format(TSLayout), Type: typ,
		Author: author, Origin: CaptureOrigin(r)}
}

// nowFn is the tool's single clock funnel: every evaluation-time or
// mint-time read in the tree goes through Now(), never time.Now()
// directly (sync spec, Addition 4/1). Production code never reassigns
// it — the released binary has no clock env var or flag, so nowFn is
// always time.Now() outside a test. SetNowForTest is the only seam.
var nowFn = time.Now

// Now returns the current time through the clock funnel.
func Now() time.Time { return nowFn() }

// SetNowForTest points the clock funnel at fn and returns a restore func
// the caller defers — the same save/restore shape as the package-var test
// seams elsewhere in the tree (e.g. cmd.lsClosedCutoff), just cross-package
// since Now() is consulted well outside internal/model. Test-only: no
// production code path may call this.
func SetNowForTest(fn func() time.Time) (restore func()) {
	old := nowFn
	nowFn = fn
	return func() { nowFn = old }
}

var _ = strings.TrimSpace // reserved for future normalizers
