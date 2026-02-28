package tunnel

import (
"bufio"
"context"
"fmt"
"log/slog"
"os/exec"
"regexp"
"strings"
"sync"
)

var tunnelURLPattern = regexp.MustCompile(`https://[^\s]*\.devtunnels\.ms[^\s]*`)

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
port           int
cancel         context.CancelFunc
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
func (m *Manager) Start(ctx context.Context, port int) error {
m.mu.Lock()
if m.running {
m.mu.Unlock()
return nil
}

ctx, cancel := context.WithCancel(ctx)
m.cancel = cancel
m.port = port

// Auto-create a persistent tunnel if access control is configured
// but no tunnel ID was set — ephemeral tunnels can't have ACLs.
needsPersistent := m.config.Access == "tenant" || m.config.AllowOrg != "" || len(m.config.AllowEmails) > 0
if needsPersistent && m.config.TunnelID == "" {
	m.config.TunnelID = "otto-dashboard"
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

cancel := m.cancel
cmd := m.cmd
m.running = false
m.url = ""
m.cmd = nil
cb := m.onStatusChange
m.mu.Unlock()

// Cancel context — this sends SIGKILL via exec.CommandContext.
if cancel != nil {
cancel()
}

// Also try explicit kill in case context cancellation isn't enough.
if cmd != nil && cmd.Process != nil {
cmd.Process.Kill()
}

slog.Info("devtunnel stopped")
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
