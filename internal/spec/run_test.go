package spec

import (
	"context"
	"testing"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecRun(t *testing.T) {
	repoDir := setupSpecDir(t, "run-test", "requirements.md", "research.md", "design.md")

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "Here is the analysis result."

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	result, err := SpecRun(context.Background(), mock, cfg, repoDir, "run-test", "Analyze this spec")
	require.NoError(t, err)
	assert.Equal(t, "Here is the analysis result.", result)

	// Verify prompt was sent with spec context.
	history := mock.GetPromptHistory()
	require.Len(t, history, 1)
	assert.Contains(t, history[0].Prompt, "Analyze this spec")
	assert.Contains(t, history[0].Prompt, "Requirements")
}

func TestSpecRun_MinimalContext(t *testing.T) {
	// Spec with only requirements.
	repoDir := setupSpecDir(t, "run-minimal", "requirements.md")

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "Response"

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	result, err := SpecRun(context.Background(), mock, cfg, repoDir, "run-minimal", "Do something")
	require.NoError(t, err)
	assert.Equal(t, "Response", result)
}
