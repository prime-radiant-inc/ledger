package cmd

import (
	"sort"
	"strings"

	"ledger/internal/fold"
	"ledger/internal/out"
)

// whereClause is one parsed --where flag: an exact-match clause
// (field=value) or a token-membership clause (field~=token, for
// multi-fields — the value is split on commas and the token must appear
// exactly).
type whereClause struct {
	Field, Value string
	Token        bool
}

// parseWhereSpecs turns raw --where strings into clauses. ~= is checked
// before = since it's the more specific separator.
func parseWhereSpecs(specs []string) ([]whereClause, error) {
	var clauses []whereClause
	for _, spec := range specs {
		if idx := strings.Index(spec, "~="); idx >= 0 {
			clauses = append(clauses, whereClause{Field: spec[:idx], Value: spec[idx+2:], Token: true})
			continue
		}
		f, v, cut := strings.Cut(spec, "=")
		if !cut {
			return nil, out.Errf("bad_usage", "use --where FIELD=VALUE or --where FIELD~=TOKEN", 4,
				"--where '%s' is not FIELD=VALUE or FIELD~=TOKEN", spec)
		}
		clauses = append(clauses, whereClause{Field: f, Value: v})
	}
	return clauses, nil
}

// declaredFields lists every field name a --where clause may legally name:
// the enum/free fields from --field plus the --multi-field names.
func declaredFields(led *fold.Ledger) []string {
	names := make([]string, 0, len(led.Schema)+len(led.MultiFields))
	for f := range led.Schema {
		names = append(names, f)
	}
	names = append(names, led.MultiFields...)
	sort.Strings(names)
	return names
}

// validateWhere rejects a --where naming a field the ledger never declared.
func validateWhere(led *fold.Ledger, clauses []whereClause) error {
	for _, cl := range clauses {
		if _, ok := led.Schema[cl.Field]; ok {
			continue
		}
		if led.IsMultiField(cl.Field) {
			continue
		}
		return out.Errf("bad_usage", "declared fields: "+strings.Join(declaredFields(led), ", "), 4,
			"--where names '%s', which is not a declared field on '%s'", cl.Field, led.Slug)
	}
	return nil
}

// matchesWhere reports whether key's current spine values satisfy every
// clause (AND). A clause whose field was never set on this key never matches.
func matchesWhere(led *fold.Ledger, key string, clauses []whereClause) bool {
	for _, cl := range clauses {
		ev, ok := led.Spine[key][cl.Field]
		if !ok {
			return false
		}
		val := ev.Fields[cl.Field]
		if cl.Token {
			if !contains(strings.Split(val, ","), cl.Value) {
				return false
			}
		} else if val != cl.Value {
			return false
		}
	}
	return true
}

// matchingKeys is every key in the fold whose current values satisfy every
// clause. An empty clause list matches every known key.
func matchingKeys(led *fold.Ledger, clauses []whereClause) map[string]bool {
	m := map[string]bool{}
	for key := range led.Spine {
		if matchesWhere(led, key, clauses) {
			m[key] = true
		}
	}
	return m
}
