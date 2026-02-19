package spec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSpecDir(t *testing.T, slug string, artifacts ...string) string {
	t.Helper()
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", slug)
	require.NoError(t, os.MkdirAll(filepath.Join(specDir, "history"), 0755))
	for _, a := range artifacts {
		require.NoError(t, os.WriteFile(filepath.Join(specDir, a), []byte("# "+a), 0644))
	}
	return dir
}

func TestLoadSpec_Exists(t *testing.T) {
	repoDir := setupSpecDir(t, "my-feature", "requirements.md")

	s, err := LoadSpec("my-feature", repoDir)
	require.NoError(t, err)
	assert.Equal(t, "my-feature", s.Slug)
	assert.Contains(t, s.Dir, "my-feature")
	assert.Contains(t, s.RequirementsPath, "requirements.md")
	assert.Contains(t, s.HistoryDir, "history")
}

func TestLoadSpec_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadSpec("nonexistent", dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestListSpecs_Empty(t *testing.T) {
	dir := t.TempDir()
	specs, err := ListSpecs(dir)
	require.NoError(t, err)
	assert.Empty(t, specs)
}

func TestListSpecs_Multiple(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".otto", "specs")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "alpha", "history"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "beta", "history"), 0755))

	specs, err := ListSpecs(dir)
	require.NoError(t, err)
	assert.Len(t, specs, 2)

	slugs := []string{specs[0].Slug, specs[1].Slug}
	assert.Contains(t, slugs, "alpha")
	assert.Contains(t, slugs, "beta")
}

func TestResolveSpec_SingleSpec(t *testing.T) {
	repoDir := setupSpecDir(t, "only-one")

	// Empty slug should resolve to the only spec.
	s, err := ResolveSpec("", repoDir)
	require.NoError(t, err)
	assert.Equal(t, "only-one", s.Slug)
}

func TestResolveSpec_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".otto", "specs")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "add-auth", "history"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "add-auth-v2", "history"), 0755))

	s, err := ResolveSpec("add-auth", dir)
	require.NoError(t, err)
	assert.Equal(t, "add-auth", s.Slug)
}

func TestResolveSpec_PrefixMatch(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".otto", "specs")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "rate-limiting", "history"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "add-auth", "history"), 0755))

	s, err := ResolveSpec("rate", dir)
	require.NoError(t, err)
	assert.Equal(t, "rate-limiting", s.Slug)
}

func TestResolveSpec_AmbiguousPrefix(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".otto", "specs")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "add-auth", "history"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "add-logging", "history"), 0755))

	_, err := ResolveSpec("add", dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestResolveSpec_EmptyMultiple(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".otto", "specs")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "history"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "b", "history"), 0755))

	_, err := ResolveSpec("", dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple specs")
}

func TestResolveSpec_NoSpecs(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveSpec("", dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no specs found")
}

func TestResolveSpec_NotFound(t *testing.T) {
	repoDir := setupSpecDir(t, "alpha")

	_, err := ResolveSpec("nonexistent", repoDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCreateSpecDir(t *testing.T) {
	dir := t.TempDir()
	err := CreateSpecDir("new-feature", dir)
	require.NoError(t, err)

	specDir := filepath.Join(dir, ".otto", "specs", "new-feature")
	info, err := os.Stat(specDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	histDir := filepath.Join(specDir, "history")
	info, err = os.Stat(histDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestCreateSpecDir_Idempotent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, CreateSpecDir("idem", dir))
	require.NoError(t, CreateSpecDir("idem", dir))
}

func TestHasArtifacts(t *testing.T) {
	repoDir := setupSpecDir(t, "full", "requirements.md", "research.md", "design.md", "tasks.md", "questions.md")

	s, err := LoadSpec("full", repoDir)
	require.NoError(t, err)

	assert.True(t, s.HasRequirements())
	assert.True(t, s.HasResearch())
	assert.True(t, s.HasDesign())
	assert.True(t, s.HasTasks())
	assert.True(t, s.HasQuestions())
}

func TestHasArtifacts_Partial(t *testing.T) {
	repoDir := setupSpecDir(t, "partial", "requirements.md")

	s, err := LoadSpec("partial", repoDir)
	require.NoError(t, err)

	assert.True(t, s.HasRequirements())
	assert.False(t, s.HasResearch())
	assert.False(t, s.HasDesign())
	assert.False(t, s.HasTasks())
	assert.False(t, s.HasQuestions())
}

func TestHasArtifacts_Empty(t *testing.T) {
	repoDir := setupSpecDir(t, "empty")

	s, err := LoadSpec("empty", repoDir)
	require.NoError(t, err)

	assert.False(t, s.HasRequirements())
	assert.False(t, s.HasResearch())
	assert.False(t, s.HasDesign())
	assert.False(t, s.HasTasks())
	assert.False(t, s.HasQuestions())
}

func TestGenerateSlug(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "simple",
			prompt: "Add JWT authentication",
			want:   "add-jwt-authentication",
		},
		{
			name:   "special chars",
			prompt: "Add rate limiting (v2) for API!",
			want:   "add-rate-limiting-v2-for-api",
		},
		{
			name:   "leading/trailing spaces",
			prompt: "  fix auth  ",
			want:   "fix-auth",
		},
		{
			name:   "multiple spaces",
			prompt: "add   lots   of   spaces",
			want:   "add-lots-of-spaces",
		},
		{
			name:   "long prompt truncated",
			prompt: "This is a very long prompt that should be truncated to fifty characters maximum length",
			want:   "this-is-a-very-long-prompt-that-should-be-truncate",
		},
		{
			name:   "empty prompt",
			prompt: "",
			want:   "spec",
		},
		{
			name:   "only special chars",
			prompt: "!@#$%^&*()",
			want:   "spec",
		},
		{
			name:   "mixed case",
			prompt: "Add JWT Auth Middleware",
			want:   "add-jwt-auth-middleware",
		},
		{
			name:   "truncation removes trailing hyphen",
			prompt: "this is exactly fifty characters long which is cool-stuff",
			want:   "this-is-exactly-fifty-characters-long-which-is-coo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateSlug(tt.prompt)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, len(got), 50)
		})
	}
}

func TestSanitizeLLMSlug(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "clean slug",
			raw:  "fault-injection",
			want: "fault-injection",
		},
		{
			name: "with backticks",
			raw:  "`fault-injection`",
			want: "fault-injection",
		},
		{
			name: "with quotes",
			raw:  `"fault-injection"`,
			want: "fault-injection",
		},
		{
			name: "with explanation after newline",
			raw:  "fault-injection\nThis slug captures the core concept.",
			want: "fault-injection",
		},
		{
			name: "uppercase normalized",
			raw:  "Fault-Injection",
			want: "fault-injection",
		},
		{
			name: "too long truncated",
			raw:  "add-fault-injection-capabilities-overlay",
			want: "add-fault-injection-capa",
		},
		{
			name: "trailing hyphen after truncation",
			raw:  "add-fault-injection-cap",
			want: "add-fault-injection-cap",
		},
		{
			name: "special chars removed",
			raw:  "fault_injection! (v2)",
			want: "fault-injection-v2",
		},
		{
			name: "empty returns empty",
			raw:  "",
			want: "",
		},
		{
			name: "whitespace with backticks",
			raw:  "  `az-fault-inject`  ",
			want: "az-fault-inject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeLLMSlug(tt.raw)
			assert.Equal(t, tt.want, got)
			if got != "" {
				assert.LessOrEqual(t, len(got), 24)
			}
		})
	}
}
