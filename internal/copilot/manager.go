package copilot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"gopkg.in/yaml.v3"
)

// PersistedSession describes a session found in ~/.copilot/session-state/.
type PersistedSession struct {
	SessionID    string    `json:"session_id"`
	LastModified time.Time `json:"last_modified"`
	Path         string    `json:"path"`
	Summary      string    `json:"summary,omitempty"`
	CreatedAt    string    `json:"created_at,omitempty"`
	UpdatedAt    string    `json:"updated_at,omitempty"`
}

// Manager manages multiple copilot sessions and broadcasts their events.
type Manager struct {
	client        *sdk.Client
	sessions      map[string]*Session
	activeSession string
	mu            sync.RWMutex
	onEvent       func(SessionEvent)
	started       bool
	serverURL     string // if set, connect to existing headless server
}

// ManagerConfig configures the copilot session Manager.
type ManagerConfig struct {
	// ServerURL connects to an existing copilot headless server (e.g. "localhost:4321").
	// If empty, the SDK spawns its own copilot process via stdio.
	ServerURL string
}

// NewManager creates a Manager. Call Start() before creating sessions.
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

// NewManagerWithConfig creates a Manager with the given configuration.
func NewManagerWithConfig(cfg ManagerConfig) *Manager {
	return &Manager{
		sessions:  make(map[string]*Session),
		serverURL: cfg.ServerURL,
	}
}

// SetEventHandler registers a callback for session events (content deltas,
// tool calls, intents, etc.). Called from any goroutine — the handler
// must be safe for concurrent use.
func (m *Manager) SetEventHandler(fn func(SessionEvent)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = fn
}

// Start initializes the Copilot SDK client.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return nil
	}

	var opts *sdk.ClientOptions
	if m.serverURL != "" {
		// Connect to an existing headless copilot server.
		opts = &sdk.ClientOptions{
			CLIUrl:    m.serverURL,
			AutoStart: sdk.Bool(false),
		}
		slog.Info("connecting to shared copilot server", "url", m.serverURL)
	}

	m.client = sdk.NewClient(opts)
	if err := m.client.Start(ctx); err != nil {
		return fmt.Errorf("starting copilot client: %w", err)
	}

	m.started = true
	slog.Info("copilot session manager started", "shared_server", m.serverURL != "")
	return nil
}

// Stop shuts down all sessions and the SDK client.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, s := range m.sessions {
		slog.Info("closing copilot session", "name", name)
		s.Destroy()
	}
	m.sessions = make(map[string]*Session)
	if m.client != nil {
		_ = m.client.Stop()
	}
	m.started = false
}

// CreateSession creates a new copilot session.
func (m *Manager) CreateSession(ctx context.Context, name, model, workingDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return fmt.Errorf("manager not started")
	}
	if _, exists := m.sessions[name]; exists {
		return fmt.Errorf("session %q already exists", name)
	}

	cfg := &sdk.SessionConfig{
		Model:               model,
		WorkingDirectory:    workingDir,
		Streaming:           true,
		OnPermissionRequest: sdk.PermissionHandler.ApproveAll,
	}

	sdkSession, err := m.client.CreateSession(ctx, cfg)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	s := newSession(name, model, sdkSession, workingDir)
	s.onEvent = m.onEvent
	m.sessions[name] = s

	if m.activeSession == "" {
		m.activeSession = name
	}

	slog.Info("copilot session created", "name", name, "model", model, "session_id", sdkSession.SessionID)
	return nil
}

// ResumeSession resumes a persisted session by its GUID.
func (m *Manager) ResumeSession(ctx context.Context, sessionID, displayName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return fmt.Errorf("manager not started")
	}
	if _, exists := m.sessions[displayName]; exists {
		return fmt.Errorf("session %q already exists", displayName)
	}

	sdkSession, err := m.client.ResumeSession(ctx, sessionID, &sdk.ResumeSessionConfig{
		OnPermissionRequest: sdk.PermissionHandler.ApproveAll,
		Streaming:           true,
	})
	if err != nil {
		return fmt.Errorf("resuming session %s: %w", sessionID, err)
	}

	s := newSession(displayName, "resumed", sdkSession, "")
	s.onEvent = m.onEvent

	// Load existing conversation history from the SDK.
	if events, err := sdkSession.GetMessages(ctx); err == nil {
		for _, evt := range events {
			var role, content string
			switch evt.Type {
			case sdk.UserMessage:
				role = "user"
				if evt.Data.Content != nil {
					content = *evt.Data.Content
				}
			case sdk.AssistantMessage:
				role = "assistant"
				if evt.Data.Content != nil {
					content = *evt.Data.Content
				}
			default:
				continue
			}
			if content != "" {
				s.mu.Lock()
				s.history = append(s.history, ChatMessage{
					Role:      role,
					Content:   content,
					Timestamp: evt.Timestamp,
				})
				s.mu.Unlock()
			}
		}
	}

	m.sessions[displayName] = s

	if m.activeSession == "" {
		m.activeSession = displayName
	}

	slog.Info("copilot session resumed", "name", displayName, "session_id", sessionID, "history_messages", len(s.history))
	return nil
}

// CloseSession closes and removes a session.
func (m *Manager) CloseSession(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[name]
	if !ok {
		return fmt.Errorf("session %q not found", name)
	}

	s.Destroy()
	delete(m.sessions, name)

	if m.activeSession == name {
		m.activeSession = ""
		for n := range m.sessions {
			m.activeSession = n
			break
		}
	}

	slog.Info("copilot session closed", "name", name)
	return nil
}

// GetSession returns a session by name, or nil.
func (m *Manager) GetSession(name string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[name]
}

// ActiveSessionName returns the name of the active session.
func (m *Manager) ActiveSessionName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeSession
}

// SetActiveSession switches the active session.
func (m *Manager) SetActiveSession(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[name]; !ok {
		return fmt.Errorf("session %q not found", name)
	}
	m.activeSession = name
	return nil
}

// ListSessions returns info about all active sessions.
func (m *Manager) ListSessions() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Info())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// SendPrompt sends a prompt to the named session.
func (m *Manager) SendPrompt(ctx context.Context, name, prompt string) error {
	s := m.GetSession(name)
	if s == nil {
		return fmt.Errorf("session %q not found", name)
	}
	return s.SendPrompt(ctx, prompt)
}

// GetHistory returns the chat history for a session.
func (m *Manager) GetHistory(name string) ([]ChatMessage, error) {
	s := m.GetSession(name)
	if s == nil {
		return nil, fmt.Errorf("session %q not found", name)
	}
	return s.History(), nil
}

// ListPersistedSessions scans ~/.copilot/session-state/ for saved session directories.
func (m *Manager) ListPersistedSessions() []PersistedSession {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	stateDir := filepath.Join(home, ".copilot", "session-state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}

	var result []PersistedSession
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ps := PersistedSession{
			SessionID:    e.Name(),
			LastModified: info.ModTime(),
			Path:         filepath.Join(stateDir, e.Name()),
		}
		// Parse workspace.yaml for summary and creation time.
		if meta := readWorkspaceYAML(ps.Path); meta != nil {
			ps.Summary = meta.summary
			ps.CreatedAt = meta.createdAt
			ps.UpdatedAt = meta.updatedAt
		}
		result = append(result, ps)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].LastModified.After(result[j].LastModified)
	})
	return result
}

type workspaceMeta struct {
	summary   string
	createdAt string
	updatedAt string
}

// readWorkspaceYAML reads summary and created_at from a session's workspace.yaml.
func readWorkspaceYAML(sessionDir string) *workspaceMeta {
	data, err := os.ReadFile(filepath.Join(sessionDir, "workspace.yaml"))
	if err != nil {
		return nil
	}
	var raw struct {
		Summary   string `yaml:"summary"`
		CreatedAt string `yaml:"created_at"`
		UpdatedAt string `yaml:"updated_at"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil
	}
	// Truncate long summaries (some are full prompt text).
	summary := raw.Summary
	if len(summary) > 200 {
		summary = summary[:197] + "..."
	}
	// Strip leading markdown headers.
	summary = strings.TrimLeft(summary, "# ")
	// Collapse to first line.
	if idx := strings.IndexByte(summary, '\n'); idx >= 0 {
		summary = summary[:idx]
	}
	return &workspaceMeta{summary: summary, createdAt: raw.CreatedAt, updatedAt: raw.UpdatedAt}
}
