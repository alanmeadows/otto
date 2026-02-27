package llm

import "context"

// SessionInfo represents a created LLM session.
type SessionInfo struct {
	ID    string
	Title string
}

// PromptResponse represents the result of a prompt.
type PromptResponse struct {
	Content string
}

// Message represents a message from a session.
type Message struct {
	Role    string
	Content string
}

// Client abstracts LLM session operations for testability.
type Client interface {
	// CreateSession creates a new isolated LLM session in the given working directory.
	CreateSession(ctx context.Context, title string, workDir string) (*SessionInfo, error)

	// SendPrompt sends a prompt to the given session and waits for completion.
	SendPrompt(ctx context.Context, sessionID string, prompt string) (*PromptResponse, error)

	// GetMessages retrieves all messages from a session.
	GetMessages(ctx context.Context, sessionID string) ([]Message, error)

	// DeleteSession deletes a session.
	DeleteSession(ctx context.Context, sessionID string) error

	// AbortSession aborts a running prompt in a session.
	AbortSession(ctx context.Context, sessionID string) error
}
