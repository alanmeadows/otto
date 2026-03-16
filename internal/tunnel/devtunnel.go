package tunnel

import (
"context"
"crypto/rand"
"encoding/json"
"fmt"
"log/slog"
"os"
"os/exec"
"regexp"
"strings"
"sync"
"time"
)

var tunnelURLPattern = regexp.MustCompile(`https://[^\s]*\.devtunnels\.ms[^\s]*`)

const bgtaskTunnelName = "otto-tunnel"

// devtunnelBin is the resolved binary name ("devtunnel" or "devtunnel.exe").
// Resolved lazily by findDevtunnel().
var devtunnelBin string

// findDevtunnel locates the devtunnel binary, checking for both "devtunnel"
// and "devtunnel.exe" (common on WSL where the Windows binary is in PATH).
func findDevtunnel() string {
	if devtunnelBin != "" {
		return devtunnelBin
	}
	for _, name := range []string{"devtunnel", "devtunnel.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			devtunnelBin = p
			return p
		}
	}
	return ""
}

// hasBgtask reports whether the bgtask binary is available in PATH.
func hasBgtask() bool {
	_, err := exec.LookPath("bgtask")
	return err == nil
}

// IsBgtaskInstalled reports whether the bgtask binary is available in PATH.
func IsBgtaskInstalled() bool {
	return hasBgtask()
}

// Config controls tunnel creation and access.
type Config struct {
TunnelID    string   // persistent tunnel name; empty = ephemeral
Access      string   // "anonymous", "tenant", or "" (authenticated, the default)
AllowOrg    string   // GitHub org to allow
}

// Manager wraps the Azure DevTunnel CLI for hosting and managing tunnels.
// Tunnels are always managed via bgtask so they survive Otto restarts.
type Manager struct {
mu             sync.Mutex
url            string
running        bool
port           int
statusHint     string // human-readable reason when tunnel is not active
onStatusChange func(running bool, url string)
config         Config
}

// NewManager returns a new tunnel Manager with default (authenticated) config.
func NewManager() *Manager {
return &Manager{}
}

// NewManagerWithConfig returns a tunnel Manager with the given config.
func NewManagerWithConfig(cfg Config) *Manager {
return &Manager{config: cfg}
}

// SetStatusHandler registers a callback invoked whenever the tunnel status changes.
func (m *Manager) SetStatusHandler(fn func(running bool, url string)) {
m.mu.Lock()
defer m.mu.Unlock()
m.onStatusChange = fn
}

// IsInstalled reports whether the devtunnel binary is available in PATH.
// Accepts both "devtunnel" and "devtunnel.exe" (WSL).
func (m *Manager) IsInstalled() bool {
	return findDevtunnel() != ""
}

// UpdateConfig replaces the tunnel configuration. Takes effect on next Start().
func (m *Manager) UpdateConfig(cfg Config) {
m.mu.Lock()
defer m.mu.Unlock()
m.config = cfg
}

// IsLoggedIn checks whether the current user is logged in to devtunnel.
func (m *Manager) IsLoggedIn() (bool, error) {
cmd := exec.Command(findDevtunnel(), "user", "show")
if err := cmd.Run(); err != nil {
if exitErr, ok := err.(*exec.ExitError); ok {
return false, fmt.Errorf("devtunnel user show exited with code %d", exitErr.ExitCode())
}
return false, err
}
return true, nil
}

// ensurePersistentTunnel creates the tunnel and port if needed,
// and configures access control entries.
func (m *Manager) ensurePersistentTunnel(port int) error {
tid := m.config.TunnelID
if tid == "" {
return nil
}

// Create tunnel (idempotent — fails silently if exists).
// This is the first devtunnel CLI call and will surface auth/connectivity issues.
if err := runCmdErr(findDevtunnel(), "create", tid); err != nil {
	return fmt.Errorf("devtunnel create: %w", err)
}

// Create port (idempotent).
runCmd(findDevtunnel(), "port", "create", tid, "-p", fmt.Sprintf("%d", port))

// Reset access control to start fresh.
runCmd(findDevtunnel(), "access", "reset", tid)

// Apply access rules.
switch m.config.Access {
case "anonymous":
runCmd(findDevtunnel(), "access", "create", tid, "--anonymous")
slog.Info("tunnel access: anonymous")
case "tenant":
runCmd(findDevtunnel(), "access", "create", tid, "--tenant")
slog.Info("tunnel access: Entra tenant")
default:
slog.Info("tunnel access: authenticated (owner only unless org specified)")
}

if m.config.AllowOrg != "" {
runCmd(findDevtunnel(), "access", "create", tid, "--org", m.config.AllowOrg)
slog.Info("tunnel access: granted to GitHub org", "org", m.config.AllowOrg)
}

return nil
}

// Start hosts a devtunnel on the given port via bgtask.
// Both bgtask and devtunnel must be installed; returns an error otherwise.
func (m *Manager) Start(ctx context.Context, port int) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	if !hasBgtask() {
		slog.Warn("tunnel skipped: bgtask is not installed. Install with: go install github.com/philsphicas/bgtask/cmd/bgtask@latest")
		m.mu.Lock()
		m.statusHint = "bgtask is not installed"
		m.mu.Unlock()
		return nil
	}
	if !m.IsInstalled() {
		slog.Warn("tunnel skipped: devtunnel is not installed. Install with: curl -sL https://aka.ms/DevTunnelCliInstall | bash (or on Windows: winget install Microsoft.devtunnel)")
		m.mu.Lock()
		m.statusHint = "devtunnel is not installed"
		m.mu.Unlock()
		return nil
	}
	if m.config.TunnelID == "" {
		slog.Warn("tunnel skipped: no tunnel_id configured")
		m.mu.Lock()
		m.statusHint = "Configure tunnel_id in otto.jsonc"
		m.mu.Unlock()
		return nil
	}

	// Check if the bgtask tunnel is already running (e.g. Otto restarting).
	if url := m.discoverBgtaskURL(); url != "" {
		// Validate the tunnel is actually connected to the relay.
		if m.isTunnelConnected() {
			m.mu.Lock()
			m.running = true
			m.url = url
			m.port = port
			m.statusHint = ""
			cb := m.onStatusChange
			m.mu.Unlock()

			slog.Info("attached to existing bgtask tunnel", "url", url)
			if cb != nil {
				cb(true, url)
			}
			// Start health monitor.
			go m.healthMonitor(ctx, port)
			return nil
		}
		// Process is alive but relay connection is dead — restart it.
		slog.Warn("existing tunnel process has no relay connection, restarting", "tunnel_id", m.config.TunnelID)
		exec.Command("bgtask", "rm", bgtaskTunnelName).Run() //nolint:errcheck
	}

	// Ensure persistent tunnel exists with correct access config.
	if err := m.ensurePersistentTunnel(port); err != nil {
		m.mu.Lock()
		m.statusHint = "devtunnel not responding — try: devtunnel user login"
		m.mu.Unlock()
		return fmt.Errorf("setting up persistent tunnel: %w", err)
	}

	// Remove stale bgtask state (ignore errors — may not exist).
	exec.Command("bgtask", "rm", bgtaskTunnelName).Run() //nolint:errcheck

	// Start the tunnel via bgtask with auto-restart.
	args := []string{"run", "--name", bgtaskTunnelName, "--restart", "always",
		"--", findDevtunnel(), "host", m.config.TunnelID}
	cmd := exec.Command("bgtask", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting tunnel via bgtask: %w", err)
	}

	m.mu.Lock()
	m.running = true
	m.port = port
	cb := m.onStatusChange
	m.mu.Unlock()

	slog.Info("devtunnel started via bgtask", "tunnel_id", m.config.TunnelID)
	if cb != nil {
		cb(true, "")
	}

	// Poll for the URL in the background, then start health monitor.
	go func() {
		m.pollBgtaskURL()
		m.healthMonitor(ctx, port)
	}()
	return nil
}

// discoverBgtaskURL checks if the otto-tunnel bgtask is running and extracts
// the tunnel URL from its logs.
func (m *Manager) discoverBgtaskURL() string {
	cmd := exec.Command("bgtask", "status", "--json", bgtaskTunnelName)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var info struct {
		Status struct {
			State   string `json:"state"`
			Running *struct {
				ChildPID int `json:"child_pid"`
			} `json:"running"`
		} `json:"status"`
	}
	if json.Unmarshal(out, &info) != nil || info.Status.State != "running" ||
		info.Status.Running == nil || info.Status.Running.ChildPID <= 0 {
		return ""
	}

	// Read recent logs to find the URL.
	logCmd := exec.Command("bgtask", "logs", "--tail", "100", bgtaskTunnelName)
	logOut, err := logCmd.Output()
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(logOut), "\n") {
		if match := tunnelURLPattern.FindString(line); match != "" {
			if !strings.Contains(match, "-inspect") {
				return match
			}
		}
	}
	return ""
}

// pollBgtaskURL polls bgtask logs until the tunnel URL appears.
func (m *Manager) pollBgtaskURL() {
	for i := 0; i < 60; i++ {
		time.Sleep(500 * time.Millisecond)

		m.mu.Lock()
		if !m.running {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		if url := m.discoverBgtaskURL(); url != "" {
			m.mu.Lock()
			m.url = url
			m.statusHint = ""
			cb := m.onStatusChange
			m.mu.Unlock()

			slog.Info("bgtask tunnel URL discovered", "url", url)
			if cb != nil {
				cb(true, url)
			}
			return
		}
	}
	slog.Warn("timed out waiting for bgtask tunnel URL")
	m.mu.Lock()
	m.statusHint = "Tunnel started but failed to connect — check devtunnel auth"
	m.running = false
	cb := m.onStatusChange
	m.mu.Unlock()
	if cb != nil {
		cb(false, "")
	}
}

// Stop terminates the bgtask-managed tunnel.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	cmd := exec.Command("bgtask", "stop", bgtaskTunnelName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("stopping tunnel bgtask: %s", strings.TrimSpace(string(out)))
	}

	m.mu.Lock()
	m.running = false
	m.url = ""
	cb := m.onStatusChange
	m.mu.Unlock()

	slog.Info("bgtask tunnel stopped")
	if cb != nil {
		cb(false, "")
	}
	return nil
}

// Status returns whether the tunnel is running and its public URL.
func (m *Manager) Status() (bool, string) {
m.mu.Lock()
defer m.mu.Unlock()
return m.running, m.url
}

// StatusHint returns a human-readable hint about why the tunnel is not active.
func (m *Manager) StatusHint() string {
m.mu.Lock()
defer m.mu.Unlock()
return m.statusHint
}

// URL returns the current tunnel URL, or empty string if not running.
func (m *Manager) URL() string {
m.mu.Lock()
defer m.mu.Unlock()
return m.url
}

func runCmd(name string, args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Debug("tunnel setup command", "cmd", append([]string{name}, args...), "output", strings.TrimSpace(string(out)), "error", err)
	}
}

// runCmdErr is like runCmd but returns the error so callers can detect failures.
func runCmdErr(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Debug("tunnel setup command failed", "cmd", append([]string{name}, args...), "output", strings.TrimSpace(string(out)), "error", err)
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("command timed out (try: devtunnel user login)")
		}
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// isTunnelConnected checks whether the tunnel has an active host connection
// to the Azure relay by running `devtunnel show`.
func (m *Manager) isTunnelConnected() bool {
	tid := m.config.TunnelID
	if tid == "" {
		return false
	}
	cmd := exec.Command(findDevtunnel(), "show", tid)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Host connections") {
			// "Host connections      : 1" means connected; "0" means dead.
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]) != "0"
			}
		}
	}
	return false
}

// healthMonitor periodically checks that the tunnel host process is actually
// connected to the Azure relay. If the connection drops (process alive but
// relay disconnected), it restarts the bgtask tunnel.
func (m *Manager) healthMonitor(ctx context.Context, port int) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			running := m.running
			m.mu.Unlock()
			if !running {
				return
			}

			if m.isTunnelConnected() {
				continue
			}

			slog.Warn("tunnel health check failed: host connection lost, restarting",
				"tunnel_id", m.config.TunnelID)

			// Kill the stale bgtask and restart.
			exec.Command("bgtask", "rm", bgtaskTunnelName).Run() //nolint:errcheck

			args := []string{"run", "--name", bgtaskTunnelName, "--restart", "always",
				"--", findDevtunnel(), "host", m.config.TunnelID}
			cmd := exec.Command("bgtask", args...)
			if err := cmd.Run(); err != nil {
				slog.Error("failed to restart tunnel after health check failure", "error", err)
				continue
			}

			slog.Info("tunnel restarted by health monitor", "tunnel_id", m.config.TunnelID)
			// Re-discover the URL.
			go m.pollBgtaskURL()
		}
	}
}

func generateShortID() string {
b := make([]byte, 4)
_, _ = rand.Read(b)
return fmt.Sprintf("%x", b)
}
