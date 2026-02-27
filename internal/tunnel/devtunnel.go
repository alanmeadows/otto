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

// Manager wraps the Azure DevTunnel CLI for hosting and managing tunnels.
type Manager struct {
	cmd            *exec.Cmd
	mu             sync.Mutex
	url            string
	running        bool
	port           int
	cancel         context.CancelFunc
	onStatusChange func(running bool, url string)
}

// NewManager returns a new tunnel Manager.
func NewManager() *Manager {
	return &Manager{}
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

// Start hosts a devtunnel on the given port. It returns immediately after
// launching the background process; the tunnel URL becomes available
// asynchronously once devtunnel prints it to stdout.
func (m *Manager) Start(ctx context.Context, port int) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.port = port

	cmd := exec.CommandContext(ctx, "devtunnel", "host", "-p", fmt.Sprintf("%d", port), "--allow-anonymous")
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

	slog.Info("devtunnel process started", "port", port, "pid", cmd.Process.Pid)

	// Read stdout in the background looking for the tunnel URL.
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			slog.Debug("devtunnel output", "line", line)

			if strings.Contains(line, ".devtunnels.ms") {
				if match := tunnelURLPattern.FindString(line); match != "" {
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

		// Process has exited or stdout was closed.
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

	// Notify that the tunnel is starting (URL not yet available).
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
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if cmd != nil {
		// Wait for the process to finish; the goroutine from Start will
		// handle resetting state and firing the callback.
		_ = cmd.Wait()
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
