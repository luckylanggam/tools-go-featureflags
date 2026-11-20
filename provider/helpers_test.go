package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/engine"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const testCatalog = `{"schema_version": 1, "attributes": {"site_id": {"type": "integer"}}}`

// flagsV1 turns new_cashier on for site 7 only.
const flagsV1 = `{"schema_version": 1, "flags": {"new_cashier": {
  "owner": "ivo", "jira": "ATOM-56", "remove_after": "2027-01-01",
  "enabled": true, "default": false,
  "rules": [{"name": "site 7", "if": [{"attr": "site_id", "op": "eq", "value": 7}], "then": true}]}}}`

// flagsV2 turns new_cashier on for everyone.
const flagsV2 = `{"schema_version": 1, "flags": {"new_cashier": {
  "owner": "ivo", "jira": "ATOM-56", "remove_after": "2027-01-01",
  "enabled": true, "default": true}}}`

// flagsBadAttr uses an attribute missing from the catalog, so engine.New fails.
const flagsBadAttr = `{"schema_version": 1, "flags": {"new_cashier": {
  "owner": "ivo", "jira": "ATOM-56", "remove_after": "2027-01-01",
  "enabled": true, "default": false,
  "rules": [{"name": "r", "if": [{"attr": "tenant", "op": "eq", "value": "x"}], "then": true}]}}}`

var errUnreachable = errors.New("appconfig unreachable")

// fetchResult is one scripted answer of fakeFetcher.
type fetchResult struct {
	data    string
	version string
	err     error
	block   bool // wait until the context is done, then fail
}

// fakeFetcher answers each profile from a script; once a script runs out,
// it repeats "unchanged".
type fakeFetcher struct {
	mu      sync.Mutex
	scripts map[string][]fetchResult
	calls   map[string]int
	started chan struct{} // receives once per blocking call, if set
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{scripts: map[string][]fetchResult{}, calls: map[string]int{}}
}

func (f *fakeFetcher) push(profile string, results ...fetchResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[profile] = append(f.scripts[profile], results...)
}

// pushBoth scripts the same kind of answer for both profiles.
func (f *fakeFetcher) pushBoth(flags, catalog fetchResult) {
	f.push(DefaultFlagsProfile, flags)
	f.push(DefaultCatalogProfile, catalog)
}

func (f *fakeFetcher) Fetch(ctx context.Context, profile string) ([]byte, string, bool, error) {
	f.mu.Lock()
	f.calls[profile]++
	var r fetchResult
	if len(f.scripts[profile]) > 0 {
		r = f.scripts[profile][0]
		f.scripts[profile] = f.scripts[profile][1:]
	}
	started := f.started
	f.mu.Unlock()

	switch {
	case r.block:
		if started != nil {
			started <- struct{}{}
		}
		<-ctx.Done()
		return nil, "", false, ctx.Err()
	case r.err != nil:
		return nil, "", false, r.err
	case r.data == "":
		return nil, r.version, false, nil
	default:
		return []byte(r.data), r.version, true, nil
	}
}

func content(data, version string) fetchResult { return fetchResult{data: data, version: version} }
func unchanged() fetchResult                   { return fetchResult{} }
func failure() fetchResult                     { return fetchResult{err: errUnreachable} }

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// setEnv replaces the environment the providers read for one test.
func setEnv(t *testing.T, vars ...string) {
	t.Helper()
	prev := environ
	environ = func() []string { return vars }
	t.Cleanup(func() { environ = prev })
}

// newReader returns a meter provider whose metrics the test can collect.
func newReader(t *testing.T) (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	return sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)), reader
}

// collect returns the metric called name from the provider's meter.
func collect(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Aggregation {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != engine.MeterName {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m.Data
			}
		}
	}
	t.Fatalf("metric %s not collected", name)
	return nil
}
