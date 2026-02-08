package opencode

import (
	"context"
	"strings"
)

// ModelRef identifies an LLM model by provider and model ID.
type ModelRef struct {
	ProviderID string
	ModelID    string
}

// ParseModelRef parses a "provider/model" string into a ModelRef.
func ParseModelRef(s string) ModelRef {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) == 2 {
		return ModelRef{ProviderID: parts[0], ModelID: parts[1]}
	}
	return ModelRef{ModelID: s}
}

// String returns the "provider/model" representation.
func (m ModelRef) String() string {
	if m.ProviderID == "" {
		return m.ModelID
	}
	return m.ProviderID + "/" + m.ModelID
}

// SessionInfo represents a created session.
type SessionInfo struct {
	ID    string
	Title string
}

// PromptResponse represents the result of a prompt.
type PromptResponse struct {
	Content  string
	Metadata map[string]any
}

// Message represents a message from a session.
type Message struct {
	Role    string
	Content string
}

// LLMClient abstracts OpenCode session operations for testability.
// TODO: Add event streaming support via Event.ListStreaming() returning
// *ssestream.Stream[EventListResponse] when needed for real-time progress tracking.
type LLMClient interface {
	// CreateSession creates a new isolated LLM session.
	CreateSession(ctx context.Context, title string, directory string) (*SessionInfo, error)

	// SendPrompt sends a prompt to the given session and waits for completion.
	SendPrompt(ctx context.Context, sessionID string, prompt string, model ModelRef, directory string) (*PromptResponse, error)

	// GetMessages retrieves all messages from a session.
	GetMessages(ctx context.Context, sessionID string, directory string) ([]Message, error)

	// DeleteSession deletes a session.
	DeleteSession(ctx context.Context, sessionID string, directory string) error

	// AbortSession aborts a running prompt in a session.
	AbortSession(ctx context.Context, sessionID string, directory string) error
}
