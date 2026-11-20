package featureflags

// FeatureFlags is the v0 flag interface: global flags looked up by their
// upper-case name without prefix. FeatureFlagManager and FromProvider
// implement it. New code should take a provider.Provider and pass the
// caller's attributes instead.
type FeatureFlags interface {
	IsEnabled(string) (bool, error)
	AllFlags() (map[string]bool, error)
	AreAllEnabled(flags []string) (bool, error)
}
