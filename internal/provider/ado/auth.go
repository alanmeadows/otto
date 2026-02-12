package ado

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"
)

// adoResourceID is the Azure DevOps application ID used for Entra ID token requests.
const adoResourceID = "499b84ac-1321-427f-aa17-267ca6975798"

// AuthProvider provides authentication tokens for ADO API calls.
// It supports two strategies:
//  1. Entra ID (Azure AD) tokens obtained via the Azure CLI
//  2. Personal Access Token (PAT) as a fallback
//
// Tokens are cached and refreshed automatically when expired.
type AuthProvider struct {
	pat         string // from config or OTTO_ADO_PAT env
	cachedToken string // Entra token
	tokenExpiry time.Time
	mu          sync.Mutex
	// execCommand is a hook for testing — defaults to exec.CommandContext.
	execCommand func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewAuthProvider creates an AuthProvider with the given PAT.
// If pat is empty, the OTTO_ADO_PAT environment variable is checked.
func NewAuthProvider(pat string) *AuthProvider {
	if pat == "" {
		pat = os.Getenv("OTTO_ADO_PAT")
	}
	return &AuthProvider{
		pat:         pat,
		execCommand: exec.CommandContext,
	}
}

// InvalidateToken clears the cached Entra ID token, forcing a fresh
// acquisition on the next GetAuthHeader call. Used when ADO returns
// HTTP 203 (auth redirect), indicating the token has expired.
func (a *AuthProvider) InvalidateToken() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedToken = ""
	a.tokenExpiry = time.Time{}
}

// azTokenResponse is the JSON structure returned by `az account get-access-token`.
type azTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpiresOn   string `json:"expiresOn"`
}

// GetAuthHeader returns an HTTP Authorization header value.
// It first attempts to obtain an Entra ID token via the Azure CLI,
// falling back to PAT-based Basic authentication if that fails.
func (a *AuthProvider) GetAuthHeader(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Return cached Entra token if still valid (with 5-minute buffer).
	if a.cachedToken != "" && time.Now().Before(a.tokenExpiry.Add(-5*time.Minute)) {
		return "Bearer " + a.cachedToken, nil
	}

	// Try Entra ID token via Azure CLI.
	token, expiry, err := a.getEntraToken(ctx)
	if err == nil {
		a.cachedToken = token
		a.tokenExpiry = expiry
		slog.Debug("using Entra ID token for ADO authentication")
		return "Bearer " + token, nil
	}

	slog.Debug("entra ID token acquisition failed, falling back to PAT", "error", err)

	// Fall back to PAT.
	if a.pat != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(":" + a.pat))
		return "Basic " + encoded, nil
	}

	return "", fmt.Errorf("no authentication available: Entra ID failed (%w) and no PAT configured", err)
}

// getEntraToken attempts to obtain a token from the Azure CLI.
func (a *AuthProvider) getEntraToken(ctx context.Context) (string, time.Time, error) {
	cmd := a.execCommand(ctx, "az", "account", "get-access-token",
		"--resource", adoResourceID,
		"--output", "json")

	output, err := cmd.Output()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("az CLI failed: %w", err)
	}

	var resp azTokenResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to parse az CLI output: %w", err)
	}

	if resp.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("empty access token from az CLI")
	}

	// Parse expiry time. The az CLI outputs "2026-02-09 12:00:00.000000" format.
	expiry, err := time.Parse("2006-01-02 15:04:05.000000", resp.ExpiresOn)
	if err != nil {
		// Try alternative format without microseconds.
		expiry, err = time.Parse("2006-01-02 15:04:05", resp.ExpiresOn)
		if err != nil {
			// If we can't parse expiry, use a conservative 30-minute window.
			slog.Warn("could not parse token expiry, using 30-minute default", "expiresOn", resp.ExpiresOn)
			expiry = time.Now().Add(30 * time.Minute)
		}
	}

	return resp.AccessToken, expiry, nil
}
