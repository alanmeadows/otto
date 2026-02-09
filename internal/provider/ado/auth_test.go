package ado

import (
	"context"
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPATAuth(t *testing.T) {
	auth := NewAuthProvider("my-secret-pat")

	// Force Entra to fail by providing a mock that fails.
	auth.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "false") // always fails
	}

	header, err := auth.GetAuthHeader(context.Background())
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(header, "Basic "))

	// Verify the encoded value.
	encoded := strings.TrimPrefix(header, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	assert.Equal(t, ":my-secret-pat", string(decoded))
}

func TestEntraToken(t *testing.T) {
	auth := NewAuthProvider("")

	// Mock az CLI to return a valid token response.
	auth.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo",
			`{"accessToken": "entra-token-123", "expiresOn": "2026-12-31 23:59:59.000000"}`)
	}

	header, err := auth.GetAuthHeader(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer entra-token-123", header)
}

func TestEntraTokenCaching(t *testing.T) {
	callCount := 0
	auth := NewAuthProvider("")

	auth.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		callCount++
		return exec.CommandContext(ctx, "echo",
			`{"accessToken": "cached-token", "expiresOn": "2026-12-31 23:59:59.000000"}`)
	}

	// First call should invoke az CLI.
	header1, err := auth.GetAuthHeader(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer cached-token", header1)
	assert.Equal(t, 1, callCount)

	// Second call should use cache.
	header2, err := auth.GetAuthHeader(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer cached-token", header2)
	assert.Equal(t, 1, callCount) // No additional az CLI call.
}

func TestEntraFallback(t *testing.T) {
	auth := NewAuthProvider("fallback-pat")

	// Mock az CLI to fail.
	auth.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "false")
	}

	header, err := auth.GetAuthHeader(context.Background())
	require.NoError(t, err)

	// Should fall back to PAT.
	assert.True(t, strings.HasPrefix(header, "Basic "))
	encoded := strings.TrimPrefix(header, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	assert.Equal(t, ":fallback-pat", string(decoded))
}

func TestNoAuthAvailable(t *testing.T) {
	auth := NewAuthProvider("")

	// Mock az CLI to fail and no PAT.
	auth.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "false")
	}

	_, err := auth.GetAuthHeader(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no authentication available")
}
