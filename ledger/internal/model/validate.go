package model

import (
	"fmt"
	"strings"
	"time"
)

// DeclErr is a board-declaration validation failure (nil = valid).
// Declarations are immutable once a ledger is created, so every minting
// path — create, and import re-validating an exported meta.json — runs
// the same checks and reports the same shape.
type DeclErr struct{ Ident, Hint, Msg string }

// ReadyCapable reports whether m's declarations opt the board into
// issue-tracker (ready) semantics: a "status" field declared with a
// vocab (not free text), and a non-empty --terminal subset on it.
func ReadyCapable(m Meta) bool {
	vocab, declared := m.Fields["status"]
	return declared && len(vocab) > 0 && len(m.Terminal["status"]) > 0
}

// ValidateDeclarations checks a Meta's board declarations for internal
// consistency (spec "The board"). Every violation is bad_value and
// names its fix, since a bad declaration has no repair path once the
// ledger exists.
func ValidateDeclarations(m Meta) *DeclErr {
	if e := validateReservedNames(m); e != nil {
		return e
	}
	if e := validateSubsets(m); e != nil {
		return e
	}
	if e := validateGuard(m); e != nil {
		return e
	}
	if e := validateStaleAfter(m); e != nil {
		return e
	}
	if e := validateMultiFieldNames(m); e != nil {
		return e
	}
	return validateReadyCapableShape(m)
}

// TitleFieldName is the one reserved field name: a key's title is not a
// board field. It is derived (the seed message, then rename events) and
// read as a pseudo-field by the contested pass, which unions BOTH title
// streams; a board that could also declare and guard a real "title" field
// would split that read path from the rename write path, so no board may
// declare, guard, or extend one.
const TitleFieldName = "title"

// validateReservedNames enforces the reserved name, ahead of every other
// check so a `--guard title` reads as reserved rather than as the
// undeclared-field error validateGuard would otherwise reach first.
func validateReservedNames(m Meta) *DeclErr {
	reserved := func(flag string) *DeclErr {
		return &DeclErr{Ident: "bad_value",
			Hint: "titles come from the seed's -m and `set <key> --rename \"<new title>\"` — drop the declaration",
			Msg:  fmt.Sprintf("%s %s is reserved: a key's title is derived, never a declared field", flag, TitleFieldName)}
	}
	if _, declared := m.Fields[TitleFieldName]; declared {
		return reserved("--field")
	}
	if Contains(m.MultiFields, TitleFieldName) {
		return reserved("--multi-field")
	}
	if Contains(m.Guard, TitleFieldName) {
		return reserved("--guard")
	}
	return nil
}

// validateSubsets enforces rule 1: --terminal and --require-evidence
// values must be a subset of the named field's declared vocab.
func validateSubsets(m Meta) *DeclErr {
	specs := []struct {
		flag   string
		values map[string][]string
	}{
		{"--terminal", m.Terminal},
		{"--require-evidence", m.RequireEvidence},
	}
	for _, spec := range specs {
		for field, vals := range spec.values {
			vocab, declared := m.Fields[field]
			if !declared {
				return &DeclErr{Ident: "bad_value",
					Hint: "declare it first with --field " + field + "=...",
					Msg:  fmt.Sprintf("%s names '%s', which is not a declared field", spec.flag, field)}
			}
			if !subsetOf(vals, vocab) {
				return &DeclErr{Ident: "bad_value",
					Hint: fmt.Sprintf("%s values must be a subset of %s's declared vocab: %s", spec.flag, field, strings.Join(vocab, ", ")),
					Msg:  fmt.Sprintf("%s %s=%s is not a subset of %s's declared vocab", spec.flag, field, strings.Join(vals, ","), field)}
			}
		}
	}
	return nil
}

// validateGuard enforces rule 2: --guard must name a declared field
// (enum or multi) — a typo'd guard silently disables every protection
// the invariant would otherwise give that field.
func validateGuard(m Meta) *DeclErr {
	for _, g := range m.Guard {
		if _, declared := m.Fields[g]; declared {
			continue
		}
		if Contains(m.MultiFields, g) {
			continue
		}
		return &DeclErr{Ident: "bad_value",
			Hint: "declare it first with --field or --multi-field, or drop the --guard",
			Msg:  fmt.Sprintf("--guard names '%s', which is not a declared field", g)}
	}
	return nil
}

// validateStaleAfter enforces rule 3: --stale-after must parse via
// time.ParseDuration. Empty (undeclared) means no claim is ever stale.
func validateStaleAfter(m Meta) *DeclErr {
	if m.StaleAfter == "" {
		return nil
	}
	if _, err := time.ParseDuration(m.StaleAfter); err != nil {
		return &DeclErr{Ident: "bad_value",
			Hint: `use a Go duration time.ParseDuration accepts, e.g. "2h" or "90m"`,
			Msg:  fmt.Sprintf("--stale-after '%s' does not parse via time.ParseDuration: %v", m.StaleAfter, err)}
	}
	return nil
}

// validateMultiFieldNames enforces rule 5: a multi-field name may not
// collide with a declared field name — Schema is a single flat
// namespace (see set.go's unknown_field check), so a collision would
// make one of the two silently unreachable.
func validateMultiFieldNames(m Meta) *DeclErr {
	for _, mf := range m.MultiFields {
		if _, declared := m.Fields[mf]; declared {
			return &DeclErr{Ident: "bad_value",
				Hint: "fields and multi-fields share one namespace — rename one",
				Msg:  fmt.Sprintf("--multi-field '%s' collides with a declared field of the same name", mf)}
		}
	}
	return nil
}

// validateReadyCapableShape enforces rule 4: ready-capability is
// syntactic and all-or-nothing. Declaring --terminal on a field named
// "status" opts the board in; from there the full shape is required.
func validateReadyCapableShape(m Meta) *DeclErr {
	terminal := m.Terminal["status"]
	if len(terminal) == 0 {
		return nil // not opted in; nothing more to check
	}
	if !Contains(m.Guard, "status") {
		return &DeclErr{Ident: "bad_value",
			Hint: "add --guard status",
			Msg:  "ready-capable boards (--terminal on status) require --guard status"}
	}
	nonTerminal := subtract(m.Fields["status"], terminal)
	for _, want := range []string{"open", "in-progress"} {
		if !Contains(nonTerminal, want) {
			return &DeclErr{Ident: "bad_value",
				Hint: fmt.Sprintf("declare status with a non-terminal value '%s'", want),
				Msg:  fmt.Sprintf("ready-capable boards' non-terminal status vocab must include '%s' (have: %s)", want, strings.Join(nonTerminal, ", "))}
		}
	}
	if len(nonTerminal) != 2 {
		return &DeclErr{Ident: "bad_value",
			Hint: "non-terminal status vocab must be exactly open, in-progress — move any third value to --terminal, or name this field something else",
			Msg:  fmt.Sprintf("ready-capable boards' non-terminal status vocab must be exactly {open, in-progress} (have: %s)", strings.Join(nonTerminal, ", "))}
	}
	if !Contains(m.MultiFields, "labels") {
		return &DeclErr{Ident: "bad_value",
			Hint: "add --multi-field labels",
			Msg:  "ready-capable boards require a 'labels' multi-field declared (keeps the human quarantine signal possible)"}
	}
	if Contains(m.MultiFields, "blocked-by") && !Contains(m.Guard, "blocked-by") {
		return &DeclErr{Ident: "bad_value",
			Hint: "add --guard blocked-by",
			Msg:  "a declared 'blocked-by' multi-field on a ready-capable board requires --guard blocked-by"}
	}
	return nil
}

func subsetOf(vals, vocab []string) bool {
	for _, v := range vals {
		if !Contains(vocab, v) {
			return false
		}
	}
	return true
}

func subtract(all, minus []string) []string {
	var out []string
	for _, v := range all {
		if !Contains(minus, v) {
			out = append(out, v)
		}
	}
	return out
}
