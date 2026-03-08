package dashboard

import (
	"encoding/json"
	"fmt"
	"time"
)

// BridgeMessage is the envelope for all WebSocket messages between
// the dashboard server and browser clients.
type BridgeMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewMessage constructs a BridgeMessage by marshaling the given payload.
func NewMessage[T any](msgType string, payload T) (BridgeMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return BridgeMessage{}, fmt.Errorf("marshal payload: %w", err)
	}
	return BridgeMessage{Type: msgType, Payload: raw}, nil
}

// ParsePayload unmarshals the raw payload of a BridgeMessage into T.
func ParsePayload[T any](msg BridgeMessage) (T, error) {
	var v T
	if err := json.Unmarshal(msg.Payload, &v); err != nil {
		return v, fmt.Errorf("unmarshal payload: %w", err)
	}
	return v, nil
}

// Server → Client message types.
const (
	MsgSessionsList   = "sessions_list"
	MsgSessionHistory = "session_history"
	MsgContentDelta   = "content_delta"
	MsgToolStarted    = "tool_started"
	MsgToolCompleted  = "tool_completed"
	MsgIntentChanged  = "intent_changed"
	MsgTurnStart      = "turn_start"
	MsgTurnEnd        = "turn_end"
	MsgSessionError   = "error"
	MsgTunnelStatus   = "tunnel_status"
	MsgWorktreesList  = "worktrees_list"
	MsgReasoningDelta       = "reasoning_delta"
	MsgPersistedSessionsList = "persisted_sessions_list"
	MsgUserMessage          = "user_message"
	MsgDashboardConfig      = "dashboard_config"
	MsgWatchHistory         = "watch_history"
	MsgWatchEvent           = "watch_event"

	// Subagent lifecycle.
	MsgSubagentStarted     = "subagent_started"
	MsgSubagentCompleted   = "subagent_completed"
	MsgSubagentFailed      = "subagent_failed"
	MsgSubagentSelected    = "subagent_selected"
	MsgSubagentDeselected  = "subagent_deselected"

	// Tool progress.
	MsgToolProgress      = "tool_progress"
	MsgToolPartialResult = "tool_partial_result"

	// Session lifecycle.
	MsgTitleChanged      = "title_changed"
	MsgCompactionStart   = "compaction_start"
	MsgCompactionComplete = "compaction_complete"
	MsgPlanChanged       = "plan_changed"
	MsgTaskComplete      = "task_complete"
	MsgContextChanged    = "context_changed"
	MsgModelChange       = "model_change"
	MsgModeChanged       = "mode_changed"
	MsgSessionWarning    = "session_warning"
	MsgSessionInfo       = "session_info"

	// User input / elicitation.
	MsgUserInputRequested    = "user_input_requested"
	MsgUserInputCompleted    = "user_input_completed"
	MsgElicitationRequested  = "elicitation_requested"
	MsgElicitationCompleted  = "elicitation_completed"

	// Permissions.
	MsgPermissionRequested = "permission_requested"
	MsgPermissionCompleted = "permission_completed"

	// Hooks & skills.
	MsgHookStart    = "hook_start"
	MsgHookEnd      = "hook_end"
	MsgSkillInvoked = "skill_invoked"
)

// Client → Server message types.
const (
	MsgGetSessions   = "get_sessions"
	MsgGetHistory    = "get_history"
	MsgSendMessage   = "send_message"
	MsgCreateSession = "create_session"
	MsgResumeSession = "resume_session"
	MsgCloseSession  = "close_session"
	MsgListWorktrees = "list_worktrees"
	MsgStartTunnel          = "start_tunnel"
	MsgStopTunnel           = "stop_tunnel"
	MsgGetPersistedSessions = "get_persisted_sessions"
	MsgSetTunnelConfig      = "set_tunnel_config"
	MsgAddAllowedUser       = "add_allowed_user"
	MsgRemoveAllowedUser    = "remove_allowed_user"
	MsgGetAllowedUsers      = "get_allowed_users"
	MsgRestartServer        = "restart_server"
	MsgUpgradeServer        = "upgrade_server"
	MsgWatchSession         = "watch_session"
	MsgForkSession          = "fork_session"
	MsgAbortSession         = "abort_session"
)

// Server → Client
const (
	MsgAllowedUsersList = "allowed_users_list"
)

// ---------------------------------------------------------------------------
// Server → Client payloads
// ---------------------------------------------------------------------------

type SessionsListPayload struct {
	Sessions      []SessionSummary `json:"sessions"`
	ActiveSession string           `json:"active_session"`
}

type SessionSummary struct {
	Name         string    `json:"name"`
	Model        string    `json:"model"`
	SessionID    string    `json:"session_id"`
	WorkingDir   string    `json:"working_dir"`
	CreatedAt    time.Time `json:"created_at"`
	MessageCount int       `json:"message_count"`
	IsProcessing bool      `json:"is_processing"`
	Intent       string    `json:"intent"`
	State        string    `json:"state"`
}

type SessionHistoryPayload struct {
	SessionName string           `json:"session_name"`
	Messages    []MessageSummary `json:"messages"`
}

type MessageSummary struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type ContentDeltaPayload struct {
	SessionName string `json:"session_name"`
	Content     string `json:"content"`
}

type ToolStartedPayload struct {
	SessionName string `json:"session_name"`
	ToolName    string `json:"tool_name"`
	CallID      string `json:"call_id"`
	ToolInput   string `json:"tool_input"`
}

type ToolCompletedPayload struct {
	SessionName string `json:"session_name"`
	CallID      string `json:"call_id"`
	Result      string `json:"result"`
	Success     bool   `json:"success"`
}

type IntentChangedPayload struct {
	SessionName string `json:"session_name"`
	Intent      string `json:"intent"`
}

type TurnPayload struct {
	SessionName string `json:"session_name"`
}

type ErrorPayload struct {
	SessionName string `json:"session_name"`
	Message     string `json:"message"`
}

type TunnelStatusPayload struct {
	Running  bool   `json:"running"`
	URL      string `json:"url"`
	KeyedURL string `json:"keyed_url,omitempty"`
}

type WorktreesListPayload struct {
	Worktrees []WorktreeSummary `json:"worktrees"`
}

type WorktreeSummary struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	RepoName string `json:"repo_name"`
}

type ReasoningDeltaPayload struct {
	SessionName string `json:"session_name"`
	ReasoningID string `json:"reasoning_id"`
	Content     string `json:"content"`
}

type PersistedSessionsListPayload struct {
	Sessions []PersistedSessionSummary `json:"sessions"`
}

type PersistedSessionSummary struct {
	SessionID    string `json:"session_id"`
	Summary      string `json:"summary"`
	LastModified string `json:"last_modified"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	IsActive     bool   `json:"is_active,omitempty"`
}

// ---------------------------------------------------------------------------
// Client → Server payloads
// ---------------------------------------------------------------------------

type GetHistoryPayload struct {
	SessionName string `json:"session_name"`
}

type SendMessagePayload struct {
	SessionName string `json:"session_name"`
	Prompt      string `json:"prompt"`
}

type CreateSessionPayload struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	WorkingDir string `json:"working_dir"`
}

type ResumeSessionPayload struct {
	SessionID   string `json:"session_id"`
	DisplayName string `json:"display_name"`
}

type CloseSessionPayload struct {
	SessionName string `json:"session_name"`
}

type WatchSessionPayload struct {
	SessionID string `json:"session_id"`
}

type ForkSessionPayload struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
}

type SetTunnelConfigPayload struct {
	TunnelID  string `json:"tunnel_id"`
	Access    string `json:"access"`
	AllowOrg  string `json:"allow_org"`
}

type AllowedUserPayload struct {
	Email string `json:"email"`
}

type AllowedUsersListPayload struct {
	OwnerEmail string   `json:"owner_email"`
	Users      []string `json:"users"`
}

type DashboardConfigPayload struct {
	OwnerNickname string `json:"owner_nickname"`
}

// ---------------------------------------------------------------------------
// Subagent payloads
// ---------------------------------------------------------------------------

type SubagentStartedPayload struct {
	SessionName      string `json:"session_name"`
	AgentName        string `json:"agent_name"`
	AgentDisplayName string `json:"agent_display_name"`
	AgentDescription string `json:"agent_description"`
	ToolCallID       string `json:"tool_call_id"`
}

type SubagentCompletedPayload struct {
	SessionName string `json:"session_name"`
	ToolCallID  string `json:"tool_call_id"`
	Summary     string `json:"summary"`
}

type SubagentFailedPayload struct {
	SessionName string `json:"session_name"`
	ToolCallID  string `json:"tool_call_id"`
	Error       string `json:"error"`
}

type SubagentSelectedPayload struct {
	SessionName      string `json:"session_name"`
	AgentName        string `json:"agent_name"`
	AgentDisplayName string `json:"agent_display_name"`
}

type SubagentDeselectedPayload struct {
	SessionName string `json:"session_name"`
}

// ---------------------------------------------------------------------------
// Tool progress payloads
// ---------------------------------------------------------------------------

type ToolProgressPayload struct {
	SessionName     string `json:"session_name"`
	CallID          string `json:"call_id"`
	ProgressMessage string `json:"progress_message"`
}

type ToolPartialResultPayload struct {
	SessionName   string `json:"session_name"`
	CallID        string `json:"call_id"`
	PartialOutput string `json:"partial_output"`
}

// ---------------------------------------------------------------------------
// Session lifecycle payloads
// ---------------------------------------------------------------------------

type TitleChangedPayload struct {
	SessionName string `json:"session_name"`
	Title       string `json:"title"`
}

type CompactionPayload struct {
	SessionName string `json:"session_name"`
}

type CompactionCompletePayload struct {
	SessionName string `json:"session_name"`
	Success     bool   `json:"success"`
	Summary     string `json:"summary"`
}

type PlanChangedPayload struct {
	SessionName string `json:"session_name"`
	Summary     string `json:"summary"`
}

type TaskCompletePayload struct {
	SessionName string `json:"session_name"`
	Summary     string `json:"summary"`
}

type ModelChangePayload struct {
	SessionName   string `json:"session_name"`
	NewModel      string `json:"new_model"`
	PreviousModel string `json:"previous_model"`
}

type ModeChangedPayload struct {
	SessionName  string `json:"session_name"`
	NewMode      string `json:"new_mode"`
	PreviousMode string `json:"previous_mode"`
}

type SessionWarningPayload struct {
	SessionName string `json:"session_name"`
	WarningType string `json:"warning_type"`
	Message     string `json:"message"`
}

type SessionInfoPayload struct {
	SessionName string `json:"session_name"`
	InfoType    string `json:"info_type"`
	Message     string `json:"message"`
}

// ---------------------------------------------------------------------------
// User input / elicitation payloads
// ---------------------------------------------------------------------------

type UserInputRequestedPayload struct {
	SessionName  string   `json:"session_name"`
	RequestID    string   `json:"request_id"`
	Question     string   `json:"question"`
	Choices      []string `json:"choices,omitempty"`
	AllowFreeform bool    `json:"allow_freeform"`
}

type UserInputCompletedPayload struct {
	SessionName string `json:"session_name"`
	RequestID   string `json:"request_id"`
}

type ElicitationRequestedPayload struct {
	SessionName  string   `json:"session_name"`
	RequestID    string   `json:"request_id"`
	Message      string   `json:"message"`
}

type ElicitationCompletedPayload struct {
	SessionName string `json:"session_name"`
	RequestID   string `json:"request_id"`
}

// ---------------------------------------------------------------------------
// Permission payloads
// ---------------------------------------------------------------------------

type PermissionRequestedPayload struct {
	SessionName    string `json:"session_name"`
	RequestID      string `json:"request_id"`
	PermissionKind string `json:"permission_kind"`
	ToolName       string `json:"tool_name"`
}

type PermissionCompletedPayload struct {
	SessionName string `json:"session_name"`
	RequestID   string `json:"request_id"`
}

// ---------------------------------------------------------------------------
// Hook & skill payloads
// ---------------------------------------------------------------------------

type HookStartPayload struct {
	SessionName string `json:"session_name"`
	HookID      string `json:"hook_id"`
	HookType    string `json:"hook_type"`
}

type HookEndPayload struct {
	SessionName string `json:"session_name"`
	HookID      string `json:"hook_id"`
	Success     bool   `json:"success"`
}

type SkillInvokedPayload struct {
	SessionName string `json:"session_name"`
	SkillName   string `json:"skill_name"`
}
