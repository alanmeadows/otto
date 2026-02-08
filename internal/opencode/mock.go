package opencode

import (
	"context"
	"fmt"
	"sync"
)

// MockLLMClient is a test double for LLMClient.
type MockLLMClient struct {
	mu             sync.Mutex
	Sessions       map[string]*SessionInfo
	PromptResults  map[string]string // sessionID -> response content
	DefaultResult  string
	PromptHistory  []PromptCall
	nextSessionID  int
	CreateErr      error
	PromptErr      error
	MessagesResult []Message
}

// PromptCall records a call to SendPrompt.
type PromptCall struct {
	SessionID string
	Prompt    string
	Model     ModelRef
	Directory string
}

// NewMockLLMClient creates a new MockLLMClient with sensible defaults.
func NewMockLLMClient() *MockLLMClient {
	return &MockLLMClient{
		Sessions:      make(map[string]*SessionInfo),
		PromptResults: make(map[string]string),
		DefaultResult: "Mock LLM response",
	}
}

func (m *MockLLMClient) CreateSession(ctx context.Context, title string, directory string) (*SessionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CreateErr != nil {
		return nil, m.CreateErr
	}
	m.nextSessionID++
	id := fmt.Sprintf("mock-session-%d", m.nextSessionID)
	info := &SessionInfo{ID: id, Title: title}
	m.Sessions[id] = info
	return info, nil
}

func (m *MockLLMClient) SendPrompt(ctx context.Context, sessionID string, prompt string, model ModelRef, directory string) (*PromptResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.PromptErr != nil {
		return nil, m.PromptErr
	}
	m.PromptHistory = append(m.PromptHistory, PromptCall{
		SessionID: sessionID,
		Prompt:    prompt,
		Model:     model,
		Directory: directory,
	})
	content := m.DefaultResult
	if r, ok := m.PromptResults[sessionID]; ok {
		content = r
	}
	return &PromptResponse{Content: content}, nil
}

func (m *MockLLMClient) GetMessages(ctx context.Context, sessionID string, directory string) ([]Message, error) {
	if m.MessagesResult != nil {
		return m.MessagesResult, nil
	}
	return []Message{
		{Role: "user", Content: "test prompt"},
		{Role: "assistant", Content: m.DefaultResult},
	}, nil
}

func (m *MockLLMClient) DeleteSession(ctx context.Context, sessionID string, directory string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Sessions, sessionID)
	return nil
}

func (m *MockLLMClient) AbortSession(ctx context.Context, sessionID string, directory string) error {
	return nil
}

// GetPromptHistory returns all prompt calls made to this mock.
func (m *MockLLMClient) GetPromptHistory() []PromptCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]PromptCall, len(m.PromptHistory))
	copy(result, m.PromptHistory)
	return result
}

// SetSessionResult pre-sets the result for a specific session ID.
func (m *MockLLMClient) SetSessionResult(sessionID string, result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PromptResults[sessionID] = result
}
