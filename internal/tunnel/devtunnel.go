package tunnel

import (
"bufio"
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
"syscall"
"time"
)

var tunnelURLPattern = regexp.MustCompile(`https://[^\s]*\.devtunnels\.ms[^\s]*`)

const bgtaskTunnelName = "otto-tunnel"

// hasBgtask reports whether the bgtask binary is available in PATH.
func hasBgtask() bool {
	_, err := exec.LookPath("bgtask")
	return err == nil
}

// Config controls tunnel creation and access.
type Config struct {
TunnelID    string   // persistent tunnel name; empty = ephemeral
Access      string   // "anonymous", "tenant", or "" (authenticated, the default)
AllowOrg    string   // GitHub org to allow
AllowEmails []string // specific emails to allow
}

// Manager wraps the Azure DevTunnel CLI for hosting and managing tunnels.
type Manager struct {
cmd            *exec.Cmd
mu             sync.Mutex
url            string
running        bool
bgtaskManaged  bool // true when the tunnel is managed by bgtask
port           int
cancel         context.CancelFunc
onStatusChange func(running bool, url string)
config         Config
}

// IsBgtaskManaged reports whether the tunnel is running as a bgtask and should
// survive Otto restarts.
func (m *Manager) IsBgtaskManaged() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bgtaskManaged
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
func (m *Manager) IsInstalled() bool {
_, err := exec.LookPath("devtunnel")
return err == nil
}

// UpdateConfig replaces the tunnel configuration. Takes effect on next Start().
func (m *Manager) UpdateConfig(cfg Config) {
m.mu.Lock()
defer m.mu.Unlock()
m.config = cfg
}

// IsLoggedIn checks whether the current user is logged in to devtunnel.
func (m *Manager) IsLoggedIn() (bool, error) {
cmd := exec.Command("devtunnel", "user", "show")
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
runCmd("devtunnel", "create", tid)

// Create port (idempotent).
runCmd("devtunnel", "port", "create", tid, "-p", fmt.Sprintf("%d", port))

// Reset access control to start fresh.
runCmd("devtunnel", "access", "reset", tid)

// Apply access rules.
switch m.config.Access {
case "anonymous":
runCmd("devtunnel", "access", "create", tid, "--anonymous")
slog.Info("tunnel access: anonymous")
case "tenant":
runCmd("devtunnel", "access", "create", tid, "--tenant")
slog.Info("tunnel access: Entra tenant")
default:
slog.Info("tunnel access: authenticated (owner only unless org specified)")
}

if m.config.AllowOrg != "" {
runCmd("devtunnel", "access", "create", tid, "--org", m.config.AllowOrg)
slog.Info("tunnel access: granted to GitHub org", "org", m.config.AllowOrg)
}

if len(m.config.AllowEmails) > 0 {
slog.Info("tunnel access: individual emails require org/tenant membership for DevTunnel auth",
"emails", m.config.AllowEmails)
}

return nil
}

// Start hosts a devtunnel on the given port.
// When bgtask is available and a persistent tunnel ID is configured,
// the tunnel runs as an independent bgtask that survives Otto restarts.
func (m *Manager) Start(ctx context.Context, port int) error {
m.mu.Lock()
if m.running {
m.mu.Unlock()
return nil
}

// Use bgtask for persistent tunnels so the tunnel survives Otto restarts.
if hasBgtask() && m.config.TunnelID != "" {
m.mu.Unlock()
return m.startBgtask(port)
}

m.mu.Unlock()
return m.startDirect(ctx, port)
}

// startBgtask starts or attaches to a bgtask-managed tunnel.
func (m *Manager) startBgtask(port int) error {
	// Check if the bgtask tunnel is already running (e.g. Otto restarting).
	if url := m.discoverBgtaskURL(); url != "" {
		m.mu.Lock()
		m.running = true
		m.bgtaskManaged = true
		m.url = url
		m.port = port
		cb := m.onStatusChange
		m.mu.Unlock()

		slog.Info("attached to existing bgtask tunnel", "url", url)
		if cb != nil {
			cb(true, url)
		}
		return nil
	}

	// Ensure persistent tunnel exists with correct access config.
	if err := m.ensurePersistentTunnel(port); err != nil {
		return fmt.Errorf("setting up persistent tunnel: %w", err)
	}

	// Remove stale bgtask state (ignore errors — may not exist).
	exec.Command("bgtask", "rm", bgtaskTunnelName).Run() //nolint:errcheck

	// Start the tunnel via bgtask with auto-restart.
	args := []string{"run", "--name", bgtaskTunnelName, "--restart", "always",
		"--", "devtunnel", "host", m.config.TunnelID}
	cmd := exec.Command("bgtask", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting tunnel via bgtask: %w", err)
	}

	m.mu.Lock()
	m.running = true
	m.bgtaskManaged = true
	m.port = port
	cb := m.onStatusChange
	m.mu.Unlock()

	slog.Info("devtunnel started via bgtask", "tunnel_id", m.config.TunnelID)
	if cb != nil {
		cb(true, "")
	}

	// Poll for the URL in the background.
	go m.pollBgtaskURL()
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
		ChildAlive bool `json:"child_alive"`
	}
	if json.Unmarshal(out, &info) != nil || !info.ChildAlive {
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
		if !m.running || !m.bgtaskManaged {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		if url := m.discoverBgtaskURL(); url != "" {
			m.mu.Lock()
			m.url = url
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
}

// startDirect starts the tunnel as a child process (original behavior).
func (m *Manager) startDirect(ctx context.Context, port int) error {
m.mu.Lock()

ctx, cancel := context.WithCancel(ctx)
m.cancel = cancel
m.port = port

// Auto-create a persistent tunnel if access control is configured
// but no tunnel ID was set — ephemeral tunnels can't have ACLs.
needsPersistent := m.config.Access == "tenant" || m.config.AllowOrg != "" || len(m.config.AllowEmails) > 0
if needsPersistent && m.config.TunnelID == "" {
	m.config.TunnelID = fmt.Sprintf("otto-%s", generateShortID())
	slog.Info("auto-creating persistent tunnel for access control", "tunnel_id", m.config.TunnelID)
}

if m.config.TunnelID != "" {
if err := m.ensurePersistentTunnel(port); err != nil {
cancel()
m.mu.Unlock()
return fmt.Errorf("setting up persistent tunnel: %w", err)
}
}

args := []string{"host"}
if m.config.TunnelID != "" {
args = append(args, m.config.TunnelID)
} else {
args = append(args, "-p", fmt.Sprintf("%d", port))
if m.config.Access == "anonymous" {
args = append(args, "--allow-anonymous")
}
}

cmd := exec.CommandContext(ctx, "devtunnel", args...)
// Bind the child process lifecycle to otto's process:
// - Pdeathsig: kernel sends SIGTERM to child when parent dies (prevents orphans)
// - Setpgid: puts child in its own process group so we can kill the whole tree
cmd.SysProcAttr = &syscall.SysProcAttr{
	Pdeathsig: syscall.SIGTERM,
	Setpgid:   true,
}
stdout, err := cmd.StdoutPipe()
if err != nil {
cancel()
m.mu.Unlock()
return fmt.Errorf("creating stdout pipe: %w", err)
}

if err := cmd.Start(); err != nil {
cancel()
m.mu.Unlock()
return fmt.Errorf("starting devtunnel: %w", err)
}

m.cmd = cmd
m.running = true
m.url = ""
callback := m.onStatusChange
m.mu.Unlock()

slog.Info("devtunnel process started", "port", port, "tunnel_id", m.config.TunnelID, "access", m.config.Access, "pid", cmd.Process.Pid)

go func() {
scanner := bufio.NewScanner(stdout)
for scanner.Scan() {
line := scanner.Text()
slog.Debug("devtunnel output", "line", line)

if strings.Contains(line, ".devtunnels.ms") {
if match := tunnelURLPattern.FindString(line); match != "" {
if strings.Contains(match, "-inspect") {
continue
}
m.mu.Lock()
m.url = match
cb := m.onStatusChange
m.mu.Unlock()

slog.Info("devtunnel URL discovered", "url", match)
if cb != nil {
cb(true, match)
}
}
}
}

_ = cmd.Wait()

m.mu.Lock()
m.running = false
m.url = ""
m.cmd = nil
cb := m.onStatusChange
m.mu.Unlock()

slog.Info("devtunnel process exited", "port", port)
if cb != nil {
cb(false, "")
}
}()

if callback != nil {
callback(true, "")
}

return nil
}

// Stop terminates the running devtunnel process and resets state.
func (m *Manager) Stop() error {
m.mu.Lock()
if !m.running {
m.mu.Unlock()
return nil
}

if m.bgtaskManaged {
m.mu.Unlock()
return m.stopBgtask()
}

cancel := m.cancel
cmd := m.cmd
m.running = false
m.url = ""
m.cmd = nil
cb := m.onStatusChange
m.mu.Unlock()

if cancel != nil {
cancel()
}

// Kill the entire process group to ensure no orphaned children.
if cmd != nil && cmd.Process != nil {
pgid, err := syscall.Getpgid(cmd.Process.Pid)
if err == nil {
	// Negative PID = kill the process group.
	syscall.Kill(-pgid, syscall.SIGTERM)
} else {
	cmd.Process.Kill()
}
// Wait briefly for clean exit.
done := make(chan struct{})
go func() { cmd.Wait(); close(done) }()
select {
case <-done:
default:
	// Force kill after 3 seconds.
	go func() {
		<-done // already closed or will be
	}()
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
}

slog.Info("devtunnel stopped")
if cb != nil {
cb(false, "")
}

return nil
}

func (m *Manager) stopBgtask() error {
	cmd := exec.Command("bgtask", "stop", bgtaskTunnelName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("stopping tunnel bgtask: %s", strings.TrimSpace(string(out)))
	}

	m.mu.Lock()
	m.running = false
	m.bgtaskManaged = false
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

// URL returns the current tunnel URL, or empty string if not running.
func (m *Manager) URL() string {
m.mu.Lock()
defer m.mu.Unlock()
return m.url
}

func runCmd(name string, args ...string) {
cmd := exec.Command(name, args...)
out, err := cmd.CombinedOutput()
if err != nil {
slog.Debug("tunnel setup command", "cmd", append([]string{name}, args...), "output", strings.TrimSpace(string(out)), "error", err)
}
}

func generateShortID() string {
b := make([]byte, 4)
_, _ = rand.Read(b)
return fmt.Sprintf("%x", b)
}
