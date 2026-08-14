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
	Host string `json:"host"`; CWD string `json:"cwd"`; PID int `json:"pid"`
	Branch string `json:"branch"`; Head string `json:"head"`
	Session string `json:"session,omitempty"`; SessionSource string `json:"session_source,omitempty"`
}

type Event struct {
	TS string `json:"ts"`; Type string `json:"type"`
	Key string `json:"key,omitempty"`; Fields map[string]string `json:"fields,omitempty"`
	Kind string `json:"kind,omitempty"`; Text string `json:"text,omitempty"`
	Field string `json:"field,omitempty"`; Value string `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`; LifecycleKind string `json:"lifecycle_kind,omitempty"`
	Successor string `json:"successor,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	Author string `json:"author"`; Origin Origin `json:"origin"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	ID string `json:"-"`
}

type Meta struct {
	Slug string `json:"slug"`; Scope string `json:"scope"`
	Created string `json:"created"`; CreatedBy string `json:"created_by"`
	Owner string `json:"owner,omitempty"`; Supersedes string `json:"supersedes,omitempty"`
	Base string `json:"base,omitempty"`
	Fields map[string][]string `json:"fields"`
	RequireEvidence map[string][]string `json:"require_evidence,omitempty"`
	FieldOrder []string `json:"field_order"`
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
