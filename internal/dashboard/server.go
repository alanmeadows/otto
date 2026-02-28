package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/copilot"
	"github.com/alanmeadows/otto/internal/tunnel"
)

// Server is the dashboard HTTP server that serves the web UI,
// REST API, and WebSocket bridge.
type Server struct {
	manager   *copilot.Manager
	bridge      *Bridge
	tunnelMgr   *tunnel.Manager
	cfg         *config.Config
	srv         *http.Server
	mu          sync.Mutex
	shareTokens map[string]*ShareToken // token -> share info
	tokenMu     sync.RWMutex
}

// ShareToken represents a time-limited share link for a single session.
type ShareToken struct {
	Token       string    `json:"token"`
	SessionName string    `json:"session_name"`
	SessionID   string    `json:"session_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewServer creates a dashboard server with all subsystems.
func NewServer(cfg *config.Config) *Server {
	var mgr *copilot.Manager
	if cfg.Dashboard.CopilotServer != "" {
		mgr = copilot.NewManagerWithConfig(copilot.ManagerConfig{
			ServerURL: cfg.Dashboard.CopilotServer,
		})
	} else {
		mgr = copilot.NewManager()
	}
	bridge := NewBridge(mgr)
	tmgr := tunnel.NewManagerWithConfig(tunnel.Config{
		TunnelID:    cfg.Dashboard.TunnelID,
		Access:      cfg.Dashboard.TunnelAccess,
		AllowOrg:    cfg.Dashboard.TunnelAllowOrg,
		AllowEmails: cfg.Dashboard.TunnelAllowEmails,
	})

	s := &Server{
		manager:     mgr,
		bridge:      bridge,
		tunnelMgr:   tmgr,
		cfg:         cfg,
		shareTokens: make(map[string]*ShareToken),
	}

	// Wire tunnel status changes into the bridge.
	tmgr.SetStatusHandler(func(running bool, url string) {
		bridge.BroadcastTunnelStatus(running, url)
	})

	// Wire tunnel and worktree commands from WebSocket to server.
	bridge.onStartTunnel = func() {
		if !tmgr.IsInstalled() {
			slog.Warn("devtunnel CLI is not installed — install with: curl -sL https://aka.ms/DevTunnelCliInstall | bash")
			bridge.BroadcastTunnelStatus(false, "devtunnel not installed")
			return
		}
		port := cfg.Dashboard.Port
		if port == 0 {
			port = 4098
		}
		slog.Info("starting devtunnel", "port", port)
		go func() {
			if err := tmgr.Start(context.Background(), port); err != nil {
				slog.Error("devtunnel start failed", "error", err)
			}
		}()
	}
	bridge.onStopTunnel = func() {
		go tmgr.Stop()
	}
	bridge.onListWorktrees = func() []WorktreeSummary {
		return s.listWorktrees()
	}
	bridge.onSetTunnelConfig = func(p SetTunnelConfigPayload) {
		// Update the tunnel manager's config.
		tmgr.UpdateConfig(tunnel.Config{
			TunnelID: p.TunnelID,
			Access:   p.Access,
			AllowOrg: p.AllowOrg,
		})
		// Also persist to otto config file.
		cfg.Dashboard.TunnelID = p.TunnelID
		cfg.Dashboard.TunnelAccess = p.Access
		cfg.Dashboard.TunnelAllowOrg = p.AllowOrg
		slog.Info("tunnel config updated", "tunnel_id", p.TunnelID, "access", p.Access, "allow_org", p.AllowOrg)
		// If tunnel is running, restart it with new config.
		if running, _ := tmgr.Status(); running {
			go func() {
				tmgr.Stop()
				port := cfg.Dashboard.Port
				if port == 0 {
					port = 4098
				}
				tmgr.Start(context.Background(), port)
			}()
		}
	}
	bridge.onAddAllowedUser = func(email string) {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			return
		}
		for _, u := range cfg.Dashboard.AllowedUsers {
			if strings.ToLower(u) == email {
				return // already in list
			}
		}
		cfg.Dashboard.AllowedUsers = append(cfg.Dashboard.AllowedUsers, email)
		slog.Info("allowed user added", "email", email)
	}
	bridge.onRemoveAllowedUser = func(email string) {
		email = strings.ToLower(strings.TrimSpace(email))
		filtered := make([]string, 0, len(cfg.Dashboard.AllowedUsers))
		for _, u := range cfg.Dashboard.AllowedUsers {
			if strings.ToLower(u) != email {
				filtered = append(filtered, u)
			}
		}
		cfg.Dashboard.AllowedUsers = filtered
		slog.Info("allowed user removed", "email", email)
	}
	bridge.onGetAllowedUsers = func() AllowedUsersListPayload {
		return AllowedUsersListPayload{
			OwnerEmail: cfg.Dashboard.OwnerEmail,
			Users:      cfg.Dashboard.AllowedUsers,
		}
	}

	return s
}

// Start initializes the copilot manager and starts the HTTP server.
func (s *Server) Start(ctx context.Context, port int) error {
	// Start the copilot SDK client.
	if err := s.manager.Start(ctx); err != nil {
		return fmt.Errorf("starting copilot manager: %w", err)
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	addr := fmt.Sprintf(":%d", port)
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // WebSocket needs no write timeout
		IdleTimeout:       120 * time.Second,
	}

	// Auto-start tunnel if configured.
	if s.cfg.Dashboard.AutoStartTunnel && s.tunnelMgr.IsInstalled() {
		go func() {
			if err := s.tunnelMgr.Start(ctx, port); err != nil {
				slog.Warn("auto-start tunnel failed", "error", err)
			}
		}()
	}

	// Poll ~/.copilot/session-state/ for changes and push updates to clients.
	go s.watchPersistedSessions(ctx)

	// Shutdown on context cancellation.
	go func() {
		<-ctx.Done()
		slog.Info("shutting down dashboard server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.srv.Shutdown(shutdownCtx)
		s.manager.Stop()
		s.tunnelMgr.Stop()
	}()

	slog.Info("starting dashboard server", "addr", addr)
	if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("dashboard server error: %w", err)
	}
	return nil
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Static files (embedded).
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		slog.Error("failed to create sub FS for static files", "error", err)
		return
	}

	// Dashboard routes — protected by tunnel identity check.
	mux.Handle("GET /", s.requireDashboardAccess(http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /api/sessions", s.guardDashboard(s.handleListSessions))
	mux.HandleFunc("POST /api/sessions", s.guardDashboard(s.handleCreateSession))
	mux.HandleFunc("DELETE /api/sessions/{name}", s.guardDashboard(s.handleDeleteSession))
	mux.HandleFunc("GET /api/worktrees", s.guardDashboard(s.handleListWorktrees))
	mux.HandleFunc("GET /api/tunnel/status", s.guardDashboard(s.handleTunnelStatus))
	mux.HandleFunc("POST /api/tunnel/start", s.guardDashboard(s.handleStartTunnel))
	mux.HandleFunc("POST /api/tunnel/stop", s.guardDashboard(s.handleStopTunnel))
	mux.HandleFunc("POST /api/share", s.guardDashboard(s.handleCreateShare))
	mux.HandleFunc("GET /ws", s.guardDashboard(s.handleWS))

	// Shared session view — token-gated, NO dashboard auth required.
	mux.HandleFunc("GET /shared/{token}", s.handleSharedSession)
	mux.HandleFunc("GET /ws/shared/{token}", s.handleSharedWS)
}

// --- WebSocket ---

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	s.bridge.HandleWS(w, r)
}

// --- REST Handlers ---

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions := s.manager.ListSessions()
	writeJSON(w, sessions)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		req.Model = "claude-opus-4.6"
	}
	if err := s.manager.CreateSession(r.Context(), req.Name, req.Model, req.WorkingDir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.bridge.broadcastSessionsList()
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"status": "created", "name": req.Name})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := s.manager.CloseSession(name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.bridge.broadcastSessionsList()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListWorktrees(w http.ResponseWriter, r *http.Request) {
	worktrees := s.listWorktrees()
	writeJSON(w, WorktreesListPayload{Worktrees: worktrees})
}

func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	running, url := s.tunnelMgr.Status()
	writeJSON(w, TunnelStatusPayload{Running: running, URL: url})
}

func (s *Server) handleStartTunnel(w http.ResponseWriter, r *http.Request) {
	if !s.tunnelMgr.IsInstalled() {
		http.Error(w, "devtunnel CLI is not installed", http.StatusPreconditionFailed)
		return
	}
	port := s.cfg.Dashboard.Port
	if port == 0 {
		port = 4098
	}
	go func() {
		if err := s.tunnelMgr.Start(context.Background(), port); err != nil {
			slog.Warn("start tunnel failed", "error", err)
		}
	}()
	writeJSON(w, map[string]string{"status": "starting"})
}

func (s *Server) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	if err := s.tunnelMgr.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "stopped"})
}

// --- Worktree integration ---

func (s *Server) listWorktrees() []WorktreeSummary {
	var worktrees []WorktreeSummary
	for _, repo := range s.cfg.Repos {
		if repo.WorktreeDir == "" {
			// No worktree directory configured — list the primary dir.
			worktrees = append(worktrees, WorktreeSummary{
				Name:     repo.Name,
				Path:     repo.PrimaryDir,
				Branch:   "main",
				RepoName: repo.Name,
			})
			continue
		}
		// List worktrees from the configured directory.
		wts := listDirWorktrees(repo.Name, repo.WorktreeDir)
		worktrees = append(worktrees, wts...)
	}
	return worktrees
}

// listDirWorktrees lists subdirectories under the worktree dir as worktrees.
func listDirWorktrees(repoName, wtDir string) []WorktreeSummary {
	var result []WorktreeSummary
	entries, err := os.ReadDir(wtDir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		result = append(result, WorktreeSummary{
			Name:     e.Name(),
			Path:     wtDir + "/" + e.Name(),
			Branch:   e.Name(),
			RepoName: repoName,
		})
	}
	return result
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// --- Dashboard access control via DevTunnel JWT ---

// extractTunnelEmail extracts the user email from the X-Tunnel-Authorization JWT.
// The JWT is already validated by the DevTunnel infrastructure — we just decode the claims.
// Returns empty string if no tunnel header is present (local access).
func extractTunnelEmail(r *http.Request) string {
	token := r.Header.Get("X-Tunnel-Authorization")
	if token == "" {
		// Also check X-Ms-Client-Principal (another common tunnel header).
		if r.Header.Get("X-Ms-Client-Principal") != "" {
			slog.Info("tunnel: X-Ms-Client-Principal header found but X-Tunnel-Authorization missing")
		}
		return ""
	}
	slog.Info("tunnel auth header present", "length", len(token))
	// Strip "tunnel " prefix if present.
	token = strings.TrimPrefix(token, "tunnel ")
	// JWT is three base64url-encoded parts separated by dots.
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		slog.Debug("tunnel auth: not a JWT", "parts", len(parts))
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		slog.Debug("tunnel auth: base64 decode failed", "error", err)
		return ""
	}
	slog.Debug("tunnel JWT payload", "raw", string(payload))
	var claims struct {
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
		UPN               string `json:"upn"`
		Name              string `json:"name"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	email := claims.Email
	if email == "" {
		email = claims.PreferredUsername
	}
	if email == "" {
		email = claims.UPN
	}
	if email != "" {
		slog.Info("tunnel user identified", "email", strings.ToLower(email), "name", claims.Name)
	}
	return strings.ToLower(email)
}

// isDashboardAccessAllowed checks if the request should be allowed to access the dashboard.
// Local requests (no tunnel header) are always allowed.
// Tunnel requests are checked against owner_email and allowed_users.
func (s *Server) isDashboardAccessAllowed(r *http.Request) bool {
	email := extractTunnelEmail(r)
	if email == "" {
		// No tunnel header — local access, always allowed.
		return true
	}

	// If no allowed_users and no owner_email configured, allow everyone
	// (the tunnel's own ACL is the only gate).
	owner := strings.ToLower(s.cfg.Dashboard.OwnerEmail)
	allowed := s.cfg.Dashboard.AllowedUsers
	if owner == "" && len(allowed) == 0 {
		return true
	}

	// Check owner.
	if owner != "" && email == owner {
		return true
	}

	// Check allowed users list.
	for _, u := range allowed {
		if strings.ToLower(u) == email {
			return true
		}
	}

	slog.Warn("dashboard access denied", "email", email)
	return false
}

// guardDashboard wraps a HandlerFunc with access control.
func (s *Server) guardDashboard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isDashboardAccessAllowed(r) {
			http.Error(w, "Access denied — your account is not authorized for this dashboard", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// requireDashboardAccess wraps an http.Handler with access control.
func (s *Server) requireDashboardAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isDashboardAccessAllowed(r) {
			http.Error(w, "Access denied — your account is not authorized for this dashboard", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// watchPersistedSessions polls ~/.copilot/session-state/ for changes and
// pushes updated persisted session lists to connected dashboard clients.
func (s *Server) watchPersistedSessions(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	var lastHash string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sessions := s.manager.ListPersistedSessions()
			hash := persistedHash(sessions)
			if hash != lastHash {
				lastHash = hash
				s.bridge.broadcastPersistedSessions()
			}
		}
	}
}

// persistedHash computes a quick fingerprint of the persisted session list
// so we only broadcast when something actually changed.
func persistedHash(sessions []copilot.PersistedSession) string {
	var b strings.Builder
	for _, s := range sessions {
		fmt.Fprintf(&b, "%s:%d:%s;", s.SessionID, s.LastModified.Unix(), s.Summary)
	}
	return b.String()
}

// --- Shared session support ---

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionName string `json:"session_name"`
		DurationMin int    `json:"duration_min"` // 0 = default 60 minutes
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.SessionName == "" {
		http.Error(w, "session_name required", http.StatusBadRequest)
		return
	}
	dur := time.Duration(req.DurationMin) * time.Minute
	if dur <= 0 {
		dur = time.Hour
	}

	// Look up session ID from active sessions.
	var sessionID string
	for _, si := range s.manager.ListSessions() {
		if si.Name == req.SessionName {
			sessionID = si.SessionID
			break
		}
	}

	token := generateToken()
	st := &ShareToken{
		Token:       token,
		SessionName: req.SessionName,
		SessionID:   sessionID,
		ExpiresAt:   time.Now().Add(dur),
		CreatedAt:   time.Now(),
	}

	s.tokenMu.Lock()
	s.shareTokens[token] = st
	s.tokenMu.Unlock()

	slog.Info("share token created", "session", req.SessionName, "token", token[:8]+"...", "expires", st.ExpiresAt.Format(time.RFC3339))

	writeJSON(w, map[string]string{
		"token":   token,
		"url":     fmt.Sprintf("/shared/%s", token),
		"expires": st.ExpiresAt.Format(time.RFC3339),
	})
}

func (s *Server) validateShareToken(token string) *ShareToken {
	s.tokenMu.RLock()
	st, ok := s.shareTokens[token]
	s.tokenMu.RUnlock()
	if !ok || time.Now().After(st.ExpiresAt) {
		if ok {
			// Expired — clean up.
			s.tokenMu.Lock()
			delete(s.shareTokens, token)
			s.tokenMu.Unlock()
		}
		return nil
	}
	return st
}

func (s *Server) handleSharedSession(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	st := s.validateShareToken(token)
	if st == nil {
		http.Error(w, "Invalid or expired share link", http.StatusForbidden)
		return
	}

	remaining := time.Until(st.ExpiresAt).Round(time.Minute)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, sharedSessionHTML, st.SessionName, token, st.SessionName, remaining.String())
}

func (s *Server) handleSharedWS(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	st := s.validateShareToken(token)
	if st == nil {
		http.Error(w, "Invalid or expired share link", http.StatusForbidden)
		return
	}
	s.bridge.HandleSharedWS(w, r, st.SessionName)
}

func generateToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

const sharedSessionHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s — Otto Shared Session</title>
<link rel="stylesheet" href="/style.css">
<style>
  body { background: var(--bg-primary); color: var(--text-primary); }
  #shared-app { display: flex; flex-direction: column; height: 100vh; }
  #shared-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 8px 16px; background: var(--bg-secondary);
    border-bottom: 1px solid var(--border); flex-shrink: 0;
  }
  #shared-header h2 { font-size: 14px; margin: 0; }
  #shared-header .meta { font-size: 11px; color: var(--text-muted); }
  #shared-messages { flex: 1; overflow-y: auto; padding: 16px; display: flex; flex-direction: column; gap: 12px; }
  .read-only-notice {
    text-align: center; font-size: 12px; color: var(--text-muted);
    padding: 8px; border-top: 1px solid var(--border); background: var(--bg-secondary);
  }
</style>
</head>
<body>
<div id="shared-app">
  <div id="shared-header">
    <h2>📡 <span id="session-title"></span></h2>
    <span class="meta">Read-only · Expires in <span id="expires"></span></span>
  </div>
  <div id="shared-messages"></div>
  <div class="read-only-notice">This is a read-only shared view</div>
</div>
<script>
const TOKEN = '%s';
const SESSION_NAME = '%s';
const EXPIRES_IN = '%s';
document.getElementById('session-title').textContent = SESSION_NAME;
document.getElementById('expires').textContent = EXPIRES_IN;

const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
const ws = new WebSocket(proto + '//' + location.host + '/ws/shared/' + TOKEN);

ws.onmessage = function(evt) {
  try {
    const msg = JSON.parse(evt.data);
    const container = document.getElementById('shared-messages');
    switch(msg.type) {
      case 'session_history':
        container.innerHTML = '';
        (msg.payload.messages || []).forEach(function(m) {
          if (!m.content || !m.content.trim()) return;
          appendMsg(m.role, m.content);
        });
        break;
      case 'content_delta':
        if (!document.getElementById('streaming')) {
          appendMsg('assistant', '', true);
        }
        var el = document.getElementById('streaming');
        if (el) el.innerHTML += esc(msg.payload.content);
        break;
      case 'turn_end':
        var s = document.getElementById('streaming');
        if (s) s.id = '';
        // Refetch history for non-streamed responses
        ws.send(JSON.stringify({type:'get_history',payload:{session_name:SESSION_NAME}}));
        break;
      case 'tool_started':
        var tool = document.createElement('div');
        tool.className = 'tool-indicator running';
        tool.id = 'tool-' + msg.payload.call_id;
        var detail = '';
        try { var a = JSON.parse(msg.payload.tool_input || '{}'); detail = a.path||a.command||a.pattern||''; } catch(e){}
        if (detail.length > 50) detail = detail.substring(0,47)+'...';
        tool.innerHTML = '⏳ <span class="tool-name">' + esc(msg.payload.tool_name) + (detail ? ': '+esc(detail) : '') + '</span>';
        container.appendChild(tool);
        container.scrollTop = container.scrollHeight;
        break;
      case 'tool_completed':
        var te = document.getElementById('tool-' + msg.payload.call_id);
        if (te) { te.className = 'tool-indicator ' + (msg.payload.success?'completed':'failed'); te.innerHTML = (msg.payload.success?'✅':'❌') + ' ' + te.querySelector('.tool-name').outerHTML; }
        break;
    }
    container.scrollTop = container.scrollHeight;
  } catch(e) {}
};

function appendMsg(role, content, streaming) {
  var div = document.createElement('div');
  div.className = 'message ' + role;
  if (streaming) div.id = 'streaming';
  div.innerHTML = renderMd(content);
  document.getElementById('shared-messages').appendChild(div);
}
function esc(s) { var d=document.createElement('div'); d.textContent=s; return d.innerHTML; }
function renderMd(t) {
  if(!t) return '';
  var h = esc(t);
  h = h.replace(/` + "```" + `(\\w*)\\n([\\s\\S]*?)` + "```" + `/g, function(_,l,c){return '<pre><code>'+c+'</code></pre>';});
  h = h.replace(/` + "`" + `([^` + "`" + `]+)` + "`" + `/g, '<code>$1</code>');
  h = h.replace(/\\*\\*([^*]+)\\*\\*/g, '<strong>$1</strong>');
  h = h.replace(/\\n/g, '<br>');
  return h;
}
</script>
</body>
</html>`
