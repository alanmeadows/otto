package dashboard

import (
	"context"
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
	bridge    *Bridge
	tunnelMgr *tunnel.Manager
	cfg       *config.Config
	srv       *http.Server
	mu        sync.Mutex
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
		manager:   mgr,
		bridge:    bridge,
		tunnelMgr: tmgr,
		cfg:       cfg,
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
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))

	// REST API.
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("DELETE /api/sessions/{name}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/worktrees", s.handleListWorktrees)
	mux.HandleFunc("GET /api/tunnel/status", s.handleTunnelStatus)
	mux.HandleFunc("POST /api/tunnel/start", s.handleStartTunnel)
	mux.HandleFunc("POST /api/tunnel/stop", s.handleStopTunnel)

	// WebSocket.
	mux.HandleFunc("GET /ws", s.handleWS)
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
