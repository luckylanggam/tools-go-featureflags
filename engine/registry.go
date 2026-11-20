package engine

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"

	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// OperatorFunc decides a custom operator for one caller. attr is the
// caller's value converted to the attribute's catalog type; value is the
// condition's value converted to the operator's catalog value_type. An
// error counts as no match and is reported in Decision.FailedOperators.
type OperatorFunc func(ctx context.Context, attr, value any) (bool, error)

// ResolverFunc fetches the attribute it is registered for when a rule needs
// it and the caller did not send it. inputs holds the attributes the catalog
// lists in the resolver's requires, converted to their catalog types. A nil
// value leaves the attribute missing; so does an error, which is also
// reported in Decision.FailedResolvers.
type ResolverFunc func(ctx context.Context, inputs Attributes) (any, error)

// Registry holds the custom operators and resolvers a service implements in
// code. Register them at startup and pass the registry to every New with
// WithRegistry; an engine copies what is registered when it is built. It is
// safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	operators map[string]OperatorFunc
	resolvers map[string]ResolverFunc
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{operators: map[string]OperatorFunc{}, resolvers: map[string]ResolverFunc{}}
}

// RegisterOperator adds the custom operator name, which the catalog must
// also declare. Conditions negate it with the not_ prefix like a built-in,
// so name may not be built in or start with not_.
func (r *Registry) RegisterOperator(name string, fn OperatorFunc) error {
	if name == "" || strings.HasPrefix(name, negPrefix) || isBuiltin(name) {
		return fmt.Errorf(ffErrors.ErrRegistryOperatorName, name)
	}
	if fn == nil {
		return fmt.Errorf(ffErrors.ErrRegistryNilFunc, "operator", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.operators[name]; ok {
		return fmt.Errorf(ffErrors.ErrRegistryDuplicate, "operator", name)
	}
	r.operators[name] = fn
	return nil
}

// RegisterResolver adds the resolver for attr. It is used only when the
// catalog declares a resolver for attr, and it runs at most once per request
// and set of inputs (see WithAttributes).
func (r *Registry) RegisterResolver(attr string, fn ResolverFunc) error {
	if attr == "" {
		return fmt.Errorf(ffErrors.ErrRegistryResolverName)
	}
	if fn == nil {
		return fmt.Errorf(ffErrors.ErrRegistryNilFunc, "resolver", attr)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.resolvers[attr]; ok {
		return fmt.Errorf(ffErrors.ErrRegistryDuplicate, "resolver", attr)
	}
	r.resolvers[attr] = fn
	return nil
}

// snapshot copies the registered functions; a nil registry has none.
func (r *Registry) snapshot() (map[string]OperatorFunc, map[string]ResolverFunc) {
	if r == nil {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return maps.Clone(r.operators), maps.Clone(r.resolvers)
}

func isBuiltin(name string) bool {
	_, ok := operators[name]
	return ok || name == opExists || name == opNotExists
}
