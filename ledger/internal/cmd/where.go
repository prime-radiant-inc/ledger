package cmd

import (
	"strings"

	"ledger/internal/board"
	"ledger/internal/model"
	"ledger/internal/out"
)

// WhereClause is one parsed `--where` term: an exact match (Field == Value)
// or, when Membership is set, a membership test (Value is a token that must
// be present in Field's multi-valued set). Shared by show and (Task 10)
// ready, both of which AND every clause together.
type WhereClause struct {
	Field, Value string
	Membership   bool
}

// parseWhere validates and parses every raw `--where` flag value against
// meta's declarations (spec "Filtered reads"): `field=value` is exact match
// and requires field to be a declared enum (meta.Fields); `field~=token` is
// membership and requires field to be a declared multi-field
// (meta.MultiFields) — the mismatch either way is bad_usage, since sets have
// no exact-string identity and enums have no membership. An undeclared
// field is unknown_field, naming the declared list. Two exact clauses on
// the same field are bad_usage — unsatisfiable, since a key can carry only
// one value for an enum field at a time.
func parseWhere(raw []string, meta model.Meta) ([]WhereClause, error) {
	declared := declaredFieldNames(meta)
	var clauses []WhereClause
	exactSeen := map[string]bool{}
	for _, r := range raw {
		field, value, membership, err := splitWhereTerm(r)
		if err != nil {
			return nil, err
		}
		_, isEnum := meta.Fields[field]
		isMulti := contains(meta.MultiFields, field)
		if !isEnum && !isMulti {
			return nil, out.Errf("unknown_field", "declared fields: "+declared, 4,
				"'%s' is not a declared field", field)
		}
		if membership && !isMulti {
			return nil, out.Errf("bad_usage", "'"+field+"' is an enum field — use --where "+field+"=<value>", 4,
				"'~=' is only valid on multi-fields; '%s' is not one", field)
		}
		if !membership && isMulti {
			return nil, out.Errf("bad_usage", "'"+field+"' is a multi-field — use --where "+field+"~=<token>", 4,
				"'=' is not valid on multi-field '%s'; sets have no exact-string identity", field)
		}
		if !membership {
			if exactSeen[field] {
				return nil, out.Errf("bad_usage", "drop one of the two --where "+field+"= clauses", 4,
					"two exact --where clauses on '%s' can never both hold", field)
			}
			exactSeen[field] = true
		}
		clauses = append(clauses, WhereClause{Field: field, Value: value, Membership: membership})
	}
	return clauses, nil
}

// splitWhereTerm parses one raw `--where` flag value into field/value/
// membership. "~=" is checked before "=" since it contains "=".
func splitWhereTerm(raw string) (field, value string, membership bool, err error) {
	if strings.Contains(raw, "~=") {
		field, value, _ = strings.Cut(raw, "~=")
		return field, value, true, nil
	}
	if strings.Contains(raw, "=") {
		field, value, _ = strings.Cut(raw, "=")
		return field, value, false, nil
	}
	return "", "", false, out.Errf("bad_usage", "format: --where FIELD=VALUE or --where FIELD~=TOKEN", 4,
		"'%s' is not a valid --where clause", raw)
}

// matchWhere reports whether key k satisfies every clause (AND'd). A
// membership clause (c.Membership, only ever produced for a declared
// multi-field — parseWhere rejects any other combination) checks
// k.Multi[c.Field], which board.Build populates for EVERY declared
// multi-field including "labels" and "blocked-by". An exact clause checks
// k.Status for "status" or k.Fields[c.Field] for any other declared enum
// field. A key with no value for the field simply doesn't match — no
// error, per spec. A nil k (no board event has ever touched the key) never
// matches a non-empty clause list.
func matchWhere(k *board.Key, cs []WhereClause) bool {
	for _, c := range cs {
		if !matchOne(k, c) {
			return false
		}
	}
	return true
}

func matchOne(k *board.Key, c WhereClause) bool {
	if k == nil {
		return false
	}
	if c.Membership {
		return contains(k.Multi[c.Field], c.Value)
	}
	if c.Field == "status" {
		return k.Status != nil && k.Status.Value == c.Value
	}
	fs := k.Fields[c.Field]
	return fs != nil && fs.Value == c.Value
}
