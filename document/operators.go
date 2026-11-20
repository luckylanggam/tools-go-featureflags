package document

import "strings"

// Built-in operators. Each also has a negated form with the NegationPrefix
// (not_eq, not_in_group, ...). Catalog custom operators may not reuse any
// of these names.
const (
	OpEq         = "eq"
	OpIn         = "in"
	OpInGroup    = "in_group"
	OpGt         = "gt"
	OpLt         = "lt"
	OpStartsWith = "starts_with"
	OpMatches    = "matches"
	OpContains   = "contains"
	OpExists     = "exists"

	NegationPrefix = "not_"
)

// BuiltinOperators lists the built-in operators without their negations.
var BuiltinOperators = []string{
	OpEq, OpIn, OpInGroup, OpGt, OpLt, OpStartsWith, OpMatches, OpContains, OpExists,
}

// BaseOperator strips the negation from a built-in operator name. ok is
// false when op is not a built-in operator or its negation.
func BaseOperator(op string) (base string, negated bool, ok bool) {
	base, negated = strings.CutPrefix(op, NegationPrefix)
	for _, b := range BuiltinOperators {
		if b == base {
			return base, negated, true
		}
	}
	return "", false, false
}
