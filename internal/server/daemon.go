package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/store"
)

// PIDFilePath returns the path to the daemon PID file.
func PIDFilePath() string {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			slog.Error("cannot determine home directory; set $HOME or $XDG_DATA_HOME", "error", err)
			os.Exit(1)
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataDir, "otto", "ottod.pid")
}

// LogFilePath returns the path to the daemon log file.
func LogFilePath() string {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataDir, "otto", "logs", "ottod.log")
}

// StartDaemon forks the current process as a daemon.
// If foreground is true, runs the server inline without forking.
func StartDaemon(port int, logDir string, foreground bool) error {
	// Use file lock to prevent concurrent starts.
	lockPath := PIDFilePath() + ".lock"

	if foreground {
		return store.WithLock(lockPath, 5*time.Second, func() error {
			if running, pid, _, _ := DaemonStatus(); running {
				return fmt.Errorf("daemon already running (PID %d)", pid)
			}
			return runForeground(port, logDir)
		})
	}

	// For daemon mode, hold the lock only for the fork, then release
	// before printing URLs / polling for tunnel (which blocks).
	var result *forkResult
	err := store.WithLock(lockPath, 5*time.Second, func() error {
		if running, pid, _, _ := DaemonStatus(); running {
			return fmt.Errorf("daemon already running (PID %d)", pid)
		}
		var forkErr error
		result, forkErr = forkDaemon(port, logDir)
		return forkErr
	})
	if err != nil {
		return err
	}

	// Print URLs and poll for tunnel — lock is released so child can start.
	result.printStatus()
	return nil
}

// forkResult holds the information needed to print startup status
// after the file lock is released.
type forkResult struct {
	pid              int
	logFile          string
	port             int
	dashPort         int
	dashboardEnabled bool
	tunnelEnabled    bool
}

// expandHome replaces a leading "~/" in a path with the user's home directory.
// If the path does not start with "~/" or the home directory cannot be determined,
// the path is returned unchanged.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") && path != "~" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func forkDaemon(port int, logDir string) (*forkResult, error) {
	// Expand ~ in log directory path.
	logDir = expandHome(logDir)

	// Create log directory.
	if logDir == "" {
		dataDir := os.Getenv("XDG_DATA_HOME")
		if dataDir == "" {
			home, _ := os.UserHomeDir()
			dataDir = filepath.Join(home, ".local", "share")
		}
		logDir = filepath.Join(dataDir, "otto", "logs")
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("creating log directory: %w", err)
	}

	logFile := filepath.Join(logDir, "ottod.log")

	// Determine dashboard port for URL output.
	dashboardEnabled := os.Getenv("OTTO_DASHBOARD") == "1"
	tunnelEnabled := os.Getenv("OTTO_TUNNEL") == "1"
	dashPort := 4098
	if v := os.Getenv("OTTO_DASHBOARD_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			dashPort = p
		}
	}

	// Fork: re-exec with --foreground, propagating port.
	// Dashboard/tunnel state is propagated via OTTO_DASHBOARD/OTTO_TUNNEL env vars
	// which are inherited by the child and read in runForeground.
	forkArgs := []string{"server", "start", "--foreground", "--port", strconv.Itoa(port)}

	if v := os.Getenv("OTTO_DASHBOARD_PORT"); v != "" {
		forkArgs = append(forkArgs, "--dashboard-port", v)
	}
	if os.Getenv("OTTO_VERBOSE") == "1" {
		forkArgs = append(forkArgs, "-v")
	}

	cmd := exec.Command(os.Args[0], forkArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Redirect output to log file.
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, fmt.Errorf("starting daemon: %w", err)
	}

	pid := cmd.Process.Pid

	// Release without waiting — do NOT call cmd.Wait() in the parent.
	// The child process writes its own PID file in runForeground.
	cmd.Process.Release()
	f.Close()

	return &forkResult{
		pid:              pid,
		logFile:          logFile,
		port:             port,
		dashPort:         dashPort,
		dashboardEnabled: dashboardEnabled,
		tunnelEnabled:    tunnelEnabled,
	}, nil
}

func (r *forkResult) printStatus() {
	fmt.Printf("daemon started (PID: %d)\n", r.pid)
	fmt.Printf("log file: %s\n", r.logFile)
	fmt.Printf("server: http://localhost:%d\n", r.port)
	if r.dashboardEnabled {
		fmt.Printf("dashboard: http://localhost:%d\n", r.dashPort)
	}

	// If tunnel is enabled, poll the dashboard API for the tunnel URL.
	if r.dashboardEnabled && r.tunnelEnabled {
		if tunnelURL := pollTunnelURL(r.dashPort); tunnelURL != "" {
			fmt.Printf("tunnel: %s\n", tunnelURL)
		} else {
			fmt.Printf("tunnel: starting (check 'otto server logs' if it doesn't come up)\n")
		}
	}
}

// PollTunnelURLQuick does a single quick check for the tunnel URL.
// Returns empty string if the tunnel isn't running or the dashboard
// doesn't respond within 2 seconds.
func PollTunnelURLQuick(dashPort int) string {
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://localhost:%d/api/tunnel/status", dashPort)

	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var status struct {
		Running  bool   `json:"running"`
		URL      string `json:"url"`
		KeyedURL string `json:"keyed_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return ""
	}
	if status.KeyedURL != "" {
		return status.KeyedURL
	}
	return status.URL
}

// pollTunnelURL polls the dashboard's tunnel status API until the tunnel URL
// is available or the timeout expires. Localhost requests bypass dashboard auth.
func pollTunnelURL(dashPort int) string {
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://localhost:%d/api/tunnel/status", dashPort)

	// Give the forked server time to start listening.
	time.Sleep(1 * time.Second)
	for i := 0; i < 28; i++ { // 1s initial + 28 × 500ms = 15s max
		resp, err := client.Get(url)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var status struct {
			Running  bool   `json:"running"`
			URL      string `json:"url"`
			KeyedURL string `json:"keyed_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			time.Sleep(500 * time.Millisecond)
			continue
		}
		resp.Body.Close()
		if status.KeyedURL != "" {
			return status.KeyedURL
		}
		if status.URL != "" {
			return status.URL
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}

func runForeground(port int, logDir string) error {
	// Load config.
	cfg, err := config.Load()
	if err != nil {
		slog.Warn("failed to load config, using defaults", "error", err)
		defaultCfg := config.DefaultConfig()
		cfg = &defaultCfg
	}

	// Apply dashboard flags from environment (set by CLI before fork).
	if os.Getenv("OTTO_DASHBOARD") == "1" {
		cfg.Dashboard.Enabled = true
	}
	if v := os.Getenv("OTTO_DASHBOARD_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Dashboard.Port = p
		}
	}
	if os.Getenv("OTTO_TUNNEL") == "1" {
		cfg.Dashboard.AutoStartTunnel = true
	}
	if os.Getenv("OTTO_NO_PR_MONITORING") == "1" {
		cfg.Server.NoPRMonitoring = true
	}

	// Write PID file for foreground mode too (for status checks).
	if err := writePIDFile(os.Getpid()); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer removePIDFile()

	// Signal handling for graceful shutdown.
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGTERM, syscall.SIGINT,
	)
	defer stop()

	// Run the HTTP server.
	return RunServer(ctx, port, cfg)
}

// StopDaemon sends SIGTERM to the running daemon and waits for exit.
func StopDaemon() error {
	running, pid, _, err := DaemonStatus()
	if err != nil {
		return err
	}
	if !running {
		return fmt.Errorf("daemon is not running")
	}

	// Send SIGTERM.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process: %w", err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Check if process is already gone.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			removePIDFile()
			return nil
		}
		return fmt.Errorf("sending SIGTERM: %w", err)
	}

	// Wait for exit with timeout.
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			// Force kill.
			_ = proc.Signal(syscall.SIGKILL)
			removePIDFile()
			return fmt.Errorf("daemon did not stop gracefully, sent SIGKILL")
		case <-ticker.C:
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				// Process is gone.
				removePIDFile()
				return nil
			}
		}
	}
}

// DaemonStatus checks whether the daemon is running.
// Returns: running bool, pid int, uptime duration, error.
func DaemonStatus() (bool, int, time.Duration, error) {
	pidFile := PIDFilePath()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, 0, nil
		}
		return false, 0, 0, fmt.Errorf("reading PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0, 0, fmt.Errorf("invalid PID file: %w", err)
	}

	// Check if process is alive.
	proc, err := os.FindProcess(pid)
	if err != nil {
		removePIDFile()
		return false, 0, 0, nil
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		// Process is not running — stale PID file.
		removePIDFile()
		return false, 0, 0, nil
	}

	// Calculate uptime from PID file modification time.
	info, err := os.Stat(pidFile)
	if err != nil {
		return true, pid, 0, nil
	}
	uptime := time.Since(info.ModTime())

	return true, pid, uptime, nil
}

func writePIDFile(pid int) error {
	pidFile := PIDFilePath()
	dir := filepath.Dir(pidFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating PID directory: %w", err)
	}

	// Atomic write: temp file + rename.
	tmp := pidFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, pidFile)
}

func removePIDFile() {
	_ = os.Remove(PIDFilePath())
}

// InstallSystemdService writes a systemd user unit file and enables the service.
func InstallSystemdService() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}

	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		return fmt.Errorf("creating systemd directory: %w", err)
	}

	// Find the otto binary path.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}

	unit := fmt.Sprintf(`[Unit]
Description=Otto Daemon
After=network.target

[Service]
Type=simple
ExecStart=%s server start --foreground
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30
Environment=HOME=%s

[Install]
WantedBy=default.target
`, execPath, home)

	unitPath := filepath.Join(unitDir, "otto.service")
	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("writing unit file: %w", err)
	}

	// Reload systemd and enable.
	reloadCmd := exec.Command("systemctl", "--user", "daemon-reload")
	if out, err := reloadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload: %s: %w", string(out), err)
	}

	enableCmd := exec.Command("systemctl", "--user", "enable", "otto")
	if out, err := enableCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("enabling service: %s: %w", string(out), err)
	}

	fmt.Printf("installed otto.service at %s\n", unitPath)
	fmt.Println("service enabled — start with: systemctl --user start otto")
	return nil
}

// RestartDaemon stops the running daemon and starts it again via a bgtask
// so the restart survives the current process exiting.
func RestartDaemon() error {
	if _, err := exec.LookPath("bgtask"); err != nil {
		return fmt.Errorf("bgtask is required — install with: go install github.com/philsphicas/bgtask/cmd/bgtask@latest")
	}

	ottoBin, err := exec.LookPath("otto")
	if err != nil {
		return fmt.Errorf("cannot find otto binary in PATH: %w", err)
	}

	script := fmt.Sprintf(`#!/bin/bash
set -e
echo "$(date): stopping otto server..."
%s server stop 2>/dev/null || true
sleep 1
echo "$(date): starting otto server..."
%s server start
echo "$(date): restart complete"
`, ottoBin, ottoBin)

	return runLifecycleScript("otto-restart", script)
}

// UpgradeDaemon stops the daemon, installs the latest version, and restarts.
// channel is "release" (go install @latest) or "main" (build from sourceDir).
func UpgradeDaemon(channel, sourceDir string) error {
	if _, err := exec.LookPath("bgtask"); err != nil {
		return fmt.Errorf("bgtask is required — install with: go install github.com/philsphicas/bgtask/cmd/bgtask@latest")
	}

	ottoBin, err := exec.LookPath("otto")
	if err != nil {
		return fmt.Errorf("cannot find otto binary in PATH: %w", err)
	}

	var installStep string
	switch channel {
	case "main":
		if sourceDir == "" {
			return fmt.Errorf("server.source_dir must be set in otto config for channel \"main\"")
		}
		sourceDir = expandHome(sourceDir)
		installStep = fmt.Sprintf(`echo "$(date): building from source at %s..."
cd %s
git pull --ff-only
make install`, sourceDir, sourceDir)
	default: // "release" or empty
		installStep = `echo "$(date): installing latest release via go install..."
GOBIN=~/.local/bin go install github.com/alanmeadows/otto/cmd/otto@latest`
	}

	script := fmt.Sprintf(`#!/bin/bash
set -e
echo "$(date): stopping otto server..."
%s server stop 2>/dev/null || true
sleep 1

%s

echo "$(date): starting otto server..."
%s server start
echo "$(date): upgrade complete"
`, ottoBin, installStep, ottoBin)

	return runLifecycleScript("otto-upgrade", script)
}

// runLifecycleScript writes a shell script to a temp file and runs it via
// bgtask so it survives the current process exiting.
func runLifecycleScript(name, script string) error {
	// Write script to temp file.
	f, err := os.CreateTemp("", name+"-*.sh")
	if err != nil {
		return fmt.Errorf("creating temp script: %w", err)
	}
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		os.Remove(f.Name())
		return fmt.Errorf("writing script: %w", err)
	}
	f.Close()

	// Remove stale bgtask state.
	exec.Command("bgtask", "rm", name).Run() //nolint:errcheck

	slog.Info("running lifecycle script via bgtask", "name", name, "script", f.Name())

	cmd := exec.Command("bgtask", "run", "--name", name, "--", "bash", f.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(f.Name())
		return fmt.Errorf("starting %s via bgtask: %w", name, err)
	}

	return nil
}
