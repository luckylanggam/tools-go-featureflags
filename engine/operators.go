package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/luckylanggam/tools-go-featureflags/document"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// operator compiles the document value of a condition into a predicate
// over a caller value already normalized to the attribute's type.
type operator func(c *compiler, attr string, attrType document.Type, value any) (func(v any) bool, error)

// operators are the built-in operators; each also has a not_ form that
// negates it for callers that sent the attribute. exists is handled apart
// because it is the only one that looks at absence.
var operators = map[string]operator{
	"eq":          compileEq,
	"in":          compileIn,
	"in_group":    compileInGroup,
	"gt":          compileOrdered(func(cmp int) bool { return cmp > 0 }),
	"lt":          compileOrdered(func(cmp int) bool { return cmp < 0 }),
	"starts_with": compileString(strings.HasPrefix),
	"contains":    compileString(strings.Contains),
	"matches":     compileMatches,
}

const (
	opExists    = "exists"
	opNotExists = "not_exists"
	negPrefix   = "not_"
)

// condition is a compiled document.Condition.
type condition struct {
	attr     string
	attrType document.Type
	// exists is set for exists / not_exists, which test presence only.
	exists *bool
	negate bool
	// Exactly one of test (built-in) and custom is set otherwise.
	test   func(v any) bool
	custom *customOperator
}

// customOperator is a condition's registered operator and its value; fn is
// nil when the engine has no code for it (WithUnregisteredOperatorsUndecided).
type customOperator struct {
	name    string
	fn      OperatorFunc
	value   any
	observe observer
}

// check evaluates the condition. A missing attribute holds only for
// not_exists; a value that does not convert to the catalog type never holds.
func (c *condition) check(ev *evaluation) match {
	raw, present := ev.lookup(c.attr)
	if c.exists != nil {
		if !present && *c.exists {
			ev.missing(c.attr)
		}
		return matchIf(present == *c.exists)
	}
	if !present {
		ev.missing(c.attr)
		return noMatch
	}
	v, ok := normalize(raw, c.attrType)
	if !ok {
		ev.invalid(c.attr)
		return noMatch
	}
	if c.custom != nil {
		return c.custom.check(ev, v, c.negate)
	}
	return matchIf(c.test(v) != c.negate)
}

// check runs the operator; an error counts as no match, negated or not.
func (o *customOperator) check(ev *evaluation, v any, negate bool) match {
	if o.fn == nil {
		addOnce(&ev.undecided, o.name)
		return undecided
	}
	start := time.Now()
	ok, err := o.fn(ev.ctx, v, o.value)
	o.observe.record(ev.ctx, start, err)
	if err != nil {
		ev.failedOperator(o.name)
		return noMatch
	}
	return matchIf(ok != negate)
}

// compiler turns document flags into evaluable ones, caching group sets.
type compiler struct {
	flags     *document.Flags
	catalog   *document.Catalog
	groups    map[string]map[any]struct{}
	operators map[string]OperatorFunc
	undecided bool
	metrics   *metrics
}

func (c *compiler) flag(name string, f document.Flag) (*flag, error) {
	out := &flag{enabled: f.Enabled, def: f.Default, rules: make([]rule, 0, len(f.Rules))}
	for i, r := range f.Rules {
		compiled, err := c.rule(r)
		if err != nil {
			return nil, fmt.Errorf(ffErrors.ErrEngineCompileRule, name, i, r.Name, err)
		}
		out.rules = append(out.rules, compiled)
	}
	return out, nil
}

func (c *compiler) rule(r document.Rule) (rule, error) {
	out := rule{name: r.Name, then: r.Then, conditions: make([]condition, 0, len(r.If))}
	for i, cond := range r.If {
		compiled, err := c.condition(cond)
		if err != nil {
			return rule{}, fmt.Errorf(ffErrors.ErrEngineCompileCondition, i, err)
		}
		out.conditions = append(out.conditions, compiled)
	}
	if r.Rollout != nil {
		attrType, err := c.attrType(r.Rollout.BucketBy)
		if err != nil {
			return rule{}, fmt.Errorf(ffErrors.ErrEngineCompileRollout, err)
		}
		if r.Rollout.Percentage < 0 || r.Rollout.Percentage > 100 {
			return rule{}, fmt.Errorf(ffErrors.ErrEngineCompileRollout,
				fmt.Errorf(ffErrors.ErrEngineBadPercentage, r.Rollout.Percentage))
		}
		out.rollout = &rollout{percentage: uint64(r.Rollout.Percentage), bucketBy: r.Rollout.BucketBy, attrType: attrType}
	}
	return out, nil
}

func (c *compiler) condition(cond document.Condition) (condition, error) {
	attrType, err := c.attrType(cond.Attr)
	if err != nil {
		return condition{}, err
	}
	out := condition{attr: cond.Attr, attrType: attrType}

	switch cond.Op {
	case opExists, opNotExists:
		want := cond.Op == opExists
		out.exists = &want
		return out, nil
	}

	name := cond.Op
	if base, ok := strings.CutPrefix(name, negPrefix); ok {
		name, out.negate = base, true
	}
	compile, ok := operators[name]
	if !ok {
		if out.custom, err = c.customOperator(cond.Op, name, cond.Value); err != nil {
			return condition{}, err
		}
		return out, nil
	}
	if out.test, err = compile(c, cond.Attr, attrType, cond.Value); err != nil {
		return condition{}, fmt.Errorf(ffErrors.ErrEngineOperator, cond.Op, err)
	}
	return out, nil
}

// customOperator compiles a condition on the custom operator name, written
// op in the document: it must be declared in the catalog, which gives the
// type of its value, and registered unless undecided operators are allowed.
func (c *compiler) customOperator(op, name string, value any) (*customOperator, error) {
	decl, ok := c.catalog.Operators[name]
	if !ok {
		return nil, fmt.Errorf(ffErrors.ErrEngineUnknownOperator, op)
	}
	fn, registered := c.operators[name]
	if !registered && !c.undecided {
		return nil, fmt.Errorf(ffErrors.ErrEngineUnregisteredOperator, name)
	}
	v, err := docValue(value, decl.ValueType)
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrEngineOperator, op, err)
	}
	return &customOperator{name: name, fn: fn, value: v, observe: newObserver(c.metrics.operator, "operator", name)}, nil
}

// resolvers compiles the registered resolvers the catalog declares; the
// others are never used.
func (c *compiler) resolvers(fns map[string]ResolverFunc) (map[string]*resolver, error) {
	out := map[string]*resolver{}
	for attr, fn := range fns {
		decl, ok := c.catalog.Resolvers[attr]
		if !ok {
			continue
		}
		if _, err := c.attrType(attr); err != nil {
			return nil, fmt.Errorf(ffErrors.ErrEngineCompileResolver, attr, err)
		}
		r := &resolver{attr: attr, fn: fn, observe: newObserver(c.metrics.resolver, "attribute", attr)}
		for _, req := range decl.Requires {
			attrType, err := c.attrType(req)
			if err != nil {
				return nil, fmt.Errorf(ffErrors.ErrEngineCompileResolver, attr, err)
			}
			r.requires = append(r.requires, input{attr: req, attrType: attrType})
			r.key += "\x00" + strconv.Quote(req) + ":" + string(attrType)
		}
		r.key = strconv.Quote(attr) + r.key
		out[attr] = r
	}
	return out, nil
}

func (c *compiler) attrType(attr string) (document.Type, error) {
	a, ok := c.catalog.Attributes[attr]
	if !ok {
		return "", fmt.Errorf(ffErrors.ErrEngineUnknownAttribute, attr)
	}
	return a.Type, nil
}

// group returns the values of a group as a set normalized to the type of
// attr, which must be the group's own attribute.
func (c *compiler) group(name, attr string, attrType document.Type) (map[any]struct{}, error) {
	g, ok := c.flags.Groups[name]
	if !ok {
		return nil, fmt.Errorf(ffErrors.ErrEngineUnknownGroup, name)
	}
	if g.Attr != attr {
		return nil, fmt.Errorf(ffErrors.ErrEngineGroupAttribute, name, g.Attr, attr)
	}
	if set, ok := c.groups[name]; ok {
		return set, nil
	}
	set, err := valueSet(g.Values, attrType)
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrEngineGroup, name, err)
	}
	c.groups[name] = set
	return set, nil
}

func compileEq(_ *compiler, _ string, attrType document.Type, value any) (func(any) bool, error) {
	want, err := docValue(value, attrType)
	if err != nil {
		return nil, err
	}
	return func(v any) bool { return v == want }, nil
}

func compileIn(_ *compiler, _ string, attrType document.Type, value any) (func(any) bool, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf(ffErrors.ErrEngineValueShape, "a list")
	}
	set, err := valueSet(list, attrType)
	if err != nil {
		return nil, err
	}
	return inSet(set), nil
}

func compileInGroup(c *compiler, attr string, attrType document.Type, value any) (func(any) bool, error) {
	name, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf(ffErrors.ErrEngineValueShape, "a group name")
	}
	set, err := c.group(name, attr, attrType)
	if err != nil {
		return nil, err
	}
	return inSet(set), nil
}

// compileOrdered builds gt / lt for integer and number attributes; ok gets
// the sign of caller value minus document value.
func compileOrdered(ok func(cmp int) bool) operator {
	return func(_ *compiler, attr string, attrType document.Type, value any) (func(any) bool, error) {
		switch attrType {
		case document.TypeInteger:
			want, err := docValue(value, attrType)
			if err != nil {
				return nil, err
			}
			w := want.(int64)
			return func(v any) bool { return ok(compare(v.(int64), w)) }, nil
		case document.TypeNumber:
			want, err := docValue(value, attrType)
			if err != nil {
				return nil, err
			}
			w := want.(float64)
			return func(v any) bool { return ok(compare(v.(float64), w)) }, nil
		}
		return nil, fmt.Errorf(ffErrors.ErrEngineAttributeType, attr, attrType, "integer or number")
	}
}

func compare[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// compileString builds starts_with / contains for string attributes.
func compileString(match func(s, substr string) bool) operator {
	return func(_ *compiler, attr string, attrType document.Type, value any) (func(any) bool, error) {
		if attrType != document.TypeString {
			return nil, fmt.Errorf(ffErrors.ErrEngineAttributeType, attr, attrType, "string")
		}
		want, err := docValue(value, attrType)
		if err != nil {
			return nil, err
		}
		w := want.(string)
		return func(v any) bool { return match(v.(string), w) }, nil
	}
}

// compileMatches builds matches: Go RE2, unanchored (write ^...$ to anchor).
func compileMatches(_ *compiler, attr string, attrType document.Type, value any) (func(any) bool, error) {
	if attrType != document.TypeString {
		return nil, fmt.Errorf(ffErrors.ErrEngineAttributeType, attr, attrType, "string")
	}
	pattern, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf(ffErrors.ErrEngineValueShape, "a regular expression string")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrEngineBadRegex, pattern, err)
	}
	return func(v any) bool { return re.MatchString(v.(string)) }, nil
}

func inSet(set map[any]struct{}) func(any) bool {
	return func(v any) bool {
		_, ok := set[v]
		return ok
	}
}

func valueSet(values []any, attrType document.Type) (map[any]struct{}, error) {
	set := make(map[any]struct{}, len(values))
	for _, raw := range values {
		v, err := docValue(raw, attrType)
		if err != nil {
			return nil, err
		}
		set[v] = struct{}{}
	}
	return set, nil
}

// docValue normalizes a value written in the document; unlike a caller
// value, one that does not convert is a compile error.
func docValue(value any, attrType document.Type) (any, error) {
	if value == nil {
		return nil, fmt.Errorf(ffErrors.ErrEngineValueShape, "a value")
	}
	if _, isList := value.([]any); isList {
		return nil, fmt.Errorf(ffErrors.ErrEngineValueShape, "a single value")
	}
	v, ok := normalize(value, attrType)
	if !ok {
		return nil, fmt.Errorf(ffErrors.ErrEngineValueType, value, attrType)
	}
	return v, nil
}
