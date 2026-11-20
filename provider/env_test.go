package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/engine"
)

func TestEnvProvider(t *testing.T) {
	setEnv(t,
		"FEATURE_NEW_CASHIER=true",
		"FEATURE_OLD_LOBBY=TRUE",
		"FEATURE_KYC_V2=false",
		"FEATURE_BROKEN=yes",
		"FEATURE_=true",
		"OTHER_FLAG=true",
		"FEATURE_NO_VALUE",
	)
	mp, _ := newReader(t)
	p, err := NewEnvProvider(Config{ClientName: "test", EnvPrefix: "FEATURE_", MeterProvider: mp})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, p.Close()) })

	tests := []struct {
		flag string
		want bool
	}{
		{flag: "new_cashier", want: true},
		{flag: "old_lobby", want: true},
		{flag: "kyc_v2", want: false},
		{flag: "broken", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			d, err := p.Evaluate(context.Background(), tt.flag, engine.Attributes{"site_id": 7})
			require.NoError(t, err)
			assert.Equal(t, tt.want, d.Enabled)
			assert.Equal(t, engine.ReasonDefault, d.Reason)
		})
	}

	for _, missing := range []string{"other_flag", "no_value", ""} {
		_, err := p.IsEnabled(context.Background(), missing, nil)
		assert.Error(t, err, missing)
	}

	snap := p.Snapshot()
	assert.Equal(t, SourceEnv, snap.Source)
	assert.Equal(t, "env", snap.FlagsVersion)
}

func TestEnvProvider_RequiresPrefix(t *testing.T) {
	_, err := NewEnvProvider(Config{EnvPrefix: " "})
	assert.Error(t, err)
}
