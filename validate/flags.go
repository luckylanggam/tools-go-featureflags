package validate

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/luckylanggam/tools-go-featureflags/document"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// MaxGroupValues is the most values a group may list. Larger, data-driven
// segments belong in a resolver or a custom operator.
const MaxGroupValues = 200

func (c *checker) checkGroups() {
	for _, name := range sortedKeys(c.flags.Groups) {
		g := c.flags.Groups[name]
		path := "groups." + name
		if !groupNameRe.MatchString(name) {
			c.fail(path, ffErrors.ErrValidateBadName, "group", name, groupNamePattern)
		}
		if n := len(g.Values); n == 0 || n > MaxGroupValues {
			c.fail(path+".values", ffErrors.ErrValidateGroupSize, n, MaxGroupValues)
		}

		attr, known := c.catalog.Attributes[g.Attr]
		if !known {
			c.fail(path+".attr", ffErrors.ErrValidateUnknownAttribute, g.Attr)
		}
		seen := make(map[string]bool, len(g.Values))
		for i, v := range g.Values {
			vpath := fmt.Sprintf("%s.values[%d]", path, i)
			if known && !hasType(v, attr.Type) {
				c.fail(vpath, ffErrors.ErrValidateValueType, display(v), attr.Type)
			}
			key := valueKey(v)
			if seen[key] {
				c.fail(vpath, ffErrors.ErrValidateDuplicateValue, display(v))
			}
			seen[key] = true
		}
	}
}

func (c *checker) checkFlags() {
	for _, name := range sortedKeys(c.flags.Flags) {
		f := c.flags.Flags[name]
		path := "flags." + name
		if !identifierRe.MatchString(name) {
			c.fail(path, ffErrors.ErrValidateBadName, "flag", name, identifierPattern)
		}
		if f.Owner == "" {
			c.fail(path+".owner", ffErrors.ErrValidateRequired, "owner")
		}
		if !jiraRe.MatchString(f.Jira) {
			c.fail(path+".jira", ffErrors.ErrValidateBadJira, f.Jira)
		}
		if _, err := time.Parse(dateLayout, f.RemoveAfter); err != nil {
			c.fail(path+".remove_after", ffErrors.ErrValidateBadDate, f.RemoveAfter)
		}

		firstByName := make(map[string]int, len(f.Rules))
		for i, r := range f.Rules {
			rpath := rulePath(path, i, r)
			if n := utf8.RuneCountInString(r.Name); n == 0 || n > maxRuleNameLength {
				c.fail(rpath, ffErrors.ErrValidateBadRuleName, maxRuleNameLength)
			}
			if first, dup := firstByName[r.Name]; dup && r.Name != "" {
				c.warn(rpath, ffErrors.WarnValidateDuplicateRule, first, first)
			} else {
				firstByName[r.Name] = i
			}
			c.checkRule(rpath, r)
		}
	}
}

func (c *checker) checkRule(path string, r document.Rule) {
	for i, cond := range r.If {
		c.checkCondition(fmt.Sprintf("%s.if[%d]", path, i), cond)
	}
	if r.Rollout == nil {
		return
	}
	if p := r.Rollout.Percentage; p < 0 || p > 100 {
		c.fail(path+".rollout.percentage", ffErrors.ErrValidateBadPercentage, p)
	}
	if _, ok := c.catalog.Attributes[r.Rollout.BucketBy]; !ok {
		c.fail(path+".rollout.bucket_by", ffErrors.ErrValidateUnknownAttribute, r.Rollout.BucketBy)
	}
}

func (c *checker) checkCondition(path string, cond document.Condition) {
	attr, known := c.catalog.Attributes[cond.Attr]
	if !known {
		c.fail(path+".attr", ffErrors.ErrValidateUnknownAttribute, cond.Attr)
	}

	base, _, builtin := document.BaseOperator(cond.Op)
	if !builtin {
		// Custom operators are negated like built-ins: not_is_vip negates is_vip.
		name, _ := strings.CutPrefix(cond.Op, document.NegationPrefix)
		if custom, ok := c.catalog.Operators[name]; ok {
			c.checkSingleValue(path, cond, custom.ValueType)
		} else {
			c.fail(path+".op", ffErrors.ErrValidateUnknownOperator, cond.Op)
		}
		return
	}

	switch base {
	case document.OpExists:
		if cond.Value != nil {
			c.fail(path+".value", ffErrors.ErrValidateValueShape, cond.Op, "no value")
		}
	case document.OpInGroup:
		c.checkInGroup(path, cond)
	case document.OpIn:
		c.checkList(path, cond, attr.Type, known)
	case document.OpEq:
		if known {
			c.checkSingleValue(path, cond, attr.Type)
		}
	case document.OpGt, document.OpLt:
		if known && attr.Type != document.TypeInteger && attr.Type != document.TypeNumber {
			c.fail(path+".attr", ffErrors.ErrValidateAttributeType, cond.Op, "an integer or number", cond.Attr, attr.Type)
			return
		}
		if known {
			c.checkSingleValue(path, cond, attr.Type)
		}
	case document.OpStartsWith, document.OpContains, document.OpMatches:
		if known && attr.Type != document.TypeString {
			c.fail(path+".attr", ffErrors.ErrValidateAttributeType, cond.Op, "a string", cond.Attr, attr.Type)
			return
		}
		if !c.checkSingleValue(path, cond, document.TypeString) || base != document.OpMatches {
			return
		}
		if _, err := regexp.Compile(cond.Value.(string)); err != nil {
			c.fail(path+".value", ffErrors.ErrValidateBadRegex, cond.Value, err)
		}
	}
}

// checkSingleValue reports whether the condition holds one value of type t.
func (c *checker) checkSingleValue(path string, cond document.Condition, t document.Type) bool {
	switch cond.Value.(type) {
	case nil:
		c.fail(path+".value", ffErrors.ErrValidateValueShape, cond.Op, "a value")
		return false
	case []any:
		c.fail(path+".value", ffErrors.ErrValidateValueShape, cond.Op, "a single value, not a list")
		return false
	}
	if !hasType(cond.Value, t) {
		c.fail(path+".value", ffErrors.ErrValidateValueType, display(cond.Value), t)
		return false
	}
	return true
}

func (c *checker) checkList(path string, cond document.Condition, t document.Type, known bool) {
	list, ok := cond.Value.([]any)
	if !ok || len(list) == 0 {
		c.fail(path+".value", ffErrors.ErrValidateValueShape, cond.Op, "a non-empty list")
		return
	}
	if !known {
		return
	}
	for i, v := range list {
		if !hasType(v, t) {
			c.fail(fmt.Sprintf("%s.value[%d]", path, i), ffErrors.ErrValidateValueType, display(v), t)
		}
	}
}

func (c *checker) checkInGroup(path string, cond document.Condition) {
	name, ok := cond.Value.(string)
	if !ok {
		c.fail(path+".value", ffErrors.ErrValidateValueShape, cond.Op, "a group name")
		return
	}
	g, ok := c.flags.Groups[name]
	if !ok {
		c.fail(path+".value", ffErrors.ErrValidateUnknownGroup, name)
		return
	}
	if g.Attr != cond.Attr {
		c.fail(path+".value", ffErrors.ErrValidateGroupAttribute, name, g.Attr, cond.Attr)
	}
}

func rulePath(flagPath string, i int, r document.Rule) string {
	return fmt.Sprintf("%s.rules[%d] %q", flagPath, i, r.Name)
}

// checkTypeChanges fails for each attribute the flags use whose type in
// prev differs from the one in c.catalog. Removed attributes are reported by
// the other checks already.
func (c *checker) checkTypeChanges(prev *document.Catalog) {
	retyped := func(path, attr string) {
		before, inPrev := prev.Attributes[attr]
		after, inNext := c.catalog.Attributes[attr]
		if inPrev && inNext && before.Type != after.Type {
			c.fail(path, ffErrors.ErrValidateAttributeRetyped, attr, before.Type, after.Type)
		}
	}
	for _, name := range sortedKeys(c.flags.Groups) {
		retyped("groups."+name+".attr", c.flags.Groups[name].Attr)
	}
	for _, name := range sortedKeys(c.flags.Flags) {
		for i, r := range c.flags.Flags[name].Rules {
			rpath := rulePath("flags."+name, i, r)
			for j, cond := range r.If {
				retyped(fmt.Sprintf("%s.if[%d].attr", rpath, j), cond.Attr)
			}
			if r.Rollout != nil {
				retyped(rpath+".rollout.bucket_by", r.Rollout.BucketBy)
			}
		}
	}
}
