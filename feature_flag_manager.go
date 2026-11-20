package featureflags

import (
	"errors"
	"fmt"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
	"github.com/luckylanggam/tools-logger"
	"os"
	"strings"
)

// FeatureFlagManager manages feature flags using environment variables.
// Flags can be set using the format FEATURE_FLAGNAME=true/false.
//
// Deprecated: use provider.NewProvider, wrapped with FromProvider where code
// still needs FeatureFlags. Removed in v2.
type FeatureFlagManager struct {
	flags map[string]bool
}

// Config configures NewFeatureFlagManager.
//
// Deprecated: use provider.Config. Removed in v2.
type Config struct {
	Prefix     string
	ClientName string
}

var log = logger.New("feature-flags", false)

// NewFeatureFlagManager initializes a FeatureFlagManager by scanning environment variables
// that begin with the provided prefix (e.g., "FF_"). It constructs a map of feature flag keys
// and their boolean enabled status. For example, if "FF_LOGIN=true" exists in the environment,
// the flag "LOGIN" will be available and enabled in the manager.
//
// Parameters:
//   - prefix: environment variable prefix to filter flags (e.g., "FF_")
//   - serviceName: logical name of the service using the flags (used for traceability)
//
// return error if no flags are found with the specified prefix.
//
// Example:
//
//	os.Setenv("FF_LOGIN", "true")
//	os.Setenv("FF_SIGNUP", "false")
//	ffMgr := NewFeatureFlagManager("FF_", "auth-service")
//
// The flags are stored in a map for quick access and thread-safe operations.
//
// Deprecated: use provider.NewProvider, wrapped with FromProvider where code
// still needs FeatureFlags. Removed in v2.
func NewFeatureFlagManager(params *Config) (*FeatureFlagManager, error) {
	if params == nil {
		log.Error("feature flags config is nil")
		return nil, fmt.Errorf("config cannot be nil")
	}
	if strings.TrimSpace(params.Prefix) == "" {
		log.Info("feature flags config prefix is empty")
		return nil, fmt.Errorf("config prefix is required")
	}
	if strings.TrimSpace(params.ClientName) == "" {
		log.Info("feature flags config service is empty")
		return nil, fmt.Errorf("config service name is required")
	}
	log.Infof("Initializing FeatureFlagManager for service %s with prefix %s", params.ClientName, params.Prefix)
	flags := make(map[string]bool)

	for _, env := range os.Environ() {
		// Each env var is in format KEY=VALUE
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := strings.ToLower(parts[1])

		if strings.HasPrefix(key, params.Prefix) {
			flagKey := strings.TrimPrefix(key, params.Prefix)
			flags[flagKey] = value == "true"
		}
	}

	if len(flags) == 0 {
		log.Warnf("No feature flags found for prefix %s in service %s", params.Prefix, params.ClientName)
	}
	log.Infof("All Feature Flags with prefix %s has been loaded for service %s", params.Prefix, params.ClientName)
	return &FeatureFlagManager{
		flags: flags,
	}, nil
}

// IsEnabled returns true if the given feature flag key exists in the manager and is enabled (true).
// Returns an error if the flag key does not exist.
//
// Parameters:
//   - key: the feature flag key to check
//
// Returns:
//   - bool: true if the flag is enabled
//   - error if the flag does not exist in the manager
func (m *FeatureFlagManager) IsEnabled(flag string) (bool, error) {
	enabled, found := m.flags[flag]
	if !found {
		return false, fmt.Errorf(ffErrors.ErrFeatureFlagNotFound, flag)
	}
	return enabled, nil
}

// AreAllEnabled checks if all provided flag keys are present in the manager
// and their values are true. Returns false if any of the flags is false.
// Returns an error if any flag is not defined in the manager.
//
// Parameters:
//   - flags: list of feature flag keys to check (e.g., "LOGIN", "SIGNUP")
//
// Returns:
//   - true if all flags are enabled
//   - false if at least one flag is disabled
//   - error if any flag is missing from the manager

func (m *FeatureFlagManager) AreAllEnabled(flags []string) (bool, error) {
	errMsg := ""
	areEnabled := true
	for _, flag := range flags {
		enabled, err := m.IsEnabled(flag)
		if err != nil {
			errMsg += fmt.Sprintf("%s\n", err.Error())
			areEnabled = false
			continue
		}
		areEnabled = areEnabled && enabled
	}
	if errMsg != "" {
		return false, errors.New(errMsg)
	}
	return areEnabled, nil
}

// AllFlags returns a map of all feature flags and their states.
// It returns an error if no feature flags are found.
func (m *FeatureFlagManager) AllFlags() (map[string]bool, error) {

	if len(m.flags) == 0 {
		return nil, errors.New(ffErrors.ErrNoFeatureFlagsFound)
	}

	flagList := make(map[string]bool, len(m.flags))
	for key, value := range m.flags {
		flagList[key] = value
	}
	return flagList, nil
}
