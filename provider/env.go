package provider

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/luckylanggam/tools-go-featureflags/document"
	"github.com/luckylanggam/tools-go-featureflags/engine"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

const envVersion = "env"

type envProvider struct {
	*store
}

// NewEnvProvider serves flags from environment variables starting with
// config.EnvPrefix, as Phase 1 does: FEATURE_NEW_CASHIER=true becomes flag
// new_cashier, on for every caller. Any value other than "true" (any case)
// is off. The variables are read once.
func NewEnvProvider(config Config) (Provider, error) {
	st, err := envState(config, environ(), time.Now())
	if err != nil {
		return nil, err
	}
	s, err := newStore(config.MeterProvider, st, time.Now)
	if err != nil {
		return nil, err
	}
	return &envProvider{store: s}, nil
}

// Close unregisters the provider's metrics.
func (p *envProvider) Close() error { return p.metrics.close() }

// environ is os.Environ, replaced in tests.
var environ = os.Environ

// envState compiles the env variables into a rule-free snapshot.
func envState(config Config, environ []string, now time.Time) (*state, error) {
	if strings.TrimSpace(config.EnvPrefix) == "" {
		log.Error(ffErrors.ErrEnvPrefixRequired)
		return nil, fmt.Errorf(ffErrors.ErrEnvPrefixRequired)
	}

	flags := &document.Flags{SchemaVersion: document.SchemaVersion, Flags: map[string]document.Flag{}}
	for _, kv := range environ {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, config.EnvPrefix) {
			continue
		}
		name := strings.ToLower(strings.TrimPrefix(key, config.EnvPrefix))
		if name == "" {
			continue
		}
		flags.Flags[name] = document.Flag{Enabled: true, Default: strings.EqualFold(value, "true")}
	}
	if len(flags.Flags) == 0 {
		log.Warnf("No feature flags found for prefix %s in service %s", config.EnvPrefix, config.ClientName)
	}

	catalog := &document.Catalog{SchemaVersion: document.SchemaVersion, Attributes: map[string]document.Attribute{}}
	e, err := engine.New(flags, catalog, config.engineOptions()...)
	if err != nil {
		log.Errorf(ffErrors.ErrEnvCompile, err)
		return nil, fmt.Errorf(ffErrors.ErrEnvCompile, err)
	}
	return &state{
		engine: e,
		info: Snapshot{
			Source:         SourceEnv,
			FlagsVersion:   envVersion,
			CatalogVersion: envVersion,
			LoadedAt:       now,
			SyncedAt:       now,
		},
	}, nil
}

var _ Provider = (*envProvider)(nil)
