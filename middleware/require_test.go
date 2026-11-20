package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	featureflags "github.com/luckylanggam/tools-go-featureflags"
	"github.com/luckylanggam/tools-go-featureflags/document"
	"github.com/luckylanggam/tools-go-featureflags/engine"
	"github.com/luckylanggam/tools-go-featureflags/middleware"
)

const siteFlags = `{
  "schema_version": 1,
  "flags": {
    "new_lobby": {
      "owner": "atom", "enabled": true, "default": false,
      "rules": [{"name": "site 7", "if": [{"attr": "site_id", "op": "eq", "value": "7"}], "then": true}]
    },
    "always_on":  {"owner": "atom", "enabled": true,  "default": true},
    "always_off": {"owner": "atom", "enabled": false, "default": true}
  }
}`

const siteCatalog = `{"schema_version": 1, "attributes": {"site_id": {"type": "string"}}}`

func newEngine(t testing.TB) *engine.Engine {
	t.Helper()
	flags, err := document.ParseFlags([]byte(siteFlags))
	require.NoError(t, err)
	catalog, err := document.ParseCatalog([]byte(siteCatalog))
	require.NoError(t, err)
	e, err := engine.New(flags, catalog)
	require.NoError(t, err)
	return e
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func serve(h http.Handler) *http.Response {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	return w.Result()
}

func errorBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body["error"]
}

func TestRequireFlags(t *testing.T) {
	e := newEngine(t)
	siteAttrs := func(site string) func(*http.Request) engine.Attributes {
		return func(*http.Request) engine.Attributes { return engine.Attributes{"site_id": site} }
	}

	tests := []struct {
		name       string
		flags      []string
		attrs      func(*http.Request) engine.Attributes
		wantStatus int
		wantError  string
	}{
		{name: "all flags on", flags: []string{"always_on"}, wantStatus: http.StatusOK},
		{name: "no flags to check", flags: nil, wantStatus: http.StatusOK},
		{name: "kill switch blocks", flags: []string{"always_on", "always_off"}, wantStatus: http.StatusForbidden, wantError: "Forbidden"},
		{name: "unknown flag fails", flags: []string{"no_such_flag"}, wantStatus: http.StatusInternalServerError, wantError: "does not exist"},
		{name: "rule matches request attributes", flags: []string{"new_lobby"}, attrs: siteAttrs("7"), wantStatus: http.StatusOK},
		{name: "rule misses request attributes", flags: []string{"new_lobby"}, attrs: siteAttrs("8"), wantStatus: http.StatusForbidden, wantError: "Forbidden"},
		{name: "no request attributes falls to default", flags: []string{"new_lobby"}, wantStatus: http.StatusForbidden, wantError: "Forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := middleware.RequireFlags(e, tt.flags)(okHandler)
			if tt.attrs != nil {
				h = middleware.Attributes(tt.attrs)(h)
			}

			resp := serve(h)

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantError != "" {
				assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
				assert.Contains(t, errorBody(t, resp), tt.wantError)
			}
		})
	}
}

type failingFlags struct{ calls int }

func (f *failingFlags) IsEnabled(context.Context, string, engine.Attributes) (bool, error) {
	f.calls++
	return false, errors.New("boom")
}

func TestRequireFlags_StopsAtFirstError(t *testing.T) {
	ff := &failingFlags{}

	resp := serve(middleware.RequireFlags(ff, []string{"a", "b"})(okHandler))

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "boom", errorBody(t, resp))
	assert.Equal(t, 1, ff.calls)
}

func TestRequireFlags_Chained(t *testing.T) {
	e := newEngine(t)
	h := middleware.RequireFlags(e, []string{"always_on"})(
		middleware.RequireFlags(e, []string{"always_off"})(okHandler))

	assert.Equal(t, http.StatusForbidden, serve(h).StatusCode)
}

var _ featureflags.Checker = (*failingFlags)(nil)

func BenchmarkRequireFlags(b *testing.B) {
	h := middleware.RequireFlags(newEngine(b), []string{"always_on"})(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}
