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
	EventUserMessage    EventType = "user_message"

	// Subagent lifecycle.
	EventSubagentStart      EventType = "subagent_start"
	EventSubagentComplete   EventType = "subagent_complete"
	EventSubagentFailed     EventType = "subagent_failed"
	EventSubagentSelected   EventType = "subagent_selected"
	EventSubagentDeselected EventType = "subagent_deselected"

	// Tool progress.
	EventToolProgress      EventType = "tool_progress"
	EventToolPartialResult EventType = "tool_partial_result"

	// Session lifecycle.
	EventTitleChanged      EventType = "title_changed"
	EventCompactionStart   EventType = "compaction_start"
	EventCompactionComplete EventType = "compaction_complete"
	EventPlanChanged       EventType = "plan_changed"
	EventTaskComplete      EventType = "task_complete"
	EventContextChanged    EventType = "context_changed"
	EventModelChange       EventType = "model_change"
	EventModeChanged       EventType = "mode_changed"
	EventSessionWarning    EventType = "session_warning"
	EventSessionInfo       EventType = "session_info"

	// User input / elicitation.
	EventUserInputRequested     EventType = "user_input_requested"
	EventUserInputCompleted     EventType = "user_input_completed"
	EventElicitationRequested   EventType = "elicitation_requested"
	EventElicitationCompleted   EventType = "elicitation_completed"

	// Permissions.
	EventPermissionRequested  EventType = "permission_requested"
	EventPermissionCompleted  EventType = "permission_completed"

	// Hooks & skills.
	EventHookStart    EventType = "hook_start"
	EventHookEnd      EventType = "hook_end"
	EventSkillInvoked EventType = "skill_invoked"
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

	// Subagent fields.
	AgentName        *string `json:"agent_name,omitempty"`
	AgentDisplayName *string `json:"agent_display_name,omitempty"`
	AgentDescription *string `json:"agent_description,omitempty"`
	ParentToolCallID *string `json:"parent_tool_call_id,omitempty"`
	Summary          *string `json:"summary,omitempty"`

	// Tool progress fields.
	ProgressMessage *string `json:"progress_message,omitempty"`
	PartialOutput   *string `json:"partial_output,omitempty"`

	// Session lifecycle fields.
	Title         *string `json:"title,omitempty"`
	NewModel      *string `json:"new_model,omitempty"`
	PreviousModel *string `json:"previous_model,omitempty"`
	NewMode       *string `json:"new_mode,omitempty"`
	PreviousMode  *string `json:"previous_mode,omitempty"`
	WarningType   *string `json:"warning_type,omitempty"`
	InfoType      *string `json:"info_type,omitempty"`
	Success       *bool   `json:"success,omitempty"`

	// User input / elicitation fields.
	RequestID    *string  `json:"request_id,omitempty"`
	Question     *string  `json:"question,omitempty"`
	Choices      []string `json:"choices,omitempty"`
	AllowFreeform *bool   `json:"allow_freeform,omitempty"`

	// Permission fields.
	PermissionKind     *string `json:"permission_kind,omitempty"`
	PermissionToolName *string `json:"permission_tool_name,omitempty"`

	// Hook fields.
	HookID   *string `json:"hook_id,omitempty"`
	HookType *string `json:"hook_type,omitempty"`

	// Skill fields.
	SkillName *string `json:"skill_name,omitempty"`
}
