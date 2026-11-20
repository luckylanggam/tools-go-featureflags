package engine

import (
	"context"
	"maps"
	"sync"
)

type scopeKey struct{}

// requestScope is what WithAttributes stores in a context: the request's
// attributes and the resolver cache shared by every evaluation in it.
type requestScope struct {
	attrs Attributes
	cache *resolverCache
}

// WithAttributes returns a context whose evaluations see attrs, typically
// set once per request by middleware. Attributes already in ctx are kept
// unless attrs sets them, and arguments passed to Evaluate win over both.
// The first call also starts the request's resolver cache, which later
// calls share, so set attributes that resolvers read before evaluating.
func WithAttributes(ctx context.Context, attrs Attributes) context.Context {
	s := &requestScope{attrs: make(Attributes, len(attrs))}
	if parent := scopeFrom(ctx); parent != nil {
		maps.Copy(s.attrs, parent.attrs)
		s.cache = parent.cache
	} else {
		s.cache = &resolverCache{}
	}
	maps.Copy(s.attrs, attrs)
	return context.WithValue(ctx, scopeKey{}, s)
}

// AttributesFromContext returns a copy of the attributes WithAttributes put
// in ctx, or nil when there are none.
func AttributesFromContext(ctx context.Context) Attributes {
	s := scopeFrom(ctx)
	if s == nil {
		return nil
	}
	return maps.Clone(s.attrs)
}

func scopeFrom(ctx context.Context) *requestScope {
	s, _ := ctx.Value(scopeKey{}).(*requestScope)
	return s
}

// resolverCache holds one resolution per resolver and set of inputs.
type resolverCache struct {
	mu      sync.Mutex
	entries map[string]*resolution
}

// resolution is a resolver result; once makes concurrent evaluations wait
// for a single call.
type resolution struct {
	once  sync.Once
	value any
	err   error
}

func (c *resolverCache) get(key string) *resolution {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*resolution{}
	}
	r, ok := c.entries[key]
	if !ok {
		r = &resolution{}
		c.entries[key] = r
	}
	return r
}
