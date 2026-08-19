package model

import (
	"strings"
	"testing"
)

// deepCopyMeta returns a Meta with every map/slice independently
// allocated, so a test case's mutate func can't corrupt the shared
// canonical fixture between table cases.
func deepCopyMeta(m Meta) Meta {
	cp := m
	cp.Fields = map[string][]string{}
	for k, v := range m.Fields {
		cp.Fields[k] = append([]string{}, v...)
	}
	cp.Terminal = map[string][]string{}
	for k, v := range m.Terminal {
		cp.Terminal[k] = append([]string{}, v...)
	}
	cp.RequireEvidence = map[string][]string{}
	for k, v := range m.RequireEvidence {
		cp.RequireEvidence[k] = append([]string{}, v...)
	}
	cp.MultiFields = append([]string{}, m.MultiFields...)
	cp.Guard = append([]string{}, m.Guard...)
	cp.FieldOrder = append([]string{}, m.FieldOrder...)
	return cp
}

func TestValidateDeclarations(t *testing.T) {
	ok := Meta{
		Fields:      map[string][]string{"status": {"open", "in-progress", "closed", "wontfix"}},
		FieldOrder:  []string{"status"},
		MultiFields: []string{"labels", "blocked-by"},
		Terminal:    map[string][]string{"status": {"closed", "wontfix"}},
		Guard:       []string{"status", "blocked-by"},
		StaleAfter:  "2h",
	}
	if e := ValidateDeclarations(ok); e != nil {
		t.Fatalf("canonical shape must validate: %+v", e)
	}
	cases := []struct {
		name    string
		mutate  func(*Meta)
		wantMsg string
	}{
		{"terminal not subset", func(m *Meta) { m.Terminal["status"] = []string{"nope"} }, "subset"},
		{"guard undeclared", func(m *Meta) { m.Guard = append(m.Guard, "priority") }, "not a declared field"},
		{"bad stale-after", func(m *Meta) { m.StaleAfter = "2 hours" }, "ParseDuration"},
		{"ready without guard status", func(m *Meta) { m.Guard = []string{"blocked-by"} }, "--guard status"},
		{"missing in-progress", func(m *Meta) { m.Fields["status"] = []string{"open", "closed", "wontfix"} }, "in-progress"},
		{"third non-terminal", func(m *Meta) { m.Fields["status"] = append(m.Fields["status"], "parked") }, "exactly"},
		{"missing labels", func(m *Meta) { m.MultiFields = []string{"blocked-by"} }, "labels"},
		{"blocked-by unguarded", func(m *Meta) { m.Guard = []string{"status"} }, "--guard blocked-by"},
		{"multi-field collides with enum", func(m *Meta) { m.MultiFields = append(m.MultiFields, "status") }, "collides"},
		{"title declared as an enum field", func(m *Meta) { m.Fields["title"] = []string{"a", "b"} }, "reserved"},
		{"title declared as a multi-field", func(m *Meta) { m.MultiFields = append(m.MultiFields, "title") }, "reserved"},
		{"title guarded", func(m *Meta) { m.Guard = append(m.Guard, "title") }, "reserved"},
	}
	for _, c := range cases {
		m := deepCopyMeta(ok)
		c.mutate(&m)
		e := ValidateDeclarations(m)
		if e == nil || e.Ident != "bad_value" || !strings.Contains(e.Msg+e.Hint, c.wantMsg) {
			t.Fatalf("%s: want bad_value mentioning %q, got %+v", c.name, c.wantMsg, e)
		}
	}
}

func TestReadyCapable(t *testing.T) {
	ready := Meta{
		Fields:   map[string][]string{"status": {"open", "in-progress", "closed"}},
		Terminal: map[string][]string{"status": {"closed"}},
	}
	if !ReadyCapable(ready) {
		t.Fatalf("declared enum status with non-empty terminal must be ready-capable: %+v", ready)
	}
	noTerminal := Meta{Fields: map[string][]string{"status": {"open", "closed"}}}
	if ReadyCapable(noTerminal) {
		t.Fatal("no --terminal on status must not be ready-capable")
	}
	freeStatus := Meta{Fields: map[string][]string{"status": nil}, Terminal: map[string][]string{"status": {"closed"}}}
	if ReadyCapable(freeStatus) {
		t.Fatal("free-text status (no vocab) must not be ready-capable")
	}
	noStatusField := Meta{Fields: map[string][]string{"other": {"a"}}, Terminal: map[string][]string{"status": {"closed"}}}
	if ReadyCapable(noStatusField) {
		t.Fatal("terminal naming an undeclared status field must not be ready-capable")
	}
}
