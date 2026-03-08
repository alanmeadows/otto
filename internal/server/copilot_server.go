package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	copilotBgtaskName = "otto-copilot"
	copilotDefaultPort = 4321
)

// findCopilotBinary locates the copilot CLI binary.
func findCopilotBinary() string {
	for _, name := range []string{"copilot", "copilot.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// ensureCopilotServer starts a headless copilot server via bgtask if one
// isn't already running. Returns the server URL (e.g. "localhost:4321").
func ensureCopilotServer() (string, error) {
	port := copilotDefaultPort

	// Check if the bgtask copilot server is already running.
	if url := discoverCopilotServer(port); url != "" {
		slog.Info("attached to existing otto-managed copilot server", "url", url)
		return url, nil
	}

	// Find the copilot binary.
	copilotBin := findCopilotBinary()
	if copilotBin == "" {
		return "", fmt.Errorf("copilot CLI not found in PATH. Install with: npm install -g @github/copilot")
	}

	// If something else is listening on our port, warn and pick a different one.
	if isPortOpen(port) {
		slog.Warn("port is in use by an unmanaged process, starting on alternate port", "blocked_port", port)
		port = port + 100 // e.g. 4421
	}

	// Remove stale bgtask state.
	exec.Command("bgtask", "rm", copilotBgtaskName).Run() //nolint:errcheck

	// Start via bgtask with auto-restart.
	args := []string{"run", "--name", copilotBgtaskName, "--restart", "always",
		"--", copilotBin, "--headless", "--no-auto-update",
		"--port", strconv.Itoa(port), "--log-level", "info"}
	cmd := exec.Command("bgtask", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("starting copilot server via bgtask: %s: %w", strings.TrimSpace(string(out)), err)
	}

	slog.Info("copilot server started via bgtask", "port", port, "binary", copilotBin)

	// Wait for it to be ready.
	url := fmt.Sprintf("localhost:%d", port)
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		if isPortOpen(port) {
			slog.Info("copilot server ready", "url", url)
			return url, nil
		}
	}

	return "", fmt.Errorf("copilot server started but not responding on port %d after 15s", port)
}

// discoverCopilotServer checks if the otto-copilot bgtask is running.
func discoverCopilotServer(port int) string {
	cmd := exec.Command("bgtask", "status", "--json", copilotBgtaskName)
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
	// Verify it's actually listening.
	if !isPortOpen(port) {
		return ""
	}
	return fmt.Sprintf("localhost:%d", port)
}

// isPortOpen checks if a TCP port is accepting connections.
func isPortOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
