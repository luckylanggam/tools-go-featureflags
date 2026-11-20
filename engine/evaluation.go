package engine

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

// resolver is a registered resolver for a catalog attribute.
type resolver struct {
	attr     string
	fn       ResolverFunc
	requires []input
	// key starts every cache key: the attribute and its inputs' names and
	// types, so engines built from different catalogs never share a result.
	key     string
	observe observer
}

type input struct {
	attr     string
	attrType document.Type
}

// evaluation is the state of one Evaluate call: where attribute values come
// from and what stopped rules from matching.
type evaluation struct {
	ctx    context.Context
	engine *Engine
	attrs  Attributes
	scope  *requestScope
	// local caches resolvers when ctx has no request scope.
	local *resolverCache
	// undecided collects the current rule's operators the engine has no
	// code for.
	undecided []string
	trace
}

// lookup returns the caller's value for attr: the Evaluate arguments first,
// then the context's attributes, then the attribute's resolver. nil counts
// as absent.
func (ev *evaluation) lookup(attr string) (any, bool) {
	if v, ok := ev.known(attr); ok {
		return v, true
	}
	r, ok := ev.engine.resolvers[attr]
	if !ok {
		return nil, false
	}
	return ev.resolve(r)
}

// known looks attr up in what the caller sent, without resolvers.
func (ev *evaluation) known(attr string) (any, bool) {
	if v, ok := ev.attrs[attr]; ok && v != nil {
		return v, true
	}
	if ev.scope != nil {
		if v, ok := ev.scope.attrs[attr]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// resolve runs r once per request and set of inputs. Missing or invalid
// inputs are reported and leave the attribute missing; resolvers do not
// feed each other.
func (ev *evaluation) resolve(r *resolver) (any, bool) {
	inputs := make(Attributes, len(r.requires))
	key := r.key
	for _, in := range r.requires {
		raw, ok := ev.known(in.attr)
		if !ok {
			ev.missing(in.attr)
			return nil, false
		}
		v, ok := normalize(raw, in.attrType)
		if !ok {
			ev.invalid(in.attr)
			return nil, false
		}
		inputs[in.attr] = v
		key += "\x00" + strconv.Quote(canonical(v))
	}

	res := ev.cache().get(key)
	res.once.Do(func() {
		start := time.Now()
		res.value, res.err = r.fn(ev.ctx, inputs)
		r.observe.record(ev.ctx, start, res.err)
	})
	if res.err != nil {
		ev.failedResolver(r.attr)
		return nil, false
	}
	return res.value, res.value != nil
}

func (ev *evaluation) cache() *resolverCache {
	if ev.scope != nil {
		return ev.scope.cache
	}
	if ev.local == nil {
		ev.local = &resolverCache{}
	}
	return ev.local
}

// trace collects what stopped rules from matching.
type trace struct {
	missingAttrs    []string
	invalidAttrs    []string
	failedOperators []string
	failedResolvers []string
}

func (t *trace) missing(attr string)        { addOnce(&t.missingAttrs, attr) }
func (t *trace) invalid(attr string)        { addOnce(&t.invalidAttrs, attr) }
func (t *trace) failedOperator(op string)   { addOnce(&t.failedOperators, op) }
func (t *trace) failedResolver(attr string) { addOnce(&t.failedResolvers, attr) }

// fill copies the trace into d, in place because Decision is large.
func (t *trace) fill(d *Decision) {
	d.MissingAttrs = sorted(t.missingAttrs)
	d.InvalidAttrs = sorted(t.invalidAttrs)
	d.FailedOperators = sorted(t.failedOperators)
	d.FailedResolvers = sorted(t.failedResolvers)
}

func addOnce(list *[]string, name string) {
	if !slices.Contains(*list, name) {
		*list = append(*list, name)
	}
}

func sorted(list []string) []string {
	slices.Sort(list)
	return list
}
