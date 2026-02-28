package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/dashboard"
	"github.com/alanmeadows/otto/internal/llm"
)

// pollTrigger is a channel that signals the monitor loop to run an immediate
// poll cycle. Used by the API server when PRs are added/modified.
var pollTrigger = make(chan struct{}, 1)

// TriggerPoll sends a non-blocking signal to the monitor loop to poll immediately.
func TriggerPoll() {
	select {
	case pollTrigger <- struct{}{}:
		slog.Debug("poll trigger sent")
	default:
		// Already triggered, don't block.
	}
}

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
	llmClient := llm.NewCopilotClient(cfg.Models.Primary)
	if err := llmClient.Start(ctx); err != nil {
		slog.Warn("Copilot LLM client not available — PR monitoring disabled", "error", err)
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer llmClient.Stop()
			if err := RunMonitorLoop(ctx, cfg, llmClient); err != nil {
				slog.Error("monitoring loop error", "error", err)
			}
		}()
	}

	// Start dashboard server if enabled.
	if cfg.Dashboard.Enabled {
		dashPort := cfg.Dashboard.Port
		if dashPort == 0 {
			dashPort = 4098
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			dashSrv := dashboard.NewServer(cfg)
			dashSrv.ListPRsFn = func() (any, error) { return ListPRs() }
			if err := dashSrv.Start(ctx, dashPort); err != nil {
				slog.Error("dashboard server error", "error", err)
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
	mux.HandleFunc("POST /poll", handlePoll)
}
