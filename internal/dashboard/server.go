package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/copilot"
	"github.com/alanmeadows/otto/internal/repo"
	"github.com/alanmeadows/otto/internal/tunnel"
)

// Server is the dashboard HTTP server that serves the web UI,
// REST API, and WebSocket bridge.
type Server struct {
	manager      *copilot.Manager
	bridge       *Bridge
	tunnelMgr    *tunnel.Manager
	cfg          *config.Config
	srv          *http.Server
	shareTokens  map[string]*ShareToken // token -> share info
	tokenMu      sync.RWMutex
	ListPRsFn    func() (any, error)
	GetPRFn      func(id string) (any, error)
	AddPRFn      func(ctx context.Context, url string) (any, error)
	RemovePRFn   func(id string) error
	dashboardKey string // secret key for dashboard access
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
	ownerNick := cfg.Dashboard.OwnerNickname
	if ownerNick == "" {
		ownerNick = "owner"
	}
	bridge := NewBridge(mgr, ownerNick)
	tmgr := tunnel.NewManagerWithConfig(tunnel.Config{
		TunnelID:    cfg.Dashboard.TunnelID,
		Access:      cfg.Dashboard.TunnelAccess,
		AllowOrg:    cfg.Dashboard.TunnelAllowOrg,
	})

	// Generate a dashboard access key.
	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	dashKey := fmt.Sprintf("%x", keyBytes)

	s := &Server{
		manager:      mgr,
		bridge:       bridge,
		tunnelMgr:    tmgr,
		cfg:          cfg,
		dashboardKey: dashKey,
		shareTokens: make(map[string]*ShareToken),
	}

	// Wire tunnel status changes into the bridge.
	tmgr.SetStatusHandler(func(running bool, url string) {
		keyedURL := ""
		if running && url != "" {
			keyedURL = url + "?key=" + dashKey
			slog.Info("tunnel connected, dashboard available remotely", "url", url)
		}
		bridge.BroadcastTunnelStatus(running, url, keyedURL)
	})

	// Wire tunnel and worktree commands from WebSocket to server.
	bridge.onStartTunnel = func() {
		if !tmgr.IsInstalled() {
			slog.Warn("devtunnel CLI is not installed. Install with: curl -sL https://aka.ms/DevTunnelCliInstall | bash")
			bridge.BroadcastTunnelStatus(false, "devtunnel not installed")
			return
		}
		if !tunnel.IsBgtaskInstalled() {
			slog.Warn("bgtask is not installed — install with: go install github.com/philsphicas/bgtask/cmd/bgtask@latest")
			bridge.BroadcastTunnelStatus(false, "bgtask not installed")
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

	bridge.onGetTunnelStatus = func() TunnelStatusPayload {
		running, url := tmgr.Status()
		p := TunnelStatusPayload{Running: running, URL: url}
		if running && url != "" {
			p.KeyedURL = url + "?key=" + dashKey
		}
		return p
	}

	bridge.onRestartServer = nil // wired by caller (server package) to avoid import cycle
	bridge.onUpgradeServer = nil // wired by caller (server package) to avoid import cycle

	return s
}

// SetRestartHandler sets the callback for restarting the server from the dashboard.
func (s *Server) SetRestartHandler(fn func() error) {
	s.bridge.onRestartServer = fn
}

// SetUpgradeHandler sets the callback for upgrading the server from the dashboard.
func (s *Server) SetUpgradeHandler(fn func() error) {
	s.bridge.onUpgradeServer = fn
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
	if s.cfg.Dashboard.AutoStartTunnel {
		if !tunnel.IsBgtaskInstalled() {
			slog.Warn("tunnel skipped: bgtask is not installed — install with: go install github.com/philsphicas/bgtask/cmd/bgtask@latest")
		} else if !s.tunnelMgr.IsInstalled() {
			slog.Warn("tunnel skipped: devtunnel is not installed. Install with: curl -sL https://aka.ms/DevTunnelCliInstall | bash (or on Windows: winget install Microsoft.devtunnel)")
		} else {
			slog.Info("starting tunnel", "tunnel_id", s.cfg.Dashboard.TunnelID, "access", s.cfg.Dashboard.TunnelAccess, "forwarding_port", port)
			go func() {
				if err := s.tunnelMgr.Start(ctx, port); err != nil {
					slog.Warn("tunnel start failed", "error", err)
				}
			}()
		}
	} else {
		slog.Info("tunnel disabled via --no-tunnel")
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
		// bgtask-managed tunnels survive Otto restarts — don't stop them.
	}()

	slog.Info("dashboard server listening", "bind", "http://0.0.0.0:"+strconv.Itoa(port))
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
	mux.HandleFunc("GET /api/sessions/search", s.guardDashboard(s.handleSearchSessions))
	mux.HandleFunc("POST /api/sessions", s.guardDashboard(s.handleCreateSession))
	mux.HandleFunc("DELETE /api/sessions/{name}", s.guardDashboard(s.handleDeleteSession))
	mux.HandleFunc("GET /api/worktrees", s.guardDashboard(s.handleListWorktrees))
	mux.HandleFunc("GET /api/prs", s.guardDashboard(s.handleListPRs))
	mux.HandleFunc("GET /api/prs/{id}", s.guardDashboard(s.handleGetPR))
	mux.HandleFunc("POST /api/prs", s.guardDashboard(s.handleAddPR))
	mux.HandleFunc("DELETE /api/prs/{id}", s.guardDashboard(s.handleRemovePR))
	mux.HandleFunc("GET /api/repos", s.guardDashboard(s.handleListRepos))
	mux.HandleFunc("POST /api/repos", s.guardDashboard(s.handleAddRepo))
	mux.HandleFunc("DELETE /api/repos/{name}", s.guardDashboard(s.handleRemoveRepo))
	mux.HandleFunc("PATCH /api/repos/{name}", s.guardDashboard(s.handleUpdateRepo))
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

func (s *Server) handleSearchSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, []any{})
		return
	}
	results := s.manager.SearchSessions(q)
	if results == nil {
		results = []copilot.SessionSearchResult{}
	}
	writeJSON(w, results)
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

func (s *Server) handleGetPR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.GetPRFn == nil {
		http.Error(w, "not configured", http.StatusNotImplemented)
		return
	}
	pr, err := s.GetPRFn(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, pr)
}

func (s *Server) handleAddPR(w http.ResponseWriter, r *http.Request) {
	if s.AddPRFn == nil {
		http.Error(w, "not configured", http.StatusNotImplemented)
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	result, err := s.AddPRFn(r.Context(), req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slog.Info("PR added via dashboard", "url", req.URL)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, result)
}

func (s *Server) handleRemovePR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.RemovePRFn == nil {
		http.Error(w, "not configured", http.StatusNotImplemented)
		return
	}
	if err := s.RemovePRFn(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	slog.Info("PR removed via dashboard", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.cfg.Repos)
}

func (s *Server) handleAddRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		PrimaryDir string `json:"primary_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.PrimaryDir == "" {
		http.Error(w, "name and primary_dir are required", http.StatusBadRequest)
		return
	}

	repoCfg := config.RepoConfig{
		Name:           req.Name,
		PrimaryDir:     req.PrimaryDir,
		GitStrategy:    config.GitStrategyWorktree,
		BranchTemplate: "otto/{{.Name}}",
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		http.Error(w, "cannot determine config dir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	mgr := repo.NewManager(configDir)
	if err := mgr.Add(s.cfg, repoCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	slog.Info("repository added via dashboard", "name", req.Name, "dir", req.PrimaryDir)

	// Refresh worktrees list for all clients.
	s.bridge.BroadcastWorktrees(s.listWorktrees())

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"status": "added", "name": req.Name})
}

func (s *Server) handleRemoveRepo(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		http.Error(w, "cannot determine config dir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	mgr := repo.NewManager(configDir)
	if err := mgr.Remove(s.cfg, name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	slog.Info("repository removed via dashboard", "name", name)
	s.bridge.BroadcastWorktrees(s.listWorktrees())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateRepo(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	var updates struct {
		PrimaryDir     *string `json:"primary_dir,omitempty"`
		WorktreeDir    *string `json:"worktree_dir,omitempty"`
		GitStrategy    *string `json:"git_strategy,omitempty"`
		BranchTemplate *string `json:"branch_template,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Find and update the repo in config.
	found := false
	for i, r := range s.cfg.Repos {
		if r.Name == name {
			if updates.PrimaryDir != nil {
				s.cfg.Repos[i].PrimaryDir = *updates.PrimaryDir
			}
			if updates.WorktreeDir != nil {
				s.cfg.Repos[i].WorktreeDir = *updates.WorktreeDir
			}
			if updates.GitStrategy != nil {
				s.cfg.Repos[i].GitStrategy = config.GitStrategy(*updates.GitStrategy)
			}
			if updates.BranchTemplate != nil {
				s.cfg.Repos[i].BranchTemplate = *updates.BranchTemplate
			}
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "repository not found", http.StatusNotFound)
		return
	}

	// Persist to user config.
	configDir, err := os.UserConfigDir()
	if err != nil {
		http.Error(w, "cannot determine config dir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	mgr := repo.NewManager(configDir)
	if err := mgr.WriteConfig(s.cfg); err != nil {
		http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("repository updated via dashboard", "name", name)
	s.bridge.BroadcastWorktrees(s.listWorktrees())
	writeJSON(w, map[string]string{"status": "updated", "name": name})
}

func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	running, url := s.tunnelMgr.Status()
	p := TunnelStatusPayload{Running: running, URL: url}
	if running && url != "" {
		p.KeyedURL = url + "?key=" + s.dashboardKey
	}
	writeJSON(w, p)
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

// --- Dashboard access control via URL key ---
//
// The dashboard is protected by a secret key embedded in the URL query string.
// When accessed via a tunnel, users must have ?key=<secret> in the URL or
// a valid otto_key cookie. Without it, they see a passcode prompt.
// Local access (localhost) always passes without a key.

const dashboardKeyCookie = "otto_key"

// isDashboardAccessAllowed checks if the request has the correct dashboard key.
// Local requests are always allowed. Remote requests need ?key= or cookie.
func (s *Server) isDashboardAccessAllowed(r *http.Request) bool {
	// Local access — always allowed.
	host := r.Host
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "[::1]") {
		return true
	}

	// Check ?key= query param.
	if r.URL.Query().Get("key") == s.dashboardKey {
		return true
	}

	// Check cookie (set after first successful key auth).
	if c, err := r.Cookie(dashboardKeyCookie); err == nil && c.Value == s.dashboardKey {
		return true
	}

	return false
}

// guardDashboard wraps a HandlerFunc with access control.
func (s *Server) guardDashboard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isDashboardAccessAllowed(r) {
			s.serveKeyPrompt(w, r)
			return
		}
		// If they came in with ?key=, set cookie so subsequent requests work
		// (WebSocket, API calls, etc.) and redirect to clean URL.
		if key := r.URL.Query().Get("key"); key == s.dashboardKey {
			http.SetCookie(w, &http.Cookie{
				Name:     dashboardKeyCookie,
				Value:    s.dashboardKey,
				Path:     "/",
				MaxAge:   86400 * 30, // 30 days
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
			// Redirect to strip key from URL (so it doesn't leak in browser history).
			clean := *r.URL
			q := clean.Query()
			q.Del("key")
			clean.RawQuery = q.Encode()
			http.Redirect(w, r, clean.String(), http.StatusFound)
			return
		}
		next(w, r)
	}
}

// requireDashboardAccess wraps an http.Handler with access control.
func (s *Server) requireDashboardAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isDashboardAccessAllowed(r) {
			s.serveKeyPrompt(w, r)
			return
		}
		if key := r.URL.Query().Get("key"); key == s.dashboardKey {
			http.SetCookie(w, &http.Cookie{
				Name:     dashboardKeyCookie,
				Value:    s.dashboardKey,
				Path:     "/",
				MaxAge:   86400 * 30,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
			clean := *r.URL
			q := clean.Query()
			q.Del("key")
			clean.RawQuery = q.Encode()
			http.Redirect(w, r, clean.String(), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveKeyPrompt shows a passcode entry page for unauthorized users.
func (s *Server) serveKeyPrompt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	fmt.Fprint(w, `<!DOCTYPE html>
<html><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Otto Dashboard — Access Required</title>
<style>
  body { background: #0d1117; color: #e6edf3; font-family: -apple-system, sans-serif;
         display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
  .box { background: #161b22; border: 1px solid #30363d; border-radius: 12px; padding: 32px;
         max-width: 360px; width: 90%; text-align: center; }
  h2 { margin: 0 0 8px; font-size: 18px; }
  p { font-size: 13px; color: #8b949e; margin: 0 0 20px; }
  input { width: 100%; padding: 10px 14px; background: #21262d; border: 1px solid #30363d;
          border-radius: 8px; color: #e6edf3; font-size: 15px; text-align: center;
          outline: none; box-sizing: border-box; }
  input:focus { border-color: #58a6ff; }
  button { margin-top: 14px; padding: 8px 24px; background: #58a6ff; color: #fff;
           border: none; border-radius: 8px; font-size: 14px; cursor: pointer; }
  button:hover { background: #79c0ff; }
  .err { color: #f85149; font-size: 12px; margin-top: 8px; display: none; }
</style>
</head><body>
<div class="box">
  <h2>🔐 Otto Dashboard</h2>
  <p>Enter the access key to continue</p>
  <input id="key" type="password" placeholder="Passcode" autocomplete="off">
  <div class="err" id="err">Invalid passcode</div>
  <br><button onclick="tryKey()">Enter</button>
</div>
<script>
document.getElementById('key').addEventListener('keydown', function(e) {
  if (e.key === 'Enter') tryKey();
});
function tryKey() {
  var k = document.getElementById('key').value.trim();
  if (!k) return;
  window.location.href = '/?key=' + encodeURIComponent(k);
}
</script>
</body></html>`)
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

	ownerNick := s.cfg.Dashboard.OwnerNickname
	if ownerNick == "" {
		ownerNick = "owner"
	}

	configJSON, _ := json.Marshal(map[string]string{
		"token":          token,
		"session":        st.SessionName,
		"mode":           st.Mode,
		"mode_label":     modeLabel,
		"expires":        remaining.String(),
		"owner_nickname": ownerNick,
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

