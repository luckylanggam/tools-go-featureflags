package featureflags

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luckylanggam/tools-go-featureflags/engine"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// Checker evaluates a flag for a caller. provider.Provider and *engine.Engine
// implement it.
type Checker interface {
	IsEnabled(ctx context.Context, flag string, attrs engine.Attributes) (bool, error)
}

type providerAdapter struct {
	c Checker
}

// FromProvider returns c behind the v0 FeatureFlags interface, so code that
// takes FeatureFlags keeps working on a v1 provider while its call sites move
// to the v1 API. v0 names are lower-cased (USER_SIGNUP checks user_signup) and
// evaluated without attributes, so flags with rules on attributes get their
// default. AllFlags is not supported.
func FromProvider(c Checker) FeatureFlags {
	return &providerAdapter{c: c}
}

func (a *providerAdapter) IsEnabled(flag string) (bool, error) {
	return a.c.IsEnabled(context.Background(), strings.ToLower(flag), nil)
}

// AreAllEnabled matches FeatureFlagManager.AreAllEnabled: false with every
// lookup error joined when any flag fails, otherwise whether all are on.
func (a *providerAdapter) AreAllEnabled(flags []string) (bool, error) {
	var errs []error
	allEnabled := true
	for _, flag := range flags {
		enabled, err := a.IsEnabled(flag)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		allEnabled = allEnabled && enabled
	}
	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}
	return allEnabled, nil
}

func (a *providerAdapter) AllFlags() (map[string]bool, error) {
	return nil, fmt.Errorf("AllFlags: %s", ffErrors.ErrNotSupportedOperation)
}
