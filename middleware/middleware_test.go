package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	featureflags "github.com/luckylanggam/tools-go-featureflags"
	"github.com/luckylanggam/tools-go-featureflags/middleware"
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

func TestPreCheckFeatureFlags_Success(t *testing.T) {
	flags := map[string]string{
		"MYAPP_FEATURE_TEST": "true",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	cfg := &featureflags.Config{
		Prefix:     "MYAPP_",
		ClientName: "test-service",
	}

	ffMgr, err := featureflags.NewFeatureFlagManager(cfg)
	require.NoError(t, err)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	middleware.PreCheckFeatureFlags(ffMgr, []string{"FEATURE_TEST"})(handler).ServeHTTP(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestPreCheckFeatureFlags_Forbidden(t *testing.T) {
	flags := map[string]string{
		"MYAPP_FEATURE_FORBIDDEN": "false",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	cfg := &featureflags.Config{
		Prefix:     "MYAPP_",
		ClientName: "test-service",
	}

	ffMgr, err := featureflags.NewFeatureFlagManager(cfg)
	require.NoError(t, err)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	middleware.PreCheckFeatureFlags(ffMgr, []string{"FEATURE_FORBIDDEN"})(handler).ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "Forbidden", body["error"])
}

func TestPreCheckFeatureFlags_MissingFlag(t *testing.T) {
	flags := map[string]string{
		"MYAPP_FEATURE_EXISTING": "true",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	cfg := &featureflags.Config{
		Prefix:     "MYAPP_",
		ClientName: "test-service",
	}

	ffMgr, err := featureflags.NewFeatureFlagManager(cfg)
	require.NoError(t, err)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	middleware.PreCheckFeatureFlags(ffMgr, []string{"FEATURE_NON_EXISTENT"})(handler).ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Contains(t, body["error"], "does not exist")
}

func TestPreCheckFeatureFlags_ChainedMiddlewares(t *testing.T) {
	flags := map[string]string{
		"MYAPP_FEATURE_ONE": "true",
		"MYAPP_FEATURE_TWO": "true",
	}
	setupEnv(flags)
	defer teardownEnv(flags)

	cfg := &featureflags.Config{
		Prefix:     "MYAPP_",
		ClientName: "test-service",
	}

	ffMgr, err := featureflags.NewFeatureFlagManager(cfg)
	require.NoError(t, err)

	baseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Final handler"))
	})

	withFirst := middleware.PreCheckFeatureFlags(ffMgr, []string{"FEATURE_ONE"})(baseHandler)
	withSecond := middleware.PreCheckFeatureFlags(ffMgr, []string{"FEATURE_TWO"})(withFirst)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	withSecond.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func BenchmarkPreCheckFeatureFlags(b *testing.B) {
	os.Setenv("MYAPP_BENCH_FEATURE", "true")
	defer os.Unsetenv("MYAPP_BENCH_FEATURE")

	cfg := &featureflags.Config{
		Prefix:     "MYAPP_",
		ClientName: "bench-service",
	}

	ffMgr, err := featureflags.NewFeatureFlagManager(cfg)
	require.NoError(b, err)

	handler := middleware.PreCheckFeatureFlags(ffMgr, []string{"BENCH_FEATURE"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)

	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}
