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

func TestPRFilename(t *testing.T) {
	name := prFilename("github", "123")
	assert.Equal(t, "github__123.md", name)
	assert.Contains(t, name, "__")

	// Verify full path includes the filename.
	path := prPath("github", "123")
	assert.Equal(t, name, filepath.Base(path))
}
