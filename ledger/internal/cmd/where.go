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

// validateWhere rejects: a --where naming a field the ledger never declared
// (unknown_field); a ~= clause on a field that isn't a multi-field
// (bad_usage — token membership only makes sense there); and two exact-match
// (=) clauses naming the same field (bad_usage — unsatisfiable by
// construction, since a field can't equal two different values at once).
func validateWhere(led *fold.Ledger, clauses []whereClause) error {
	seenExact := map[string]bool{}
	for _, cl := range clauses {
		_, isEnum := led.Schema[cl.Field]
		isMulti := led.IsMultiField(cl.Field)
		if !isEnum && !isMulti {
			return out.Errf("unknown_field", "declared fields: "+strings.Join(declaredFields(led), ", "), 4,
				"--where names '%s', which is not a declared field on '%s'", cl.Field, led.Slug)
		}
		if cl.Token && !isMulti {
			return out.Errf("bad_usage", "ledger show --help", 4,
				"--where '%s~=...' is token membership, but '%s' is not a multi-field", cl.Field, cl.Field)
		}
		if !cl.Token {
			if seenExact[cl.Field] {
				return out.Errf("bad_usage", "a field can equal only one value — drop one of the --where clauses", 4,
					"two --where clauses both test '%s=...' — unsatisfiable together", cl.Field)
			}
			seenExact[cl.Field] = true
		}
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
