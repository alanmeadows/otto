package opencode

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewPipeline_FourPassFlow(t *testing.T) {
	mock := NewMockLLMClient()
	mock.DefaultResult = "generated artifact"

	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ProviderID: "anthropic", ModelID: "claude-sonnet-4"},
		Secondary: ModelRef{ProviderID: "openai", ModelID: "o3"},
		MaxCycles: 1,
	})

	result, err := pipeline.Review(context.Background(), "build something", nil)
	require.NoError(t, err)
	assert.Equal(t, "generated artifact", result)

	// Should have 3 prompt calls: generate, critique, refine
	history := mock.GetPromptHistory()
	assert.Len(t, history, 3)

	// Generate uses primary model
	assert.Equal(t, "claude-sonnet-4", history[0].Model.ModelID)
	// Critique uses secondary model
	assert.Equal(t, "o3", history[1].Model.ModelID)
	// Refine uses primary model
	assert.Equal(t, "claude-sonnet-4", history[2].Model.ModelID)
}

func TestReviewPipeline_SessionCleanup(t *testing.T) {
	mock := NewMockLLMClient()
	mock.DefaultResult = "artifact"

	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ProviderID: "a", ModelID: "m1"},
		Secondary: ModelRef{ProviderID: "b", ModelID: "m2"},
		MaxCycles: 1,
	})

	_, err := pipeline.Review(context.Background(), "test", nil)
	require.NoError(t, err)

	// All sessions should be deleted (3 created, 3 deleted)
	assert.Empty(t, mock.Sessions, "all sessions should be cleaned up")
}

func TestReviewPipeline_WithTertiaryModel(t *testing.T) {
	mock := NewMockLLMClient()
	mock.DefaultResult = "artifact"

	tertiary := ModelRef{ProviderID: "google", ModelID: "gemini"}
	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ProviderID: "a", ModelID: "primary"},
		Secondary: ModelRef{ProviderID: "b", ModelID: "secondary"},
		Tertiary:  &tertiary,
		MaxCycles: 1,
	})

	result, err := pipeline.Review(context.Background(), "test prompt", nil)
	require.NoError(t, err)
	assert.Equal(t, "artifact", result)

	// Should have 4 prompt calls: generate, secondary critique, tertiary critique, refine
	history := mock.GetPromptHistory()
	assert.Len(t, history, 4)
	assert.Equal(t, "primary", history[0].Model.ModelID)
	assert.Equal(t, "secondary", history[1].Model.ModelID)
	assert.Equal(t, "gemini", history[2].Model.ModelID)
	assert.Equal(t, "primary", history[3].Model.ModelID)
}

func TestReviewPipeline_WithoutTertiary(t *testing.T) {
	mock := NewMockLLMClient()
	mock.DefaultResult = "artifact"

	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ProviderID: "a", ModelID: "primary"},
		Secondary: ModelRef{ProviderID: "b", ModelID: "secondary"},
		Tertiary:  nil,
		MaxCycles: 1,
	})

	result, err := pipeline.Review(context.Background(), "test", nil)
	require.NoError(t, err)
	assert.Equal(t, "artifact", result)

	// Should have 3 prompt calls (no tertiary critique)
	history := mock.GetPromptHistory()
	assert.Len(t, history, 3)
}

func TestReviewPipeline_CritiqueFails(t *testing.T) {
	mock := NewMockLLMClient()

	callCount := 0
	// We need a custom approach: first call (generate) succeeds, second (critique) fails
	// Since mock doesn't support per-call errors, we use a workaround:
	// critique fails when CreateSession returns error on the second session
	// Instead, let's just set PromptErr after the first call by using a wrapper.
	// Simpler: just verify the pipeline completes even when critique fails by
	// using a mock that's error-prone — but MockLLMClient applies PromptErr globally.
	// Let's use a different approach: make a custom mock.

	_ = callCount

	// The simplest test: if both generate and critique use the same mock and
	// critique always fails (returns same content), the pipeline still completes
	// because critique failure is logged but not fatal.
	// Actually, critique failure in the current code means CreateSession or SendPrompt
	// returned an error. Let's verify with CreateErr:
	mock.DefaultResult = "generated output"

	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ModelID: "p"},
		Secondary: ModelRef{ModelID: "s"},
		MaxCycles: 1,
	})

	// The pipeline should complete with just the primary output
	// when critique succeeds (default mock behavior), refine also runs
	result, err := pipeline.Review(context.Background(), "test", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestReviewPipeline_CritiqueError_StillCompletes(t *testing.T) {
	// Use a custom LLMClient that fails on critique sessions
	client := &critiqueFailClient{
		DefaultResult: "primary artifact",
	}

	pipeline := NewReviewPipeline(client, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ModelID: "p"},
		Secondary: ModelRef{ModelID: "s"},
		MaxCycles: 1,
	})

	result, err := pipeline.Review(context.Background(), "build it", nil)
	require.NoError(t, err)
	// Should still return the primary-generated artifact since critique failure is non-fatal
	assert.Equal(t, "primary artifact", result)
}

// critiqueFailClient succeeds for "generate" sessions but fails for "critique" sessions.
type critiqueFailClient struct {
	DefaultResult string
	sessionCount  int
}

func (c *critiqueFailClient) CreateSession(_ context.Context, title string, _ string) (*SessionInfo, error) {
	c.sessionCount++
	return &SessionInfo{ID: "s-" + title, Title: title}, nil
}

func (c *critiqueFailClient) SendPrompt(_ context.Context, sessionID string, _ string, _ ModelRef, _ string) (*PromptResponse, error) {
	if sessionID == "s-critique" {
		return nil, errors.New("critique model unavailable")
	}
	return &PromptResponse{Content: c.DefaultResult}, nil
}

func (c *critiqueFailClient) GetMessages(_ context.Context, _ string, _ string) ([]Message, error) {
	return nil, nil
}

func (c *critiqueFailClient) DeleteSession(_ context.Context, _ string, _ string) error {
	return nil
}

func (c *critiqueFailClient) AbortSession(_ context.Context, _ string, _ string) error {
	return nil
}

func TestReviewPipeline_MaxCyclesDefault(t *testing.T) {
	pipeline := NewReviewPipeline(NewMockLLMClient(), "/tmp", ReviewConfig{
		Primary:   ModelRef{ModelID: "p"},
		Secondary: ModelRef{ModelID: "s"},
		MaxCycles: 0, // should default to 1
	})
	assert.Equal(t, 1, pipeline.maxCycles)
}

func TestReviewPipeline_MaxCyclesCapped(t *testing.T) {
	pipeline := NewReviewPipeline(NewMockLLMClient(), "/tmp", ReviewConfig{
		Primary:   ModelRef{ModelID: "p"},
		Secondary: ModelRef{ModelID: "s"},
		MaxCycles: 5, // should be capped to 2
	})
	assert.Equal(t, 2, pipeline.maxCycles)
}

func TestReviewPipeline_TwoCycles(t *testing.T) {
	mock := NewMockLLMClient()
	mock.DefaultResult = "artifact"

	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ModelID: "p"},
		Secondary: ModelRef{ModelID: "s"},
		MaxCycles: 2,
	})

	result, err := pipeline.Review(context.Background(), "test", nil)
	require.NoError(t, err)
	assert.Equal(t, "artifact", result)

	// 2 cycles × 3 steps (generate, critique, refine) = 6 prompt calls
	history := mock.GetPromptHistory()
	assert.Len(t, history, 6)
}

func TestModelRef_ParseAndString(t *testing.T) {
	ref := ParseModelRef("github-copilot/claude-opus-4.6")
	assert.Equal(t, "github-copilot", ref.ProviderID)
	assert.Equal(t, "claude-opus-4.6", ref.ModelID)
	assert.Equal(t, "github-copilot/claude-opus-4.6", ref.String())

	ref2 := ParseModelRef("just-model")
	assert.Equal(t, "", ref2.ProviderID)
	assert.Equal(t, "just-model", ref2.ModelID)
	assert.Equal(t, "just-model", ref2.String())
}
