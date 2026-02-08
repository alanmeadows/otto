package opencode

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseJSONResponse_DirectJSON(t *testing.T) {
	type Result struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	raw := `{"name":"test","value":42}`
	result, err := ParseJSONResponse[Result](context.Background(), nil, "", "", ModelRef{}, raw)
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, 42, result.Value)
}

func TestParseJSONResponse_MarkdownWrapped(t *testing.T) {
	type Result struct {
		Name string `json:"name"`
	}

	raw := "Here is the JSON:\n```json\n{\"name\":\"wrapped\"}\n```\n"
	result, err := ParseJSONResponse[Result](context.Background(), nil, "", "", ModelRef{}, raw)
	require.NoError(t, err)
	assert.Equal(t, "wrapped", result.Name)
}

func TestParseJSONResponse_PreambleText(t *testing.T) {
	type Result struct {
		Status string `json:"status"`
	}

	raw := "Sure, here is the result:\n{\"status\":\"ok\"}\nHope that helps!"
	result, err := ParseJSONResponse[Result](context.Background(), nil, "", "", ModelRef{}, raw)
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Status)
}

func TestParseJSONResponse_Array(t *testing.T) {
	raw := `["one","two","three"]`
	result, err := ParseJSONResponse[[]string](context.Background(), nil, "", "", ModelRef{}, raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two", "three"}, result)
}

func TestParseJSONResponse_ArrayWithPreamble(t *testing.T) {
	raw := "Here are the items:\n[\"alpha\",\"beta\"]\nDone."
	result, err := ParseJSONResponse[[]string](context.Background(), nil, "", "", ModelRef{}, raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta"}, result)
}

func TestParseJSONResponse_RetryViaSession(t *testing.T) {
	type Result struct {
		Answer string `json:"answer"`
	}

	mock := NewMockLLMClient()
	mock.DefaultResult = `{"answer":"retried"}`

	// Initial raw is invalid
	result, err := ParseJSONResponse[Result](context.Background(), mock, "sess-1", "/tmp", ModelRef{ModelID: "test"}, "not json at all")
	require.NoError(t, err)
	assert.Equal(t, "retried", result.Answer)

	// Check that retry prompts were sent
	history := mock.GetPromptHistory()
	assert.GreaterOrEqual(t, len(history), 1)
}

func TestParseJSONResponse_FailsAfterRetries(t *testing.T) {
	type Result struct {
		X int `json:"x"`
	}

	mock := NewMockLLMClient()
	mock.DefaultResult = "still not json"

	_, err := ParseJSONResponse[Result](context.Background(), mock, "sess-1", "/tmp", ModelRef{}, "bad input")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse JSON response")
}

func TestStripMarkdownJSON_CodeFence(t *testing.T) {
	input := "```json\n{\"key\":\"value\"}\n```"
	result := stripMarkdownJSON(input)
	assert.Equal(t, `{"key":"value"}`, result)
}

func TestStripMarkdownJSON_NoFence(t *testing.T) {
	input := `{"key":"value"}`
	result := stripMarkdownJSON(input)
	assert.Equal(t, `{"key":"value"}`, result)
}

func TestStripMarkdownJSON_WithPreamble(t *testing.T) {
	input := "Here is the output:\n{\"a\":1}"
	result := stripMarkdownJSON(input)
	assert.Equal(t, `{"a":1}`, result)
}

func TestStripMarkdownJSON_Array(t *testing.T) {
	input := "Result: [1,2,3] done"
	result := stripMarkdownJSON(input)
	assert.Equal(t, "[1,2,3]", result)
}

func TestStripMarkdownJSON_PlainText(t *testing.T) {
	input := "no json here"
	result := stripMarkdownJSON(input)
	assert.Equal(t, "no json here", result)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hel...", truncate("hello world", 3))
	assert.Equal(t, "", truncate("", 5))
}
