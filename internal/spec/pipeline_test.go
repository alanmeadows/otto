package spec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeSpec(t *testing.T, artifacts ...string) *Spec {
	t.Helper()
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "test-spec")
	require.NoError(t, os.MkdirAll(filepath.Join(specDir, "history"), 0755))
	for _, a := range artifacts {
		require.NoError(t, os.WriteFile(filepath.Join(specDir, a), []byte("# "+a), 0644))
	}
	s, err := LoadSpec("test-spec", dir)
	require.NoError(t, err)
	return s
}

func TestCheckPrerequisites_Requirements(t *testing.T) {
	s := makeSpec(t)
	assert.NoError(t, CheckPrerequisites(s, "requirements"))
}

func TestCheckPrerequisites_Research(t *testing.T) {
	t.Run("missing requirements", func(t *testing.T) {
		s := makeSpec(t)
		err := CheckPrerequisites(s, "research")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requirements.md")
		assert.Contains(t, err.Error(), "otto spec requirements")
	})

	t.Run("has requirements", func(t *testing.T) {
		s := makeSpec(t, "requirements.md")
		assert.NoError(t, CheckPrerequisites(s, "research"))
	})
}

func TestCheckPrerequisites_Design(t *testing.T) {
	t.Run("missing requirements", func(t *testing.T) {
		s := makeSpec(t)
		err := CheckPrerequisites(s, "design")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requirements.md")
	})

	t.Run("missing research", func(t *testing.T) {
		s := makeSpec(t, "requirements.md")
		err := CheckPrerequisites(s, "design")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "research.md")
		assert.Contains(t, err.Error(), "otto spec research")
	})

	t.Run("all present", func(t *testing.T) {
		s := makeSpec(t, "requirements.md", "research.md")
		assert.NoError(t, CheckPrerequisites(s, "design"))
	})
}

func TestCheckPrerequisites_TaskGenerate(t *testing.T) {
	t.Run("missing requirements", func(t *testing.T) {
		s := makeSpec(t)
		err := CheckPrerequisites(s, "task-generate")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requirements.md")
	})

	t.Run("missing research", func(t *testing.T) {
		s := makeSpec(t, "requirements.md")
		err := CheckPrerequisites(s, "task-generate")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "research.md")
	})

	t.Run("missing design", func(t *testing.T) {
		s := makeSpec(t, "requirements.md", "research.md")
		err := CheckPrerequisites(s, "task-generate")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "design.md")
		assert.Contains(t, err.Error(), "otto spec design")
	})

	t.Run("all present", func(t *testing.T) {
		s := makeSpec(t, "requirements.md", "research.md", "design.md")
		assert.NoError(t, CheckPrerequisites(s, "task-generate"))
	})
}

func TestCheckPrerequisites_Execute(t *testing.T) {
	t.Run("missing requirements", func(t *testing.T) {
		s := makeSpec(t)
		err := CheckPrerequisites(s, "execute")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requirements.md")
	})

	t.Run("missing research", func(t *testing.T) {
		s := makeSpec(t, "requirements.md")
		err := CheckPrerequisites(s, "execute")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "research.md")
	})

	t.Run("missing design", func(t *testing.T) {
		s := makeSpec(t, "requirements.md", "research.md")
		err := CheckPrerequisites(s, "execute")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "design.md")
	})

	t.Run("missing tasks", func(t *testing.T) {
		s := makeSpec(t, "requirements.md", "research.md", "design.md")
		err := CheckPrerequisites(s, "execute")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tasks.md")
		assert.Contains(t, err.Error(), "otto spec task generate")
	})

	t.Run("all present", func(t *testing.T) {
		s := makeSpec(t, "requirements.md", "research.md", "design.md", "tasks.md")
		assert.NoError(t, CheckPrerequisites(s, "execute"))
	})
}

func TestCheckPrerequisites_Run(t *testing.T) {
	// Run is exempt — no prerequisites.
	s := makeSpec(t)
	assert.NoError(t, CheckPrerequisites(s, "run"))
}

func TestCheckPrerequisites_UnknownCommand(t *testing.T) {
	s := makeSpec(t)
	err := CheckPrerequisites(s, "invalid-command")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}
