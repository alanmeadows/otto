package config

import "time"

// Config is the top-level otto configuration.
type Config struct {
	Models        ModelsConfig        `json:"models"`
	PR            PRConfig            `json:"pr"`
	Repos         []RepoConfig        `json:"repos"`
	Server        ServerConfig        `json:"server"`
	Dashboard     DashboardConfig     `json:"dashboard"`
	Notifications NotificationsConfig `json:"notifications"`
}

// ModelsConfig defines the LLM models used by the Copilot SDK.
type ModelsConfig struct {
	Primary   string `json:"primary"`
	Secondary string `json:"secondary"`
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
	// WorkItemAreaPath is the ADO area path for created work items (e.g., "One\\Compute\\AzLocal").
	WorkItemAreaPath string `json:"work_item_area_path,omitempty"`

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
		return 10 * time.Minute
	}
	return d
}

// DashboardConfig holds settings for the Copilot session dashboard.
type DashboardConfig struct {
	Port              int      `json:"port"`
	Enabled           bool     `json:"enabled"`
	AutoStartTunnel   bool     `json:"auto_start_tunnel"`
	CopilotServer     string   `json:"copilot_server"`       // e.g. "localhost:4321" to connect to shared headless server
	TunnelID          string   `json:"tunnel_id"`             // persistent tunnel name (e.g. "otto-dash"); empty = ephemeral
	TunnelAccess      string   `json:"tunnel_access"`         // "anonymous", "tenant", or "authenticated" (default)
	TunnelAllowOrg    string   `json:"tunnel_allow_org"`      // GitHub org to grant access (e.g. "my-org")
	TunnelAllowEmails []string `json:"tunnel_allow_emails"`   // specific email addresses to grant access
	OwnerEmail        string   `json:"owner_email"`           // dashboard owner email (auto-detected from tunnel JWT if empty)
	AllowedUsers      []string `json:"allowed_users"`         // emails allowed full dashboard access (e.g. ["alice@microsoft.com"])
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
			Primary:   "claude-opus-4.6",
			Secondary: "gpt-5.2-codex",
		},
		PR: PRConfig{
			DefaultProvider: "ado",
			MaxFixAttempts:  5,
			Providers:       make(map[string]ProviderConfig),
		},
		Server: ServerConfig{
			PollInterval: "10m",
			Port:         4097,
			LogDir:       "~/.local/share/otto/logs",
		},
		Dashboard: DashboardConfig{
			Port:    4098,
			Enabled: false,
		},
	}
}
