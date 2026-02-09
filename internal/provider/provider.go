package provider

import (
	"context"
	"errors"
	"time"
)

// ErrUnsupported is returned when a backend doesn't support a given operation.
var ErrUnsupported = errors.New("operation not supported by this backend")

// PRBackend is the interface for PR lifecycle management backends.
// Implementations handle provider-specific API calls for pull request operations
// including retrieval, commenting, pipeline status, build logs, and workflow actions.
type PRBackend interface {
	// Name returns the short identifier for this backend (e.g., "ado", "github").
	Name() string

	// MatchesURL returns true if the given URL belongs to this backend's hosting service.
	MatchesURL(url string) bool

	// GetPR retrieves pull request information by ID or URL.
	GetPR(ctx context.Context, id string) (*PRInfo, error)

	// GetPipelineStatus returns the current CI/CD pipeline status for a pull request.
	GetPipelineStatus(ctx context.Context, pr *PRInfo) (*PipelineStatus, error)

	// GetBuildLogs retrieves and distills build logs for a specific build, focusing on errors.
	GetBuildLogs(ctx context.Context, pr *PRInfo, buildID string) (string, error)

	// GetComments retrieves all comments/threads on a pull request.
	GetComments(ctx context.Context, pr *PRInfo) ([]Comment, error)

	// PostComment posts a general (non-inline) comment on a pull request.
	PostComment(ctx context.Context, pr *PRInfo, body string) error

	// PostInlineComment posts a comment on a specific file and line in the PR diff.
	PostInlineComment(ctx context.Context, pr *PRInfo, comment InlineComment) error

	// ReplyToComment adds a reply to an existing comment thread.
	ReplyToComment(ctx context.Context, pr *PRInfo, threadID string, body string) error

	// ResolveComment resolves or closes a comment thread with the given resolution.
	ResolveComment(ctx context.Context, pr *PRInfo, threadID string, resolution CommentResolution) error

	// RunWorkflow executes a workflow action on the pull request (e.g., auto-complete, create work item).
	RunWorkflow(ctx context.Context, pr *PRInfo, action WorkflowAction) error
}

// PRInfo contains metadata about a pull request.
type PRInfo struct {
	// ID is the provider-specific pull request identifier (e.g., numeric ID for ADO).
	ID string
	// Title is the pull request title.
	Title string
	// Description is the pull request description/body text.
	Description string
	// Status is the current PR status (e.g., "active", "completed", "abandoned").
	Status string
	// SourceBranch is the branch being merged from.
	SourceBranch string
	// TargetBranch is the branch being merged into.
	TargetBranch string
	// Author is the display name of the PR author.
	Author string
	// URL is the web URL to view the pull request.
	URL string
	// RepoID is the repository identifier used for API routing.
	RepoID string
	// Project is the project name (used by ADO for API routing).
	Project string
	// Organization is the organization name (used by ADO for API routing).
	Organization string
}

// PipelineStatus represents the CI/CD pipeline status for a pull request.
type PipelineStatus struct {
	// State is the overall pipeline state: "succeeded", "failed", "pending", or "inProgress".
	State string
	// Builds contains information about individual builds associated with the PR.
	Builds []BuildInfo
}

// BuildInfo contains metadata about a single CI/CD build.
type BuildInfo struct {
	// ID is the build identifier.
	ID string
	// Name is the build definition/pipeline name.
	Name string
	// Status is the current build status (e.g., "completed", "inProgress", "notStarted").
	Status string
	// Result is the build result when completed (e.g., "succeeded", "failed", "canceled").
	Result string
	// URL is the web URL to view the build.
	URL string
}

// Comment represents a comment or thread on a pull request.
type Comment struct {
	// ID is the comment identifier.
	ID string
	// ThreadID is the parent thread identifier.
	ThreadID string
	// Author is the display name of the comment author.
	Author string
	// Body is the comment text content.
	Body string
	// IsResolved indicates whether the comment thread has been resolved.
	IsResolved bool
	// FilePath is the file path for inline comments (empty for general comments).
	FilePath string
	// Line is the line number for inline comments (0 for general comments).
	Line int
	// CreatedAt is the timestamp when the comment was created.
	CreatedAt time.Time
}

// InlineComment contains the data needed to post a comment on a specific line in a PR diff.
type InlineComment struct {
	// FilePath is the path of the file to comment on.
	FilePath string
	// Line is the line number to comment on.
	Line int
	// Body is the comment text.
	Body string
	// Side indicates which side of the diff to comment on ("left" or "right").
	Side string
}

// CommentResolution represents how a comment thread was resolved.
type CommentResolution int

const (
	// ResolutionUnknown is the zero value — invalid, must not be used.
	ResolutionUnknown CommentResolution = iota
	// ResolutionFixed indicates the issue was fixed.
	ResolutionFixed
	// ResolutionWontFix indicates the issue will not be fixed.
	ResolutionWontFix
	// ResolutionByDesign indicates the behavior is intentional.
	ResolutionByDesign
)

// WorkflowAction represents a workflow operation that can be performed on a pull request.
type WorkflowAction int

const (
	// WorkflowSubmit is reserved for future use.
	WorkflowSubmit WorkflowAction = iota
	// WorkflowAutoComplete sets the PR to auto-complete when all policies pass.
	WorkflowAutoComplete
	// WorkflowCreateWorkItem creates a work item linked to the PR.
	WorkflowCreateWorkItem
	// WorkflowAddressBot identifies and addresses bot comments (e.g., MerlinBot) on the PR.
	WorkflowAddressBot
)
