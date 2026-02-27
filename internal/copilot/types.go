package copilot

import "time"

// SessionState represents the current state of a copilot session.
type SessionState string

const (
	StateIdle       SessionState = "idle"
	StateProcessing SessionState = "processing"
	StateError      SessionState = "error"
)

// SessionInfo represents a managed copilot session.
type SessionInfo struct {
	Name          string       `json:"name"`
	Model         string       `json:"model"`
	SessionID     string       `json:"session_id"`
	WorkingDir    string       `json:"working_dir"`
	WorktreeID    string       `json:"worktree_id,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	State         SessionState `json:"state"`
	MessageCount  int          `json:"message_count"`
	LastActivity  time.Time    `json:"last_activity"`
	Intent        string       `json:"intent"`
	ToolCallCount int          `json:"tool_call_count"`
}

// ChatMessage represents a single message in a copilot conversation.
type ChatMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// EventType represents the type of event emitted during a copilot session.
type EventType string

const (
	EventContentDelta  EventType = "content_delta"
	EventToolStart     EventType = "tool_start"
	EventToolComplete  EventType = "tool_complete"
	EventIntentChanged EventType = "intent_changed"
	EventTurnStart     EventType = "turn_start"
	EventTurnEnd       EventType = "turn_end"
	EventSessionError  EventType = "session_error"
	EventSessionIdle   EventType = "session_idle"
	EventUsageInfo     EventType = "usage_info"
	EventReasoningDelta EventType = "reasoning_delta"
)

// SessionEvent represents an event emitted by a copilot session.
type SessionEvent struct {
	Type        EventType `json:"type"`
	SessionName string    `json:"session_name"`
	Data        EventData `json:"data"`
}

// EventData carries the payload for a session event.
type EventData struct {
	Content      *string `json:"content,omitempty"`
	ToolName     *string `json:"tool_name,omitempty"`
	ToolCallID   *string `json:"tool_call_id,omitempty"`
	ToolInput    *string `json:"tool_input,omitempty"`
	ToolResult   *string `json:"tool_result,omitempty"`
	ToolSuccess  *bool   `json:"tool_success,omitempty"`
	Intent       *string `json:"intent,omitempty"`
	ReasoningID  *string `json:"reasoning_id,omitempty"`
	Model        *string `json:"model,omitempty"`
	InputTokens  *int    `json:"input_tokens,omitempty"`
	OutputTokens *int    `json:"output_tokens,omitempty"`
	ErrorMessage *string `json:"error_message,omitempty"`
}
