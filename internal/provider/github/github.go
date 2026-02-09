package github

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	github_ratelimit "github.com/gofri/go-github-ratelimit/v2/github_ratelimit"
	gh "github.com/google/go-github/v82/github"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"

	"github.com/alanmeadows/otto/internal/provider"
)

// ansiPattern matches ANSI escape codes for stripping from build logs.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// maxLogSize caps the amount of log data read per job to 10 MB.
const maxLogSize = 10 * 1024 * 1024

// Backend implements provider.PRBackend for GitHub.
type Backend struct {
	client    *gh.Client
	gqlOnce   sync.Once
	gqlClient *githubv4.Client
	owner     string
	repo      string
	token     string
	baseURL   string // override for testing
}

// NewBackend creates a new GitHub backend for the given owner/repo.
// Uses go-github-ratelimit middleware for automatic rate limit handling.
func NewBackend(owner, repo, token string) *Backend {
	rateLimiter := github_ratelimit.NewClient(nil)
	client := gh.NewClient(rateLimiter).WithAuthToken(token)
	return &Backend{
		client: client,
		owner:  owner,
		repo:   repo,
		token:  token,
	}
}

// Name returns "github".
func (b *Backend) Name() string {
	return "github"
}

// MatchesURL returns true if the URL belongs to GitHub.
func (b *Backend) MatchesURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "github.com" || host == "www.github.com"
}

// GetPR retrieves pull request information by ID or URL.
func (b *Backend) GetPR(ctx context.Context, id string) (*provider.PRInfo, error) {
	parsed, err := b.parsePRIdentifier(id)
	if err != nil {
		return nil, fmt.Errorf("could not parse PR identifier %q: %w", id, err)
	}

	pr, _, err := b.client.PullRequests.Get(ctx, parsed.Owner, parsed.Repo, parsed.Number)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR: %w", err)
	}

	return b.mapPR(pr, parsed.Owner, parsed.Repo), nil
}

// GetPipelineStatus returns the CI/CD pipeline status for a pull request.
// Queries both GitHub Check Runs and legacy Commit Statuses for a complete picture.
func (b *Backend) GetPipelineStatus(ctx context.Context, pr *provider.PRInfo) (*provider.PipelineStatus, error) {
	owner, repo := b.resolveOwnerRepo(pr)
	prNum, err := strconv.Atoi(pr.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid PR number: %s", pr.ID)
	}

	// Get head SHA from the PR.
	ghPR, _, err := b.client.PullRequests.Get(ctx, owner, repo, prNum)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR for head SHA: %w", err)
	}
	headSHA := ghPR.GetHead().GetSHA()
	if headSHA == "" {
		return nil, fmt.Errorf("PR head SHA is empty")
	}

	status := &provider.PipelineStatus{
		State:  "succeeded",
		Builds: make([]provider.BuildInfo, 0),
	}

	// Query check runs (with pagination).
	checkOpts := &gh.ListCheckRunsOptions{
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		checkResult, resp, err := b.client.Checks.ListCheckRunsForRef(ctx, owner, repo, headSHA, checkOpts)
		if err != nil {
			slog.Warn("failed to list check runs", "error", err)
			break
		}
		for _, cr := range checkResult.CheckRuns {
			bi := provider.BuildInfo{
				ID:     strconv.FormatInt(cr.GetID(), 10),
				Name:   cr.GetName(),
				Status: cr.GetStatus(),
				Result: cr.GetConclusion(),
				URL:    cr.GetHTMLURL(),
			}
			status.Builds = append(status.Builds, bi)
			b.updateOverallState(status, cr.GetStatus(), cr.GetConclusion())
		}
		if resp.NextPage == 0 {
			break
		}
		checkOpts.Page = resp.NextPage
	}

	// Query combined commit status (legacy status API).
	combined, _, err := b.client.Repositories.GetCombinedStatus(ctx, owner, repo, headSHA, &gh.ListOptions{PerPage: 100})
	if err != nil {
		slog.Warn("failed to get combined status", "error", err)
	} else {
		for _, s := range combined.Statuses {
			bi := provider.BuildInfo{
				ID:     strconv.FormatInt(s.GetID(), 10),
				Name:   s.GetContext(),
				Status: "completed",
				Result: s.GetState(), // "success", "failure", "error", "pending"
				URL:    s.GetTargetURL(),
			}
			status.Builds = append(status.Builds, bi)

			switch s.GetState() {
			case "failure", "error":
				status.State = "failed"
			case "pending":
				if status.State != "failed" {
					status.State = "pending"
				}
			}
		}
	}

	if len(status.Builds) == 0 {
		status.State = "pending"
	}

	return status, nil
}

// GetComments retrieves all comments on a pull request.
// Fetches both issue comments (general) and review comments (inline).
func (b *Backend) GetComments(ctx context.Context, pr *provider.PRInfo) ([]provider.Comment, error) {
	owner, repo := b.resolveOwnerRepo(pr)
	prNum, err := strconv.Atoi(pr.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid PR number: %s", pr.ID)
	}

	var comments []provider.Comment

	// Fetch issue comments (general PR comments).
	opts := &gh.IssueListCommentsOptions{
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		issueComments, resp, err := b.client.Issues.ListComments(ctx, owner, repo, prNum, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list issue comments: %w", err)
		}
		for _, c := range issueComments {
			comments = append(comments, provider.Comment{
				ID:        strconv.FormatInt(c.GetID(), 10),
				ThreadID:  strconv.FormatInt(c.GetID(), 10),
				Author:    c.GetUser().GetLogin(),
				Body:      c.GetBody(),
				CreatedAt: c.GetCreatedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	// Fetch review comments (inline/diff comments).
	reviewOpts := &gh.PullRequestListCommentsOptions{
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		reviewComments, resp, err := b.client.PullRequests.ListComments(ctx, owner, repo, prNum, reviewOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to list review comments: %w", err)
		}
		for _, c := range reviewComments {
			// Use InReplyTo as ThreadID to group replies under the root comment.
			threadID := c.GetID()
			if c.InReplyTo != nil && c.GetInReplyTo() != 0 {
				threadID = c.GetInReplyTo()
			}
			comments = append(comments, provider.Comment{
				ID:        strconv.FormatInt(c.GetID(), 10),
				ThreadID:  strconv.FormatInt(threadID, 10),
				Author:    c.GetUser().GetLogin(),
				Body:      c.GetBody(),
				FilePath:  c.GetPath(),
				Line:      c.GetLine(),
				CreatedAt: c.GetCreatedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		reviewOpts.Page = resp.NextPage
	}

	return comments, nil
}

// PostComment posts a general comment on a pull request.
func (b *Backend) PostComment(ctx context.Context, pr *provider.PRInfo, body string) error {
	owner, repo := b.resolveOwnerRepo(pr)
	prNum, err := strconv.Atoi(pr.ID)
	if err != nil {
		return fmt.Errorf("invalid PR number: %s", pr.ID)
	}

	_, _, err = b.client.Issues.CreateComment(ctx, owner, repo, prNum, &gh.IssueComment{
		Body: gh.Ptr(body),
	})
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}
	return nil
}

// PostInlineComment posts a comment on a specific file and line in the PR diff.
// Uses CreateReview with a single comment to avoid secondary rate limits.
func (b *Backend) PostInlineComment(ctx context.Context, pr *provider.PRInfo, comment provider.InlineComment) error {
	owner, repo := b.resolveOwnerRepo(pr)
	prNum, err := strconv.Atoi(pr.ID)
	if err != nil {
		return fmt.Errorf("invalid PR number: %s", pr.ID)
	}

	// Get the PR head SHA — required for CommitID.
	ghPR, _, err := b.client.PullRequests.Get(ctx, owner, repo, prNum)
	if err != nil {
		return fmt.Errorf("failed to get PR for head SHA: %w", err)
	}
	headSHA := ghPR.GetHead().GetSHA()

	side := "RIGHT"
	if strings.EqualFold(comment.Side, "left") {
		side = "LEFT"
	}

	// Create a single-comment review with Event "COMMENT" (immediately visible).
	_, _, err = b.client.PullRequests.CreateReview(ctx, owner, repo, prNum, &gh.PullRequestReviewRequest{
		CommitID: gh.Ptr(headSHA),
		Event:    gh.Ptr("COMMENT"),
		Comments: []*gh.DraftReviewComment{
			{
				Path: gh.Ptr(comment.FilePath),
				Line: gh.Ptr(comment.Line),
				Side: gh.Ptr(side),
				Body: gh.Ptr(comment.Body),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to post inline comment: %w", err)
	}
	return nil
}

// ReplyToComment adds a reply to an existing review comment thread.
// threadID must be the root comment ID of the thread.
func (b *Backend) ReplyToComment(ctx context.Context, pr *provider.PRInfo, threadID string, body string) error {
	owner, repo := b.resolveOwnerRepo(pr)
	prNum, err := strconv.Atoi(pr.ID)
	if err != nil {
		return fmt.Errorf("invalid PR number: %s", pr.ID)
	}

	commentID, err := strconv.ParseInt(threadID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid thread/comment ID: %s", threadID)
	}

	_, _, err = b.client.PullRequests.CreateCommentInReplyTo(ctx, owner, repo, prNum, body, commentID)
	if err != nil {
		return fmt.Errorf("failed to reply to comment: %w", err)
	}
	return nil
}

// ResolveComment resolves a review thread using the GitHub GraphQL API.
// threadID must be the thread's node ID (e.g., "PRRT_...").
// REST API cannot resolve threads — GraphQL is required.
func (b *Backend) ResolveComment(ctx context.Context, pr *provider.PRInfo, threadID string, resolution provider.CommentResolution) error {
	if resolution == provider.ResolutionUnknown {
		return fmt.Errorf("invalid comment resolution: %d", resolution)
	}

	gql := b.getGraphQLClient(ctx)

	var mutation struct {
		ResolveReviewThread struct {
			Thread struct {
				IsResolved bool
			}
		} `graphql:"resolveReviewThread(input: $input)"`
	}

	input := githubv4.ResolveReviewThreadInput{
		ThreadID: githubv4.ID(threadID),
	}

	if err := gql.Mutate(ctx, &mutation, input, nil); err != nil {
		return fmt.Errorf("failed to resolve review thread: %w", err)
	}

	return nil
}

// GetBuildLogs retrieves and distills build logs for a specific workflow run,
// focusing on failed jobs and their error output.
func (b *Backend) GetBuildLogs(ctx context.Context, pr *provider.PRInfo, buildID string) (string, error) {
	owner, repo := b.resolveOwnerRepo(pr)

	runID, err := strconv.ParseInt(buildID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid build/run ID: %s", buildID)
	}

	// List all jobs for the workflow run (with pagination).
	var allJobs []*gh.WorkflowJob
	jobOpts := &gh.ListWorkflowJobsOptions{
		Filter:      "latest",
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		jobs, resp, err := b.client.Actions.ListWorkflowJobs(ctx, owner, repo, runID, jobOpts)
		if err != nil {
			return "", fmt.Errorf("failed to list workflow jobs: %w", err)
		}
		allJobs = append(allJobs, jobs.Jobs...)
		if resp.NextPage == 0 {
			break
		}
		jobOpts.Page = resp.NextPage
	}

	var errorSummary strings.Builder

	for _, job := range allJobs {
		if job.GetConclusion() != "failure" {
			continue
		}

		errorSummary.WriteString(fmt.Sprintf("=== Failed Job: %s ===\n", job.GetName()))

		// List failed steps.
		for _, step := range job.Steps {
			if step.GetConclusion() == "failure" {
				errorSummary.WriteString(fmt.Sprintf("  Failed Step: %s\n", step.GetName()))
			}
		}

		// Download per-job log.
		logURL, _, err := b.client.Actions.GetWorkflowJobLogs(ctx, owner, repo, job.GetID(), 2)
		if err != nil {
			slog.Warn("failed to get job log URL", "jobName", job.GetName(), "error", err)
			continue
		}

		logText, err := b.downloadLog(ctx, logURL.String())
		if err != nil {
			slog.Warn("failed to download job log", "jobName", job.GetName(), "error", err)
			continue
		}

		// Strip ANSI codes and extract error context.
		cleaned := stripANSI(logText)
		distilled := extractErrorContext(cleaned)
		errorSummary.WriteString(distilled)
		errorSummary.WriteString("\n")
	}

	result := errorSummary.String()
	if result == "" {
		return "No failed jobs found in workflow run.", nil
	}

	return result, nil
}

// RunWorkflow returns ErrUnsupported for all workflow actions.
// GitHub does not have equivalents for ADO-specific workflow operations:
// - AutoComplete: GitHub has auto-merge but it works differently
// - CreateWorkItem: GitHub Issues are separate from PR workflows
// - AddressBot: No MerlinBot equivalent on GitHub
func (b *Backend) RunWorkflow(ctx context.Context, pr *provider.PRInfo, action provider.WorkflowAction) error {
	return provider.ErrUnsupported
}

// --- Internal helpers ---

// parsePRIdentifier extracts owner, repo, and PR number from a string.
// Accepts bare numbers, "owner/repo#number", or full GitHub URLs.
func (b *Backend) parsePRIdentifier(id string) (*prIdentifier, error) {
	// Bare number — use backend defaults.
	if num, err := strconv.Atoi(id); err == nil {
		return &prIdentifier{Owner: b.owner, Repo: b.repo, Number: num}, nil
	}

	// Try "owner/repo#number" format.
	if parts := strings.SplitN(id, "#", 2); len(parts) == 2 {
		ownerRepo := strings.SplitN(parts[0], "/", 2)
		if len(ownerRepo) == 2 {
			num, err := strconv.Atoi(parts[1])
			if err == nil {
				return &prIdentifier{Owner: ownerRepo[0], Repo: ownerRepo[1], Number: num}, nil
			}
		}
	}

	// Try URL: https://github.com/{owner}/{repo}/pull/{number}
	u, err := url.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid PR identifier: %s", id)
	}

	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Pattern: {owner}/{repo}/pull/{number}
	if len(pathParts) >= 4 && pathParts[2] == "pull" {
		num, err := strconv.Atoi(pathParts[3])
		if err != nil {
			return nil, fmt.Errorf("invalid PR number in URL: %s", pathParts[3])
		}
		return &prIdentifier{Owner: pathParts[0], Repo: pathParts[1], Number: num}, nil
	}

	return nil, fmt.Errorf("could not parse PR identifier: %s", id)
}

// mapPR converts a GitHub PullRequest to provider.PRInfo.
func (b *Backend) mapPR(pr *gh.PullRequest, owner, repo string) *provider.PRInfo {
	status := "active"
	if pr.GetMerged() {
		status = "completed"
	} else if pr.GetState() == "closed" {
		status = "abandoned"
	}

	return &provider.PRInfo{
		ID:           strconv.Itoa(pr.GetNumber()),
		Title:        pr.GetTitle(),
		Description:  pr.GetBody(),
		Status:       status,
		SourceBranch: pr.GetHead().GetRef(),
		TargetBranch: pr.GetBase().GetRef(),
		Author:       pr.GetUser().GetLogin(),
		URL:          pr.GetHTMLURL(),
		RepoID:       repo,
		Project:      "", // GitHub doesn't use project for routing.
		Organization: owner,
	}
}

// resolveOwnerRepo returns the owner and repo for API calls, preferring
// values from the PRInfo if available.
func (b *Backend) resolveOwnerRepo(pr *provider.PRInfo) (string, string) {
	owner := b.owner
	repo := b.repo
	if pr.Organization != "" {
		owner = pr.Organization
	}
	if pr.RepoID != "" {
		repo = pr.RepoID
	}
	return owner, repo
}

// updateOverallState updates the pipeline status state based on a check run.
func (b *Backend) updateOverallState(status *provider.PipelineStatus, checkStatus, conclusion string) {
	switch {
	case conclusion == "failure" || conclusion == "timed_out" || conclusion == "cancelled" || conclusion == "action_required":
		status.State = "failed"
	case checkStatus == "in_progress" && status.State != "failed":
		status.State = "inProgress"
	case checkStatus == "queued" && status.State != "failed" && status.State != "inProgress":
		status.State = "pending"
	}
}

// getGraphQLClient returns (and lazily creates) the GitHub GraphQL client.
// Thread-safe via sync.Once.
func (b *Backend) getGraphQLClient(ctx context.Context) *githubv4.Client {
	b.gqlOnce.Do(func() {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: b.token})
		httpClient := oauth2.NewClient(ctx, ts)
		b.gqlClient = githubv4.NewClient(httpClient)
	})
	return b.gqlClient
}

// downloadLog fetches a log from the given pre-signed URL.
// Does NOT send auth headers — the redirect URL from GitHub is self-authenticating
// (pre-signed Azure Blob Storage URL).
func (b *Backend) downloadLog(ctx context.Context, logURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, logURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create log request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download log: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("log download returned status %d", resp.StatusCode)
	}

	// Limit read to maxLogSize to avoid OOM on huge logs.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLogSize))
	if err != nil {
		return "", fmt.Errorf("failed to read log body: %w", err)
	}

	return string(body), nil
}

// stripANSI removes ANSI escape codes from a string.
func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// extractErrorContext extracts context windows around error markers in build logs.
// Matches GitHub Actions "##[error]" markers for consistent behavior.
func extractErrorContext(log string) string {
	lines := strings.Split(log, "\n")
	const contextWindow = 5

	var errorIndices []int
	for i, line := range lines {
		if strings.Contains(line, "##[error]") {
			errorIndices = append(errorIndices, i)
		}
	}

	if len(errorIndices) == 0 {
		// No error markers found; return last 50 lines as fallback.
		start := len(lines) - 50
		if start < 0 {
			start = 0
		}
		return strings.Join(lines[start:], "\n")
	}

	// Collect unique lines within context windows around errors.
	included := make(map[int]bool)
	for _, idx := range errorIndices {
		start := idx - contextWindow
		if start < 0 {
			start = 0
		}
		end := idx + contextWindow + 1
		if end > len(lines) {
			end = len(lines)
		}
		for i := start; i < end; i++ {
			included[i] = true
		}
	}

	var result strings.Builder
	prevIncluded := false
	for i, line := range lines {
		if included[i] {
			if !prevIncluded && i > 0 {
				result.WriteString("...\n")
			}
			result.WriteString(line)
			result.WriteString("\n")
			prevIncluded = true
		} else {
			prevIncluded = false
		}
	}

	return result.String()
}

// Verify Backend implements PRBackend at compile time.
var _ provider.PRBackend = (*Backend)(nil)
