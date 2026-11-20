package validate

import (
	"fmt"
	"strings"

	"github.com/luckylanggam/tools-go-featureflags/document"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

func (c *checker) checkCatalog() {
	for _, name := range sortedKeys(c.catalog.Attributes) {
		path := "catalog.attributes." + name
		if !identifierRe.MatchString(name) {
			c.fail(path, ffErrors.ErrValidateBadName, "attribute", name, identifierPattern)
		}
		if !knownType(c.catalog.Attributes[name].Type) {
			c.fail(path, ffErrors.ErrValidateBadType, c.catalog.Attributes[name].Type)
		}
	}

	for _, name := range sortedKeys(c.catalog.Operators) {
		path := "catalog.operators." + name
		if !identifierRe.MatchString(name) {
			c.fail(path, ffErrors.ErrValidateBadName, "operator", name, identifierPattern)
		}
		if _, _, builtin := document.BaseOperator(name); builtin {
			c.fail(path, ffErrors.ErrValidateShadowsBuiltin, name)
		} else if strings.HasPrefix(name, document.NegationPrefix) {
			c.fail(path, ffErrors.ErrValidateNegatedOperator, name, document.NegationPrefix)
		}
		if !knownType(c.catalog.Operators[name].ValueType) {
			c.fail(path, ffErrors.ErrValidateBadType, c.catalog.Operators[name].ValueType)
		}
	}

	for _, name := range sortedKeys(c.catalog.Resolvers) {
		path := "catalog.resolvers." + name
		if _, ok := c.catalog.Attributes[name]; !ok {
			c.fail(path, ffErrors.ErrValidateResolverUnknown, name)
		}
		for i, req := range c.catalog.Resolvers[name].Requires {
			if _, ok := c.catalog.Attributes[req]; !ok {
				c.fail(fmt.Sprintf("%s.requires[%d]", path, i), ffErrors.ErrValidateUnknownAttribute, req)
			}
		}
	}
}

func knownType(t document.Type) bool {
	switch t {
	case document.TypeString, document.TypeInteger, document.TypeNumber, document.TypeBoolean:
		return true
	}
	return false
}
