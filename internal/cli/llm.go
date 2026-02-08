package cli

import (
	"fmt"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
)

// newLLMClient creates a ServerManager, ensures the OpenCode server is running,
// and returns the LLMClient along with the ServerManager for lifecycle management.
func newLLMClient(cfg *config.Config) (opencode.LLMClient, *opencode.ServerManager, error) {
	repoRoot := config.RepoRoot()
	if repoRoot == "" {
		return nil, nil, fmt.Errorf("not in a git repository — otto requires a git repo")
	}

	mgr := opencode.NewServerManager(opencode.ServerManagerConfig{
		BaseURL:   cfg.OpenCode.URL,
		AutoStart: cfg.OpenCode.AutoStart,
		Password:  cfg.OpenCode.Password,
		Username:  cfg.OpenCode.Username,
		RepoRoot:  repoRoot,
	})

	if err := mgr.EnsureRunning(rootCmd.Context()); err != nil {
		return nil, nil, fmt.Errorf("starting OpenCode server: %w", err)
	}

	client := mgr.LLM()
	if client == nil {
		return nil, nil, fmt.Errorf("OpenCode server started but no LLM client available")
	}

	return client, mgr, nil
}
