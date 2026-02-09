package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
)

// RunServer starts the HTTP server and blocks until the context is cancelled.
func RunServer(ctx context.Context, port int, cfg *config.Config) error {
	serverStartTime = time.Now()

	mux := http.NewServeMux()
	registerRoutes(mux)

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	var wg sync.WaitGroup

	// Start the monitoring loop in background.
	smCfg := opencode.ServerManagerConfig{
		BaseURL:   cfg.OpenCode.URL,
		AutoStart: cfg.OpenCode.AutoStart,
		Password:  cfg.OpenCode.Password,
		Username:  cfg.OpenCode.Username,
	}
	serverMgr := opencode.NewServerManager(smCfg)
	if err := serverMgr.EnsureRunning(ctx); err != nil {
		slog.Warn("OpenCode server not available — PR monitoring disabled", "error", err)
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer serverMgr.Shutdown()
			if err := RunMonitorLoop(ctx, cfg, serverMgr); err != nil {
				slog.Error("monitoring loop error", "error", err)
			}
		}()
	}

	// Shutdown on context cancellation.
	go func() {
		<-ctx.Done()
		slog.Info("shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("HTTP server shutdown error", "error", err)
		}
	}()

	slog.Info("starting HTTP server", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	// Wait for monitoring loop to finish.
	wg.Wait()
	return nil
}

// serverStartTime records when the server started for uptime calculation.
var serverStartTime time.Time

func registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /status", handleStatus)
	mux.HandleFunc("GET /prs", handleListPRs)
	mux.HandleFunc("POST /prs", handleAddPR)
	mux.HandleFunc("DELETE /prs/{id}", handleDeletePR)
	mux.HandleFunc("POST /prs/{id}/fix", handleFixPR)
}
