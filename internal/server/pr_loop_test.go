package server

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSavePRAndLoadPR(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	pr := &PRDocument{
		ID:             "123",
		Title:          "Test PR Title",
		Provider:       "github",
		Repo:           "owner/repo",
		Branch:         "feature-branch",
		Target:         "main",
		Status:         "watching",
		URL:            "https://github.com/owner/repo/pull/123",
		Created:        "2026-01-01T00:00:00Z",
		LastChecked:    "2026-01-02T00:00:00Z",
		FixAttempts:    2,
		MaxFixAttempts: 5,
		SeenCommentIDs: []string{"c1", "c2"},
		Body:           "# Test PR\n\nSome body content.",
	}

	err := SavePR(pr)
	assert.NoError(t, err)

	loaded, err := LoadPR("github", "123")
	assert.NoError(t, err)
	assert.Equal(t, pr.ID, loaded.ID)
	assert.Equal(t, pr.Title, loaded.Title)
	assert.Equal(t, pr.Provider, loaded.Provider)
	assert.Equal(t, pr.Repo, loaded.Repo)
	assert.Equal(t, pr.Branch, loaded.Branch)
	assert.Equal(t, pr.Target, loaded.Target)
	assert.Equal(t, pr.Status, loaded.Status)
	assert.Equal(t, pr.URL, loaded.URL)
	assert.Equal(t, pr.Created, loaded.Created)
	assert.Equal(t, pr.LastChecked, loaded.LastChecked)
	assert.Equal(t, pr.FixAttempts, loaded.FixAttempts)
	assert.Equal(t, pr.MaxFixAttempts, loaded.MaxFixAttempts)
	assert.Equal(t, pr.SeenCommentIDs, loaded.SeenCommentIDs)
	assert.Contains(t, loaded.Body, "Test PR")
}

func TestListPRs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	pr1 := &PRDocument{ID: "1", Provider: "github", Status: "watching"}
	pr2 := &PRDocument{ID: "2", Provider: "github", Status: "watching"}
	assert.NoError(t, SavePR(pr1))
	assert.NoError(t, SavePR(pr2))

	prs, err := ListPRs()
	assert.NoError(t, err)
	assert.Len(t, prs, 2)
}

func TestListPRsEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	prs, err := ListPRs()
	assert.NoError(t, err)
	assert.Nil(t, prs)
}

func TestDeletePR(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	pr := &PRDocument{ID: "42", Provider: "ado", Status: "watching"}
	assert.NoError(t, SavePR(pr))

	_, err := LoadPR("ado", "42")
	assert.NoError(t, err)

	assert.NoError(t, DeletePR("ado", "42"))

	_, err = LoadPR("ado", "42")
	assert.Error(t, err)
}

func TestFindPR(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	pr1 := &PRDocument{ID: "100", Provider: "github", Status: "watching"}
	pr2 := &PRDocument{ID: "200", Provider: "github", Status: "fixing"}
	assert.NoError(t, SavePR(pr1))
	assert.NoError(t, SavePR(pr2))

	found, err := FindPR("100")
	assert.NoError(t, err)
	assert.Equal(t, "100", found.ID)

	found, err = FindPR("200")
	assert.NoError(t, err)
	assert.Equal(t, "200", found.ID)
}

func TestFindPRNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	_, err := FindPR("nonexistent")
	assert.Error(t, err)
}

func TestInferPRSingle(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	pr := &PRDocument{ID: "solo", Provider: "github", Status: "watching"}
	assert.NoError(t, SavePR(pr))

	inferred, err := InferPR()
	assert.NoError(t, err)
	assert.Equal(t, "solo", inferred.ID)
}

func TestInferPRMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	assert.NoError(t, SavePR(&PRDocument{ID: "a", Provider: "github", Status: "watching"}))
	assert.NoError(t, SavePR(&PRDocument{ID: "b", Provider: "github", Status: "watching"}))

	_, err := InferPR()
	assert.Error(t, err)
}

func TestInferPRNone(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	_, err := InferPR()
	assert.Error(t, err)
}

func TestIsInfraFailure(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"exact match", "CLASSIFICATION: INFRASTRUCTURE\n\nSome details.", true},
		{"lowercase", "classification: infrastructure\n\nDetails.", true},
		{"mixed case", "Classification: Infrastructure\n\nDetails.", true},
		{"trailing text", "CLASSIFICATION: INFRASTRUCTURE - transient test failure\nDetails.", true},
		{"markdown bold", "**CLASSIFICATION: INFRASTRUCTURE**\n\nDetails.", true},
		{"backtick wrapped", "`CLASSIFICATION: INFRASTRUCTURE`\n\nDetails.", true},
		{"extra whitespace", "CLASSIFICATION:   INFRASTRUCTURE  \nDetails.", true},
		{"no space after colon", "CLASSIFICATION:INFRASTRUCTURE\nDetails.", true},
		{"preceded by blank lines", "\n\n\nCLASSIFICATION: INFRASTRUCTURE\nDetails.", true},
		{"code failure", "CLASSIFICATION: CODE\n\nCompilation error in main.go.", false},
		{"code lowercase", "classification: code\n\nTest failure.", false},
		{"no classification", "The build failed because of a network error.", false},
		{"empty string", "", false},
		{"preamble then classification", "Here is my analysis:\n\nCLASSIFICATION: INFRASTRUCTURE\n\nDetails.", true},
		{"markdown heading prefix", "## CLASSIFICATION: INFRASTRUCTURE\n\nDetails.", true},
		// Classification marker past line 10.
		{"marker buried deep", "Line1\nLine2\nLine3\nLine4\nLine5\nLine6\nLine7\nLine8\nLine9\nLine10\nLine11\nCLASSIFICATION: INFRASTRUCTURE\nDetails.", true},
		// Fallback heuristics — no CLASSIFICATION marker.
		{"fallback infra root cause with retry", "## Failure Summary\n\nBuild failed.\n\n### Root Cause Analysis\n\nBoth failures point to **infrastructure/environment issues**.\n\n### Recommended Action\n\n**Retry the build.** No code changes are indicated.", true},
		{"fallback infra diagnosis with no code changes", "## Diagnosis\n\nThe failure is an infrastructure issue.\n\nNo code changes are needed.", true},
		{"fallback code failure no infra signals", "## Failure Summary\n\nCompilation failed.\n\n### Root Cause\n\nType error in main.go.\n\n### Recommended Action\n\nFix the type mismatch.", false},
		{"fallback infra without retry or no-code-changes", "## Root Cause\n\nInfrastructure issue detected.\n\n### Action\n\nInvestigate the agent pool.", false},
		{"fallback retry without infra root cause", "## Summary\n\nTest failed.\n\n### Recommended Action\n\nRetry the build.", false},
		{"fallback recommendation heading with retry", "## Failure Summary\n\n**Build:** Azlocal-Overlay-PullRequest\n\n### Root Cause\n\nThe primary failure is consistent with a flaky or environment-dependent test infrastructure issue.\n\n### Recommendation\n\n**Retry the build.**", true},
		{"fallback retry-resolve phrasing", "## Failure Summary\n\n**Build:** Azlocal-Overlay-PullRequest\n\n### Root Cause\n\nThe failure points to a transient infrastructure issue in the Windows build container. **A retry is likely to resolve this.**", true},
		{"fallback transient build environment with retry", "## Failure Summary\n\n**Build:** Azlocal-Overlay-PullRequest\n\n### Root Cause\n\nBoth failures are characteristic of a **transient build environment issue**: the PowerShell unit test runner crashed or errored in a way unrelated to the PR's code changes.\n\n### Recommendation\n\n**Retry the build.** No code changes are indicated.", true},
		{"fallback transient environment without structural marker", "The failure is a transient environment glitch.\n\nRetry recommended.", false},
		{"fallback flaky test environment with recommended retry", "## Failure Summary\n\n**Build:** Azlocal-Overlay-PullRequest\n\n### Failed Tasks\n\n1. **Run PowerShell Scriptlets Unit Tests** — PowerShell exited with code '1' via Write-Error.\n\n### Root Cause\n\nThe PowerShell unit test runner failed with a non-specific WriteErrorException — this points to a **flaky test environment or transient test harness issue** rather than a code defect. A retry is the recommended next step.", true},
		{"fallback flaky environment without transient keyword", "## Failure Summary\n\n**Build:** Azlocal-Overlay-PullRequest\n\n### Root Cause\n\nThe test failures are consistent with a **flaky test environment** issue. No code changes in this PR are implicated.\n\n### Recommendation\n\n**Retry the build.**", true},
		{"fallback flaky test or transient environment cascading", "## Failure Summary\n\n**Build:** Azlocal-Overlay-PullRequest\n\n### Failed Tasks\n\n1. **Copy Test Logs to Output Directory** — `Not found SourceFolder: C:\\__w\\1\\a\\TestLogs`\n   The test log directory was never created, meaning an upstream step (the unit tests) failed or crashed before producing output artifacts.\n\n2. **Run PowerShell Scriptlets Unit Tests** — `PowerShell exited with code '1'` via `Write-Error` in a `Main` function.\n   The error is a generic `WriteErrorException` with no specific test assertion or code-change-related detail in the logs.\n\n### Root Cause\n\nThe PowerShell unit test runner failed with an unspecified `Write-Error`, which prevented test logs from being written to `C:\\__w\\1\\a\\TestLogs`. The \"Copy Test Logs\" failure is a **cascading side-effect** of the test runner failure. The logs show no compilation error, no missing import, and no reference to files changed in this PR — this points to a **flaky test or transient environment issue** in the Windows build container. A retry is the recommended next step.", true},
		{"fallback transient infrastructure or environment with retry-resolve", "## Failure Summary\n\n**Build:** Azlocal-Overlay-PullRequest\n\n### Failed Tasks\n\n1. **Copy Test Logs to Output Directory** — `Not found SourceFolder: C:\\__w\\1\\a\\TestLogs`\n   - The test logs directory was never created, meaning an upstream test step either failed or was skipped before it could produce output. This is a pipeline artifact-handling issue, not a code issue.\n\n2. **Run PowerShell Scriptlets Unit Tests** — `PowerShell exited with code '1'`\n   - The error is a generic `WriteErrorException` from a `Write-Error` call in a `Main` function. No specific test name or assertion failure tied to PR-changed files is present in the logs. The sparse output suggests an environment or setup failure rather than a logic bug.\n\n### Root Cause\n\nThe PowerShell unit test runner failed (likely a test environment/setup issue), which prevented `TestLogs` from being created. The subsequent \"Copy Test Logs\" step then failed because its expected source directory didn't exist. Both failures are consistent with a transient infrastructure or environment problem — **a retry would likely resolve this**.", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isInfraFailure(tt.input)
			assert.Equal(t, tt.expected, result, "input: %q", tt.input)
		})
	}
}

func TestPRFilename(t *testing.T) {
	name := prFilename("github", "123")
	assert.Equal(t, "github__123.md", name)
	assert.Contains(t, name, "__")

	// Verify full path includes the filename.
	path := prPath("github", "123")
	assert.Equal(t, name, filepath.Base(path))
}
