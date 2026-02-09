package ado

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alanmeadows/otto/internal/provider"
)

// ansiPattern matches ANSI escape codes for stripping from build logs.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Backend implements provider.PRBackend for Azure DevOps.
type Backend struct {
	auth         *AuthProvider
	organization string
	project      string
	repository   string
	httpClient   *http.Client
	baseURL      string // override for testing
}

// NewBackend creates a new ADO backend for the given organization and project.
func NewBackend(organization, project string, auth *AuthProvider) *Backend {
	return &Backend{
		auth:         auth,
		organization: organization,
		project:      project,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

// SetRepository sets the default repository name for API calls.
func (b *Backend) SetRepository(repo string) {
	b.repository = repo
}

// Name returns "ado".
func (b *Backend) Name() string {
	return "ado"
}

// MatchesURL returns true if the URL belongs to Azure DevOps.
func (b *Backend) MatchesURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, ".visualstudio.com") || host == "dev.azure.com"
}

// GetPR retrieves pull request information by ID or URL.
func (b *Backend) GetPR(ctx context.Context, id string) (*provider.PRInfo, error) {
	prID, repo, org, project := b.parsePRIdentifier(id)
	if prID == "" {
		return nil, fmt.Errorf("could not parse PR identifier: %s", id)
	}

	if repo == "" {
		repo = b.repository
	}
	if repo == "" {
		return nil, fmt.Errorf("repository not specified and could not be determined from PR identifier")
	}

	if org == "" {
		org = b.organization
	}
	if project == "" {
		project = b.project
	}

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests/%s",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repo), prID)

	resp, err := b.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, b.parseError(resp)
	}

	var adoPR adoPullRequest
	if err := json.NewDecoder(resp.Body).Decode(&adoPR); err != nil {
		return nil, fmt.Errorf("failed to decode PR response: %w", err)
	}

	// Prefer web URL from _links; fall back to constructing it.
	webURL := adoPR.Links.Web.Href
	if webURL == "" {
		webURL = fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s/pullrequest/%d",
			org, project, adoPR.Repository.Name, adoPR.PullRequestID)
	}

	return &provider.PRInfo{
		ID:           strconv.Itoa(adoPR.PullRequestID),
		Title:        adoPR.Title,
		Description:  adoPR.Description,
		Status:       adoPR.Status,
		SourceBranch: adoPR.SourceRefName,
		TargetBranch: adoPR.TargetRefName,
		Author:       adoPR.CreatedBy.DisplayName,
		URL:          webURL,
		RepoID:       adoPR.Repository.Name,
		Project:      project,
		Organization: org,
	}, nil
}

// GetPipelineStatus returns the CI/CD build status for a pull request.
func (b *Backend) GetPipelineStatus(ctx context.Context, pr *provider.PRInfo) (*provider.PipelineStatus, error) {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)

	branchName := fmt.Sprintf("refs/pull/%s/merge", pr.ID)
	path := fmt.Sprintf("/%s/%s/_apis/build/builds?branchName=%s",
		url.PathEscape(org), url.PathEscape(project), url.QueryEscape(branchName))

	resp, err := b.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, b.parseError(resp)
	}

	var buildList adoBuildList
	if err := json.NewDecoder(resp.Body).Decode(&buildList); err != nil {
		return nil, fmt.Errorf("failed to decode builds response: %w", err)
	}

	status := &provider.PipelineStatus{
		State:  "succeeded",
		Builds: make([]provider.BuildInfo, 0, len(buildList.Value)),
	}

	for _, build := range buildList.Value {
		bi := provider.BuildInfo{
			ID:     strconv.Itoa(build.ID),
			Name:   build.Definition.Name,
			Status: build.Status,
			Result: build.Result,
			URL:    build.Links.Web.Href,
		}
		status.Builds = append(status.Builds, bi)

		// Determine overall state.
		switch {
		case build.Result == "failed" || build.Result == "partiallySucceeded" || build.Result == "canceled":
			status.State = "failed"
		case build.Status == "inProgress" && status.State != "failed":
			status.State = "inProgress"
		case build.Status == "notStarted" && status.State != "failed" && status.State != "inProgress":
			status.State = "pending"
		}
	}

	if len(buildList.Value) == 0 {
		status.State = "pending"
	}

	return status, nil
}

// GetComments retrieves all comment threads on a pull request.
func (b *Backend) GetComments(ctx context.Context, pr *provider.PRInfo) ([]provider.Comment, error) {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)
	repo := b.resolveRepo(pr)

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests/%s/threads",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repo), pr.ID)

	resp, err := b.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get comments: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, b.parseError(resp)
	}

	var threadList adoThreadList
	if err := json.NewDecoder(resp.Body).Decode(&threadList); err != nil {
		return nil, fmt.Errorf("failed to decode threads response: %w", err)
	}

	var comments []provider.Comment
	for _, thread := range threadList.Value {
		// ADO thread status: 1=active, 2=fixed, 3=wontFix, 4=closed, 5=byDesign
		isResolved := thread.Status >= 2

		var filePath string
		var line int
		if thread.ThreadContext != nil {
			filePath = thread.ThreadContext.FilePath
			if thread.ThreadContext.RightFileStart != nil {
				line = thread.ThreadContext.RightFileStart.Line
			}
		}

		for _, c := range thread.Comments {
			// Skip system-generated comments.
			if c.CommentType == "system" {
				continue
			}
			comments = append(comments, provider.Comment{
				ID:         strconv.Itoa(c.ID),
				ThreadID:   strconv.Itoa(thread.ID),
				Author:     c.Author.DisplayName,
				Body:       c.Content,
				IsResolved: isResolved,
				FilePath:   filePath,
				Line:       line,
				CreatedAt:  c.PublishedDate,
			})
		}
	}

	return comments, nil
}

// PostComment posts a general comment on a pull request.
func (b *Backend) PostComment(ctx context.Context, pr *provider.PRInfo, body string) error {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)
	repo := b.resolveRepo(pr)

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests/%s/threads",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repo), pr.ID)

	thread := map[string]any{
		"comments": []map[string]any{
			{
				"content":     body,
				"commentType": "text",
			},
		},
		"status": 1, // active
	}

	resp, err := b.doRequest(ctx, http.MethodPost, path, thread)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return b.parseError(resp)
	}

	return nil
}

// PostInlineComment posts a comment on a specific file and line in the PR diff.
func (b *Backend) PostInlineComment(ctx context.Context, pr *provider.PRInfo, comment provider.InlineComment) error {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)
	repo := b.resolveRepo(pr)

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests/%s/threads",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repo), pr.ID)

	// Ensure file path starts with "/".
	filePath := comment.FilePath
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}

	// Build thread context based on which side of the diff to comment on.
	linePos := map[string]int{"line": comment.Line, "offset": 1}
	threadCtx := map[string]any{"filePath": filePath}
	if strings.EqualFold(comment.Side, "left") {
		threadCtx["leftFileStart"] = linePos
		threadCtx["leftFileEnd"] = linePos
	} else {
		threadCtx["rightFileStart"] = linePos
		threadCtx["rightFileEnd"] = linePos
	}

	thread := map[string]any{
		"comments": []map[string]any{
			{
				"content":     comment.Body,
				"commentType": "text",
			},
		},
		"threadContext": threadCtx,
		"status":        1, // active
	}

	resp, err := b.doRequest(ctx, http.MethodPost, path, thread)
	if err != nil {
		return fmt.Errorf("failed to post inline comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return b.parseError(resp)
	}

	return nil
}

// ReplyToComment adds a reply to an existing comment thread.
func (b *Backend) ReplyToComment(ctx context.Context, pr *provider.PRInfo, threadID string, body string) error {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)
	repo := b.resolveRepo(pr)

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests/%s/threads/%s/comments",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repo), pr.ID, threadID)

	comment := map[string]any{
		"content":         body,
		"commentType":     "text",
		"parentCommentId": 1,
	}

	resp, err := b.doRequest(ctx, http.MethodPost, path, comment)
	if err != nil {
		return fmt.Errorf("failed to reply to comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return b.parseError(resp)
	}

	return nil
}

// ResolveComment resolves a comment thread with the given resolution.
func (b *Backend) ResolveComment(ctx context.Context, pr *provider.PRInfo, threadID string, resolution provider.CommentResolution) error {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)
	repo := b.resolveRepo(pr)

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests/%s/threads/%s",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repo), pr.ID, threadID)

	// Map CommentResolution to ADO thread status.
	var status int
	switch resolution {
	case provider.ResolutionFixed:
		status = 2
	case provider.ResolutionWontFix:
		status = 3
	case provider.ResolutionByDesign:
		status = 5
	default:
		return fmt.Errorf("invalid comment resolution: %d", resolution)
	}

	update := map[string]any{
		"status": status,
	}

	resp, err := b.doRequest(ctx, http.MethodPatch, path, update)
	if err != nil {
		return fmt.Errorf("failed to resolve comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return b.parseError(resp)
	}

	return nil
}

// GetBuildLogs retrieves and distills build logs for a specific build, focusing on errors.
func (b *Backend) GetBuildLogs(ctx context.Context, pr *provider.PRInfo, buildID string) (string, error) {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)

	// Step 1: Fetch build timeline.
	timelinePath := fmt.Sprintf("/%s/%s/_apis/build/builds/%s/timeline",
		url.PathEscape(org), url.PathEscape(project), buildID)

	resp, err := b.doRequest(ctx, http.MethodGet, timelinePath, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get build timeline: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", b.parseError(resp)
	}

	var timeline adoBuildTimeline
	if err := json.NewDecoder(resp.Body).Decode(&timeline); err != nil {
		return "", fmt.Errorf("failed to decode timeline: %w", err)
	}

	// Step 2: Find failed Task records.
	var errorSummary strings.Builder
	for _, record := range timeline.Records {
		if record.Type != "Task" || record.Result != "failed" {
			continue
		}

		errorSummary.WriteString(fmt.Sprintf("=== Failed Task: %s ===\n", record.Name))

		// Include any reported issues.
		for _, issue := range record.Issues {
			errorSummary.WriteString(fmt.Sprintf("[%s] %s\n", issue.Type, issue.Message))
		}

		// Step 3: Fetch log for failed task.
		if record.Log == nil {
			continue
		}

		logPath := fmt.Sprintf("/%s/%s/_apis/build/builds/%s/logs/%d",
			url.PathEscape(org), url.PathEscape(project), buildID, record.Log.ID)

		logResp, err := b.doRequestWithAccept(ctx, http.MethodGet, logPath, nil, "text/plain")
		if err != nil {
			slog.Warn("failed to fetch build log", "taskName", record.Name, "error", err)
			continue
		}

		logBytes, err := io.ReadAll(logResp.Body)
		logResp.Body.Close()
		if err != nil {
			slog.Warn("failed to read build log", "taskName", record.Name, "error", err)
			continue
		}

		// Step 4: Strip ANSI escape codes.
		logText := stripANSI(string(logBytes))

		// Step 5: Apply error-anchored truncation.
		distilled := extractErrorContext(logText)
		errorSummary.WriteString(distilled)
		errorSummary.WriteString("\n")
	}

	result := errorSummary.String()
	if result == "" {
		return "No failed tasks found in build timeline.", nil
	}

	return result, nil
}

// stripANSI removes ANSI escape codes from a string.
func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// extractErrorContext extracts context windows around ##[error] markers in build logs.
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

// doRequest makes an authenticated HTTP request to the ADO API.
// It handles rate limiting with exponential backoff on 429 responses.
func (b *Backend) doRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	return b.doRequestFull(ctx, method, path, body, "application/json", "application/json")
}

// doRequestWithAccept makes an authenticated HTTP request with a custom Accept header.
func (b *Backend) doRequestWithAccept(ctx context.Context, method, path string, body any, accept string) (*http.Response, error) {
	return b.doRequestFull(ctx, method, path, body, "application/json", accept)
}

// doRequestWithContentType makes an authenticated HTTP request with a custom Content-Type.
func (b *Backend) doRequestWithContentType(ctx context.Context, method, path string, body any, contentType string) (*http.Response, error) {
	return b.doRequestFull(ctx, method, path, body, contentType, "application/json")
}

// doRequestFull makes an authenticated HTTP request with custom Content-Type and Accept headers.
// It handles rate limiting with exponential backoff on 429 responses.
func (b *Backend) doRequestFull(ctx context.Context, method, path string, body any, contentType, accept string) (*http.Response, error) {
	baseURL := b.baseURL
	if baseURL == "" {
		baseURL = "https://dev.azure.com"
	}

	// Append api-version query parameter.
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	fullURL := baseURL + path + separator + "api-version=7.1"

	const maxRetries = 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		var bodyReader io.Reader
		if body != nil {
			jsonBytes, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
			bodyReader = bytes.NewReader(jsonBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		authHeader, err := b.auth.GetAuthHeader(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth header: %w", err)
		}
		req.Header.Set("Authorization", authHeader)
		req.Header.Set("Accept", accept)

		if body != nil {
			req.Header.Set("Content-Type", contentType)
		}

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		// Rate limited — close body and retry.
		resp.Body.Close()

		if attempt == maxRetries {
			return nil, fmt.Errorf("rate limited after %d retries", maxRetries)
		}

		delay := b.calculateRetryDelay(resp, attempt)
		slog.Warn("rate limited by ADO API, retrying",
			"attempt", attempt+1,
			"delay", delay)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return nil, fmt.Errorf("unexpected: exhausted retries")
}

// calculateRetryDelay determines how long to wait before retrying a rate-limited request.
func (b *Backend) calculateRetryDelay(resp *http.Response, attempt int) time.Duration {
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil {
			return time.Duration(seconds) * time.Second
		}
	}
	// Exponential backoff: 1s, 2s, 4s, ...
	return time.Duration(math.Pow(2, float64(attempt))) * time.Second
}

// parsePRIdentifier extracts the PR ID and optionally repository, org, and project
// from a PR identifier string. The ID can be a bare number or a full ADO URL.
// Supports both dev.azure.com and *.visualstudio.com URL formats.
func (b *Backend) parsePRIdentifier(id string) (prID, repo, org, project string) {
	// Check if it's a bare number.
	if _, err := strconv.Atoi(id); err == nil {
		return id, "", "", ""
	}

	// Try to parse as URL.
	u, err := url.Parse(id)
	if err != nil {
		return "", "", "", ""
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	host := strings.ToLower(u.Hostname())

	// Pattern: https://{org}.visualstudio.com/{project}/_git/{repo}/pullrequest/{id}
	if strings.HasSuffix(host, ".visualstudio.com") {
		orgName := strings.TrimSuffix(host, ".visualstudio.com")
		if len(parts) >= 5 && parts[1] == "_git" && parts[3] == "pullrequest" {
			return parts[4], parts[2], orgName, parts[0]
		}
		return "", "", "", ""
	}

	// Pattern: https://dev.azure.com/{org}/{project}/_git/{repo}/pullrequest/{id}
	// Also handles test URLs with same path structure.
	if len(parts) >= 6 && parts[2] == "_git" && parts[4] == "pullrequest" {
		return parts[5], parts[3], parts[0], parts[1]
	}

	return "", "", "", ""
}

// parseError extracts error information from an ADO API error response.
func (b *Backend) parseError(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("ADO API error (status %d): could not read response body", resp.StatusCode)
	}

	var adoErr adoError
	if err := json.Unmarshal(body, &adoErr); err != nil {
		return fmt.Errorf("ADO API error (status %d): %s", resp.StatusCode, string(body))
	}

	return fmt.Errorf("ADO API error (status %d, %s): %s", resp.StatusCode, adoErr.TypeKey, adoErr.Message)
}

// resolveOrg returns the organization from the PR or the backend default.
func (b *Backend) resolveOrg(pr *provider.PRInfo) string {
	if pr.Organization != "" {
		return pr.Organization
	}
	return b.organization
}

// resolveProject returns the project from the PR or the backend default.
func (b *Backend) resolveProject(pr *provider.PRInfo) string {
	if pr.Project != "" {
		return pr.Project
	}
	return b.project
}

// resolveRepo returns the repository from the PR or the backend default.
func (b *Backend) resolveRepo(pr *provider.PRInfo) string {
	if pr.RepoID != "" {
		return pr.RepoID
	}
	return b.repository
}

// Verify Backend implements PRBackend at compile time.
var _ provider.PRBackend = (*Backend)(nil)
