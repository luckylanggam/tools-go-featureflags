// Package engine evaluates schema_version 1 feature-flags documents in
// memory: kill switch, then ordered rules (first match wins), then the
// flag's default. Every answer carries the reason that produced it.
//
// An Engine is built once per document version and never changes, so a
// provider can swap engines atomically while requests keep evaluating.
// Evaluation does no network or other I/O of its own; only the custom
// operators and resolvers a service registers run its code.
package engine

import (
	"context"
	"fmt"

	"github.com/luckylanggam/tools-go-featureflags/document"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// Attributes describe the caller (site_id, network_id, player_id, ...).
// Keys are catalog attribute names; values are converted to the catalog
// type before comparison, so 101 and "101" are the same integer.
type Attributes map[string]any

// Reason says what decided a flag.
type Reason string

const (
	// ReasonUnknownFlag: the flag is not in the document; it is off.
	ReasonUnknownFlag Reason = "unknown_flag"
	// ReasonKillSwitch: the flag has enabled=false; it is off for everyone.
	ReasonKillSwitch Reason = "kill_switch"
	// ReasonRule: the rule named in Decision.Rule matched.
	ReasonRule Reason = "rule"
	// ReasonDefault: no rule matched; the flag's default applies.
	ReasonDefault Reason = "default"
	// ReasonDecidedByService: every other check of the rule in Decision.Rule
	// passed, but it needs custom operators only the service implements
	// (see WithUnregisteredOperatorsUndecided). Enabled carries no meaning:
	// the flag is not known to be off.
	ReasonDecidedByService Reason = "decided_by_service"
)

// Decision is the result of evaluating one flag for one caller.
// MissingAttrs and InvalidAttrs name the attributes that stopped a rule
// from matching because the caller did not send them (and no resolver
// filled them) or sent a value that does not convert to the catalog type.
// FailedOperators and FailedResolvers name the custom operators and the
// resolvers' attributes that returned an error. All four are sorted and may
// be set whatever the Reason. UndecidedOperators is set only with
// ReasonDecidedByService.
type Decision struct {
	Flag               string
	Enabled            bool
	Reason             Reason
	Rule               string
	RuleIndex          int
	MissingAttrs       []string
	InvalidAttrs       []string
	FailedOperators    []string
	FailedResolvers    []string
	UndecidedOperators []string
}

// Engine evaluates one feature-flags document. It is immutable and safe
// for concurrent use.
type Engine struct {
	flags     map[string]*flag
	resolvers map[string]*resolver
}

type flag struct {
	enabled bool
	def     bool
	rules   []rule
}

type rule struct {
	name       string
	conditions []condition
	then       bool
	rollout    *rollout
}

type rollout struct {
	percentage uint64
	bucketBy   string
	attrType   document.Type
}

// New compiles a feature-flags document against its catalog. It fails when
// the document cannot be evaluated: an attribute missing from the catalog,
// an operator that is neither built in nor declared in the catalog, a
// declared custom operator that is not registered (unless
// WithUnregisteredOperatorsUndecided), an unknown group or one over another
// attribute, a value of the wrong type, or an invalid regular expression.
// Run validate on the documents first for the full set of checks.
func New(flags *document.Flags, catalog *document.Catalog, opts ...Option) (*Engine, error) {
	if flags == nil {
		return nil, fmt.Errorf(ffErrors.ErrEngineNilDocument, "feature-flags")
	}
	if catalog == nil {
		return nil, fmt.Errorf(ffErrors.ErrEngineNilDocument, "catalog")
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	m, err := newMetrics(o.meterProvider)
	if err != nil {
		return nil, err
	}
	operatorFns, resolverFns := o.registry.snapshot()
	c := compiler{
		flags:     flags,
		catalog:   catalog,
		groups:    map[string]map[any]struct{}{},
		operators: operatorFns,
		undecided: o.undecided,
		metrics:   m,
	}
	e := &Engine{flags: make(map[string]*flag, len(flags.Flags))}
	if e.resolvers, err = c.resolvers(resolverFns); err != nil {
		return nil, err
	}
	for name, f := range flags.Flags {
		compiled, err := c.flag(name, f)
		if err != nil {
			return nil, err
		}
		e.flags[name] = compiled
	}
	return e, nil
}

// IsEnabled reports whether flag is on for the caller described by attrs.
// An unknown flag is off and returns an error.
func (e *Engine) IsEnabled(ctx context.Context, flag string, attrs Attributes) (bool, error) {
	d, err := e.Evaluate(ctx, flag, attrs)
	return d.Enabled, err
}

// Evaluate returns the decision for flag and the reason behind it. An
// unknown flag is off with ReasonUnknownFlag and an error. Attributes in
// attrs win over those set with WithAttributes on ctx; resolvers fill the
// rest when a rule needs them.
func (e *Engine) Evaluate(ctx context.Context, name string, attrs Attributes) (Decision, error) {
	f, ok := e.flags[name]
	if !ok {
		return Decision{Flag: name, Reason: ReasonUnknownFlag, RuleIndex: -1},
			fmt.Errorf(ffErrors.ErrFeatureFlagNotFound, name)
	}
	if !f.enabled {
		return Decision{Flag: name, Reason: ReasonKillSwitch, RuleIndex: -1}, nil
	}

	ev := evaluation{ctx: ctx, engine: e, attrs: attrs, scope: scopeFrom(ctx)}
	d := Decision{Flag: name, Enabled: f.def, Reason: ReasonDefault, RuleIndex: -1}
	for i := range f.rules {
		r := &f.rules[i]
		m := r.match(name, &ev)
		if m == noMatch {
			continue
		}
		d.Rule, d.RuleIndex = r.name, i
		if m == matched {
			d.Enabled, d.Reason = r.then, ReasonRule
		} else {
			d.Enabled, d.Reason = false, ReasonDecidedByService
			d.UndecidedOperators = sorted(ev.undecided)
		}
		break
	}
	ev.fill(&d)
	return d, nil
}

// match is the outcome of a condition or a rule.
type match int

const (
	noMatch match = iota
	matched
	// undecided: the outcome depends on a custom operator the engine has no
	// code for.
	undecided
)

func matchIf(ok bool) match {
	if ok {
		return matched
	}
	return noMatch
}

// match checks every condition and, with a rollout, that the caller's
// bucket is inside the percentage. Callers outside the bucket do not match,
// so evaluation falls through to the next rule. A rule is undecided only
// when everything the engine can check holds.
func (r *rule) match(flagName string, ev *evaluation) match {
	ev.undecided = ev.undecided[:0]
	result := matched
	for i := range r.conditions {
		switch r.conditions[i].check(ev) {
		case noMatch:
			return noMatch
		case undecided:
			result = undecided
		}
	}
	if r.rollout == nil {
		return result
	}
	raw, ok := ev.lookup(r.rollout.bucketBy)
	if !ok {
		ev.missing(r.rollout.bucketBy)
		return noMatch
	}
	v, ok := normalize(raw, r.rollout.attrType)
	if !ok {
		ev.invalid(r.rollout.bucketBy)
		return noMatch
	}
	if bucket(flagName, v) >= r.rollout.percentage {
		return noMatch
	}
	return result
}
