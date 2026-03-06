package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/dashboard"
	"github.com/alanmeadows/otto/internal/llm"
	"github.com/alanmeadows/otto/internal/provider"
	"github.com/alanmeadows/otto/internal/provider/ado"
	ghbackend "github.com/alanmeadows/otto/internal/provider/github"
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
		var llmClient *llm.CopilotClient
		if cfg.Dashboard.CopilotServer != "" {
			llmClient = llm.NewCopilotClientWithServer(cfg.Models.Primary, cfg.Dashboard.CopilotServer)
		} else {
			llmClient = llm.NewCopilotClient(cfg.Models.Primary)
		}
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
			dashSrv.AddPRFn = func(ctx context.Context, prURL string) (any, error) {
				return addPRByURL(ctx, prURL, cfg)
			}
			dashSrv.RemovePRFn = func(id string) error { return RemovePR(id) }
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

// addPRByURL detects the provider from a PR URL, fetches metadata, and saves it.
func addPRByURL(ctx context.Context, prURL string, cfg *config.Config) (any, error) {
	reg := buildRegistryFromConfig(cfg)

	backend, err := reg.Detect(prURL)
	if err != nil {
		return nil, fmt.Errorf("detecting provider: %w", err)
	}

	prInfo, err := backend.GetPR(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("fetching PR: %w", err)
	}

	maxAttempts := cfg.PR.MaxFixAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	pr := &PRDocument{
		ID:             prInfo.ID,
		Title:          prInfo.Title,
		Provider:       backend.Name(),
		Repo:           prInfo.RepoID,
		Branch:         prInfo.SourceBranch,
		Target:         prInfo.TargetBranch,
		Status:         "watching",
		URL:            prInfo.URL,
		Created:        time.Now().UTC().Format(time.RFC3339),
		LastChecked:    time.Now().UTC().Format(time.RFC3339),
		MaxFixAttempts: maxAttempts,
		Body:           fmt.Sprintf("# %s\n\n%s\n", prInfo.Title, prInfo.Description),
	}

	if err := SavePR(pr); err != nil {
		return nil, fmt.Errorf("saving PR: %w", err)
	}

	slog.Info("PR tracked via dashboard", "id", pr.ID, "provider", pr.Provider, "title", pr.Title)
	return pr, nil
}

// buildRegistryFromConfig creates a provider registry from config (server-side).
func buildRegistryFromConfig(cfg *config.Config) *provider.Registry {
	reg := provider.NewRegistry()

	if cfg.PR.Providers != nil {
		if adoCfg, ok := cfg.PR.Providers["ado"]; ok {
			auth := ado.NewAuthProvider(adoCfg.PAT)
			adoBackend := ado.NewBackend(adoCfg.Organization, adoCfg.Project, auth)
			reg.Register(adoBackend)
		}
		if ghCfg, ok := cfg.PR.Providers["github"]; ok {
			ghBack := ghbackend.NewBackend("", "", ghCfg.Token)
			reg.Register(ghBack)
		}
	}

	// Fallback: GitHub via GITHUB_TOKEN or gh CLI.
	if !reg.HasBackendFor("github.com") {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
				token = strings.TrimSpace(string(out))
			}
		}
		ghBack := ghbackend.NewBackend("", "", token)
		reg.Register(ghBack)
	}

	return reg
}

// RemovePR finds a PR by ID and deletes it.
func RemovePR(id string) error {
	pr, err := FindPR(id)
	if err != nil {
		return err
	}
	return DeletePR(pr.Provider, pr.ID)
}

func registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /status", handleStatus)
	mux.HandleFunc("GET /prs", handleListPRs)
	mux.HandleFunc("POST /prs", handleAddPR)
	mux.HandleFunc("DELETE /prs/{id}", handleDeletePR)
	mux.HandleFunc("POST /prs/{id}/fix", handleFixPR)
	mux.HandleFunc("POST /poll", handlePoll)
}
