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
)

type CLIError struct {
	Code, Message, Hint string
	ExitCode            int
}

func (e *CLIError) Error() string { return e.Code + ": " + e.Message }

func Errf(code, hint string, exit int, format string, a ...any) *CLIError {
	return &CLIError{Code: code, Message: fmt.Sprintf(format, a...), Hint: hint, ExitCode: exit}
}

func Emit(w io.Writer, tty bool, payload map[string]any, lines []string) {
	if tty {
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
		return
	}
	payload["ok"] = true
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
	doc, _ := json.Marshal(map[string]string{"error": e.Code, "message": e.Message, "hint": e.Hint})
	fmt.Fprintln(w, string(doc))
}

func IsTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

func Age(ts string) string {
	t, err := time.Parse("2006-01-02T15:04:05", ts)
	if err != nil {
		return ts
	}
	s := int(time.Since(t.UTC()).Seconds())
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
