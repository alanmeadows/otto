package config

import "time"

// Config is the top-level otto configuration.
type Config struct {
	Models        ModelsConfig        `json:"models"`
	OpenCode      OpenCodeConfig      `json:"opencode"`
	PR            PRConfig            `json:"pr"`
	Repos         []RepoConfig        `json:"repos"`
	Server        ServerConfig        `json:"server"`
	Spec          SpecConfig          `json:"spec"`
	Notifications NotificationsConfig `json:"notifications"`
}

// ModelsConfig defines the LLM models used in the multi-model review pipeline.
type ModelsConfig struct {
	Primary   string `json:"primary"`
	Secondary string `json:"secondary"`
	Tertiary  string `json:"tertiary"`
}

// OpenCodeConfig controls the OpenCode server integration.
type OpenCodeConfig struct {
	URL         string `json:"url"`
	AutoStart   bool   `json:"auto_start"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Permissions string `json:"permissions"`
}

// PRConfig holds PR lifecycle management settings.
type PRConfig struct {
	DefaultProvider string                    `json:"default_provider"`
	MaxFixAttempts  int                       `json:"max_fix_attempts"`
	Providers       map[string]ProviderConfig `json:"providers"`
}

// ProviderConfig holds provider-specific PR settings (ADO, GitHub).
// Uses a unified struct with omitempty rather than separate ADOConfig/GitHubConfig types,
// since the providers map is keyed by provider name ("ado", "github") and a single struct
// simplifies the JSON schema and deep merge logic.
type ProviderConfig struct {
	// ADO fields
	Organization   string `json:"organization,omitempty"`
	Project        string `json:"project,omitempty"`
	PAT            string `json:"pat,omitempty"`
	AutoComplete   bool   `json:"auto_complete,omitempty"`
	MerlinBot      bool   `json:"merlinbot,omitempty"`
	CreateWorkItem bool   `json:"create_work_item,omitempty"`

	// GitHub fields
	Token string `json:"token,omitempty"`
}

// GitStrategy defines how otto manages branches/worktrees for a repo.
type GitStrategy string

const (
	GitStrategyWorktree GitStrategy = "worktree"
	GitStrategyBranch   GitStrategy = "branch"
	GitStrategyHandsOff GitStrategy = "hands-off"
)

// RepoConfig defines a tracked repository.
type RepoConfig struct {
	Name           string      `json:"name"`
	PrimaryDir     string      `json:"primary_dir"`
	WorktreeDir    string      `json:"worktree_dir,omitempty"`
	GitStrategy    GitStrategy `json:"git_strategy"`
	BranchTemplate string      `json:"branch_template"`
	BranchPatterns []string    `json:"branch_patterns"`
}

// ServerConfig holds daemon settings.
type ServerConfig struct {
	PollInterval string `json:"poll_interval"`
	Port         int    `json:"port"`
	LogDir       string `json:"log_dir"`
}

// ParsePollInterval returns the poll interval as a time.Duration.
func (s ServerConfig) ParsePollInterval() time.Duration {
	d, err := time.ParseDuration(s.PollInterval)
	if err != nil {
		return 2 * time.Minute
	}
	return d
}

// SpecConfig holds specification system settings.
type SpecConfig struct {
	MaxParallelTasks int    `json:"max_parallel_tasks"`
	TaskTimeout      string `json:"task_timeout"`
	MaxTaskRetries   int    `json:"max_task_retries"`
	TaskBriefing     *bool  `json:"task_briefing"`
}

// IsTaskBriefingEnabled returns whether the task briefing step is enabled.
// Defaults to true when not explicitly set.
func (s SpecConfig) IsTaskBriefingEnabled() bool {
	if s.TaskBriefing == nil {
		return true
	}
	return *s.TaskBriefing
}

// ParseTaskTimeout returns the task timeout as a time.Duration.
func (s SpecConfig) ParseTaskTimeout() time.Duration {
	d, err := time.ParseDuration(s.TaskTimeout)
	if err != nil {
		return 30 * time.Minute
	}
	return d
}

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool {
	return &b
}

// NotificationsConfig holds notification settings.
type NotificationsConfig struct {
	TeamsWebhookURL string   `json:"teams_webhook_url"`
	Events          []string `json:"events"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Models: ModelsConfig{
			Primary:   "anthropic/claude-sonnet-4-20250514",
			Secondary: "openai/o3",
			Tertiary:  "google/gemini-2.5-pro",
		},
		OpenCode: OpenCodeConfig{
			URL:         "http://localhost:4096",
			AutoStart:   true,
			Username:    "opencode",
			Permissions: "allow",
		},
		PR: PRConfig{
			DefaultProvider: "ado",
			MaxFixAttempts:  5,
			Providers:       make(map[string]ProviderConfig),
		},
		Server: ServerConfig{
			PollInterval: "2m",
			Port:         4097,
			LogDir:       "~/.local/share/otto/logs",
		},
		Spec: SpecConfig{
			MaxParallelTasks: 4,
			TaskTimeout:      "30m",
			MaxTaskRetries:   15,
			TaskBriefing:     boolPtr(true),
		},
	}
}
