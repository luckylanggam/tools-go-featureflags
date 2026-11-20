//go:build integration

package provider

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_AppConfig loads the real feature-flags and catalog
// profiles of one AppConfig application with the caller's AWS credentials:
//
//	FF_IT_APPLICATION=atom FF_IT_ENVIRONMENT=uat AWS_PROFILE=atom-dev \
//	  go test -tags integration -run TestIntegration ./provider/
func TestIntegration_AppConfig(t *testing.T) {
	app, env := os.Getenv("FF_IT_APPLICATION"), os.Getenv("FF_IT_ENVIRONMENT")
	if app == "" || env == "" {
		t.Skip("FF_IT_APPLICATION and FF_IT_ENVIRONMENT not set")
	}
	mp, reader := newReader(t)

	p, err := NewAppConfigProvider(context.Background(),
		Config{ClientName: "integration-test", EnvPrefix: "FEATURE_", MeterProvider: mp},
		AppConfigConfig{Application: app, Environment: env, Region: os.Getenv("FF_IT_REGION")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, p.Close()) })

	snap := p.Snapshot()
	require.Equal(t, SourceAppConfig, snap.Source, "AppConfig was not loaded at boot")
	assert.NotEmpty(t, snap.FlagsVersion)
	assert.NotEmpty(t, snap.CatalogVersion)
	t.Logf("loaded feature-flags %s, catalog %s", snap.FlagsVersion, snap.CatalogVersion)

	if flag := os.Getenv("FF_IT_FLAG"); flag != "" {
		d, err := p.Evaluate(context.Background(), flag, nil)
		require.NoError(t, err)
		t.Logf("%s: enabled=%t reason=%s", flag, d.Enabled, d.Reason)
	}
	collect(t, reader, "featureflags.snapshot.age")
}
