package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	return store.WithLock(lockPath, 5*time.Second, func() error {
		// Check if already running.
		if running, pid, _, _ := DaemonStatus(); running {
			return fmt.Errorf("daemon already running (PID %d)", pid)
		}

		if foreground {
			return runForeground(port, logDir)
		}

		return forkDaemon(port, logDir)
	})
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

func forkDaemon(port int, logDir string) error {
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
		return fmt.Errorf("creating log directory: %w", err)
	}

	logFile := filepath.Join(logDir, "ottod.log")

	// Fork: re-exec with --foreground, propagating port and dashboard flags.
	forkArgs := []string{"server", "start", "--foreground", "--port", strconv.Itoa(port)}

	// Check environment for dashboard flags (set by CLI layer).
	if os.Getenv("OTTO_DASHBOARD") == "1" {
		forkArgs = append(forkArgs, "--dashboard")
	}
	if os.Getenv("OTTO_TUNNEL") == "1" {
		forkArgs = append(forkArgs, "--tunnel")
	}
	if v := os.Getenv("OTTO_DASHBOARD_PORT"); v != "" {
		forkArgs = append(forkArgs, "--dashboard-port", v)
	}

	cmd := exec.Command(os.Args[0], forkArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Redirect output to log file.
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		f.Close()
		return fmt.Errorf("starting daemon: %w", err)
	}

	pid := cmd.Process.Pid

	// Release without waiting — do NOT call cmd.Wait() in the parent.
	// The child process writes its own PID file in runForeground.
	cmd.Process.Release()
	f.Close()

	fmt.Printf("daemon started (PID: %d)\n", pid)
	fmt.Printf("log file: %s\n", logFile)
	return nil
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
