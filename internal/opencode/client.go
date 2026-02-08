package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	opencode "github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/option"
)

// HealthResponse represents the /global/health response.
type HealthResponse struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}

// HealthCheck checks if the OpenCode server is healthy.
func HealthCheck(ctx context.Context, baseURL string, username, password string) (*HealthResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/global/health", nil)
	if err != nil {
		return nil, fmt.Errorf("creating health request: %w", err)
	}

	if password != "" {
		user := username
		if user == "" {
			user = "opencode"
		}
		auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
		req.Header.Set("Authorization", "Basic "+auth)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("decoding health response: %w", err)
	}

	return &health, nil
}

// ServerManager manages the OpenCode server lifecycle and provides an SDK client.
type ServerManager struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	sdkClient   *opencode.Client
	llmClient   LLMClient
	baseURL     string
	ownsProcess bool
	username    string
	password    string
	autoStart   bool
	repoRoot    string
}

// ServerManagerConfig holds configuration for the ServerManager.
type ServerManagerConfig struct {
	BaseURL   string
	AutoStart bool
	Password  string
	Username  string
	RepoRoot  string
}

// NewServerManager creates a new ServerManager.
func NewServerManager(cfg ServerManagerConfig) *ServerManager {
	username := cfg.Username
	if username == "" {
		username = os.Getenv("OPENCODE_SERVER_USERNAME")
	}
	if username == "" {
		username = "opencode"
	}
	password := cfg.Password
	if password == "" {
		password = os.Getenv("OPENCODE_SERVER_PASSWORD")
	}

	return &ServerManager{
		baseURL:   cfg.BaseURL,
		autoStart: cfg.AutoStart,
		password:  password,
		username:  username,
		repoRoot:  cfg.RepoRoot,
	}
}

// EnsureRunning starts the OpenCode server if not already reachable.
func (m *ServerManager) EnsureRunning(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Already have a client?
	if m.sdkClient != nil {
		if _, err := HealthCheck(ctx, m.baseURL, m.username, m.password); err == nil {
			return nil
		}
		// Server died — try to restart if we own it
		m.sdkClient = nil
	}

	// Check if server is already running externally
	if _, err := HealthCheck(ctx, m.baseURL, m.username, m.password); err == nil {
		slog.Info("connected to existing OpenCode server", "url", m.baseURL)
		m.ownsProcess = false
		m.sdkClient = m.createSDKClient()
		m.llmClient = NewSDKLLMClient(m.sdkClient)
		return nil
	}

	// Auto-start disabled?
	if !m.autoStart {
		return fmt.Errorf("OpenCode server is not reachable at %s and auto_start is disabled. Start it manually with: opencode serve", m.baseURL)
	}

	// Start opencode serve
	slog.Info("starting OpenCode server", "url", m.baseURL)
	m.cmd = exec.CommandContext(ctx, "opencode", "serve")
	if m.repoRoot != "" {
		m.cmd.Dir = m.repoRoot
	}
	env := os.Environ()
	if m.password != "" {
		env = append(env, "OPENCODE_SERVER_PASSWORD="+m.password)
	}
	m.cmd.Env = env
	m.cmd.Stdout = os.Stderr // redirect to stderr to not interfere with otto output
	m.cmd.Stderr = os.Stderr

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start opencode serve: %w", err)
	}

	// Wait for healthy with backoff
	if err := m.waitForHealthy(ctx, 30*time.Second); err != nil {
		// Kill the process we just started
		if m.cmd.Process != nil {
			m.cmd.Process.Kill()
		}
		return fmt.Errorf("OpenCode server failed to become healthy: %w", err)
	}

	m.ownsProcess = true
	m.sdkClient = m.createSDKClient()
	m.llmClient = NewSDKLLMClient(m.sdkClient)
	slog.Info("OpenCode server started", "url", m.baseURL)
	return nil
}

func (m *ServerManager) waitForHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	delay := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if _, err := HealthCheck(ctx, m.baseURL, m.username, m.password); err == nil {
			return nil
		}

		time.Sleep(delay)
		delay = min(delay*2, 2*time.Second)
	}
	return fmt.Errorf("timed out waiting for OpenCode server at %s", m.baseURL)
}

func (m *ServerManager) createSDKClient() *opencode.Client {
	opts := []option.RequestOption{
		option.WithBaseURL(m.baseURL),
	}
	if m.password != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(m.username + ":" + m.password))
		opts = append(opts, option.WithHeader("Authorization", "Basic "+auth))
	}
	return opencode.NewClient(opts...)
}

// Shutdown gracefully stops the OpenCode server if otto started it.
func (m *ServerManager) Shutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil && m.ownsProcess {
		slog.Info("shutting down OpenCode server")
		m.cmd.Process.Signal(syscall.SIGTERM)

		done := make(chan error, 1)
		go func() { done <- m.cmd.Wait() }()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			slog.Warn("OpenCode server did not shutdown gracefully, killing")
			m.cmd.Process.Kill()
		}
	}
	return nil
}

// Client returns the underlying SDK client.
func (m *ServerManager) Client() *opencode.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sdkClient
}

// LLM returns the LLMClient interface for session operations.
func (m *ServerManager) LLM() LLMClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.llmClient
}

// IsRunning returns true if the server manager has an active client.
func (m *ServerManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sdkClient != nil
}
