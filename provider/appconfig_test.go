package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/luckylanggam/tools-go-featureflags/engine"
	"github.com/luckylanggam/tools-go-featureflags/validate"
)

var site7 = engine.Attributes{"site_id": 7}

func newTestProvider(t *testing.T, f *fakeFetcher, c *clock, opts ...func(*AppConfigConfig)) (*appConfigProvider, *sdkmetric.ManualReader) {
	t.Helper()
	acCfg := AppConfigConfig{Application: "atom", Environment: "uat", Fetcher: f, BootTimeout: 50 * time.Millisecond}
	for _, o := range opts {
		o(&acCfg)
	}
	acCfg, err := withDefaults(acCfg)
	require.NoError(t, err)
	mp, reader := newReader(t)
	p, err := newAppConfigProvider(context.Background(), Config{ClientName: "test", EnvPrefix: "FEATURE_", MeterProvider: mp}, acCfg, c.now)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, p.Close()) })
	return p, reader
}

func requireEnabled(t *testing.T, p Provider, attrs engine.Attributes, want bool) {
	t.Helper()
	got, err := p.IsEnabled(context.Background(), "new_cashier", attrs)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestAppConfig_BootLoadsAppConfig(t *testing.T) {
	setEnv(t, "FEATURE_NEW_CASHIER=true")
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	c := newClock()

	p, _ := newTestProvider(t, f, c)

	snap := p.Snapshot()
	assert.Equal(t, SourceAppConfig, snap.Source)
	assert.Equal(t, "v1", snap.FlagsVersion)
	assert.Equal(t, "c1", snap.CatalogVersion)
	assert.Equal(t, c.now(), snap.LoadedAt)
	requireEnabled(t, p, site7, true)
	requireEnabled(t, p, engine.Attributes{"site_id": 8}, false)
}

func TestAppConfig_BootUnreachableServesEnvUntilAPollSucceeds(t *testing.T) {
	setEnv(t, "FEATURE_NEW_CASHIER=true")
	f := newFakeFetcher()
	f.pushBoth(failure(), failure())
	c := newClock()

	p, _ := newTestProvider(t, f, c)

	assert.Equal(t, SourceEnv, p.Snapshot().Source)
	requireEnabled(t, p, nil, true)

	f.pushBoth(failure(), failure())
	p.poll(context.Background())
	assert.Equal(t, SourceEnv, p.Snapshot().Source, "failed polls keep env, not cache")

	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	p.poll(context.Background())
	assert.Equal(t, SourceAppConfig, p.Snapshot().Source)
	requireEnabled(t, p, nil, false)
}

func TestAppConfig_BootWaitsAtMostBootTimeout(t *testing.T) {
	setEnv(t, "FEATURE_NEW_CASHIER=true")
	f := newFakeFetcher()
	f.pushBoth(fetchResult{block: true}, unchanged())

	start := time.Now()
	p, _ := newTestProvider(t, f, newClock())

	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, SourceEnv, p.Snapshot().Source)
	requireEnabled(t, p, nil, true)
}

func TestAppConfig_IsEnabledDoesNotWaitForAPoll(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	p, _ := newTestProvider(t, f, newClock())

	f.started = make(chan struct{}, 1)
	f.push(DefaultFlagsProfile, fetchResult{block: true})
	pollCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.poll(pollCtx)
		close(done)
	}()
	<-f.started

	start := time.Now()
	requireEnabled(t, p, site7, true)
	assert.Less(t, time.Since(start), 100*time.Millisecond)

	cancel()
	<-done
	assert.Equal(t, SourceCache, p.Snapshot().Source)
}

func TestAppConfig_NewVersionIsSwapped(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	c := newClock()
	p, _ := newTestProvider(t, f, c)

	c.advance(30 * time.Second)
	f.pushBoth(content(flagsV2, "v2"), unchanged())
	p.poll(context.Background())

	snap := p.Snapshot()
	assert.Equal(t, SourceAppConfig, snap.Source)
	assert.Equal(t, "v2", snap.FlagsVersion)
	assert.Equal(t, "c1", snap.CatalogVersion)
	assert.Equal(t, c.now(), snap.LoadedAt)
	requireEnabled(t, p, engine.Attributes{"site_id": 8}, true)
}

func TestAppConfig_InvalidVersionKeepsPreviousSnapshot(t *testing.T) {
	tests := []struct {
		name    string
		flags   string
		catalog string
		blame   string
	}{
		{name: "flags do not parse", flags: `{"schema_version": 1, "flags": `, blame: DefaultFlagsProfile},
		{name: "unknown schema version", flags: `{"schema_version": 2, "flags": {}}`, blame: DefaultFlagsProfile},
		{name: "engine rejects the flags", flags: flagsBadAttr, blame: DefaultFlagsProfile},
		{name: "catalog does not parse", catalog: `{"schema_version": 1, "attributes": {}, "extra": 1}`, blame: DefaultCatalogProfile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t)
			f := newFakeFetcher()
			f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
			p, reader := newTestProvider(t, f, newClock())

			flags, catalog := unchanged(), unchanged()
			if tt.flags != "" {
				flags = content(tt.flags, "bad")
			}
			if tt.catalog != "" {
				catalog = content(tt.catalog, "bad")
			}
			f.pushBoth(flags, catalog)
			p.poll(context.Background())

			snap := p.Snapshot()
			assert.Equal(t, SourceCache, snap.Source)
			assert.Equal(t, "v1", snap.FlagsVersion)
			requireEnabled(t, p, site7, true)
			assertPollFailures(t, reader, map[[2]string]int64{{tt.blame, "invalid"}: 1})

			// The same bad content stays rejected without being rebuilt.
			f.pushBoth(unchanged(), unchanged())
			p.poll(context.Background())
			assert.Equal(t, SourceCache, p.Snapshot().Source)
			assertPollFailures(t, reader, map[[2]string]int64{{tt.blame, "invalid"}: 1})
		})
	}
}

func TestAppConfig_RecoversAfterAnInvalidVersion(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	p, _ := newTestProvider(t, f, newClock())

	f.pushBoth(content(flagsBadAttr, "bad"), unchanged())
	p.poll(context.Background())
	require.Equal(t, SourceCache, p.Snapshot().Source)

	f.pushBoth(content(flagsV2, "v3"), unchanged())
	p.poll(context.Background())
	assert.Equal(t, SourceAppConfig, p.Snapshot().Source)
	assert.Equal(t, "v3", p.Snapshot().FlagsVersion)
}

func TestAppConfig_ValidationRejectsAVersion(t *testing.T) {
	tests := []struct {
		name    string
		flags   string
		catalog string
		blame   string
	}{
		{
			name:  "value of the wrong type",
			flags: strings.Replace(flagsV1, `"value": 7`, `"value": "7"`, 1),
			blame: DefaultFlagsProfile,
		},
		{
			name:    "catalog drops an attribute the flags use",
			catalog: `{"schema_version": 1, "attributes": {"tenant": {"type": "string"}}}`,
			blame:   DefaultCatalogProfile,
		},
		{
			name:  "document over the AppConfig size limit",
			flags: `{"schema_version": 1, "flags": {}, "pad": "` + strings.Repeat("x", validate.MaxDocumentBytes) + `"}`,
			blame: DefaultFlagsProfile,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t)
			f := newFakeFetcher()
			f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
			p, reader := newTestProvider(t, f, newClock())

			flags, catalog := unchanged(), unchanged()
			if tt.flags != "" {
				flags = content(tt.flags, "bad")
			}
			if tt.catalog != "" {
				catalog = content(tt.catalog, "bad")
			}
			f.pushBoth(flags, catalog)
			p.poll(context.Background())

			assert.Equal(t, SourceCache, p.Snapshot().Source)
			assert.Equal(t, "v1", p.Snapshot().FlagsVersion)
			assert.Equal(t, "c1", p.Snapshot().CatalogVersion)
			requireEnabled(t, p, site7, true)
			assertPollFailures(t, reader, map[[2]string]int64{{tt.blame, "invalid"}: 1})
		})
	}
}

// A rollout bucket holds no value, so only the previous catalog shows that
// its attribute changed type.
func TestAppConfig_CatalogRetypingAUsedAttributeIsRejected(t *testing.T) {
	const rollout = `{"schema_version": 1, "flags": {"new_cashier": {
  "owner": "ivo", "jira": "ATOM-56", "remove_after": "2027-01-01",
  "enabled": true, "default": false,
  "rules": [{"name": "all", "if": [], "then": true, "rollout": {"percentage": 100, "bucket_by": "network_id"}}]}}}`
	const asString = `{"schema_version": 1, "attributes": {"network_id": {"type": "string"}}}`
	const asInteger = `{"schema_version": 1, "attributes": {"network_id": {"type": "integer"}}}`
	network := engine.Attributes{"network_id": "N-1"}

	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(rollout, "v1"), content(asString, "c1"))
	p, reader := newTestProvider(t, f, newClock())
	requireEnabled(t, p, network, true)

	f.pushBoth(unchanged(), content(asInteger, "c2"))
	p.poll(context.Background())

	assert.Equal(t, SourceCache, p.Snapshot().Source)
	assert.Equal(t, "c1", p.Snapshot().CatalogVersion)
	requireEnabled(t, p, network, true)
	assertPollFailures(t, reader, map[[2]string]int64{{DefaultCatalogProfile, "invalid"}: 1})

	// Going back to the served type is accepted.
	f.pushBoth(unchanged(), content(asString, "c3"))
	p.poll(context.Background())
	assert.Equal(t, SourceAppConfig, p.Snapshot().Source)
	assert.Equal(t, "c3", p.Snapshot().CatalogVersion)
}

// With nothing served from AppConfig yet there is no catalog to compare
// against, so the first catalog is checked on its own.
func TestAppConfig_FirstCatalogIsNotComparedWithEnv(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(fetchResult{err: errUnreachable}, fetchResult{err: errUnreachable})
	p, _ := newTestProvider(t, f, newClock())
	require.Equal(t, SourceEnv, p.Snapshot().Source)

	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	p.poll(context.Background())
	assert.Equal(t, SourceAppConfig, p.Snapshot().Source)
}

func TestAppConfig_FetchFailureServesCacheThenConfirms(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	c := newClock()
	p, reader := newTestProvider(t, f, c)
	loadedAt := p.Snapshot().LoadedAt

	c.advance(30 * time.Second)
	f.pushBoth(unchanged(), failure())
	p.poll(context.Background())
	assert.Equal(t, SourceCache, p.Snapshot().Source)
	assert.Equal(t, loadedAt, p.Snapshot().SyncedAt)
	assertPollFailures(t, reader, map[[2]string]int64{{DefaultCatalogProfile, "fetch"}: 1})

	c.advance(30 * time.Second)
	f.pushBoth(unchanged(), unchanged())
	p.poll(context.Background())
	snap := p.Snapshot()
	assert.Equal(t, SourceAppConfig, snap.Source)
	assert.Equal(t, loadedAt, snap.LoadedAt)
	assert.Equal(t, c.now(), snap.SyncedAt)
}

func TestAppConfig_SameContentAfterNewSessionIsNotRebuilt(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	c := newClock()
	p, _ := newTestProvider(t, f, c)
	loadedAt := p.Snapshot().LoadedAt

	// A new Data API session returns the full content again.
	c.advance(30 * time.Second)
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	p.poll(context.Background())

	assert.Equal(t, loadedAt, p.Snapshot().LoadedAt)
	assert.Equal(t, c.now(), p.Snapshot().SyncedAt)
}

func TestAppConfig_ContentFetchedBeforeAFailureIsBuiltLater(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	c := newClock()
	p, _ := newTestProvider(t, f, c)

	// flags v2 arrives, but the catalog fetch fails in the same poll.
	c.advance(30 * time.Second)
	f.pushBoth(content(flagsV2, "v2"), failure())
	p.poll(context.Background())
	assert.Equal(t, SourceCache, p.Snapshot().Source)
	assert.Equal(t, "v1", p.Snapshot().FlagsVersion)

	// The Data API returns v2 only once, so both profiles look unchanged now.
	c.advance(30 * time.Second)
	f.pushBoth(unchanged(), unchanged())
	p.poll(context.Background())

	snap := p.Snapshot()
	assert.Equal(t, SourceAppConfig, snap.Source)
	assert.Equal(t, "v2", snap.FlagsVersion)
	assert.Equal(t, c.now(), snap.LoadedAt)
	requireEnabled(t, p, engine.Attributes{"site_id": 8}, true)
}

func TestAppConfig_SameContentUnderANewLabelRefreshesTheLabel(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	c := newClock()
	p, reader := newTestProvider(t, f, c)
	loadedAt := p.Snapshot().LoadedAt

	c.advance(30 * time.Second)
	f.pushBoth(content(flagsV1, "v2"), content(testCatalog, "c2"))
	p.poll(context.Background())

	snap := p.Snapshot()
	assert.Equal(t, SourceAppConfig, snap.Source)
	assert.Equal(t, "v2", snap.FlagsVersion)
	assert.Equal(t, "c2", snap.CatalogVersion)
	assert.Equal(t, loadedAt, snap.LoadedAt, "the engine is reused")
	assert.Equal(t, c.now(), snap.SyncedAt)

	gauge := collect(t, reader, "featureflags.snapshot.age").(metricdata.Gauge[float64])
	require.Len(t, gauge.DataPoints, 1)
	v, _ := gauge.DataPoints[0].Attributes.Value("flags_version")
	assert.Equal(t, "v2", v.AsString())
}

func TestAppConfig_Metrics(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	c := newClock()
	p, reader := newTestProvider(t, f, c)

	requireEnabled(t, p, site7, true)
	requireEnabled(t, p, site7, true)
	requireEnabled(t, p, nil, false)
	_, err := p.Evaluate(context.Background(), "missing", nil)
	require.Error(t, err)

	sum := collect(t, reader, "featureflags.evaluations").(metricdata.Sum[int64])
	got := map[[3]string]int64{}
	for _, dp := range sum.DataPoints {
		got[[3]string{attr(dp.Attributes, "flag"), attr(dp.Attributes, "enabled"), attr(dp.Attributes, "reason")}] = dp.Value
	}
	assert.Equal(t, map[[3]string]int64{
		{"new_cashier", "true", "rule"}:      2,
		{"new_cashier", "false", "default"}:  1,
		{"missing", "false", "unknown_flag"}: 1,
	}, got)

	c.advance(45 * time.Second)
	f.pushBoth(failure(), unchanged())
	p.poll(context.Background())

	gauge := collect(t, reader, "featureflags.snapshot.age").(metricdata.Gauge[float64])
	require.Len(t, gauge.DataPoints, 1)
	dp := gauge.DataPoints[0]
	assert.Equal(t, 45.0, dp.Value)
	assert.Equal(t, "cache", attr(dp.Attributes, "source"))
	assert.Equal(t, "v1", attr(dp.Attributes, "flags_version"))
	assert.Equal(t, "c1", attr(dp.Attributes, "catalog_version"))
}

func TestAppConfig_CloseStopsPollingAndIsIdempotent(t *testing.T) {
	setEnv(t)
	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	acCfg, err := withDefaults(AppConfigConfig{Application: "atom", Environment: "uat", Fetcher: f})
	require.NoError(t, err)
	acCfg.PollInterval = 5 * time.Millisecond // below the public minimum, tests only
	mp, _ := newReader(t)
	p, err := newAppConfigProvider(context.Background(), Config{EnvPrefix: "FEATURE_", MeterProvider: mp}, acCfg, time.Now)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.calls[DefaultFlagsProfile] >= 3
	}, time.Second, time.Millisecond)

	require.NoError(t, p.Close())
	require.NoError(t, p.Close())
	f.mu.Lock()
	calls := f.calls[DefaultFlagsProfile]
	f.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, calls, f.calls[DefaultFlagsProfile])
	requireEnabled(t, p, site7, true)
}

func TestWithDefaults(t *testing.T) {
	got, err := withDefaults(AppConfigConfig{Application: "atom", Environment: "uat"})
	require.NoError(t, err)
	assert.Equal(t, DefaultFlagsProfile, got.FlagsProfile)
	assert.Equal(t, DefaultCatalogProfile, got.CatalogProfile)
	assert.Equal(t, DefaultPollInterval, got.PollInterval)
	assert.Equal(t, DefaultBootTimeout, got.BootTimeout)

	_, err = withDefaults(AppConfigConfig{Environment: "uat"})
	assert.Error(t, err)
	_, err = withDefaults(AppConfigConfig{Application: "atom"})
	assert.Error(t, err)
	_, err = withDefaults(AppConfigConfig{Application: "atom", Environment: "uat", PollInterval: 10 * time.Second})
	assert.Error(t, err)
}

func TestNewProvider(t *testing.T) {
	setEnv(t, "FEATURE_NEW_CASHIER=true")
	mp, _ := newReader(t)

	p, err := NewProvider(context.Background(), Config{ProviderType: Env, EnvPrefix: "FEATURE_", MeterProvider: mp})
	require.NoError(t, err)
	requireEnabled(t, p, nil, true)
	require.NoError(t, p.Close())

	_, err = NewProvider(context.Background(), Config{ProviderType: "vault", EnvPrefix: "FEATURE_"})
	assert.Error(t, err)

	_, err = NewProvider(context.Background(), Config{ProviderType: AppConfig, EnvPrefix: "FEATURE_"}, "not a config")
	assert.Error(t, err)

	f := newFakeFetcher()
	f.pushBoth(content(flagsV1, "v1"), content(testCatalog, "c1"))
	p, err = NewProvider(context.Background(), Config{ProviderType: AppConfig, EnvPrefix: "FEATURE_", MeterProvider: mp},
		AppConfigConfig{Application: "atom", Environment: "uat", Fetcher: f})
	require.NoError(t, err)
	assert.Equal(t, SourceAppConfig, p.Snapshot().Source)
	require.NoError(t, p.Close())
}

func assertPollFailures(t *testing.T, reader *sdkmetric.ManualReader, want map[[2]string]int64) {
	t.Helper()
	sum := collect(t, reader, "featureflags.poll.failures").(metricdata.Sum[int64])
	got := map[[2]string]int64{}
	for _, dp := range sum.DataPoints {
		got[[2]string{attr(dp.Attributes, "profile"), attr(dp.Attributes, "failure")}] = dp.Value
	}
	assert.Equal(t, want, got)
}

func attr(set attribute.Set, key string) string {
	v, _ := set.Value(attribute.Key(key))
	return v.AsString()
}

const vipCatalog = `{"schema_version": 1,
  "attributes": {"site_id": {"type": "integer"}},
  "operators": {"is_vip": {"value_type": "boolean"}}}`

// flagsVIP turns new_cashier on for sites the is_vip operator accepts.
const flagsVIP = `{"schema_version": 1, "flags": {"new_cashier": {
  "owner": "ivo", "jira": "ATOM-56", "remove_after": "2027-01-01",
  "enabled": true, "default": false,
  "rules": [{"name": "vip", "if": [{"attr": "site_id", "op": "is_vip", "value": true}], "then": true}]}}}`

func TestAppConfig_CustomOperators(t *testing.T) {
	newProvider := func(t *testing.T, f *fakeFetcher, reg *engine.Registry) (*appConfigProvider, *sdkmetric.ManualReader) {
		t.Helper()
		acCfg, err := withDefaults(AppConfigConfig{Application: "atom", Environment: "uat", Fetcher: f, BootTimeout: 50 * time.Millisecond})
		require.NoError(t, err)
		mp, reader := newReader(t)
		p, err := newAppConfigProvider(context.Background(),
			Config{EnvPrefix: "FEATURE_", Registry: reg, MeterProvider: mp}, acCfg, newClock().now)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, p.Close()) })
		return p, reader
	}

	t.Run("registered operator is used", func(t *testing.T) {
		setEnv(t)
		reg := engine.NewRegistry()
		require.NoError(t, reg.RegisterOperator("is_vip", func(_ context.Context, attr, _ any) (bool, error) {
			return attr == int64(7), nil
		}))
		f := newFakeFetcher()
		f.pushBoth(content(flagsVIP, "v1"), content(vipCatalog, "c1"))

		p, _ := newProvider(t, f, reg)

		require.Equal(t, SourceAppConfig, p.Snapshot().Source)
		requireEnabled(t, p, site7, true)
		requireEnabled(t, p, engine.Attributes{"site_id": 8}, false)
	})

	t.Run("negated registered operator passes validation and is used", func(t *testing.T) {
		setEnv(t)
		reg := engine.NewRegistry()
		require.NoError(t, reg.RegisterOperator("is_vip", func(_ context.Context, attr, _ any) (bool, error) {
			return attr == int64(7), nil
		}))
		f := newFakeFetcher()
		notVIP := strings.Replace(flagsVIP, `"op": "is_vip"`, `"op": "not_is_vip"`, 1)
		f.pushBoth(content(notVIP, "v1"), content(vipCatalog, "c1"))

		p, _ := newProvider(t, f, reg)

		require.Equal(t, SourceAppConfig, p.Snapshot().Source)
		requireEnabled(t, p, site7, false)
		requireEnabled(t, p, engine.Attributes{"site_id": 8}, true)
	})

	t.Run("unregistered operator rejects the version", func(t *testing.T) {
		setEnv(t)
		f := newFakeFetcher()
		f.pushBoth(content(flagsV1, "v1"), content(vipCatalog, "c1"))
		p, reader := newProvider(t, f, engine.NewRegistry())
		require.Equal(t, SourceAppConfig, p.Snapshot().Source)

		f.pushBoth(content(flagsVIP, "v2"), unchanged())
		p.poll(context.Background())

		assert.Equal(t, SourceCache, p.Snapshot().Source)
		assert.Equal(t, "v1", p.Snapshot().FlagsVersion)
		requireEnabled(t, p, site7, true)
		assertPollFailures(t, reader, map[[2]string]int64{{DefaultFlagsProfile, "invalid"}: 1})
	})
}
