package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/luckylanggam/tools-go-featureflags/engine"
	"github.com/luckylanggam/tools-go-featureflags/middleware"
)

func TestAttributes(t *testing.T) {
	var got engine.Attributes
	handler := middleware.Attributes(func(r *http.Request) engine.Attributes {
		return engine.Attributes{"site_id": r.Header.Get("X-Site-Id")}
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = engine.AttributesFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Site-Id", "7")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, engine.Attributes{"site_id": "7"}, got)
}

func TestAttributes_Nil(t *testing.T) {
	var got engine.Attributes
	handler := middleware.Attributes(func(*http.Request) engine.Attributes { return nil })(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = engine.AttributesFromContext(r.Context())
		}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	assert.NotNil(t, got, "the request still gets a scope")
	assert.Empty(t, got)
}
