package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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

	// Start the monitoring loop in background (unless disabled).
	if cfg.Server.NoPRMonitoring {
		slog.Info("PR monitoring disabled via --no-pr-monitoring")
	} else {
		slog.Info("starting PR monitoring", "model", cfg.Models.Primary, "interval", cfg.PR.Providers)
		llmClient := llm.NewCopilotClient(cfg.Models.Primary)
		if err := llmClient.Start(ctx); err != nil {
			slog.Warn("Copilot LLM client not available, PR monitoring disabled", "error", err)
		} else {
			interval := cfg.Server.ParsePollInterval()
			slog.Info("PR monitoring started", "model", cfg.Models.Primary, "poll_interval", interval)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer llmClient.Stop()
				if err := RunMonitorLoop(ctx, cfg, llmClient); err != nil {
					slog.Error("monitoring loop error", "error", err)
				}
			}()
		}
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
			dashSrv.GetPRFn = func(id string) (any, error) { return FindPRDetail(id) }
			dashSrv.SetRestartHandler(func() error { return RestartDaemon() })
			dashSrv.SetUpgradeHandler(func() error {
				return UpgradeDaemon(cfg.Server.UpgradeChannel, cfg.Server.SourceDir)
			})
			if err := dashSrv.Start(ctx, dashPort); err != nil {
				slog.Error("dashboard server error", "error", err)
			}
		}()
	} else {
		slog.Info("dashboard disabled via --no-dashboard")
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

	slog.Info("PR API server listening", "bind", "http://0.0.0.0:"+strconv.Itoa(port))
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
