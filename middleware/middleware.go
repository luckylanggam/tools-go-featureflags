package middleware

import (
	"encoding/json"
	"net/http"

	featureflags "github.com/luckylanggam/tools-go-featureflags"
)

// PreCheckFeatureFlags is an HTTP middleware that conditionally allows requests
// to proceed based on the status of specific feature flags.
// It checks if all specified feature flags are enabled in the FeatureFlagManager.
//
// Example: if FF_LOGIN=true and FF_SIGNUP=false, and you check for SIGNUP,
//
//	the middleware will block the request with a 403 Forbidden.
//
// Deprecated: use RequireFlags, which evaluates flags for the request's
// attributes. Removed in v2.
func PreCheckFeatureFlags(ffMgr *featureflags.FeatureFlagManager, checkFlags []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			areAllFFEnabled, err := ffMgr.AreAllEnabled(checkFlags)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			if !areAllFFEnabled {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": "Forbidden"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
