package provider

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/luckylanggam/tools-go-featureflags/document"
	"github.com/luckylanggam/tools-go-featureflags/engine"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
	"github.com/luckylanggam/tools-go-featureflags/validate"
)

const (
	DefaultFlagsProfile   = "feature-flags"
	DefaultCatalogProfile = "catalog"
	DefaultPollInterval   = 30 * time.Second
	DefaultBootTimeout    = 5 * time.Second

	// MinPollInterval is the shortest poll interval the Data API accepts.
	MinPollInterval = 15 * time.Second
)

// AppConfigConfig configures the appconfig provider. Application and
// Environment are required; the rest have defaults.
type AppConfigConfig struct {
	Application    string
	Environment    string
	FlagsProfile   string
	CatalogProfile string
	// Region overrides the region from the AWS default config chain.
	Region string
	// PollInterval is how often AppConfig is polled; at least MinPollInterval.
	PollInterval time.Duration
	// BootTimeout bounds the first load in NewAppConfigProvider. When it
	// fails, the provider starts on the env values and keeps polling.
	BootTimeout time.Duration
	// Fetcher replaces the Data API fetcher, e.g. with one that reads from
	// the AppConfig Agent.
	Fetcher Fetcher
}

type appConfigProvider struct {
	*store
	cfg        AppConfigConfig
	fetcher    Fetcher
	engineOpts []engine.Option

	// Only the polling goroutine (and the boot load before it starts)
	// touches the fields below.
	raw          map[string][]byte
	versions     map[string]string
	unbuilt      map[string]bool   // profiles whose new content has not been built yet
	catalog      *document.Catalog // catalog of the AppConfig snapshot served last
	pendingError bool

	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// NewAppConfigProvider loads flags from AWS AppConfig and keeps them current
// by polling in the background. The first load waits at most BootTimeout;
// if AppConfig cannot be reached by then, the provider serves the env
// values (config.EnvPrefix) until a poll succeeds. Call Close to stop
// polling.
func NewAppConfigProvider(ctx context.Context, cfg Config, acCfg AppConfigConfig) (Provider, error) {
	acCfg, err := withDefaults(acCfg)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	if acCfg.Fetcher == nil {
		acCfg.Fetcher, err = newDataAPIFetcher(ctx, acCfg)
		if err != nil {
			log.Error(err)
			return nil, err
		}
	}
	return newAppConfigProvider(ctx, cfg, acCfg, time.Now)
}

func withDefaults(c AppConfigConfig) (AppConfigConfig, error) {
	if strings.TrimSpace(c.Application) == "" || strings.TrimSpace(c.Environment) == "" {
		return c, fmt.Errorf(ffErrors.ErrAppConfigMissingTarget)
	}
	if c.FlagsProfile == "" {
		c.FlagsProfile = DefaultFlagsProfile
	}
	if c.CatalogProfile == "" {
		c.CatalogProfile = DefaultCatalogProfile
	}
	if c.PollInterval == 0 {
		c.PollInterval = DefaultPollInterval
	}
	if c.PollInterval < MinPollInterval {
		return c, fmt.Errorf(ffErrors.ErrAppConfigPollInterval, c.PollInterval, MinPollInterval)
	}
	if c.BootTimeout == 0 {
		c.BootTimeout = DefaultBootTimeout
	}
	return c, nil
}

func newDataAPIFetcher(ctx context.Context, c AppConfigConfig) (Fetcher, error) {
	var opts []func(*config.LoadOptions) error
	if c.Region != "" {
		opts = append(opts, config.WithRegion(c.Region))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrAppConfigLoadAWSConfig, err)
	}
	client := appconfigdata.NewFromConfig(awsCfg)
	return NewDataAPIFetcher(client, c.Application, c.Environment, int32(c.PollInterval/time.Second)), nil
}

// newAppConfigProvider expects acCfg with defaults applied and a fetcher.
func newAppConfigProvider(ctx context.Context, cfg Config, acCfg AppConfigConfig, now func() time.Time) (*appConfigProvider, error) {
	fallback, err := envState(cfg, environ(), now())
	if err != nil {
		return nil, err
	}

	s, err := newStore(cfg.MeterProvider, fallback, now)
	if err != nil {
		return nil, err
	}
	p := &appConfigProvider{
		store:      s,
		cfg:        acCfg,
		fetcher:    acCfg.Fetcher,
		engineOpts: cfg.engineOptions(),
		raw:        map[string][]byte{},
		versions:   map[string]string{},
		unbuilt:    map[string]bool{},
	}

	bootCtx, cancelBoot := context.WithTimeout(ctx, acCfg.BootTimeout)
	p.poll(bootCtx)
	cancelBoot()
	if p.Snapshot().Source == SourceEnv {
		log.Warnf("AppConfig %s/%s not loaded at boot for service %s; serving env flags until a poll succeeds",
			acCfg.Application, acCfg.Environment, cfg.ClientName)
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.cancel = cancel
	p.wg.Add(1)
	go p.run(runCtx)
	return p, nil
}

// Close stops polling, waits for an in-flight poll to finish and
// unregisters the provider's metrics.
func (p *appConfigProvider) Close() error {
	var err error
	p.closeOnce.Do(func() {
		p.cancel()
		p.wg.Wait()
		err = p.metrics.close()
	})
	return err
}

func (p *appConfigProvider) run(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollCtx, cancel := context.WithTimeout(ctx, p.cfg.PollInterval/2)
			p.poll(pollCtx)
			cancel()
		}
	}
}

// poll fetches both profiles and decides what to serve next:
//   - a fetch failed: keep the snapshot; an AppConfig one becomes "cache".
//     Content already fetched stays unbuilt, because the Data API will not
//     return it again;
//   - new content that builds: serve it as "appconfig";
//   - new content that does not build: keep the snapshot as "cache" until
//     a later version builds;
//   - nothing new: the snapshot is confirmed current.
func (p *appConfigProvider) poll(ctx context.Context) {
	flagsErr := p.fetch(ctx, p.cfg.FlagsProfile)
	catalogErr := p.fetch(ctx, p.cfg.CatalogProfile)
	if flagsErr != nil || catalogErr != nil {
		p.markStale()
		return
	}

	if len(p.unbuilt) > 0 {
		st, catalog, profile, err := p.build(!p.unbuilt[p.cfg.FlagsProfile])
		clear(p.unbuilt)
		if err != nil {
			log.Errorf(ffErrors.ErrAppConfigInvalidVersion, profile, err)
			p.metrics.pollFailed(ctx, profile, PollFailureInvalid)
			p.pendingError = true
			p.markStale()
			return
		}
		p.pendingError = false
		p.catalog = catalog
		p.swap(st)
		log.Infof("Serving feature flags %s, catalog %s from AppConfig", st.info.FlagsVersion, st.info.CatalogVersion)
		return
	}

	if p.pendingError {
		p.markStale()
		return
	}
	p.confirm()
}

// fetch fetches one profile and marks it unbuilt when its content changed.
func (p *appConfigProvider) fetch(ctx context.Context, profile string) error {
	data, version, changed, err := p.fetcher.Fetch(ctx, profile)
	if err != nil {
		log.Warnf(ffErrors.ErrAppConfigFetch, profile, err)
		p.metrics.pollFailed(ctx, profile, PollFailureFetch)
		return err
	}
	if !changed {
		return nil
	}
	p.versions[profile] = version
	if bytes.Equal(data, p.raw[profile]) {
		return nil
	}
	p.raw[profile] = data
	p.unbuilt[profile] = true
	return nil
}

// build checks and compiles the latest fetched documents. A new catalog is
// checked against the one served last, so an attribute the flags use cannot
// change type under them. On failure it also returns the profile to blame;
// catalogOnly blames validation problems on the catalog.
func (p *appConfigProvider) build(catalogOnly bool) (*state, *document.Catalog, string, error) {
	flagsProfile, catalogProfile := p.cfg.FlagsProfile, p.cfg.CatalogProfile
	for _, profile := range []string{flagsProfile, catalogProfile} {
		if p.raw[profile] == nil {
			return nil, nil, profile, fmt.Errorf(ffErrors.ErrAppConfigNoContent)
		}
		warning, err := validate.CheckSize(p.raw[profile])
		if err != nil {
			return nil, nil, profile, err
		}
		if warning != "" {
			log.Warnf("AppConfig profile %s: %s", profile, warning)
		}
	}
	flags, err := document.ParseFlags(p.raw[flagsProfile])
	if err != nil {
		return nil, nil, flagsProfile, err
	}
	catalog, err := document.ParseCatalog(p.raw[catalogProfile])
	if err != nil {
		return nil, nil, catalogProfile, err
	}
	blame := flagsProfile
	if catalogOnly {
		blame = catalogProfile
	}
	warnings, err := validate.ValidateCatalogChange(p.catalog, catalog, flags)
	if err != nil {
		return nil, nil, blame, err
	}
	for _, w := range warnings {
		log.Warnf("AppConfig profile %s: %s", flagsProfile, w)
	}
	e, err := engine.New(flags, catalog, p.engineOpts...)
	if err != nil {
		return nil, nil, blame, err
	}
	now := p.now()
	return &state{
		engine: e,
		info: Snapshot{
			Source:         SourceAppConfig,
			FlagsVersion:   p.versions[flagsProfile],
			CatalogVersion: p.versions[catalogProfile],
			LoadedAt:       now,
			SyncedAt:       now,
		},
	}, catalog, "", nil
}

// markStale turns a served AppConfig snapshot into "cache". Env stays env.
func (p *appConfigProvider) markStale() {
	cur := p.load()
	if cur.info.Source != SourceAppConfig {
		return
	}
	next := *cur
	next.info.Source = SourceCache
	p.swap(&next)
	log.Warnf("Serving cached feature flags %s; last synced %s", cur.info.FlagsVersion, cur.info.SyncedAt.Format(time.RFC3339))
}

// confirm records that AppConfig still holds the snapshot being served.
// Version labels are refreshed, since AppConfig can deploy the same
// content under a new label.
func (p *appConfigProvider) confirm() {
	cur := p.load()
	if cur.info.Source == SourceEnv {
		return
	}
	next := *cur
	next.info.Source = SourceAppConfig
	next.info.FlagsVersion = p.versions[p.cfg.FlagsProfile]
	next.info.CatalogVersion = p.versions[p.cfg.CatalogProfile]
	next.info.SyncedAt = p.now()
	p.swap(&next)
}

var _ Provider = (*appConfigProvider)(nil)
