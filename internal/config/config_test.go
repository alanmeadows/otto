package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Models.Primary != "github-copilot/claude-opus-4.6" {
		t.Errorf("expected primary model github-copilot/claude-opus-4.6, got %s", cfg.Models.Primary)
	}
	if cfg.Models.Secondary != "github-copilot/gpt-5.2-codex" {
		t.Errorf("expected secondary model github-copilot/gpt-5.2-codex, got %s", cfg.Models.Secondary)
	}
	if cfg.Models.Tertiary != "github-copilot/gemini-3-pro-preview" {
		t.Errorf("expected tertiary model github-copilot/gemini-3-pro-preview, got %s", cfg.Models.Tertiary)
	}
	if cfg.PR.MaxFixAttempts != 5 {
		t.Errorf("expected max_fix_attempts 5, got %d", cfg.PR.MaxFixAttempts)
	}
	if cfg.Server.Port != 4097 {
		t.Errorf("expected server port 4097, got %d", cfg.Server.Port)
	}
	if cfg.Spec.MaxParallelTasks != 4 {
		t.Errorf("expected max_parallel_tasks 4, got %d", cfg.Spec.MaxParallelTasks)
	}
	if cfg.Server.ParsePollInterval() != 2*time.Minute {
		t.Errorf("expected poll interval 2m, got %v", cfg.Server.ParsePollInterval())
	}
	if cfg.Spec.ParseTaskTimeout() != 30*time.Minute {
		t.Errorf("expected task timeout 30m, got %v", cfg.Spec.ParseTaskTimeout())
	}
}

func TestLoadJSONC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonc")

	content := []byte(`{
  // This is a JSONC comment
  "models": {
    "primary": "test-model"
  },
  "server": {
    "port": 9999
  }
}`)

	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	m, err := loadJSONC(path)
	if err != nil {
		t.Fatalf("loadJSONC failed: %v", err)
	}

	models, ok := m["models"].(map[string]any)
	if !ok {
		t.Fatal("expected models to be a map")
	}
	if models["primary"] != "test-model" {
		t.Errorf("expected primary=test-model, got %v", models["primary"])
	}

	server, ok := m["server"].(map[string]any)
	if !ok {
		t.Fatal("expected server to be a map")
	}
	if server["port"] != float64(9999) {
		t.Errorf("expected port=9999, got %v", server["port"])
	}
}

func TestLoadJSONC_FileNotFound(t *testing.T) {
	_, err := loadJSONC("/nonexistent/path/config.jsonc")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestMergeIntoConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Override nested values
	src := map[string]any{
		"models": map[string]any{
			"primary": "override-model",
		},
		"server": map[string]any{
			"port": json.Number("8080"),
		},
	}

	if err := mergeIntoConfig(&cfg, src); err != nil {
		t.Fatalf("mergeIntoConfig failed: %v", err)
	}

	if cfg.Models.Primary != "override-model" {
		t.Errorf("expected primary=override-model, got %s", cfg.Models.Primary)
	}
	// Secondary should remain untouched
	if cfg.Models.Secondary != "github-copilot/gpt-5.2-codex" {
		t.Errorf("expected secondary to remain github-copilot/gpt-5.2-codex, got %s", cfg.Models.Secondary)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("OTTO_ADO_PAT", "test-pat-123")
	t.Setenv("GITHUB_TOKEN", "gh-token-456")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "secret")
	t.Setenv("OPENCODE_SERVER_USERNAME", "admin")

	applyEnvOverrides(&cfg)

	if cfg.PR.Providers["ado"].PAT != "test-pat-123" {
		t.Errorf("expected ADO PAT=test-pat-123, got %s", cfg.PR.Providers["ado"].PAT)
	}
	if cfg.PR.Providers["github"].Token != "gh-token-456" {
		t.Errorf("expected GitHub token=gh-token-456, got %s", cfg.PR.Providers["github"].Token)
	}
	if cfg.OpenCode.Password != "secret" {
		t.Errorf("expected OpenCode password=secret, got %s", cfg.OpenCode.Password)
	}
	if cfg.OpenCode.Username != "admin" {
		t.Errorf("expected OpenCode username=admin, got %s", cfg.OpenCode.Username)
	}
}

func TestServerConfigParsePollInterval_Invalid(t *testing.T) {
	s := ServerConfig{PollInterval: "not-a-duration"}
	if s.ParsePollInterval() != 2*time.Minute {
		t.Error("expected fallback to 2m for invalid duration")
	}
}

func TestSpecConfigParseTaskTimeout_Invalid(t *testing.T) {
	s := SpecConfig{TaskTimeout: "bad"}
	if s.ParseTaskTimeout() != 30*time.Minute {
		t.Error("expected fallback to 30m for invalid duration")
	}
}

func TestLoadJSONC_MalformedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonc")

	// Truncated JSON
	if err := os.WriteFile(path, []byte(`{"models": {"primary": "test"`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	_, err := loadJSONC(path)
	if err == nil {
		t.Error("expected error for malformed JSONC")
	}
}

func TestMergeDeepPreservesNestedFields(t *testing.T) {
	cfg := DefaultConfig()

	// Override only models.primary — everything else should survive
	src := map[string]any{
		"models": map[string]any{
			"primary": "override-model",
		},
	}
	if err := mergeIntoConfig(&cfg, src); err != nil {
		t.Fatalf("mergeIntoConfig failed: %v", err)
	}

	if cfg.Models.Primary != "override-model" {
		t.Errorf("expected primary=override-model, got %s", cfg.Models.Primary)
	}
	if cfg.Models.Secondary != "github-copilot/gpt-5.2-codex" {
		t.Errorf("expected secondary preserved as github-copilot/gpt-5.2-codex, got %s", cfg.Models.Secondary)
	}
	if cfg.Models.Tertiary != "github-copilot/gemini-3-pro-preview" {
		t.Errorf("expected tertiary preserved as github-copilot/gemini-3-pro-preview, got %s", cfg.Models.Tertiary)
	}
	if cfg.Server.Port != 4097 {
		t.Errorf("expected server.port preserved as 4097, got %d", cfg.Server.Port)
	}
	if cfg.Spec.MaxParallelTasks != 4 {
		t.Errorf("expected spec.max_parallel_tasks preserved as 4, got %d", cfg.Spec.MaxParallelTasks)
	}
	if cfg.OpenCode.URL != "http://localhost:4096" {
		t.Errorf("expected opencode.url preserved, got %s", cfg.OpenCode.URL)
	}
}

func TestDefaultConfigHasUsername(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.OpenCode.Username != "opencode" {
		t.Errorf("expected default username=opencode, got %s", cfg.OpenCode.Username)
	}
}

func TestLoadMergesUserAndOverride(t *testing.T) {
	// Create a temp dir for user config via XDG_CONFIG_HOME.
	userConfigDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", userConfigDir)

	// Prevent repo-level config from interfering (run from a non-git dir).
	t.Setenv("GIT_CEILING_DIRECTORIES", t.TempDir())

	// Clear env vars that would override config fields.
	t.Setenv("OTTO_ADO_PAT", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "")
	t.Setenv("OPENCODE_SERVER_USERNAME", "")

	// Write user-level config.
	ottoDir := filepath.Join(userConfigDir, "otto")
	if err := os.MkdirAll(ottoDir, 0755); err != nil {
		t.Fatalf("failed to create otto config dir: %v", err)
	}
	userConfig := []byte(`{"models":{"primary":"user-model"},"server":{"port":5555}}`)
	if err := os.WriteFile(filepath.Join(ottoDir, "otto.jsonc"), userConfig, 0644); err != nil {
		t.Fatalf("failed to write user config: %v", err)
	}

	// Write override config (simulates repo-level override).
	overrideDir := t.TempDir()
	overridePath := filepath.Join(overrideDir, "override.jsonc")
	overrideConfig := []byte(`{"models":{"primary":"repo-model"}}`)
	if err := os.WriteFile(overridePath, overrideConfig, 0644); err != nil {
		t.Fatalf("failed to write override config: %v", err)
	}

	cfg, err := Load(overridePath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Override wins for models.primary.
	if cfg.Models.Primary != "repo-model" {
		t.Errorf("expected models.primary=repo-model, got %s", cfg.Models.Primary)
	}
	// User value preserved for server.port (override didn't set it).
	if cfg.Server.Port != 5555 {
		t.Errorf("expected server.port=5555, got %d", cfg.Server.Port)
	}
	// Defaults preserved for fields neither user nor override set.
	if cfg.Models.Secondary != "github-copilot/gpt-5.2-codex" {
		t.Errorf("expected models.secondary=github-copilot/gpt-5.2-codex, got %s", cfg.Models.Secondary)
	}
}
