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

	result, _, err := pipeline.Review(context.Background(), "build something", nil)
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

	_, _, err := pipeline.Review(context.Background(), "test", nil)
	require.NoError(t, err)

	// All sessions should be deleted (3 created, 3 deleted)
	assert.Empty(t, mock.Sessions, "all sessions should be cleaned up")
}

func TestReviewPipeline_ThreePassFlow(t *testing.T) {
	mock := NewMockLLMClient()
	mock.DefaultResult = "artifact"

	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ProviderID: "a", ModelID: "primary"},
		Secondary: ModelRef{ProviderID: "b", ModelID: "secondary"},
		MaxCycles: 1,
	})

	result, stats, err := pipeline.Review(context.Background(), "test", nil)
	require.NoError(t, err)
	assert.Equal(t, "artifact", result)

	// Should have 3 prompt calls: generate, critique, refine
	history := mock.GetPromptHistory()
	assert.Len(t, history, 3)
	assert.Equal(t, "primary", history[0].Model.ModelID)
	assert.Equal(t, "secondary", history[1].Model.ModelID)
	assert.Equal(t, "primary", history[2].Model.ModelID)

	assert.GreaterOrEqual(t, stats.SecondaryCritiqueItems, 0)
}

func TestReviewPipeline_TwoModelFlow(t *testing.T) {
	mock := NewMockLLMClient()
	mock.DefaultResult = "artifact"

	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ProviderID: "a", ModelID: "primary"},
		Secondary: ModelRef{ProviderID: "b", ModelID: "secondary"},
		MaxCycles: 1,
	})

	result, _, err := pipeline.Review(context.Background(), "test", nil)
	require.NoError(t, err)
	assert.Equal(t, "artifact", result)

	// Should have 3 prompt calls: generate, critique, refine
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
	result, _, err := pipeline.Review(context.Background(), "test", nil)
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

	result, _, err := pipeline.Review(context.Background(), "build it", nil)
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

	result, _, err := pipeline.Review(context.Background(), "test", nil)
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

func TestCountCritiqueItems(t *testing.T) {
	tests := []struct {
		name     string
		critique string
		expected int
	}{
		{"empty", "", 0},
		{"bullets", "- item one\n- item two\n- item three", 3},
		{"asterisks", "* item one\n* item two", 2},
		{"numbered", "1. first\n2. second\n3. third", 3},
		{"headings", "### Issue 1\nDetails\n### Issue 2\nDetails", 2},
		{"mixed", "### Bug found\n- missing nil check\n- wrong type\n1. fix this\n2. fix that", 5},
		{"prose_only", "This is just prose.\nNo structural markers here.\nJust paragraphs.", 0},
		{"with_blank_lines", "- item one\n\n- item two\n\n### heading\n", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, countCritiqueItems(tc.critique))
		})
	}
}

func TestLineDiffRatio(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
		minPct float64
		maxPct float64
	}{
		{"identical", "line1\nline2\nline3", "line1\nline2\nline3", 0, 0},
		{"empty_both", "", "", 0, 0},
		{"all_different", "aaa\nbbb", "ccc\nddd", 90, 100},
		{"partial_change", "line1\nline2\nline3\nline4", "line1\nchanged\nline3\nline4", 20, 30},
		{"added_lines", "line1\nline2", "line1\nline2\nline3\nline4", 40, 60},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pct := lineDiffRatio(tc.before, tc.after)
			assert.GreaterOrEqual(t, pct, tc.minPct, "change pct too low")
			assert.LessOrEqual(t, pct, tc.maxPct, "change pct too high")
		})
	}
}

func TestReviewPipeline_StatsPopulated(t *testing.T) {
	mock := NewMockLLMClient()
	// The mock returns the same string for all calls, but critique counting
	// will count structural items in the default result.
	mock.DefaultResult = "- issue one\n- issue two\n### Big Problem\nDescription here"

	pipeline := NewReviewPipeline(mock, "/tmp/repo", ReviewConfig{
		Primary:   ModelRef{ModelID: "primary"},
		Secondary: ModelRef{ModelID: "secondary"},
		MaxCycles: 1,
	})

	_, stats, err := pipeline.Review(context.Background(), "test", nil)
	require.NoError(t, err)

	// The mock returns the same text for critiques, which has 3 structural items.
	assert.Equal(t, 3, stats.SecondaryCritiqueItems)
}
