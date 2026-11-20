package middleware

import (
	"net/http"

	"github.com/luckylanggam/tools-go-featureflags/engine"
)

// Attributes is HTTP middleware that puts the attributes fn returns for a
// request in its context once, so every flag evaluation further down the
// chain sees them (see engine.WithAttributes). fn may return nil; the
// request still gets its own resolver cache.
func Attributes(fn func(*http.Request) engine.Attributes) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := engine.WithAttributes(r.Context(), fn(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
