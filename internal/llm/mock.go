package llm

import (
	"context"
	"fmt"
	"sync"
)

// MockClient is a test double for Client.
type MockClient struct {
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
}

// NewMockClient creates a new MockClient with sensible defaults.
func NewMockClient() *MockClient {
	return &MockClient{
		Sessions:      make(map[string]*SessionInfo),
		PromptResults: make(map[string]string),
		DefaultResult: "Mock LLM response",
	}
}

func (m *MockClient) CreateSession(_ context.Context, title string, _ string) (*SessionInfo, error) {
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

func (m *MockClient) SendPrompt(_ context.Context, sessionID string, prompt string) (*PromptResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.PromptErr != nil {
		return nil, m.PromptErr
	}
	m.PromptHistory = append(m.PromptHistory, PromptCall{
		SessionID: sessionID,
		Prompt:    prompt,
	})
	content := m.DefaultResult
	if r, ok := m.PromptResults[sessionID]; ok {
		content = r
	}
	return &PromptResponse{Content: content}, nil
}

func (m *MockClient) GetMessages(_ context.Context, _ string) ([]Message, error) {
	if m.MessagesResult != nil {
		return m.MessagesResult, nil
	}
	return []Message{
		{Role: "user", Content: "test prompt"},
		{Role: "assistant", Content: m.DefaultResult},
	}, nil
}

func (m *MockClient) DeleteSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Sessions, sessionID)
	return nil
}

func (m *MockClient) AbortSession(_ context.Context, _ string) error {
	return nil
}

// GetPromptHistory returns all prompt calls made to this mock.
func (m *MockClient) GetPromptHistory() []PromptCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]PromptCall, len(m.PromptHistory))
	copy(result, m.PromptHistory)
	return result
}

// SetSessionResult pre-sets the result for a specific session ID.
func (m *MockClient) SetSessionResult(sessionID string, result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PromptResults[sessionID] = result
}
