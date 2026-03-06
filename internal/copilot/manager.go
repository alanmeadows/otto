package copilot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	_ "modernc.org/sqlite"
	"gopkg.in/yaml.v3"

	"github.com/alanmeadows/otto/internal/config"
)

// PersistedSession describes a session found in ~/.copilot/session-state/.
type PersistedSession struct {
	SessionID    string    `json:"session_id"`
	LastModified time.Time `json:"last_modified"`
	Path         string    `json:"path"`
	Summary      string    `json:"summary,omitempty"`
	CreatedAt    string    `json:"created_at,omitempty"`
	UpdatedAt    string    `json:"updated_at,omitempty"`
	IsActive     bool      `json:"is_active,omitempty"` // true if session files were modified recently
	updatedTime  time.Time // parsed UpdatedAt for sorting
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
		slog.Info("copilot SDK connecting to shared server", "server", m.serverURL)
	} else {
		slog.Info("copilot SDK starting embedded process")
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

	// Expand ~ so paths like "~/repos/foo" resolve correctly.
	workingDir = config.ExpandHome(workingDir)

	cfg := &sdk.SessionConfig{
		Model:               model,
		WorkingDirectory:    workingDir,
		Streaming:           true,
		OnPermissionRequest: sdk.PermissionHandler.ApproveAll,
	}

	// Load MCP servers from ~/.copilot/mcp-config.json for CLI parity.
	if mcpServers := loadMCPConfig(); mcpServers != nil {
		cfg.MCPServers = mcpServers
		if m.serverURL != "" {
			slog.Warn("MCP servers configured but using shared copilot server. MCP may not work unless the shared server is recent. Remove dashboard.copilot_server to use embedded mode.", "server", m.serverURL, "mcp_count", len(mcpServers))
		}
		for name := range mcpServers {
			slog.Info("configuring MCP server for session", "server", name)
		}
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
// If the SDK reports "Session not found" (e.g. server-side expiry after
// idle timeout), it automatically resumes the session and retries once.
func (m *Manager) SendPrompt(ctx context.Context, name, prompt string) error {
	s := m.GetSession(name)
	if s == nil {
		return fmt.Errorf("session %q not found", name)
	}
	err := s.SendPrompt(ctx, prompt)
	if err != nil && isSessionExpired(err) {
		slog.Warn("session expired server-side, attempting auto-resume", "name", name, "session_id", s.info.SessionID)
		if resumeErr := m.recoverSession(ctx, name, s); resumeErr != nil {
			slog.Error("auto-resume failed", "name", name, "error", resumeErr)
			return err // return original error
		}
		// Retry with the recovered session.
		s = m.GetSession(name)
		if s == nil {
			return fmt.Errorf("session %q lost during recovery", name)
		}
		return s.SendPrompt(ctx, prompt)
	}
	return err
}

// isSessionExpired checks if an error indicates the SDK session was
// expired/garbage-collected server-side.
func isSessionExpired(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Session not found") ||
		strings.Contains(msg, "session not found")
}

// recoverSession replaces a dead session by resuming it from persisted state.
// The session keeps its display name, history, and position in the session map.
func (m *Manager) recoverSession(ctx context.Context, name string, dead *Session) error {
	sessionID := dead.info.SessionID

	// Resume via the SDK (this creates a fresh server-side session from
	// the persisted state on disk).
	sdkSession, err := m.client.ResumeSession(ctx, sessionID, &sdk.ResumeSessionConfig{
		OnPermissionRequest: sdk.PermissionHandler.ApproveAll,
		Streaming:           true,
	})
	if err != nil {
		return fmt.Errorf("resuming session %s: %w", sessionID, err)
	}

	// Tear down the dead session's SDK resources.
	dead.Destroy()

	// Build a new Session wrapper, preserving the existing history so the
	// UI doesn't lose the conversation.
	recovered := newSession(name, dead.info.Model, sdkSession, dead.info.WorkingDir)
	recovered.onEvent = m.onEvent

	// Carry over the chat history from the dead session.
	recovered.mu.Lock()
	recovered.history = dead.History()
	recovered.mu.Unlock()

	// Swap into the manager map.
	m.mu.Lock()
	m.sessions[name] = recovered
	m.mu.Unlock()

	slog.Info("session auto-recovered", "name", name, "session_id", sessionID, "new_sdk_id", sdkSession.SessionID)
	return nil
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
// Uses session-store.db for metadata when available, falling back to workspace.yaml.
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

	// Load metadata from session-store.db (single query vs N file reads).
	dbMeta := loadSessionMetaFromDB(filepath.Join(home, ".copilot", "session-store.db"))

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

		if meta, ok := dbMeta[e.Name()]; ok {
			// Fast path: metadata from DB.
			ps.Summary = meta.summary
			ps.CreatedAt = meta.createdAt
			ps.UpdatedAt = meta.updatedAt
		} else {
			// Fallback: read workspace.yaml for sessions not in DB.
			if meta := readWorkspaceYAML(ps.Path); meta != nil {
				ps.Summary = meta.summary
				ps.CreatedAt = meta.createdAt
				ps.UpdatedAt = meta.updatedAt
			}
		}

		if t, err := time.Parse(time.RFC3339Nano, ps.UpdatedAt); err == nil {
			ps.updatedTime = t
			if t.After(ps.LastModified) {
				ps.LastModified = t
			} else {
				ps.updatedTime = ps.LastModified
			}
		}

		// Detect actively running sessions by checking the most recent
		// modification across session files. A 5-minute window accounts for
		// long tool calls (builds, tests) where events.jsonl isn't written.
		latestMod := ps.LastModified
		for _, fname := range []string{"events.jsonl", "workspace.yaml"} {
			if fi, err := os.Stat(filepath.Join(ps.Path, fname)); err == nil {
				if fi.ModTime().After(latestMod) {
					latestMod = fi.ModTime()
				}
			}
		}
		// Also check the checkpoints dir.
		if fi, err := os.Stat(filepath.Join(ps.Path, "checkpoints", "index.md")); err == nil {
			if fi.ModTime().After(latestMod) {
				latestMod = fi.ModTime()
			}
		}
		if latestMod.After(ps.LastModified) {
			ps.LastModified = latestMod
			ps.updatedTime = latestMod
		}
		if time.Since(latestMod) < 5*time.Minute {
			ps.IsActive = true
		}

		result = append(result, ps)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].updatedTime.After(result[j].updatedTime)
	})

	// Filter out otto-automated sessions (CI fix, PR comment response) which
	// clutter the saved sessions list meant for human-initiated sessions.
	filtered := result[:0]
	for _, ps := range result {
		if isAutomatedSession(ps.Summary) {
			continue
		}
		filtered = append(filtered, ps)
	}
	return filtered
}

// isAutomatedSession returns true if the summary matches known otto automation patterns.
func isAutomatedSession(summary string) bool {
	prefixes := []string{
		"You are analyzing CI/CD",
		"You are fixing CI/CD",
		"# PR Comment Response Prompt",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(summary, p) {
			return true
		}
	}
	return false
}

type workspaceMeta struct {
	summary   string
	createdAt string
	updatedAt string
}

// loadSessionMetaFromDB reads summary/timestamps for all sessions from
// ~/.copilot/session-store.db in a single query. Returns empty map on error.
func loadSessionMetaFromDB(dbPath string) map[string]*workspaceMeta {
	result := make(map[string]*workspaceMeta)
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return result
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, summary, created_at, updated_at FROM sessions")
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var summary, createdAt, updatedAt sql.NullString
		if err := rows.Scan(&id, &summary, &createdAt, &updatedAt); err != nil {
			continue
		}
		meta := &workspaceMeta{
			summary:   summary.String,
			createdAt: createdAt.String,
			updatedAt: updatedAt.String,
		}
		// Clean up summary the same way as workspace.yaml.
		if len(meta.summary) > 200 {
			meta.summary = meta.summary[:197] + "..."
		}
		meta.summary = strings.TrimLeft(meta.summary, "# ")
		if idx := strings.IndexByte(meta.summary, '\n'); idx >= 0 {
			meta.summary = meta.summary[:idx]
		}
		result[id] = meta
	}
	return result
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

// loadMCPConfig reads MCP server configuration from ~/.copilot/mcp-config.json.
// Returns nil if the file doesn't exist or can't be parsed.
func loadMCPConfig() map[string]sdk.MCPServerConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".copilot", "mcp-config.json"))
	if err != nil {
		return nil
	}
	var raw struct {
		MCPServers map[string]sdk.MCPServerConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Debug("failed to parse mcp-config.json", "error", err)
		return nil
	}
	if len(raw.MCPServers) > 0 {
		slog.Info("loaded MCP servers from ~/.copilot/mcp-config.json", "count", len(raw.MCPServers))
	}
	return raw.MCPServers
}

// SessionSearchResult represents a single search hit across session history.
type SessionSearchResult struct {
	SessionID string `json:"session_id"`
	Summary   string `json:"summary"`
	Snippet   string `json:"snippet"`
	Hits      int    `json:"hits"`
	UpdatedAt string `json:"updated_at"`
}

// ReadSessionEvents parses events.jsonl for a persisted session and returns
// the conversation as chat messages (user + assistant only).
func (m *Manager) ReadSessionEvents(sessionID string) ([]ChatMessage, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	eventsPath := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")
	return parseEventsFile(eventsPath)
}

// WatchSession tails events.jsonl for a persisted session and sends new
// chat messages to the callback as they appear. Blocks until ctx is cancelled.
func (m *Manager) WatchSession(ctx context.Context, sessionID string, onMessage func(ChatMessage)) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	eventsPath := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")

	f, err := os.Open(eventsPath)
	if err != nil {
		return fmt.Errorf("opening events file: %w", err)
	}
	defer f.Close()

	// Seek to end — only watch new events.
	if _, err := f.Seek(0, 2); err != nil {
		return fmt.Errorf("seeking to end: %w", err)
	}

	decoder := json.NewDecoder(f)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for decoder.More() {
				var raw eventRecord
				if err := decoder.Decode(&raw); err != nil {
					break
				}
				if msg, ok := eventToMessage(raw); ok {
					onMessage(msg)
				}
			}
		}
	}
}

// ForkSession creates a new session seeded with the conversation history
// from an existing persisted session's events.jsonl. Returns the new session name.
func (m *Manager) ForkSession(ctx context.Context, sessionID, model string) (string, error) {
	// Read history from the source session.
	history, err := m.ReadSessionEvents(sessionID)
	if err != nil {
		return "", fmt.Errorf("reading source session: %w", err)
	}

	// Get the source session summary for the fork name.
	home, _ := os.UserHomeDir()
	summary := sessionID[:8]
	if meta := readWorkspaceYAML(filepath.Join(home, ".copilot", "session-state", sessionID)); meta != nil {
		if meta.summary != "" {
			summary = meta.summary
			if len(summary) > 30 {
				summary = summary[:27] + "..."
			}
		}
	}

	forkName := "fork: " + summary

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return "", fmt.Errorf("manager not started")
	}
	// Deduplicate name.
	name := forkName
	for i := 2; ; i++ {
		if _, exists := m.sessions[name]; !exists {
			break
		}
		name = fmt.Sprintf("%s (%d)", forkName, i)
	}

	if model == "" {
		model = "claude-opus-4.6"
	}

	cfg := &sdk.SessionConfig{
		Model:               model,
		Streaming:           true,
		OnPermissionRequest: sdk.PermissionHandler.ApproveAll,
	}
	if mcpServers := loadMCPConfig(); mcpServers != nil {
		cfg.MCPServers = mcpServers
	}

	sdkSession, err := m.client.CreateSession(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("creating fork session: %w", err)
	}

	// Build a context summary from the conversation history.
	var contextBuf strings.Builder
	contextBuf.WriteString("You are continuing a conversation that was started in another session. Here is the conversation history for context:\n\n")
	for _, msg := range history {
		if msg.Role == "user" {
			contextBuf.WriteString("**User:** " + msg.Content + "\n\n")
		} else if msg.Role == "assistant" && msg.Content != "" {
			content := msg.Content
			if len(content) > 500 {
				content = content[:497] + "..."
			}
			contextBuf.WriteString("**Assistant:** " + content + "\n\n")
		}
	}
	contextBuf.WriteString("\n---\nThe conversation above is history from the original session. You are now in a forked session. Continue from where it left off. Await the user's next message.")

	// Send context as the first message.
	_, err = sdkSession.Send(ctx, sdk.MessageOptions{
		Prompt: contextBuf.String(),
	})
	if err != nil {
		slog.Warn("failed to seed fork with history", "error", err)
	}

	s := newSession(name, model, sdkSession, "")
	s.onEvent = m.onEvent
	// Pre-populate the local history so the UI shows the source conversation.
	s.mu.Lock()
	s.history = make([]ChatMessage, len(history))
	copy(s.history, history)
	s.mu.Unlock()

	m.sessions[name] = s
	if m.activeSession == "" {
		m.activeSession = name
	}

	slog.Info("session forked", "name", name, "source_session", sessionID, "history_messages", len(history))
	return name, nil
}

// eventRecord is a single line from events.jsonl.
type eventRecord struct {
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	Timestamp string          `json:"timestamp"`
}

// parseEventsFile reads events.jsonl and returns user+assistant messages.
func parseEventsFile(path string) ([]ChatMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []ChatMessage
	decoder := json.NewDecoder(f)
	for decoder.More() {
		var raw eventRecord
		if err := decoder.Decode(&raw); err != nil {
			continue
		}
		if msg, ok := eventToMessage(raw); ok {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

// eventToMessage converts a single event record to a ChatMessage, if applicable.
func eventToMessage(raw eventRecord) (ChatMessage, bool) {
	ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)
	switch raw.Type {
	case "user.message":
		var data struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(raw.Data, &data) == nil && data.Content != "" {
			return ChatMessage{Role: "user", Content: data.Content, Timestamp: ts}, true
		}
	case "assistant.message":
		var data struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(raw.Data, &data) == nil && data.Content != "" {
			return ChatMessage{Role: "assistant", Content: data.Content, Timestamp: ts}, true
		}
	}
	return ChatMessage{}, false
}

// SearchSessions performs FTS5 search across session history in session-store.db.
func (m *Manager) SearchSessions(query string) []SessionSearchResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dbPath := filepath.Join(home, ".copilot", "session-store.db")
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	// Get hit counts per session.
	rows, err := db.Query(`
		SELECT si.session_id, COUNT(*) AS hits,
		       COALESCE(s.summary, ''), COALESCE(s.updated_at, '')
		FROM search_index si
		LEFT JOIN sessions s ON s.id = si.session_id
		WHERE search_index MATCH ?
		GROUP BY si.session_id
		ORDER BY hits DESC
		LIMIT 20
	`, query)
	if err != nil {
		slog.Debug("session search query failed", "error", err)
		return nil
	}
	defer rows.Close()

	type hitRow struct {
		sessionID string
		hits      int
		summary   string
		updatedAt string
	}
	var hits []hitRow
	for rows.Next() {
		var h hitRow
		if err := rows.Scan(&h.sessionID, &h.hits, &h.summary, &h.updatedAt); err != nil {
			continue
		}
		hits = append(hits, h)
	}

	// Fetch one snippet per session (snippet() only works on direct FTS5 match).
	var results []SessionSearchResult
	for _, h := range hits {
		snippet := ""
		row := db.QueryRow(`
			SELECT snippet(search_index, 0, '»', '«', '…', 20)
			FROM search_index
			WHERE search_index MATCH ? AND session_id = ?
			LIMIT 1
		`, query, h.sessionID)
		row.Scan(&snippet)

		summary := strings.TrimLeft(h.summary, "# ")
		if idx := strings.IndexByte(summary, '\n'); idx >= 0 {
			summary = summary[:idx]
		}
		results = append(results, SessionSearchResult{
			SessionID: h.sessionID,
			Summary:   summary,
			Snippet:   snippet,
			Hits:      h.hits,
			UpdatedAt: h.updatedAt,
		})
	}
	return results
}
