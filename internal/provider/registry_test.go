package provider_test

import (
	"context"
	"testing"

	"github.com/alanmeadows/otto/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBackend is a minimal PRBackend implementation for testing the registry.
type mockBackend struct {
	name    string
	matches func(string) bool
}

func (m *mockBackend) Name() string               { return m.name }
func (m *mockBackend) MatchesURL(url string) bool { return m.matches(url) }
func (m *mockBackend) GetPR(ctx context.Context, id string) (*provider.PRInfo, error) {
	return nil, nil
}
func (m *mockBackend) GetPipelineStatus(ctx context.Context, pr *provider.PRInfo) (*provider.PipelineStatus, error) {
	return nil, nil
}
func (m *mockBackend) GetBuildLogs(ctx context.Context, pr *provider.PRInfo, buildID string) (string, error) {
	return "", nil
}
func (m *mockBackend) GetComments(ctx context.Context, pr *provider.PRInfo) ([]provider.Comment, error) {
	return nil, nil
}
func (m *mockBackend) PostComment(ctx context.Context, pr *provider.PRInfo, body string) error {
	return nil
}
func (m *mockBackend) PostInlineComment(ctx context.Context, pr *provider.PRInfo, comment provider.InlineComment) error {
	return nil
}
func (m *mockBackend) ReplyToComment(ctx context.Context, pr *provider.PRInfo, threadID string, body string) error {
	return nil
}
func (m *mockBackend) ResolveComment(ctx context.Context, pr *provider.PRInfo, threadID string, resolution provider.CommentResolution) error {
	return nil
}
func (m *mockBackend) RunWorkflow(ctx context.Context, pr *provider.PRInfo, action provider.WorkflowAction) error {
	return nil
}

func TestDetect(t *testing.T) {
	reg := provider.NewRegistry()

	adoBackend := &mockBackend{
		name: "ado",
		matches: func(url string) bool {
			return url == "https://dev.azure.com/org/project/_git/repo/pullrequest/123"
		},
	}
	ghBackend := &mockBackend{
		name: "github",
		matches: func(url string) bool {
			return url == "https://github.com/owner/repo/pull/123"
		},
	}

	reg.Register(adoBackend)
	reg.Register(ghBackend)

	t.Run("detect ADO", func(t *testing.T) {
		b, err := reg.Detect("https://dev.azure.com/org/project/_git/repo/pullrequest/123")
		require.NoError(t, err)
		assert.Equal(t, "ado", b.Name())
	})

	t.Run("detect GitHub", func(t *testing.T) {
		b, err := reg.Detect("https://github.com/owner/repo/pull/123")
		require.NoError(t, err)
		assert.Equal(t, "github", b.Name())
	})

	t.Run("detect unknown", func(t *testing.T) {
		_, err := reg.Detect("https://gitlab.com/owner/repo/-/merge_requests/1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no registered backend")
	})
}

func TestGet(t *testing.T) {
	reg := provider.NewRegistry()

	adoBackend := &mockBackend{name: "ado", matches: func(string) bool { return false }}
	ghBackend := &mockBackend{name: "github", matches: func(string) bool { return false }}

	reg.Register(adoBackend)
	reg.Register(ghBackend)

	t.Run("get by name", func(t *testing.T) {
		b, err := reg.Get("ado")
		require.NoError(t, err)
		assert.Equal(t, "ado", b.Name())
	})

	t.Run("get github by name", func(t *testing.T) {
		b, err := reg.Get("github")
		require.NoError(t, err)
		assert.Equal(t, "github", b.Name())
	})

	t.Run("get unknown", func(t *testing.T) {
		_, err := reg.Get("bitbucket")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no registered backend with name")
	})
}
