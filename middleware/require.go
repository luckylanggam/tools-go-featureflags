package middleware

import (
	"encoding/json"
	"net/http"

	featureflags "github.com/luckylanggam/tools-go-featureflags"
)

// RequireFlags is HTTP middleware that lets a request through only when every
// flag in flags is on for it. Flags are evaluated with the request context, so
// the attributes set by Attributes earlier in the chain apply. A disabled flag
// answers 403; an evaluation error, such as an unknown flag, answers 500.
func RequireFlags(ff featureflags.Checker, flags []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, flag := range flags {
				enabled, err := ff.IsEnabled(r.Context(), flag, nil)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if !enabled {
					writeError(w, http.StatusForbidden, "Forbidden")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
