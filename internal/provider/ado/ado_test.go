package ado

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/alanmeadows/otto/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBackend creates a Backend pointing to the given test server.
// It configures auth to skip the real az CLI and use PAT directly.
func newTestBackend(t *testing.T, server *httptest.Server) *Backend {
	t.Helper()
	auth := NewAuthProvider("test-pat")
	// Make Entra token acquisition fail immediately so tests use PAT.
	auth.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "false")
	}
	b := NewBackend("testorg", "testproject", auth)
	b.SetRepository("testrepo")
	b.baseURL = server.URL
	return b
}

func TestMatchesURL(t *testing.T) {
	b := NewBackend("org", "proj", NewAuthProvider("pat"))

	tests := []struct {
		url     string
		matches bool
	}{
		{"https://dev.azure.com/org/project/_git/repo/pullrequest/123", true},
		{"https://org.visualstudio.com/project/_git/repo/pullrequest/123", true},
		{"https://github.com/owner/repo/pull/123", false},
		{"https://gitlab.com/owner/repo", false},
		{"not-a-url", false},
		{"https://dev.azure.com/another/project", true},
		{"https://myorg.visualstudio.com/stuff", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			assert.Equal(t, tt.matches, b.MatchesURL(tt.url))
		})
	}
}

func TestGetPR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/pullrequests/1234") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		resp := adoPullRequest{
			PullRequestID: 1234,
			Title:         "Test PR",
			Description:   "A test pull request",
			Status:        "active",
			SourceRefName: "refs/heads/feature/test",
			TargetRefName: "refs/heads/main",
			CreatedBy:     adoIdentity{DisplayName: "Test User", ID: "user-1"},
			URL:           "https://dev.azure.com/org/proj/_git/repo/pullrequest/1234",
		}
		resp.Repository.Name = "testrepo"
		resp.Repository.ID = "repo-id"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	b := newTestBackend(t, server)

	t.Run("numeric ID", func(t *testing.T) {
		pr, err := b.GetPR(context.Background(), "1234")
		require.NoError(t, err)
		assert.Equal(t, "1234", pr.ID)
		assert.Equal(t, "Test PR", pr.Title)
		assert.Equal(t, "A test pull request", pr.Description)
		assert.Equal(t, "active", pr.Status)
		assert.Equal(t, "refs/heads/feature/test", pr.SourceBranch)
		assert.Equal(t, "refs/heads/main", pr.TargetBranch)
		assert.Equal(t, "Test User", pr.Author)
		assert.Equal(t, "testrepo", pr.RepoID)
	})

	t.Run("invalid ID", func(t *testing.T) {
		_, err := b.GetPR(context.Background(), "not-a-number-or-url")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "could not parse PR identifier")
	})
}

func TestGetPRFromURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the URL contains the right org/project/repo/PR path.
		if !strings.Contains(r.URL.Path, "/myorg/myproject/_apis/git/repositories/myrepo/pullrequests/5678") {
			http.Error(w, fmt.Sprintf("unexpected path: %s", r.URL.Path), http.StatusNotFound)
			return
		}

		resp := adoPullRequest{
			PullRequestID: 5678,
			Title:         "URL-based PR",
			Status:        "active",
			SourceRefName: "refs/heads/feature",
			TargetRefName: "refs/heads/main",
			CreatedBy:     adoIdentity{DisplayName: "URL User"},
		}
		resp.Repository.Name = "myrepo"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	auth := NewAuthProvider("test-pat")
	auth.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "false")
	}
	b := NewBackend("myorg", "myproject", auth)
	b.baseURL = server.URL

	prURL := server.URL + "/myorg/myproject/_git/myrepo/pullrequest/5678"
	pr, err := b.GetPR(context.Background(), prURL)
	require.NoError(t, err)
	assert.Equal(t, "5678", pr.ID)
	assert.Equal(t, "URL-based PR", pr.Title)
}

func TestGetPipelineStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := adoBuildList{
			Value: []adoBuild{
				{
					ID:           100,
					BuildNumber:  "20260209.1",
					Status:       "completed",
					Result:       "succeeded",
					SourceBranch: "refs/pull/1234/merge",
				},
				{
					ID:           101,
					BuildNumber:  "20260209.2",
					Status:       "completed",
					Result:       "failed",
					SourceBranch: "refs/pull/1234/merge",
				},
			},
			Count: 2,
		}
		resp.Value[0].Definition.Name = "CI Build"
		resp.Value[1].Definition.Name = "Integration Tests"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{ID: "1234", Organization: "testorg", Project: "testproject"}

	status, err := b.GetPipelineStatus(context.Background(), pr)
	require.NoError(t, err)
	assert.Equal(t, "failed", status.State)
	assert.Len(t, status.Builds, 2)
	assert.Equal(t, "CI Build", status.Builds[0].Name)
	assert.Equal(t, "succeeded", status.Builds[0].Result)
	assert.Equal(t, "Integration Tests", status.Builds[1].Name)
	assert.Equal(t, "failed", status.Builds[1].Result)
}

func TestGetComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := adoThreadList{
			Value: []adoThread{
				{
					ID:     1,
					Status: 1, // active
					Comments: []adoComment{
						{
							ID:          1,
							Content:     "General comment",
							Author:      adoIdentity{DisplayName: "Reviewer"},
							CommentType: "text",
						},
					},
				},
				{
					ID:     2,
					Status: 2, // fixed (resolved)
					ThreadContext: &adoThreadContext{
						FilePath:       "/src/main.go",
						RightFileStart: &adoLineOffset{Line: 42, Offset: 1},
						RightFileEnd:   &adoLineOffset{Line: 42, Offset: 1},
					},
					Comments: []adoComment{
						{
							ID:          1,
							Content:     "Inline comment on line 42",
							Author:      adoIdentity{DisplayName: "Reviewer"},
							CommentType: "text",
						},
					},
				},
				{
					ID:     3,
					Status: 1,
					Comments: []adoComment{
						{
							ID:          1,
							Content:     "System message",
							Author:      adoIdentity{DisplayName: "System"},
							CommentType: "system",
						},
					},
				},
			},
			Count: 3,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{ID: "1234", RepoID: "testrepo", Organization: "testorg", Project: "testproject"}

	comments, err := b.GetComments(context.Background(), pr)
	require.NoError(t, err)
	// Should have 3 comments (system comments are no longer filtered).
	assert.Len(t, comments, 3)

	// General comment.
	assert.Equal(t, "General comment", comments[0].Body)
	assert.Equal(t, "1", comments[0].ThreadID)
	assert.False(t, comments[0].IsResolved)
	assert.Equal(t, "", comments[0].FilePath)
	assert.Equal(t, 0, comments[0].Line)

	// Inline comment (resolved).
	assert.Equal(t, "Inline comment on line 42", comments[1].Body)
	assert.Equal(t, "2", comments[1].ThreadID)
	assert.True(t, comments[1].IsResolved)
	assert.Equal(t, "/src/main.go", comments[1].FilePath)
	assert.Equal(t, 42, comments[1].Line)

	// System comment (active, no longer filtered).
	assert.Equal(t, "System message", comments[2].Body)
	assert.Equal(t, "3", comments[2].ThreadID)
	assert.False(t, comments[2].IsResolved)
}

func TestPostComment(t *testing.T) {
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}

		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": 1}`))
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{ID: "1234", RepoID: "testrepo", Organization: "testorg", Project: "testproject"}

	err := b.PostComment(context.Background(), pr, "Test comment body")
	require.NoError(t, err)

	// Verify request body structure.
	comments, ok := receivedBody["comments"].([]any)
	require.True(t, ok)
	require.Len(t, comments, 1)

	comment := comments[0].(map[string]any)
	assert.Equal(t, "Test comment body", comment["content"])
	assert.Equal(t, "text", comment["commentType"])
	assert.Equal(t, float64(1), receivedBody["status"])
}

func TestPostInlineComment(t *testing.T) {
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": 1}`))
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{ID: "1234", RepoID: "testrepo", Organization: "testorg", Project: "testproject"}

	err := b.PostInlineComment(context.Background(), pr, provider.InlineComment{
		FilePath: "src/main.go", // without leading slash
		Line:     42,
		Body:     "Inline review comment",
		Side:     "right",
	})
	require.NoError(t, err)

	// Verify threadContext.
	tc, ok := receivedBody["threadContext"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "/src/main.go", tc["filePath"]) // should have leading slash added

	rfs, ok := tc["rightFileStart"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(42), rfs["line"])
}

func TestReplyToComment(t *testing.T) {
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify path includes thread ID and comments.
		if !strings.Contains(r.URL.Path, "/threads/10/comments") {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}

		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": 2}`))
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{ID: "1234", RepoID: "testrepo", Organization: "testorg", Project: "testproject"}

	err := b.ReplyToComment(context.Background(), pr, "10", "Reply text")
	require.NoError(t, err)

	assert.Equal(t, "Reply text", receivedBody["content"])
	assert.Equal(t, "text", receivedBody["commentType"])
	assert.Equal(t, float64(1), receivedBody["parentCommentId"])
}

func TestResolveComment(t *testing.T) {
	tests := []struct {
		resolution     provider.CommentResolution
		expectedStatus float64
	}{
		{provider.ResolutionFixed, 2},
		{provider.ResolutionWontFix, 3},
		{provider.ResolutionByDesign, 5},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("resolution_%d", tt.resolution), func(t *testing.T) {
			var receivedBody map[string]any

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch {
					http.Error(w, "expected PATCH", http.StatusMethodNotAllowed)
					return
				}

				json.NewDecoder(r.Body).Decode(&receivedBody)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"id": 5}`))
			}))
			defer server.Close()

			b := newTestBackend(t, server)
			pr := &provider.PRInfo{ID: "1234", RepoID: "testrepo", Organization: "testorg", Project: "testproject"}

			err := b.ResolveComment(context.Background(), pr, "5", tt.resolution)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, receivedBody["status"])
		})
	}
}

func TestGetBuildLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/timeline"):
			timeline := adoBuildTimeline{
				Records: []adoTimelineRecord{
					{
						Type:   "Task",
						Name:   "Build Solution",
						State:  "completed",
						Result: "succeeded",
					},
					{
						Type:   "Task",
						Name:   "Run Tests",
						State:  "completed",
						Result: "failed",
						Log: &struct {
							ID int `json:"id"`
						}{ID: 42},
						Issues: []adoIssue{
							{Type: "error", Message: "Tests failed"},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(timeline)

		case strings.Contains(r.URL.Path, "/logs/42"):
			w.Header().Set("Content-Type", "text/plain")
			// Include ANSI codes and error markers.
			fmt.Fprintln(w, "2026-02-09T10:00:00 Starting tests")
			fmt.Fprintln(w, "2026-02-09T10:00:01 Running test suite")
			fmt.Fprintln(w, "2026-02-09T10:00:02 \x1b[31mTest 1 failed\x1b[0m")
			fmt.Fprintln(w, "2026-02-09T10:00:03 ##[error]Assert.Equal failed: expected 42, got 0")
			fmt.Fprintln(w, "2026-02-09T10:00:04 at TestCalculate (calc_test.go:15)")
			fmt.Fprintln(w, "2026-02-09T10:00:05 Cleaning up")

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{ID: "1234", Organization: "testorg", Project: "testproject"}

	logs, err := b.GetBuildLogs(context.Background(), pr, "200")
	require.NoError(t, err)
	assert.Contains(t, logs, "Failed Task: Run Tests")
	assert.Contains(t, logs, "##[error]Assert.Equal failed")
	assert.Contains(t, logs, "Tests failed")
	// Verify ANSI codes are stripped.
	assert.NotContains(t, logs, "\x1b[31m")
	assert.NotContains(t, logs, "\x1b[0m")
}

func TestRateLimiting(t *testing.T) {
	attempt := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		// Succeed on third attempt.
		resp := adoPullRequest{
			PullRequestID: 1,
			Title:         "Retry PR",
			Status:        "active",
			SourceRefName: "refs/heads/feature",
			TargetRefName: "refs/heads/main",
			CreatedBy:     adoIdentity{DisplayName: "User"},
		}
		resp.Repository.Name = "repo"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	b := newTestBackend(t, server)

	pr, err := b.GetPR(context.Background(), "1")
	require.NoError(t, err)
	assert.Equal(t, "Retry PR", pr.Title)
	assert.Equal(t, 3, attempt) // 2 retries + 1 success
}

func TestName(t *testing.T) {
	b := NewBackend("org", "proj", NewAuthProvider("pat"))
	assert.Equal(t, "ado", b.Name())
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"\x1b[31mred text\x1b[0m", "red text"},
		{"\x1b[1;32mbold green\x1b[0m", "bold green"},
		{"no ansi here", "no ansi here"},
		{"\x1b[0m\x1b[31m\x1b[1m", ""},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, stripANSI(tt.input))
	}
}

func TestExtractErrorContext(t *testing.T) {
	t.Run("with error markers", func(t *testing.T) {
		var lines []string
		for i := 0; i < 20; i++ {
			lines = append(lines, fmt.Sprintf("line %d: normal output", i))
		}
		lines[10] = "line 10: ##[error]Something went wrong"
		log := strings.Join(lines, "\n")

		result := extractErrorContext(log)
		assert.Contains(t, result, "##[error]Something went wrong")
		// Should include context lines.
		assert.Contains(t, result, "line 5: normal output")
		assert.Contains(t, result, "line 15: normal output")
	})

	t.Run("without error markers", func(t *testing.T) {
		var lines []string
		for i := 0; i < 100; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i))
		}
		log := strings.Join(lines, "\n")

		result := extractErrorContext(log)
		// Should return last 50 lines as fallback.
		assert.Contains(t, result, "line 99")
		assert.Contains(t, result, "line 50")
		assert.NotContains(t, result, "line 49")
	})
}

func TestParsePRIdentifier(t *testing.T) {
	b := NewBackend("org", "proj", NewAuthProvider("pat"))

	t.Run("numeric ID", func(t *testing.T) {
		prID, repo, org, project := b.parsePRIdentifier("1234")
		assert.Equal(t, "1234", prID)
		assert.Empty(t, repo)
		assert.Empty(t, org)
		assert.Empty(t, project)
	})

	t.Run("full URL", func(t *testing.T) {
		prID, repo, org, project := b.parsePRIdentifier(
			"https://dev.azure.com/myorg/myproject/_git/myrepo/pullrequest/5678")
		assert.Equal(t, "5678", prID)
		assert.Equal(t, "myrepo", repo)
		assert.Equal(t, "myorg", org)
		assert.Equal(t, "myproject", project)
	})

	t.Run("invalid", func(t *testing.T) {
		prID, _, _, _ := b.parsePRIdentifier("not-valid")
		assert.Empty(t, prID)
	})
}

func TestWorkflowSubmitUnsupported(t *testing.T) {
	b := NewBackend("org", "proj", NewAuthProvider("pat"))
	pr := &provider.PRInfo{ID: "1"}

	err := b.RunWorkflow(context.Background(), pr, provider.WorkflowSubmit)
	assert.ErrorIs(t, err, provider.ErrUnsupported)
}

func TestWorkflowAutoComplete(t *testing.T) {
	requestLog := make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/connectiondata"):
			requestLog["connectiondata"]++
			resp := adoConnectionData{
				AuthenticatedUser: adoIdentity{
					ID:          "user-id-123",
					DisplayName: "Test User",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		case strings.Contains(r.URL.Path, "/pullrequests/") && r.Method == http.MethodPatch:
			requestLog["patchPR"]++

			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)

			// Verify autoCompleteSetBy.
			acBy, ok := body["autoCompleteSetBy"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "user-id-123", acBy["id"])

			// Verify completionOptions.
			opts, ok := body["completionOptions"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "squash", opts["mergeStrategy"])

			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{ID: "1234", RepoID: "testrepo", Organization: "testorg", Project: "testproject"}

	err := b.RunWorkflow(context.Background(), pr, provider.WorkflowAutoComplete)
	require.NoError(t, err)
	assert.Equal(t, 1, requestLog["connectiondata"])
	assert.Equal(t, 1, requestLog["patchPR"])
}

func TestWorkflowCreateWorkItem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{
		ID:           "1234",
		Title:        "My PR",
		URL:          "https://dev.azure.com/org/proj/_git/repo/pullrequest/1234",
		Organization: "testorg",
		Project:      "testproject",
	}

	err := b.RunWorkflow(context.Background(), pr, provider.WorkflowCreateWorkItem)
	require.ErrorIs(t, err, provider.ErrUnsupported)
}

func TestWorkflowAddressBot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := adoThreadList{
			Value: []adoThread{
				{
					ID:     1,
					Status: 1,
					Comments: []adoComment{
						{ID: 1, Content: "Bot says fix this", Author: adoIdentity{DisplayName: "MerlinBot"}, CommentType: "text"},
					},
				},
				{
					ID:     2,
					Status: 1,
					Comments: []adoComment{
						{ID: 1, Content: "Human comment", Author: adoIdentity{DisplayName: "Human"}, CommentType: "text"},
					},
				},
				{
					ID:     3,
					Status: 2, // resolved
					Comments: []adoComment{
						{ID: 1, Content: "Old bot message", Author: adoIdentity{DisplayName: "MerlinBot"}, CommentType: "text"},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	b := newTestBackend(t, server)
	pr := &provider.PRInfo{ID: "1234", RepoID: "testrepo", Organization: "testorg", Project: "testproject"}

	err := b.RunWorkflow(context.Background(), pr, provider.WorkflowAddressBot)
	require.NoError(t, err)
	// This currently just logs; no error expected.
}
