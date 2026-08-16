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

func ValidSlug(s string) bool { return slugRE.MatchString(s) }

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
	// MultiFields names fields that are multi-valued and vocab-free (comma
	// token lists, replace-wholesale on set — spike addition). Omitted for
	// every ledger created before this existed, so old meta.json unmarshals
	// to a nil slice and behaves exactly as before.
	MultiFields []string `json:"multi_fields,omitempty"`
	// Terminal declares, per field, which values mean "no longer blocks
	// anything" — read by the `ready` verb to resolve blocked-by edges.
	Terminal map[string][]string `json:"terminal,omitempty"`
}

// LatestSetEvent is the most recent "set" event touching key in evs (which
// must be in chronological order) — the per-key version stamp `set
// --expect` claims against, and the id `ready` hands back to a would-be
// claimant. Shared by the store's conditional-write precondition (reading
// a freshly-fetched chain) and cmd's read side (reading the already-folded
// one), so the "latest event for a key" rule lives in exactly one place.
func LatestSetEvent(evs []Event, key string) (Event, bool) {
	var found Event
	ok := false
	for _, ev := range evs {
		if ev.Type == "set" && ev.Key == key {
			found, ok = ev, true
		}
	}
	return found, ok
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
	return Event{TS: time.Now().UTC().Format("2006-01-02T15:04:05"), Type: typ,
		Author: author, Origin: CaptureOrigin(r)}
}

var _ = strings.TrimSpace // reserved for future normalizers
