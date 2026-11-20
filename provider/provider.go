// Package provider loads feature-flags documents and serves evaluations from
// an in-memory snapshot. Two providers exist: env, which turns today's Helm
// FEATURE_* variables into a document, and appconfig, which polls AWS
// AppConfig in the background and falls back to env when AppConfig cannot
// be reached at boot.
//
// IsEnabled and Evaluate only read the current snapshot, so they never wait
// on the network.
package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/luckylanggam/tools-go-featureflags/engine"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
	logger "github.com/luckylanggam/tools-logger"
	"go.opentelemetry.io/otel/metric"
)

// ProviderType selects where flags are loaded from.
type ProviderType string

const (
	Env       ProviderType = "env"
	AppConfig ProviderType = "appconfig"
)

// Source says where the snapshot being served came from.
type Source string

const (
	// SourceAppConfig: the last poll reached AppConfig and its documents
	// are being served.
	SourceAppConfig Source = "appconfig"
	// SourceCache: the last good AppConfig snapshot is being served because
	// later polls failed or returned an invalid version.
	SourceCache Source = "cache"
	// SourceEnv: the Helm env values are being served because AppConfig has
	// not been reached since boot, or the env provider is in use.
	SourceEnv Source = "env"
)

// Snapshot describes the documents being served. FlagsVersion and
// CatalogVersion are the AppConfig version labels, or a content hash when a
// version has no label. LoadedAt is when this snapshot was built; SyncedAt
// is when its source last confirmed it is current.
type Snapshot struct {
	Source         Source
	FlagsVersion   string
	CatalogVersion string
	LoadedAt       time.Time
	SyncedAt       time.Time
}

// Provider evaluates flags against the current snapshot.
type Provider interface {
	// IsEnabled reports whether flag is on for the caller described by
	// attrs. An unknown flag is off and returns an error.
	IsEnabled(ctx context.Context, flag string, attrs engine.Attributes) (bool, error)
	// Evaluate returns the decision for flag and the reason behind it.
	Evaluate(ctx context.Context, flag string, attrs engine.Attributes) (engine.Decision, error)
	// Snapshot describes the documents currently being served.
	Snapshot() Snapshot
	// Close stops background polling. Evaluations keep working on the last
	// snapshot.
	Close() error
}

// Config selects and names a provider.
type Config struct {
	ProviderType ProviderType
	// ClientName is the service using the flags, used in logs.
	ClientName string
	// EnvPrefix is the prefix of the Helm flag variables, e.g. "FEATURE_".
	// The env provider reads them, and the appconfig provider falls back to
	// them at boot.
	EnvPrefix string
	// Registry holds the service's custom operators and resolvers. Every
	// document version is compiled with it.
	Registry *engine.Registry
	// MeterProvider records the provider's and the engine's metrics. Nil
	// uses the global OpenTelemetry meter provider.
	MeterProvider metric.MeterProvider
}

// engineOptions are the options every engine.New gets.
func (c Config) engineOptions() []engine.Option {
	var opts []engine.Option
	if c.Registry != nil {
		opts = append(opts, engine.WithRegistry(c.Registry))
	}
	if c.MeterProvider != nil {
		opts = append(opts, engine.WithMeterProvider(c.MeterProvider))
	}
	return opts
}

var log = logger.New("feature-flags", false)

// NewProvider returns the provider selected by config.ProviderType. The
// appconfig provider takes an AppConfigConfig as providerConfig.
func NewProvider(ctx context.Context, config Config, providerConfig ...any) (Provider, error) {
	switch config.ProviderType {
	case Env:
		return NewEnvProvider(config)
	case AppConfig:
		var acCfg AppConfigConfig
		if len(providerConfig) > 0 && providerConfig[0] != nil {
			var ok bool
			acCfg, ok = providerConfig[0].(AppConfigConfig)
			if !ok {
				log.Error(ffErrors.ErrInvalidAppConfigConfigType)
				return nil, fmt.Errorf(ffErrors.ErrInvalidAppConfigConfigType)
			}
		}
		return NewAppConfigProvider(ctx, config, acCfg)
	default:
		log.Errorf(ffErrors.ErrUnsupportedProvider, config.ProviderType)
		return nil, fmt.Errorf(ffErrors.ErrUnsupportedProvider, config.ProviderType)
	}
}
