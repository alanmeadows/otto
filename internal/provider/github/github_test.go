package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gh "github.com/google/go-github/v82/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alanmeadows/otto/internal/provider"
)

// newTestBackend creates a Backend wired to a test HTTP server.
func newTestBackend(t *testing.T, handler http.Handler) (*Backend, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := gh.NewClient(nil).WithEnterpriseURLs(server.URL+"/", server.URL+"/")
	require.NoError(t, err)

	return &Backend{
		client:  client,
		owner:   "testowner",
		repo:    "testrepo",
		token:   "test-token",
		baseURL: server.URL,
	}, server
}

func TestName(t *testing.T) {
	b := &Backend{}
	assert.Equal(t, "github", b.Name())
}

func TestMatchesURL(t *testing.T) {
	b := &Backend{}
	tests := []struct {
		url     string
		matches bool
	}{
		{"https://github.com/owner/repo/pull/123", true},
		{"https://www.github.com/owner/repo/pull/456", true},
		{"https://github.com/owner/repo", true},
		{"https://dev.azure.com/org/project/_git/repo/pullrequest/1", false},
		{"https://gitlab.com/owner/repo", false},
		{"not-a-url", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			assert.Equal(t, tt.matches, b.MatchesURL(tt.url))
		})
	}
}

func TestParsePRIdentifier(t *testing.T) {
	b := &Backend{owner: "default-owner", repo: "default-repo"}

	tests := []struct {
		name    string
		input   string
		want    *prIdentifier
		wantErr bool
	}{
		{
			name:  "bare number",
			input: "42",
			want:  &prIdentifier{Owner: "default-owner", Repo: "default-repo", Number: 42},
		},
		{
			name:  "owner/repo#number",
			input: "myorg/myrepo#99",
			want:  &prIdentifier{Owner: "myorg", Repo: "myrepo", Number: 99},
		},
		{
			name:  "full URL",
			input: "https://github.com/someowner/somerepo/pull/123",
			want:  &prIdentifier{Owner: "someowner", Repo: "somerepo", Number: 123},
		},
		{
			name:    "invalid string",
			input:   "not-valid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := b.parsePRIdentifier(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestGetPR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number:  gh.Ptr(42),
			Title:   gh.Ptr("Test PR"),
			Body:    gh.Ptr("PR description"),
			State:   gh.Ptr("open"),
			HTMLURL: gh.Ptr("https://github.com/testowner/testrepo/pull/42"),
			Head: &gh.PullRequestBranch{
				Ref: gh.Ptr("feature-branch"),
				SHA: gh.Ptr("abc123"),
			},
			Base: &gh.PullRequestBranch{
				Ref: gh.Ptr("main"),
			},
			User: &gh.User{
				Login: gh.Ptr("testuser"),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	backend, _ := newTestBackend(t, mux)

	pr, err := backend.GetPR(t.Context(), "42")
	require.NoError(t, err)

	assert.Equal(t, "42", pr.ID)
	assert.Equal(t, "Test PR", pr.Title)
	assert.Equal(t, "PR description", pr.Description)
	assert.Equal(t, "active", pr.Status)
	assert.Equal(t, "feature-branch", pr.SourceBranch)
	assert.Equal(t, "main", pr.TargetBranch)
	assert.Equal(t, "testuser", pr.Author)
	assert.Equal(t, "https://github.com/testowner/testrepo/pull/42", pr.URL)
}

func TestGetPR_Merged(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/10", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number:  gh.Ptr(10),
			Title:   gh.Ptr("Merged PR"),
			State:   gh.Ptr("closed"),
			Merged:  gh.Ptr(true),
			HTMLURL: gh.Ptr("https://github.com/testowner/testrepo/pull/10"),
			Head:    &gh.PullRequestBranch{Ref: gh.Ptr("branch"), SHA: gh.Ptr("sha")},
			Base:    &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			User:    &gh.User{Login: gh.Ptr("u")},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	backend, _ := newTestBackend(t, mux)
	pr, err := backend.GetPR(t.Context(), "10")
	require.NoError(t, err)
	assert.Equal(t, "completed", pr.Status)
}

func TestGetPR_Closed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/11", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number:  gh.Ptr(11),
			Title:   gh.Ptr("Closed PR"),
			State:   gh.Ptr("closed"),
			Merged:  gh.Ptr(false),
			HTMLURL: gh.Ptr("https://github.com/testowner/testrepo/pull/11"),
			Head:    &gh.PullRequestBranch{Ref: gh.Ptr("branch"), SHA: gh.Ptr("sha")},
			Base:    &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			User:    &gh.User{Login: gh.Ptr("u")},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	backend, _ := newTestBackend(t, mux)
	pr, err := backend.GetPR(t.Context(), "11")
	require.NoError(t, err)
	assert.Equal(t, "abandoned", pr.Status)
}

func TestGetPR_FromURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/someowner/somerepo/pulls/99", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number:  gh.Ptr(99),
			Title:   gh.Ptr("URL PR"),
			State:   gh.Ptr("open"),
			HTMLURL: gh.Ptr("https://github.com/someowner/somerepo/pull/99"),
			Head:    &gh.PullRequestBranch{Ref: gh.Ptr("branch"), SHA: gh.Ptr("sha")},
			Base:    &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			User:    &gh.User{Login: gh.Ptr("u")},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	backend, _ := newTestBackend(t, mux)
	pr, err := backend.GetPR(t.Context(), "https://github.com/someowner/somerepo/pull/99")
	require.NoError(t, err)
	assert.Equal(t, "99", pr.ID)
	assert.Equal(t, "someowner", pr.Organization)
	assert.Equal(t, "somerepo", pr.RepoID)
}

func TestGetPipelineStatus(t *testing.T) {
	mux := http.NewServeMux()

	// PR endpoint to get head SHA.
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number: gh.Ptr(1),
			Head:   &gh.PullRequestBranch{SHA: gh.Ptr("abc123")},
			Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			User:   &gh.User{Login: gh.Ptr("u")},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	// Check runs endpoint.
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/commits/abc123/check-runs", func(w http.ResponseWriter, r *http.Request) {
		result := gh.ListCheckRunsResults{
			Total: gh.Ptr(2),
			CheckRuns: []*gh.CheckRun{
				{
					ID:         gh.Ptr(int64(100)),
					Name:       gh.Ptr("CI Build"),
					Status:     gh.Ptr("completed"),
					Conclusion: gh.Ptr("success"),
					HTMLURL:    gh.Ptr("https://github.com/testowner/testrepo/runs/100"),
				},
				{
					ID:         gh.Ptr(int64(101)),
					Name:       gh.Ptr("Lint"),
					Status:     gh.Ptr("completed"),
					Conclusion: gh.Ptr("failure"),
					HTMLURL:    gh.Ptr("https://github.com/testowner/testrepo/runs/101"),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// Combined status endpoint.
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/commits/abc123/status", func(w http.ResponseWriter, r *http.Request) {
		status := gh.CombinedStatus{
			State:    gh.Ptr("success"),
			Statuses: []*gh.RepoStatus{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	})

	backend, _ := newTestBackend(t, mux)
	prInfo := &provider.PRInfo{ID: "1"}

	status, err := backend.GetPipelineStatus(t.Context(), prInfo)
	require.NoError(t, err)

	assert.Equal(t, "failed", status.State)
	assert.Len(t, status.Builds, 2)
	assert.Equal(t, "CI Build", status.Builds[0].Name)
	assert.Equal(t, "Lint", status.Builds[1].Name)
}

func TestGetPipelineStatus_AllSucceeded(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number: gh.Ptr(1),
			Head:   &gh.PullRequestBranch{SHA: gh.Ptr("abc123")},
			Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			User:   &gh.User{Login: gh.Ptr("u")},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/commits/abc123/check-runs", func(w http.ResponseWriter, r *http.Request) {
		result := gh.ListCheckRunsResults{
			Total: gh.Ptr(1),
			CheckRuns: []*gh.CheckRun{
				{ID: gh.Ptr(int64(1)), Name: gh.Ptr("CI"), Status: gh.Ptr("completed"), Conclusion: gh.Ptr("success")},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/commits/abc123/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(gh.CombinedStatus{State: gh.Ptr("success"), Statuses: []*gh.RepoStatus{}})
	})

	backend, _ := newTestBackend(t, mux)
	status, err := backend.GetPipelineStatus(t.Context(), &provider.PRInfo{ID: "1"})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", status.State)
}

func TestGetPipelineStatus_NoBuilds(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number: gh.Ptr(1),
			Head:   &gh.PullRequestBranch{SHA: gh.Ptr("abc123")},
			Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			User:   &gh.User{Login: gh.Ptr("u")},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/commits/abc123/check-runs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(gh.ListCheckRunsResults{Total: gh.Ptr(0), CheckRuns: []*gh.CheckRun{}})
	})

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/commits/abc123/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(gh.CombinedStatus{State: gh.Ptr("pending"), Statuses: []*gh.RepoStatus{}})
	})

	backend, _ := newTestBackend(t, mux)
	status, err := backend.GetPipelineStatus(t.Context(), &provider.PRInfo{ID: "1"})
	require.NoError(t, err)
	assert.Equal(t, "pending", status.State)
}

func TestGetComments(t *testing.T) {
	mux := http.NewServeMux()

	// Issue comments.
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		comments := []*gh.IssueComment{
			{
				ID:   gh.Ptr(int64(201)),
				Body: gh.Ptr("General comment"),
				User: &gh.User{Login: gh.Ptr("alice")},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(comments)
	})

	// Review comments.
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/5/comments", func(w http.ResponseWriter, r *http.Request) {
		comments := []*gh.PullRequestComment{
			{
				ID:   gh.Ptr(int64(301)),
				Body: gh.Ptr("Inline comment"),
				Path: gh.Ptr("main.go"),
				Line: gh.Ptr(10),
				User: &gh.User{Login: gh.Ptr("bob")},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(comments)
	})

	backend, _ := newTestBackend(t, mux)
	prInfo := &provider.PRInfo{ID: "5"}

	comments, err := backend.GetComments(t.Context(), prInfo)
	require.NoError(t, err)

	assert.Len(t, comments, 2)

	// Issue comment.
	assert.Equal(t, "201", comments[0].ID)
	assert.Equal(t, "General comment", comments[0].Body)
	assert.Equal(t, "alice", comments[0].Author)
	assert.Empty(t, comments[0].FilePath)

	// Review comment.
	assert.Equal(t, "301", comments[1].ID)
	assert.Equal(t, "Inline comment", comments[1].Body)
	assert.Equal(t, "bob", comments[1].Author)
	assert.Equal(t, "main.go", comments[1].FilePath)
	assert.Equal(t, 10, comments[1].Line)
}

func TestPostComment(t *testing.T) {
	var receivedBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/testowner/testrepo/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		var comment gh.IssueComment
		json.NewDecoder(r.Body).Decode(&comment)
		receivedBody = comment.GetBody()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(&gh.IssueComment{ID: gh.Ptr(int64(999)), Body: comment.Body})
	})

	backend, _ := newTestBackend(t, mux)
	err := backend.PostComment(t.Context(), &provider.PRInfo{ID: "5"}, "Hello from Otto!")
	require.NoError(t, err)
	assert.Equal(t, "Hello from Otto!", receivedBody)
}

func TestPostInlineComment(t *testing.T) {
	var receivedReview gh.PullRequestReviewRequest
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/5", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number: gh.Ptr(5),
			Head:   &gh.PullRequestBranch{SHA: gh.Ptr("headsha123")},
			Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			User:   &gh.User{Login: gh.Ptr("u")},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	mux.HandleFunc("POST /api/v3/repos/testowner/testrepo/pulls/5/reviews", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedReview)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(&gh.PullRequestReview{ID: gh.Ptr(int64(1))})
	})

	backend, _ := newTestBackend(t, mux)
	err := backend.PostInlineComment(t.Context(), &provider.PRInfo{ID: "5"}, provider.InlineComment{
		FilePath: "main.go",
		Line:     42,
		Body:     "Fix this line",
		Side:     "right",
	})
	require.NoError(t, err)

	assert.Equal(t, "headsha123", receivedReview.GetCommitID())
	assert.Equal(t, "COMMENT", receivedReview.GetEvent())
	require.Len(t, receivedReview.Comments, 1)
	assert.Equal(t, "main.go", receivedReview.Comments[0].GetPath())
	assert.Equal(t, 42, receivedReview.Comments[0].GetLine())
	assert.Equal(t, "RIGHT", receivedReview.Comments[0].GetSide())
	assert.Equal(t, "Fix this line", receivedReview.Comments[0].GetBody())
}

func TestPostInlineComment_LeftSide(t *testing.T) {
	var receivedReview gh.PullRequestReviewRequest
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/5", func(w http.ResponseWriter, r *http.Request) {
		pr := gh.PullRequest{
			Number: gh.Ptr(5),
			Head:   &gh.PullRequestBranch{SHA: gh.Ptr("sha")},
			Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			User:   &gh.User{Login: gh.Ptr("u")},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})

	mux.HandleFunc("POST /api/v3/repos/testowner/testrepo/pulls/5/reviews", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedReview)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(&gh.PullRequestReview{ID: gh.Ptr(int64(1))})
	})

	backend, _ := newTestBackend(t, mux)
	err := backend.PostInlineComment(t.Context(), &provider.PRInfo{ID: "5"}, provider.InlineComment{
		FilePath: "old.go",
		Line:     15,
		Body:     "Deleted line comment",
		Side:     "LEFT",
	})
	require.NoError(t, err)
	require.Len(t, receivedReview.Comments, 1)
	assert.Equal(t, "LEFT", receivedReview.Comments[0].GetSide())
}

func TestReplyToComment(t *testing.T) {
	var replyCalled bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/testowner/testrepo/pulls/5/comments", func(w http.ResponseWriter, r *http.Request) {
		replyCalled = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(&gh.PullRequestComment{
			ID:   gh.Ptr(int64(500)),
			Body: gh.Ptr("Reply body"),
		})
	})

	backend, _ := newTestBackend(t, mux)
	err := backend.ReplyToComment(t.Context(), &provider.PRInfo{ID: "5"}, "400", "Reply body")
	require.NoError(t, err)
	assert.True(t, replyCalled)
}

func TestReplyToComment_InvalidID(t *testing.T) {
	backend := &Backend{owner: "o", repo: "r"}
	err := backend.ReplyToComment(t.Context(), &provider.PRInfo{ID: "5"}, "not-a-number", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid thread/comment ID")
}

func TestRunWorkflow_AllUnsupported(t *testing.T) {
	backend := &Backend{}
	actions := []provider.WorkflowAction{
		provider.WorkflowSubmit,
		provider.WorkflowAutoComplete,
		provider.WorkflowCreateWorkItem,
		provider.WorkflowAddressBot,
	}

	for _, action := range actions {
		err := backend.RunWorkflow(t.Context(), &provider.PRInfo{ID: "1"}, action)
		assert.ErrorIs(t, err, provider.ErrUnsupported)
	}
}

func TestGetBuildLogs_FailedJobs(t *testing.T) {
	logContent := "Step 1: setup\nStep 2: build\n##[error] compilation failed\nStep 3: done\n"
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/actions/runs/1000/jobs", func(w http.ResponseWriter, r *http.Request) {
		jobs := gh.Jobs{
			TotalCount: gh.Ptr(2),
			Jobs: []*gh.WorkflowJob{
				{
					ID:         gh.Ptr(int64(2001)),
					Name:       gh.Ptr("Build"),
					Conclusion: gh.Ptr("failure"),
					Steps: []*gh.TaskStep{
						{Name: gh.Ptr("Compile"), Conclusion: gh.Ptr("failure")},
					},
				},
				{
					ID:         gh.Ptr(int64(2002)),
					Name:       gh.Ptr("Lint"),
					Conclusion: gh.Ptr("success"),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	})

	// Job log download — return a redirect to a log URL on the same server.
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/actions/jobs/2001/logs", func(w http.ResponseWriter, r *http.Request) {
		// The go-github SDK follows redirects, so just return the log directly.
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, logContent)
	})

	backend, _ := newTestBackend(t, mux)

	result, err := backend.GetBuildLogs(t.Context(), &provider.PRInfo{ID: "5"}, "1000")
	require.NoError(t, err)

	assert.Contains(t, result, "Failed Job: Build")
	assert.Contains(t, result, "Failed Step: Compile")
}

func TestGetBuildLogs_NoFailedJobs(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/actions/runs/1000/jobs", func(w http.ResponseWriter, r *http.Request) {
		jobs := gh.Jobs{
			TotalCount: gh.Ptr(1),
			Jobs: []*gh.WorkflowJob{
				{ID: gh.Ptr(int64(2001)), Name: gh.Ptr("Build"), Conclusion: gh.Ptr("success")},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	})

	backend, _ := newTestBackend(t, mux)
	result, err := backend.GetBuildLogs(t.Context(), &provider.PRInfo{ID: "5"}, "1000")
	require.NoError(t, err)
	assert.Equal(t, "No failed jobs found in workflow run.", result)
}

func TestGetBuildLogs_InvalidBuildID(t *testing.T) {
	backend := &Backend{owner: "o", repo: "r"}
	_, err := backend.GetBuildLogs(t.Context(), &provider.PRInfo{ID: "5"}, "not-a-number")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid build/run ID")
}

func TestStripANSI(t *testing.T) {
	input := "\x1b[31mERROR\x1b[0m: something failed"
	assert.Equal(t, "ERROR: something failed", stripANSI(input))
}

func TestExtractErrorContext(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("line %d: normal output", i))
	}
	lines[10] = "##[error] compilation failed at line 10"

	log := strings.Join(lines, "\n")
	result := extractErrorContext(log)

	assert.Contains(t, result, "##[error] compilation failed")
	assert.Contains(t, result, "line 5: normal output")
	assert.Contains(t, result, "line 15: normal output")
}

func TestExtractErrorContext_NoErrors(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d: normal output", i))
	}
	log := strings.Join(lines, "\n")
	result := extractErrorContext(log)

	// Should return last 50 lines.
	assert.Contains(t, result, "line 99: normal output")
	assert.Contains(t, result, "line 50: normal output")
	assert.NotContains(t, result, "line 49: normal output")
}

func TestMapPR_StatusMapping(t *testing.T) {
	b := &Backend{}

	tests := []struct {
		name       string
		state      string
		merged     bool
		wantStatus string
	}{
		{"open PR", "open", false, "active"},
		{"merged PR", "closed", true, "completed"},
		{"closed PR", "closed", false, "abandoned"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &gh.PullRequest{
				Number:  gh.Ptr(1),
				State:   gh.Ptr(tt.state),
				Merged:  gh.Ptr(tt.merged),
				HTMLURL: gh.Ptr("https://github.com/o/r/pull/1"),
				Head:    &gh.PullRequestBranch{Ref: gh.Ptr("b")},
				Base:    &gh.PullRequestBranch{Ref: gh.Ptr("main")},
				User:    &gh.User{Login: gh.Ptr("u")},
			}
			result := b.mapPR(pr, "o", "r")
			assert.Equal(t, tt.wantStatus, result.Status)
		})
	}
}

func TestResolveComment_InvalidResolution(t *testing.T) {
	backend := &Backend{token: "test"}
	err := backend.ResolveComment(t.Context(), &provider.PRInfo{ID: "1"}, "PRRT_123", provider.ResolutionUnknown)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid comment resolution")
}

func TestGetComments_Pagination(t *testing.T) {
	page := 0
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		page++
		comments := []*gh.IssueComment{
			{ID: gh.Ptr(int64(page * 100)), Body: gh.Ptr(fmt.Sprintf("Comment page %d", page)), User: &gh.User{Login: gh.Ptr("u")}},
		}
		if page < 2 {
			// Link header for pagination.
			nextURL := fmt.Sprintf("<%s%s?page=2>; rel=\"next\"", r.Host, r.URL.Path)
			w.Header().Set("Link", nextURL)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(comments)
	})

	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/5/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*gh.PullRequestComment{})
	})

	backend, _ := newTestBackend(t, mux)
	comments, err := backend.GetComments(t.Context(), &provider.PRInfo{ID: "5"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(comments), 1)
}

func TestGetPR_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/testowner/testrepo/pulls/404", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	})

	backend, _ := newTestBackend(t, mux)
	_, err := backend.GetPR(t.Context(), "404")
	assert.Error(t, err)
}

func TestResolveOwnerRepo(t *testing.T) {
	b := &Backend{owner: "default-owner", repo: "default-repo"}

	// Uses defaults when PR has empty values.
	owner, repo := b.resolveOwnerRepo(&provider.PRInfo{})
	assert.Equal(t, "default-owner", owner)
	assert.Equal(t, "default-repo", repo)

	// Uses PR values when set.
	owner, repo = b.resolveOwnerRepo(&provider.PRInfo{Organization: "pr-owner", RepoID: "pr-repo"})
	assert.Equal(t, "pr-owner", owner)
	assert.Equal(t, "pr-repo", repo)
}

// Compile-time interface check.
func TestBackendImplementsPRBackend(t *testing.T) {
	var _ provider.PRBackend = (*Backend)(nil)
}
