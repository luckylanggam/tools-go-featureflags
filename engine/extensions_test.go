package engine_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/luckylanggam/tools-go-featureflags/document"
	"github.com/luckylanggam/tools-go-featureflags/engine"
)

const extCatalog = `{
  "schema_version": 1,
  "attributes": {
    "network_id": {"type": "string"},
    "player_id":  {"type": "string"},
    "site_id":    {"type": "integer"},
    "tier":       {"type": "integer"}
  },
  "operators": {
    "is_vip":        {"value_type": "boolean"},
    "tier_at_least": {"value_type": "integer"}
  },
  "resolvers": {
    "network_id": {"requires": ["player_id"]}
  }
}`

func extEngine(t *testing.T, flags map[string]string, opts ...engine.Option) (*engine.Engine, error) {
	t.Helper()
	doc, err := document.ParseFlags([]byte(flagsDoc("", flags)))
	require.NoError(t, err)
	return engine.New(doc, parseCatalog(t, extCatalog), opts...)
}

func mustExtEngine(t *testing.T, flags map[string]string, opts ...engine.Option) *engine.Engine {
	t.Helper()
	e, err := extEngine(t, flags, opts...)
	require.NoError(t, err)
	return e
}

func registry(t *testing.T, ops map[string]engine.OperatorFunc, resolvers map[string]engine.ResolverFunc) *engine.Registry {
	t.Helper()
	r := engine.NewRegistry()
	for name, fn := range ops {
		require.NoError(t, r.RegisterOperator(name, fn))
	}
	for attr, fn := range resolvers {
		require.NoError(t, r.RegisterResolver(attr, fn))
	}
	return r
}

// isVIP is true for players whose ID starts with "vip-" when the condition
// value is true.
func isVIP(_ context.Context, attr, value any) (bool, error) {
	if attr.(string) == "boom" {
		return false, errors.New("vip service down")
	}
	return (len(attr.(string)) > 4 && attr.(string)[:4] == "vip-") == value.(bool), nil
}

func TestRegistry_Errors(t *testing.T) {
	noop := func(context.Context, any, any) (bool, error) { return true, nil }
	r := engine.NewRegistry()
	require.NoError(t, r.RegisterOperator("is_vip", noop))

	assert.EqualError(t, r.RegisterOperator("is_vip", noop), `operator "is_vip" is already registered`)
	for _, name := range []string{"", "eq", "in_group", "exists", "not_exists", "not_vip"} {
		assert.EqualError(t, r.RegisterOperator(name, noop),
			`operator "`+name+`": name is empty, built in or starts with not_`, name)
	}
	assert.EqualError(t, r.RegisterOperator("tier_at_least", nil), `operator "tier_at_least": function is nil`)

	resolve := func(context.Context, engine.Attributes) (any, error) { return "N-1", nil }
	require.NoError(t, r.RegisterResolver("network_id", resolve))
	assert.EqualError(t, r.RegisterResolver("network_id", resolve), `resolver "network_id" is already registered`)
	assert.EqualError(t, r.RegisterResolver("", resolve), "resolver: attribute name is empty")
	assert.EqualError(t, r.RegisterResolver("tier", nil), `resolver "tier": function is nil`)
}

func TestNew_CustomOperatorErrors(t *testing.T) {
	vip := map[string]engine.OperatorFunc{"is_vip": isVIP}
	tests := []struct {
		name    string
		cond    string
		opts    []engine.Option
		wantErr string
	}{
		{"declared but not registered", `{"attr": "player_id", "op": "is_vip", "value": true}`, nil,
			`flag "f" rule 0 "r": if[0]: operator "is_vip" is declared in the catalog but not registered`},
		{"negated, declared but not registered", `{"attr": "player_id", "op": "not_is_vip", "value": true}`,
			[]engine.Option{engine.WithRegistry(engine.NewRegistry())},
			`operator "is_vip" is declared in the catalog but not registered`},
		{"registered but not declared", `{"attr": "player_id", "op": "is_whale", "value": true}`,
			[]engine.Option{engine.WithRegistry(registry(t, map[string]engine.OperatorFunc{"is_whale": isVIP}, nil))},
			`operator "is_whale" is not built in or declared in the catalog`},
		{"not declared, even when undecided operators are allowed", `{"attr": "player_id", "op": "is_whale", "value": true}`,
			[]engine.Option{engine.WithUnregisteredOperatorsUndecided()},
			`operator "is_whale" is not built in or declared in the catalog`},
		{"value of the wrong type", `{"attr": "player_id", "op": "is_vip", "value": "yes"}`,
			[]engine.Option{engine.WithRegistry(registry(t, vip, nil))},
			`operator "is_vip": value yes is not a valid boolean`},
		{"list value", `{"attr": "player_id", "op": "is_vip", "value": [true]}`,
			[]engine.Option{engine.WithRegistry(registry(t, vip, nil))},
			`operator "is_vip": value must be a single value`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := extEngine(t, map[string]string{"f": oneCondition(tt.cond)}, tt.opts...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, e)
		})
	}
}

func TestEvaluate_CustomOperators(t *testing.T) {
	var gotTier []any
	tierAtLeast := func(_ context.Context, attr, value any) (bool, error) {
		gotTier = append(gotTier, attr, value)
		return attr.(int64) >= value.(int64), nil
	}
	e := mustExtEngine(t, map[string]string{
		"vip":     oneCondition(`{"attr": "player_id", "op": "is_vip", "value": true}`),
		"not_vip": oneCondition(`{"attr": "player_id", "op": "not_is_vip", "value": true}`),
		"tier":    oneCondition(`{"attr": "tier", "op": "tier_at_least", "value": 3}`),
	}, engine.WithRegistry(registry(t, map[string]engine.OperatorFunc{"is_vip": isVIP, "tier_at_least": tierAtLeast}, nil)))

	tests := []struct {
		name  string
		flag  string
		attrs engine.Attributes
		want  engine.Decision
	}{
		{"operator holds", "vip", engine.Attributes{"player_id": "vip-1"},
			engine.Decision{Flag: "vip", Enabled: true, Reason: engine.ReasonRule, Rule: "r"}},
		{"operator does not hold", "vip", engine.Attributes{"player_id": "p-1"},
			engine.Decision{Flag: "vip", Reason: engine.ReasonDefault, RuleIndex: -1}},
		{"negated", "not_vip", engine.Attributes{"player_id": "p-1"},
			engine.Decision{Flag: "not_vip", Enabled: true, Reason: engine.ReasonRule, Rule: "r"}},
		{"negated, missing attribute never matches", "not_vip", engine.Attributes{},
			engine.Decision{Flag: "not_vip", Reason: engine.ReasonDefault, RuleIndex: -1, MissingAttrs: []string{"player_id"}}},
		{"error is no match and is reported", "vip", engine.Attributes{"player_id": "boom"},
			engine.Decision{Flag: "vip", Reason: engine.ReasonDefault, RuleIndex: -1, FailedOperators: []string{"is_vip"}}},
		{"negated error is still no match", "not_vip", engine.Attributes{"player_id": "boom"},
			engine.Decision{Flag: "not_vip", Reason: engine.ReasonDefault, RuleIndex: -1, FailedOperators: []string{"is_vip"}}},
		{"invalid attribute never reaches the operator", "tier", engine.Attributes{"tier": "high"},
			engine.Decision{Flag: "tier", Reason: engine.ReasonDefault, RuleIndex: -1, InvalidAttrs: []string{"tier"}}},
		{"values arrive in their catalog types", "tier", engine.Attributes{"tier": "4"},
			engine.Decision{Flag: "tier", Enabled: true, Reason: engine.ReasonRule, Rule: "r"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.Evaluate(context.Background(), tt.flag, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
	assert.Equal(t, []any{int64(4), int64(3)}, gotTier)
}

func TestEvaluate_UndecidedOperators(t *testing.T) {
	flags := map[string]string{
		"f": `"enabled": true, "default": false, "rules": [
			{"name": "site 7 vips", "if": [
				{"attr": "site_id", "op": "eq", "value": 7},
				{"attr": "player_id", "op": "is_vip", "value": true}], "then": true},
			{"name": "vips", "if": [{"attr": "player_id", "op": "not_is_vip", "value": false}], "then": true},
			{"name": "everyone", "if": [], "then": false}]`,
		"rollout": `"enabled": true, "default": true, "rules": [
			{"name": "nobody", "if": [{"attr": "player_id", "op": "is_vip", "value": true}], "then": true,
			 "rollout": {"percentage": 0, "bucket_by": "player_id"}}]`,
		"killed": `"enabled": false, "default": true, "rules": [
			{"name": "vips", "if": [{"attr": "player_id", "op": "is_vip", "value": true}], "then": true}]`,
	}
	e := mustExtEngine(t, flags, engine.WithUnregisteredOperatorsUndecided())

	tests := []struct {
		name  string
		flag  string
		attrs engine.Attributes
		want  engine.Decision
	}{
		{"stops at the first rule whose other checks pass", "f", engine.Attributes{"site_id": 7, "player_id": "p-1"},
			engine.Decision{Flag: "f", Reason: engine.ReasonDecidedByService, Rule: "site 7 vips",
				UndecidedOperators: []string{"is_vip"}}},
		{"a failed condition skips the rule before the undecided one", "f", engine.Attributes{"site_id": 8, "player_id": "p-1"},
			engine.Decision{Flag: "f", Reason: engine.ReasonDecidedByService, Rule: "vips", RuleIndex: 1,
				UndecidedOperators: []string{"is_vip"}}},
		{"a missing attribute is decidable and reported", "f", engine.Attributes{"site_id": 7},
			engine.Decision{Flag: "f", Reason: engine.ReasonRule, Rule: "everyone", RuleIndex: 2,
				MissingAttrs: []string{"player_id"}}},
		{"skipped rules keep their missing attributes", "f", engine.Attributes{"player_id": "p-1"},
			engine.Decision{Flag: "f", Reason: engine.ReasonDecidedByService, Rule: "vips", RuleIndex: 1,
				MissingAttrs: []string{"site_id"}, UndecidedOperators: []string{"is_vip"}}},
		{"outside the rollout is decidable", "rollout", engine.Attributes{"player_id": "p-1"},
			engine.Decision{Flag: "rollout", Enabled: true, Reason: engine.ReasonDefault, RuleIndex: -1}},
		{"kill switch still wins", "killed", engine.Attributes{"player_id": "p-1"},
			engine.Decision{Flag: "killed", Reason: engine.ReasonKillSwitch, RuleIndex: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.Evaluate(context.Background(), tt.flag, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("registered operators still run", func(t *testing.T) {
		e := mustExtEngine(t, flags, engine.WithUnregisteredOperatorsUndecided(),
			engine.WithRegistry(registry(t, map[string]engine.OperatorFunc{"is_vip": isVIP}, nil)))
		got, err := e.Evaluate(context.Background(), "f", engine.Attributes{"site_id": 7, "player_id": "vip-1"})
		require.NoError(t, err)
		assert.Equal(t, engine.Decision{Flag: "f", Enabled: true, Reason: engine.ReasonRule, Rule: "site 7 vips"}, got)
	})
}

// networkResolver counts its calls and maps player p-N to network N-p-N.
type networkResolver struct {
	calls atomic.Int32
	err   error
}

func (r *networkResolver) resolve(_ context.Context, inputs engine.Attributes) (any, error) {
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	if inputs["player_id"] == "nobody" {
		return nil, nil
	}
	return "N-" + inputs["player_id"].(string), nil
}

func resolverEngine(t *testing.T, r *networkResolver) *engine.Engine {
	t.Helper()
	return mustExtEngine(t, map[string]string{
		"a": oneCondition(`{"attr": "network_id", "op": "eq", "value": "N-p-1"}`),
		"b": `"enabled": true, "default": false, "rules": [
			{"name": "not 2", "if": [{"attr": "network_id", "op": "eq", "value": "N-p-2"}], "then": false},
			{"name": "starts", "if": [{"attr": "network_id", "op": "exists"},
				{"attr": "network_id", "op": "starts_with", "value": "N-"}], "then": true}]`,
		"c": `"enabled": true, "default": false, "rules": [
			{"name": "rollout", "if": [], "then": true, "rollout": {"percentage": 100, "bucket_by": "network_id"}}]`,
		"no_network": oneCondition(`{"attr": "site_id", "op": "eq", "value": 1}`),
	}, engine.WithRegistry(registry(t, nil, map[string]engine.ResolverFunc{"network_id": r.resolve})))
}

func TestResolvers_OncePerRequest(t *testing.T) {
	r := &networkResolver{}
	e := resolverEngine(t, r)

	req := engine.WithAttributes(context.Background(), engine.Attributes{"player_id": "p-1"})
	for _, flag := range []string{"a", "b", "c", "a", "b", "c"} {
		d, err := e.Evaluate(req, flag, nil)
		require.NoError(t, err)
		assert.True(t, d.Enabled, flag)
	}
	assert.EqualValues(t, 1, r.calls.Load(), "one call for the whole request")

	_, err := e.Evaluate(engine.WithAttributes(context.Background(), engine.Attributes{"player_id": "p-3"}), "no_network", nil)
	require.NoError(t, err)
	assert.EqualValues(t, 1, r.calls.Load(), "only rules that need the attribute resolve it")

	_, err = e.Evaluate(engine.WithAttributes(context.Background(), engine.Attributes{"player_id": "p-1"}), "a", nil)
	require.NoError(t, err)
	assert.EqualValues(t, 2, r.calls.Load(), "a new request resolves again")

	d, err := e.Evaluate(req, "a", engine.Attributes{"player_id": "p-2"})
	require.NoError(t, err)
	assert.False(t, d.Enabled)
	assert.EqualValues(t, 3, r.calls.Load(), "other inputs in the same request resolve again")
	_, err = e.Evaluate(req, "b", engine.Attributes{"player_id": "p-2"})
	require.NoError(t, err)
	assert.EqualValues(t, 3, r.calls.Load())

	_, err = e.Evaluate(req, "a", engine.Attributes{"player_id": 7})
	require.NoError(t, err)
	_, err = e.Evaluate(req, "a", engine.Attributes{"player_id": "7"})
	require.NoError(t, err)
	assert.EqualValues(t, 4, r.calls.Load(), "inputs are compared in their catalog types")
}

func TestResolvers_Concurrent(t *testing.T) {
	r := &networkResolver{}
	e := resolverEngine(t, r)
	req := engine.WithAttributes(context.Background(), engine.Attributes{"player_id": "p-1"})

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := e.Evaluate(req, "a", nil)
			assert.NoError(t, err)
			assert.True(t, d.Enabled)
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, r.calls.Load())
}

func TestResolvers_WithoutRequestScope(t *testing.T) {
	r := &networkResolver{}
	e := resolverEngine(t, r)

	d, err := e.Evaluate(context.Background(), "b", engine.Attributes{"player_id": "p-1"})
	require.NoError(t, err)
	assert.Equal(t, "starts", d.Rule)
	assert.EqualValues(t, 1, r.calls.Load(), "cached for the call")

	_, err = e.Evaluate(context.Background(), "b", engine.Attributes{"player_id": "p-1"})
	require.NoError(t, err)
	assert.EqualValues(t, 2, r.calls.Load(), "not across calls")
}

func TestResolvers_CatalogChangeInOneRequest(t *testing.T) {
	var calls []engine.Attributes
	resolve := func(_ context.Context, inputs engine.Attributes) (any, error) {
		calls = append(calls, inputs)
		for name, v := range inputs {
			return name + ":" + v.(string), nil
		}
		return nil, nil
	}
	build := func(requires string) *engine.Engine {
		doc, err := document.ParseFlags([]byte(flagsDoc("", map[string]string{
			"a": oneCondition(`{"attr": "network_id", "op": "exists"}`),
		})))
		require.NoError(t, err)
		catalog := parseCatalog(t, `{"schema_version": 1,
			"attributes": {"network_id": {"type": "string"}, "player_id": {"type": "string"}, "account_id": {"type": "string"}},
			"resolvers": {"network_id": {"requires": ["`+requires+`"]}}}`)
		e, err := engine.New(doc, catalog, engine.WithRegistry(registry(t, nil, map[string]engine.ResolverFunc{"network_id": resolve})))
		require.NoError(t, err)
		return e
	}
	before, after := build("player_id"), build("account_id")

	req := engine.WithAttributes(context.Background(), engine.Attributes{"player_id": "x", "account_id": "x"})
	for _, e := range []*engine.Engine{before, after, before, after} {
		d, err := e.Evaluate(req, "a", nil)
		require.NoError(t, err)
		assert.True(t, d.Enabled)
	}
	assert.Equal(t, []engine.Attributes{{"player_id": "x"}, {"account_id": "x"}}, calls,
		"a resolver whose inputs changed does not reuse the old result")
}

func TestResolvers_Outcomes(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		ctxAttrs  engine.Attributes
		attrs     engine.Attributes
		want      engine.Decision
		wantCalls int32
	}{
		{"sent attribute skips the resolver", nil, nil, engine.Attributes{"player_id": "p-9", "network_id": "N-p-1"},
			engine.Decision{Flag: "a", Enabled: true, Reason: engine.ReasonRule, Rule: "r"}, 0},
		{"context attribute skips the resolver", nil, engine.Attributes{"network_id": "N-p-1"}, engine.Attributes{"player_id": "p-9"},
			engine.Decision{Flag: "a", Enabled: true, Reason: engine.ReasonRule, Rule: "r"}, 0},
		{"missing input leaves both missing", nil, nil, nil,
			engine.Decision{Flag: "a", Reason: engine.ReasonDefault, RuleIndex: -1, MissingAttrs: []string{"network_id", "player_id"}}, 0},
		{"invalid input is reported", nil, nil, engine.Attributes{"player_id": true},
			engine.Decision{Flag: "a", Reason: engine.ReasonDefault, RuleIndex: -1,
				MissingAttrs: []string{"network_id"}, InvalidAttrs: []string{"player_id"}}, 0},
		{"nil value is missing", nil, nil, engine.Attributes{"player_id": "nobody"},
			engine.Decision{Flag: "a", Reason: engine.ReasonDefault, RuleIndex: -1, MissingAttrs: []string{"network_id"}}, 1},
		{"error is missing, reported and cached", errors.New("player service down"), nil, engine.Attributes{"player_id": "p-1"},
			engine.Decision{Flag: "a", Reason: engine.ReasonDefault, RuleIndex: -1,
				MissingAttrs: []string{"network_id"}, FailedResolvers: []string{"network_id"}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &networkResolver{err: tt.err}
			e := resolverEngine(t, r)
			req := engine.WithAttributes(context.Background(), tt.ctxAttrs)
			for range 2 {
				got, err := e.Evaluate(req, "a", tt.attrs)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
			assert.Equal(t, tt.wantCalls, r.calls.Load())
		})
	}
}

func TestResolvers_Catalog(t *testing.T) {
	flags := map[string]string{"a": oneCondition(`{"attr": "network_id", "op": "eq", "value": "N-p-1"}`)}
	ctx := context.Background()

	t.Run("declared but not registered leaves the attribute missing", func(t *testing.T) {
		e := mustExtEngine(t, flags)
		d, err := e.Evaluate(ctx, "a", engine.Attributes{"player_id": "p-1"})
		require.NoError(t, err)
		assert.Equal(t, []string{"network_id"}, d.MissingAttrs)
	})

	t.Run("registered but not declared is not used", func(t *testing.T) {
		r := &networkResolver{}
		e := mustExtEngine(t, map[string]string{"t": oneCondition(`{"attr": "tier", "op": "eq", "value": 1}`)},
			engine.WithRegistry(registry(t, nil, map[string]engine.ResolverFunc{"tier": r.resolve})))
		d, err := e.Evaluate(ctx, "t", engine.Attributes{"player_id": "p-1"})
		require.NoError(t, err)
		assert.Equal(t, []string{"tier"}, d.MissingAttrs)
		assert.Zero(t, r.calls.Load())
	})

	t.Run("requires an attribute not in the catalog", func(t *testing.T) {
		catalog := parseCatalog(t, `{"schema_version": 1,
			"attributes": {"network_id": {"type": "string"}},
			"resolvers": {"network_id": {"requires": ["player_id"]}}}`)
		doc, err := document.ParseFlags([]byte(flagsDoc("", flags)))
		require.NoError(t, err)
		_, err = engine.New(doc, catalog,
			engine.WithRegistry(registry(t, nil, map[string]engine.ResolverFunc{"network_id": (&networkResolver{}).resolve})))
		assert.EqualError(t, err, `resolver "network_id": attribute "player_id" is not in the catalog`)
	})
}

func TestWithAttributes(t *testing.T) {
	r := &networkResolver{}
	e := resolverEngine(t, r)

	base := engine.WithAttributes(context.Background(), engine.Attributes{"player_id": "p-2", "site_id": 1})
	ctx := engine.WithAttributes(base, engine.Attributes{"player_id": "p-1"})
	assert.Equal(t, engine.Attributes{"player_id": "p-1", "site_id": 1}, engine.AttributesFromContext(ctx), "later calls win")
	assert.Equal(t, engine.Attributes{"player_id": "p-2", "site_id": 1}, engine.AttributesFromContext(base), "parent unchanged")
	assert.Nil(t, engine.AttributesFromContext(context.Background()))

	got := engine.AttributesFromContext(ctx)
	got["site_id"] = 99
	assert.Equal(t, 1, engine.AttributesFromContext(ctx)["site_id"], "a copy")

	d, err := e.Evaluate(ctx, "a", nil)
	require.NoError(t, err)
	assert.True(t, d.Enabled, "resolver reads context attributes")

	d, err = e.Evaluate(ctx, "a", engine.Attributes{"network_id": "N-p-9"})
	require.NoError(t, err)
	assert.False(t, d.Enabled, "arguments win over context and resolvers")

	d, err = e.Evaluate(ctx, "no_network", engine.Attributes{"site_id": nil})
	require.NoError(t, err)
	assert.True(t, d.Enabled, "a nil argument is absent, so the context value applies")

	_, err = e.Evaluate(base, "a", nil)
	require.NoError(t, err)
	assert.EqualValues(t, 2, r.calls.Load(), "derived contexts share the cache, keyed by inputs")
	_, err = e.Evaluate(ctx, "c", nil)
	require.NoError(t, err)
	assert.EqualValues(t, 2, r.calls.Load())
}

func TestMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	e := mustExtEngine(t, map[string]string{
		"vip": oneCondition(`{"attr": "player_id", "op": "is_vip", "value": true}`),
		"net": oneCondition(`{"attr": "network_id", "op": "eq", "value": "N-p-1"}`),
	}, engine.WithMeterProvider(mp), engine.WithRegistry(registry(t,
		map[string]engine.OperatorFunc{"is_vip": isVIP},
		map[string]engine.ResolverFunc{"network_id": (&networkResolver{}).resolve})))

	ctx := context.Background()
	for _, player := range []string{"vip-1", "p-1", "boom"} {
		_, err := e.Evaluate(ctx, "vip", engine.Attributes{"player_id": player})
		require.NoError(t, err)
	}
	_, err := e.Evaluate(ctx, "net", engine.Attributes{"player_id": "p-1"})
	require.NoError(t, err)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))
	counts := map[string]map[attribute.Distinct]uint64{}
	sets := map[string][]attribute.Set{}
	for _, sm := range rm.ScopeMetrics {
		assert.Equal(t, engine.MeterName, sm.Scope.Name)
		for _, m := range sm.Metrics {
			assert.Equal(t, "s", m.Unit)
			hist, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok, m.Name)
			counts[m.Name] = map[attribute.Distinct]uint64{}
			for _, dp := range hist.DataPoints {
				counts[m.Name][dp.Attributes.Equivalent()] = dp.Count
				sets[m.Name] = append(sets[m.Name], dp.Attributes)
			}
		}
	}
	set := func(kv ...attribute.KeyValue) attribute.Distinct { s := attribute.NewSet(kv...); return s.Equivalent() }
	assert.Equal(t, map[attribute.Distinct]uint64{
		set(attribute.String("operator", "is_vip"), attribute.String("outcome", "ok")):    2,
		set(attribute.String("operator", "is_vip"), attribute.String("outcome", "error")): 1,
	}, counts[engine.MetricOperatorDuration], "%v", sets[engine.MetricOperatorDuration])
	assert.Equal(t, map[attribute.Distinct]uint64{
		set(attribute.String("attribute", "network_id"), attribute.String("outcome", "ok")): 1,
	}, counts[engine.MetricResolverDuration], "%v", sets[engine.MetricResolverDuration])
}
