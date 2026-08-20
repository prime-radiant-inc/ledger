// Package out is the single output pathway: one JSON document per command
// (non-TTY), aligned text on a TTY, errors as {error,message,hint}.
package out

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ledger/internal/model"
)

type CLIError struct {
	Code, Message, Hint string
	ExitCode            int
	// Signals names the standing rule-5 signals that produced a
	// needs_override error, machine-readable. The message already renders
	// them for a person ("human (labeled 'human')"), but a program deciding
	// what to do next — override a dissolving claim, refuse a human label —
	// must never have to parse English to tell them apart. Empty on every
	// other error.
	Signals []string
}

func (e *CLIError) Error() string { return e.Code + ": " + e.Message }

func Errf(code, hint string, exit int, format string, a ...any) *CLIError {
	return &CLIError{Code: code, Message: fmt.Sprintf(format, a...), Hint: hint, ExitCode: exit}
}

// WithSignals attaches the signal names to a needs_override error. Returned
// for call-site chaining, so the error stays one expression.
func (e *CLIError) WithSignals(names []string) *CLIError {
	e.Signals = names
	return e
}

func Emit(w io.Writer, tty bool, payload map[string]any, lines []string) {
	if tty {
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
		return
	}
	// A caller building the sync/push partial-failure envelope pre-sets
	// ok:false (plus error/message/hint) directly on the payload so the
	// whole document — outcomes and error contract together — is written
	// in one write, never a second error document tacked on after. Every
	// other caller leaves "ok" unset and gets the ordinary true.
	if _, has := payload["ok"]; !has {
		payload["ok"] = true
	}
	doc, _ := json.MarshalIndent(payload, "", " ")
	fmt.Fprintln(w, string(doc))
}

func WriteError(w io.Writer, tty bool, e *CLIError) {
	if tty {
		fmt.Fprintf(w, "error: %s\n", e.Message)
		if e.Hint != "" {
			fmt.Fprintf(w, "  fix: %s\n", e.Hint)
		}
		return
	}
	payload := map[string]any{"error": e.Code, "message": e.Message, "hint": e.Hint}
	if len(e.Signals) > 0 {
		payload["signals"] = e.Signals
	}
	doc, _ := json.Marshal(payload)
	fmt.Fprintln(w, string(doc))
}

func IsTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// Age renders ts's age against the funnel's current time (model.Now()).
func Age(ts string) string { return AgeAt(ts, model.Now()) }

// AgeAt renders ts's age relative to an explicit evaluation clock — ready
// and notes --latest pass --at's fixed clock here; every other caller goes
// through Age, which supplies model.Now(). A future ts (at is before ts —
// --at fixed in the past, or a peer host whose clock runs ahead) clamps to
// zero rather than rendering a negative age: the sync spec's general age
// clamp (Addition 4), which applies everywhere, under both clocks.
func AgeAt(ts string, at time.Time) string {
	t, err := model.ParseTS(ts)
	if err != nil {
		return ts
	}
	s := int(at.Sub(t).Seconds())
	if s < 0 {
		s = 0
	}
	switch {
	case s >= 86400:
		return fmt.Sprintf("%dd ago", s/86400)
	case s >= 3600:
		return fmt.Sprintf("%dh ago", s/3600)
	case s >= 60:
		return fmt.Sprintf("%dm ago", s/60)
	default:
		return fmt.Sprintf("%ds ago", s)
	}
}

// ContestedResolvedMarker renders an event's tool-computed contest
// resolution for a TTY (sync design, Addition 3): the losing write-heads
// this event collapsed. Mandatory labeling, the same class as `override:` —
// a JSON-only record fails the reader it exists for, whether that reader is
// the writer seeing their own unwitting touch-base resolve a race or
// somebody reading the chain back later. Empty for the overwhelmingly
// common event that resolved nothing.
func ContestedResolvedMarker(losers []string) string {
	if len(losers) == 0 {
		return ""
	}
	return "contested_resolved: " + strings.Join(losers, ",")
}

// OverrideMarker renders the standing signals a write overrode, for a TTY —
// the peer of ContestedResolvedMarker and for the same reason: the writer
// must see the attribution the tool just recorded in their name. Empty when
// the write overrode nothing.
func OverrideMarker(signals string) string {
	if signals == "" {
		return ""
	}
	return "override: " + signals
}

// EscapeControls neutralizes C0 controls and ESC so a note body can never
// visually overwrite the render on a TTY (counterfeit-provenance attack).
func EscapeControls(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == 0x1b:
			b.WriteString("^[")
		case r < 0x20:
			b.WriteString("^" + string(rune('@'+r)))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
