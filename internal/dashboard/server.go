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
	manager     *copilot.Manager
	bridge      *Bridge
	tunnelMgr   *tunnel.Manager
	cfg         *config.Config
	srv         *http.Server
	shareTokens map[string]*ShareToken // token -> share info
	tokenMu     sync.RWMutex
	ListPRsFn   func() (any, error) // injected by server package to avoid import cycle
}

// ShareToken represents a time-limited share link for a single session.
type ShareToken struct {
	Token       string    `json:"token"`
	SessionName string    `json:"session_name"`
	SessionID   string    `json:"session_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
	Mode        string    `json:"mode"` // "readonly" or "readwrite"
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
	mux.HandleFunc("GET /api/prs", s.guardDashboard(s.handleListPRs))
	mux.HandleFunc("GET /api/repos", s.guardDashboard(s.handleListRepos))
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

func (s *Server) handleListPRs(w http.ResponseWriter, r *http.Request) {
	if s.ListPRsFn == nil {
		writeJSON(w, []any{})
		return
	}
	prs, err := s.ListPRsFn()
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, prs)
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.cfg.Repos)
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
	// Log all headers from tunnel requests for debugging.
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Original-Host") != "" {
		var hdrs []string
		for name := range r.Header {
			hdrs = append(hdrs, name)
		}
		slog.Info("tunnel request headers", "names", hdrs, "remote", r.RemoteAddr)
	}

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
		fmt.Fprintf(&b, "%s:%s:%s;", s.SessionID, s.UpdatedAt, s.Summary)
	}
	return b.String()
}

// --- Shared session support ---

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionName string `json:"session_name"`
		DurationMin int    `json:"duration_min"` // 0 = default 60 minutes
		Mode        string `json:"mode"`         // "readonly" or "readwrite"; default "readonly"
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
	mode := req.Mode
	if mode != "readwrite" {
		mode = "readonly"
	}

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
		Mode:        mode,
	}

	s.tokenMu.Lock()
	s.shareTokens[token] = st
	s.tokenMu.Unlock()

	slog.Info("share token created", "session", req.SessionName, "mode", mode, "token", token[:8]+"...", "expires", st.ExpiresAt.Format(time.RFC3339))

	writeJSON(w, map[string]string{
		"token":   token,
		"url":     fmt.Sprintf("/shared/%s", token),
		"expires": st.ExpiresAt.Format(time.RFC3339),
		"mode":    mode,
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
	modeLabel := "🔒 Read-only"
	if st.Mode == "readwrite" {
		modeLabel = "✏️ Read-write"
	}

	configJSON, _ := json.Marshal(map[string]string{
		"token":      token,
		"session":    st.SessionName,
		"mode":       st.Mode,
		"mode_label": modeLabel,
		"expires":    remaining.String(),
	})

	tmpl, err := staticFiles.ReadFile("static/shared.html")
	if err != nil {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}

	html := strings.Replace(string(tmpl), "{{TITLE}}", st.SessionName, 1)
	html = strings.Replace(html, "{{CONFIG_JSON}}", string(configJSON), 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func (s *Server) handleSharedWS(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	st := s.validateShareToken(token)
	if st == nil {
		http.Error(w, "Invalid or expired share link", http.StatusForbidden)
		return
	}
	s.bridge.HandleSharedWS(w, r, st.SessionName, st.Mode)
}

func generateToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

