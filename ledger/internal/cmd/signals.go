package cmd

import (
	"fmt"
	"strings"
	"time"

	"ledger/internal/fold"
	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

// standingSignal is one of rule 5's three interference gates, with the fact
// that justifies it — the needs_override error names every standing signal
// together with its facts (claimant and age; the label; the settled value
// and its evidence state).
type standingSignal struct{ Name, Detail string }

// needsOverrideError is rule 5's rejection: at least one signal stood and
// the write carried no --override. Propagated verbatim up through
// store.AppendExpectChecked's CAS loop, same as store.ClaimLostError — not
// retried, since a signal check failure is a decision, not a race.
type needsOverrideError struct {
	Key     string
	Signals []standingSignal
}

func (e *needsOverrideError) Error() string { return "needs_override: " + e.Key }

// computeSignals folds freshLed's current state for key and reports every
// standing signal against a write to field, scoped per rule 5: human gates
// every guarded write (key-scoped, everyone including the write's own
// author); claim and settled gate status writes only. Order is fixed
// (claim, human, settled) so a composed override record ("claim,human") is
// reproducible rather than map-order flaky.
func computeSignals(freshLed *fold.Ledger, key, field, author string) []standingSignal {
	ks := classifyKey(freshLed, key, time.Now())
	var sigs []standingSignal
	if field == "status" && ks.HasStatus && ks.Claimed && !ks.Stale && ks.StatusEv.Author != author {
		sigs = append(sigs, standingSignal{"claim",
			fmt.Sprintf("claimed by %s, age %s", ks.StatusEv.Author, out.Age(ks.StatusEv.TS))})
	}
	if ks.Human {
		sigs = append(sigs, standingSignal{"human", "labeled 'human'"})
	}
	if field == "status" && ks.HasStatus && ks.Terminal {
		detail := "status=" + ks.Status
		if len(ks.StatusEv.Evidence) > 0 {
			detail += ", evidence: " + strings.Join(ks.StatusEv.Evidence, ",")
		} else {
			detail += ", no evidence"
		}
		sigs = append(sigs, standingSignal{"settled", detail})
	}
	return sigs
}

// rule5Check builds the store.SignalCheck a guarded write on a ready-capable
// board wires into AppendExpectChecked: it re-derives signals from the same
// fresh events the field-scoped CAS precondition just read (rule 7 — never a
// pre-loop snapshot), rejects with needsOverrideError when a signal stands
// and the caller didn't pass --override, and otherwise returns the
// tool-computed override marker (never caller-asserted) to record on the
// landing event.
func rule5Check(led *fold.Ledger, key, field, author string, override bool) store.SignalCheck {
	return func(evs []model.Event) (string, error) {
		freshLed := fold.Fold(led.Slug, evs, led.Meta)
		sigs := computeSignals(freshLed, key, field, author)
		if len(sigs) == 0 {
			return "", nil
		}
		if !override {
			return "", &needsOverrideError{Key: key, Signals: sigs}
		}
		names := make([]string, len(sigs))
		for i, s := range sigs {
			names[i] = s.Name
		}
		return strings.Join(names, ","), nil
	}
}

// needsOverrideMessage renders every standing signal with its facts and the
// paste-ready fix, per rule 5.
func needsOverrideMessage(e *needsOverrideError) string {
	parts := make([]string, len(e.Signals))
	for i, s := range e.Signals {
		parts[i] = s.Name + " (" + s.Detail + ")"
	}
	return fmt.Sprintf("'%s' has standing signal(s) that guard this write: %s", e.Key, strings.Join(parts, "; "))
}
