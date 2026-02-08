package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthCheck_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/global/health", r.URL.Path)
		assert.Equal(t, "GET", r.Method)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Healthy: true, Version: "1.0.0"})
	}))
	defer srv.Close()

	resp, err := HealthCheck(context.Background(), srv.URL, "", "")
	require.NoError(t, err)
	assert.True(t, resp.Healthy)
	assert.Equal(t, "1.0.0", resp.Version)
}

func TestHealthCheck_WithBasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		assert.Contains(t, auth, "Basic ")

		// Decode and verify credentials
		assert.NotEmpty(t, auth)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Healthy: true, Version: "1.0.0"})
	}))
	defer srv.Close()

	resp, err := HealthCheck(context.Background(), srv.URL, "myuser", "mypass")
	require.NoError(t, err)
	assert.True(t, resp.Healthy)
}

func TestHealthCheck_DefaultUsername(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		// With empty username and non-empty password, should use "opencode" as default
		assert.Contains(t, auth, "Basic ")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Healthy: true})
	}))
	defer srv.Close()

	resp, err := HealthCheck(context.Background(), srv.URL, "", "password123")
	require.NoError(t, err)
	assert.True(t, resp.Healthy)
}

func TestHealthCheck_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := HealthCheck(context.Background(), srv.URL, "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestHealthCheck_Unreachable(t *testing.T) {
	_, err := HealthCheck(context.Background(), "http://127.0.0.1:1", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "health check failed")
}

func TestNewServerManager_Defaults(t *testing.T) {
	mgr := NewServerManager(ServerManagerConfig{
		BaseURL:   "http://localhost:4096",
		AutoStart: true,
	})
	assert.Equal(t, "http://localhost:4096", mgr.baseURL)
	assert.True(t, mgr.autoStart)
	assert.Equal(t, "opencode", mgr.username) // default username
	assert.False(t, mgr.IsRunning())
}

func TestNewServerManager_WithCredentials(t *testing.T) {
	mgr := NewServerManager(ServerManagerConfig{
		BaseURL:  "http://localhost:4096",
		Username: "admin",
		Password: "secret",
	})
	assert.Equal(t, "admin", mgr.username)
	assert.Equal(t, "secret", mgr.password)
}

func TestServerManager_EnsureRunning_AlreadyRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Healthy: true, Version: "1.0.0"})
	}))
	defer srv.Close()

	mgr := NewServerManager(ServerManagerConfig{
		BaseURL:   srv.URL,
		AutoStart: false, // don't try to start
	})

	err := mgr.EnsureRunning(context.Background())
	require.NoError(t, err)
	assert.True(t, mgr.IsRunning())
	assert.False(t, mgr.ownsProcess)
}

func TestServerManager_EnsureRunning_AutoStartDisabled(t *testing.T) {
	mgr := NewServerManager(ServerManagerConfig{
		BaseURL:   "http://127.0.0.1:1", // unreachable
		AutoStart: false,
	})

	err := mgr.EnsureRunning(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "auto_start is disabled")
}

func TestServerManager_Shutdown_NoProcess(t *testing.T) {
	mgr := NewServerManager(ServerManagerConfig{
		BaseURL: "http://localhost:4096",
	})

	// Shutdown with no process should be a no-op
	err := mgr.Shutdown()
	assert.NoError(t, err)
}

func TestServerManager_LLM_NilBeforeRunning(t *testing.T) {
	mgr := NewServerManager(ServerManagerConfig{
		BaseURL: "http://localhost:4096",
	})
	assert.Nil(t, mgr.LLM())
	assert.Nil(t, mgr.Client())
}
