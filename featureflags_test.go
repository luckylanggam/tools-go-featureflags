package featureflags_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	featureflags "github.com/luckylanggam/tools-go-featureflags"
	"github.com/luckylanggam/tools-go-featureflags/engine"
	"github.com/luckylanggam/tools-go-featureflags/provider"
)

var (
	_ featureflags.Checker      = provider.Provider(nil)
	_ featureflags.Checker      = (*engine.Engine)(nil)
	_ featureflags.FeatureFlags = (*featureflags.FeatureFlagManager)(nil)
)

func newAdapter(t *testing.T) featureflags.FeatureFlags {
	t.Helper()
	t.Setenv("ADAPTER_FEATURE_NEW_CASHIER", "true")
	t.Setenv("ADAPTER_FEATURE_OLD_LOBBY", "false")
	p, err := provider.NewProvider(context.Background(), provider.Config{
		ProviderType: provider.Env,
		ClientName:   "adapter-test",
		EnvPrefix:    "ADAPTER_FEATURE_",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return featureflags.FromProvider(p)
}

func TestFromProvider_IsEnabled(t *testing.T) {
	ff := newAdapter(t)

	tests := []struct {
		name    string
		flag    string
		want    bool
		wantErr string
	}{
		{name: "v0 upper-case name", flag: "NEW_CASHIER", want: true},
		{name: "v1 lower-case name", flag: "new_cashier", want: true},
		{name: "disabled flag", flag: "OLD_LOBBY", want: false},
		{name: "unknown flag", flag: "MISSING", wantErr: "'missing' does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ff.IsEnabled(tt.flag)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFromProvider_AreAllEnabled(t *testing.T) {
	ff := newAdapter(t)

	tests := []struct {
		name     string
		flags    []string
		want     bool
		wantErrs []string
	}{
		{name: "all on", flags: []string{"NEW_CASHIER"}, want: true},
		{name: "none given", flags: nil, want: true},
		{name: "one off", flags: []string{"NEW_CASHIER", "OLD_LOBBY"}, want: false},
		{name: "every missing flag reported", flags: []string{"NEW_CASHIER", "MISSING_A", "MISSING_B"}, wantErrs: []string{"missing_a", "missing_b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ff.AreAllEnabled(tt.flags)
			if tt.wantErrs != nil {
				require.Error(t, err)
				for _, e := range tt.wantErrs {
					assert.Contains(t, err.Error(), e)
				}
				assert.False(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFromProvider_AllFlagsUnsupported(t *testing.T) {
	flags, err := newAdapter(t).AllFlags()

	assert.Nil(t, flags)
	assert.ErrorContains(t, err, "not supported")
}

func TestFromProvider_EvaluatesWithoutAttributes(t *testing.T) {
	ff := featureflags.FromProvider(checkerFunc(func(ctx context.Context, flag string, attrs engine.Attributes) (bool, error) {
		assert.Equal(t, "new_lobby", flag)
		assert.Nil(t, attrs)
		assert.NotNil(t, ctx)
		return true, nil
	}))

	on, err := ff.IsEnabled("NEW_LOBBY")

	require.NoError(t, err)
	assert.True(t, on)
}

type checkerFunc func(context.Context, string, engine.Attributes) (bool, error)

func (f checkerFunc) IsEnabled(ctx context.Context, flag string, attrs engine.Attributes) (bool, error) {
	return f(ctx, flag, attrs)
}
