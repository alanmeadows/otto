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
	Running bool   `json:"running"`
	URL     string `json:"url"`
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
