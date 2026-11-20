// Package validate checks a feature-flags document against a project
// catalog: every cross-reference the JSON Schemas cannot express, plus the
// name and size rules, so callers that never run the schemas (the Flag
// Console, the library on load) still reject bad documents. The AppConfig
// validator Lambda runs the same checks at deployment.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luckylanggam/tools-go-featureflags/document"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// Problem is one finding, located by Path, e.g.
// `flags.new_withdraw_flow.rules[1] "10% rollout".if[0]`.
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string {
	return p.Path + ": " + p.Message
}

// Error is returned when the documents are invalid. It lists every problem
// found, in a stable order.
type Error struct {
	Problems []Problem
}

func (e *Error) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	return fmt.Sprintf(ffErrors.ErrValidateInvalid, len(e.Problems), strings.Join(lines, "\n"))
}

// Validate checks flags against catalog, including the catalog itself.
// Warnings never make the documents invalid; they are for the person
// editing (e.g. two rules of a flag share a name). The error, when not nil,
// is an *Error.
//
// Validating the live flags against a new catalog catches removed
// attributes and operators; ValidateCatalogChange also catches retyped ones.
func Validate(flags *document.Flags, catalog *document.Catalog) (warnings []Problem, err error) {
	c, err := newChecker(flags, catalog)
	if err != nil {
		return nil, err
	}
	return c.result()
}

// ValidateCatalogChange checks a new catalog before it replaces prev. It
// runs Validate(flags, next), where flags are the flags that will be
// evaluated against next, and also fails for every attribute those flags use
// whose type differs between prev and next: a rollout bucket or an exists
// condition holds no value, so only prev shows the change. A nil prev skips
// the comparison.
func ValidateCatalogChange(prev, next *document.Catalog, flags *document.Flags) (warnings []Problem, err error) {
	c, err := newChecker(flags, next)
	if err != nil {
		return nil, err
	}
	if prev != nil {
		c.checkTypeChanges(prev)
	}
	return c.result()
}

func newChecker(flags *document.Flags, catalog *document.Catalog) (*checker, error) {
	if flags == nil {
		return nil, fmt.Errorf(ffErrors.ErrValidateNilDocument, "feature-flags")
	}
	if catalog == nil {
		return nil, fmt.Errorf(ffErrors.ErrValidateNilDocument, "catalog")
	}
	c := &checker{flags: flags, catalog: catalog}
	c.checkCatalog()
	c.checkGroups()
	c.checkFlags()
	return c, nil
}

// ValidateCatalog checks a catalog on its own: names, types, custom
// operators not shadowing built-ins, and resolvers keyed by and requiring
// catalog attributes. The error, when not nil, is an *Error.
func ValidateCatalog(catalog *document.Catalog) error {
	if catalog == nil {
		return fmt.Errorf(ffErrors.ErrValidateNilDocument, "catalog")
	}
	c := &checker{catalog: catalog}
	c.checkCatalog()
	_, err := c.result()
	return err
}

type checker struct {
	flags    *document.Flags
	catalog  *document.Catalog
	problems []Problem
	warnings []Problem
}

func (c *checker) fail(path, format string, args ...any) {
	c.problems = append(c.problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) warn(path, format string, args ...any) {
	c.warnings = append(c.warnings, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) result() ([]Problem, error) {
	if len(c.problems) > 0 {
		return c.warnings, &Error{Problems: c.problems}
	}
	return c.warnings, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
