package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	sdk "github.com/github/copilot-sdk/go"
)

// CopilotClient wraps the GitHub Copilot SDK to implement Client.
type CopilotClient struct {
	sdk      *sdk.Client
	model    string
	sessions map[string]*sdk.Session
	mu       sync.Mutex
	started  bool
}

// NewCopilotClient creates a CopilotClient that uses the given model for all sessions.
func NewCopilotClient(model string) *CopilotClient {
	return &CopilotClient{
		model:    model,
		sessions: make(map[string]*sdk.Session),
	}
}

// Start initializes the underlying Copilot SDK client.
func (c *CopilotClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return nil
	}
	c.sdk = sdk.NewClient(nil)
	if err := c.sdk.Start(ctx); err != nil {
		return fmt.Errorf("starting copilot SDK: %w", err)
	}
	c.started = true
	slog.Info("copilot LLM client started", "model", c.model)
	return nil
}

// Stop shuts down all sessions and the SDK client.
func (c *CopilotClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, s := range c.sessions {
		_ = s.Destroy()
		delete(c.sessions, id)
	}
	if c.sdk != nil {
		return c.sdk.Stop()
	}
	return nil
}

func (c *CopilotClient) CreateSession(ctx context.Context, title string, workDir string) (*SessionInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return nil, fmt.Errorf("client not started")
	}

	slog.Debug("creating copilot session", "title", title, "model", c.model, "workDir", workDir)

	session, err := c.sdk.CreateSession(ctx, &sdk.SessionConfig{
		Model:               c.model,
		OnPermissionRequest: sdk.PermissionHandler.ApproveAll,
	})
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	c.sessions[session.SessionID] = session

	return &SessionInfo{
		ID:    session.SessionID,
		Title: title,
	}, nil
}

func (c *CopilotClient) SendPrompt(ctx context.Context, sessionID string, prompt string) (*PromptResponse, error) {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	c.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	slog.Debug("sending prompt via copilot SDK", "session", sessionID)

	resp, err := session.SendAndWait(ctx, sdk.MessageOptions{
		Prompt: prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("sending prompt: %w", err)
	}

	var content string
	if resp != nil && resp.Data.Content != nil {
		content = *resp.Data.Content
	}

	return &PromptResponse{Content: content}, nil
}

func (c *CopilotClient) GetMessages(_ context.Context, sessionID string) ([]Message, error) {
	// The Copilot SDK doesn't expose a GetMessages API on sessions in the same way.
	// For now return empty — callers that need history should track it themselves.
	return nil, nil
}

func (c *CopilotClient) DeleteSession(_ context.Context, sessionID string) error {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	if ok {
		delete(c.sessions, sessionID)
	}
	c.mu.Unlock()

	if ok {
		slog.Debug("deleting copilot session", "session", sessionID)
		return session.Destroy()
	}
	return nil
}

func (c *CopilotClient) AbortSession(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	c.mu.Unlock()

	if !ok {
		return nil
	}
	return session.Abort(ctx)
}
