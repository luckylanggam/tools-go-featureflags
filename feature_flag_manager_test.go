package featureflags_test

import (
	"github.com/stretchr/testify/require"
	"os"
	"testing"

	"github.com/luckylanggam/tools-go-featureflags"
	"github.com/stretchr/testify/assert"
)

func setupEnv(flags map[string]string) {
	for key, value := range flags {
		os.Setenv(key, value)
	}
}

func teardownEnv(flags map[string]string) {
	for key := range flags {
		os.Unsetenv(key)
	}
}

var cfg = &featureflags.Config{
	Prefix:     "FF_",
	ClientName: "test-service",
}

func TestNewFeatureFlagManager_InvalidConfigs(t *testing.T) {
	// Config nil
	_, err := featureflags.NewFeatureFlagManager(nil)
	assert.Error(t, err)

	// Empty fields
	_, err = featureflags.NewFeatureFlagManager(&featureflags.Config{})
	assert.Error(t, err)

	// Missing prefix
	_, err = featureflags.NewFeatureFlagManager(&featureflags.Config{ClientName: "svc"})
	assert.Error(t, err)

	// Missing service
	_, err = featureflags.NewFeatureFlagManager(&featureflags.Config{Prefix: "FF_"})
	assert.Error(t, err)
}

func TestNewFeatureFlagManager(t *testing.T) {
	flags := map[string]string{
		"FF_FEATURE_A": "true",
		"FF_FEATURE_B": "false",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	manager, err := featureflags.NewFeatureFlagManager(cfg)
	assert.NoError(t, err)
	assert.NotNil(t, manager)
}

func TestNewFeatureFlagManager_NoFlagsFound(t *testing.T) {
	flags := map[string]string{
		"OTHER_FEATURE_X": "true",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	manager, err := featureflags.NewFeatureFlagManager(cfg)
	assert.Nil(t, err)
	assert.NotNil(t, manager)
}

func TestNewFeatureFlagManager_NoMatchingEnvVars(t *testing.T) {
	os.Clearenv()

	cfg := &featureflags.Config{
		Prefix:     "NONEXISTENT_",
		ClientName: "test-service",
	}

	_, err := featureflags.NewFeatureFlagManager(cfg)
	require.Nil(t, err)
}

func TestAllFlags_EmptyFlagsMap(t *testing.T) {
	m := &featureflags.FeatureFlagManager{}
	all, err := m.AllFlags()
	assert.Nil(t, all)
	assert.Error(t, err)
}

func TestIsEnabled(t *testing.T) {
	flags := map[string]string{
		"FF_FEATURE_C": "true",
		"FF_FEATURE_D": "false",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	manager, err := featureflags.NewFeatureFlagManager(cfg)
	assert.NoError(t, err)

	enabled, err := manager.IsEnabled("FEATURE_C")
	assert.NoError(t, err)
	assert.True(t, enabled)

	disabled, errDisabled := manager.IsEnabled("FEATURE_D")
	assert.NoError(t, errDisabled)
	assert.False(t, disabled)

	_, err = manager.IsEnabled("FEATURE_UNKNOWN")
	assert.Error(t, err)
}

func TestAreAllEnabled(t *testing.T) {
	flags := map[string]string{
		"FF_FEATURE_D": "true",
		"FF_FEATURE_E": "true",
		"FF_FEATURE_F": "false",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	manager, err := featureflags.NewFeatureFlagManager(cfg)
	assert.NoError(t, err)

	ok, err := manager.AreAllEnabled([]string{"FEATURE_D", "FEATURE_E"})
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, err = manager.AreAllEnabled([]string{"FEATURE_D", "FEATURE_F"})
	assert.NoError(t, err)
	assert.False(t, ok)

	ok, err = manager.AreAllEnabled([]string{"FEATURE_D", "FEATURE_UNKNOWN"})
	assert.Error(t, err)
	assert.False(t, ok)
}

func TestAllFlags(t *testing.T) {
	flags := map[string]string{
		"FF_FEATURE_G": "true",
		"FF_FEATURE_H": "false",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	manager, err := featureflags.NewFeatureFlagManager(cfg)
	assert.NoError(t, err)

	allFlags, err := manager.AllFlags()
	assert.NoError(t, err)
	assert.Equal(t, 2, len(allFlags))
	assert.Equal(t, true, allFlags["FEATURE_G"])
	assert.Equal(t, false, allFlags["FEATURE_H"])
}
