package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	sdk "github.com/github/copilot-sdk/go"
)

// Session wraps a single Copilot SDK session with event handling and history tracking.
type Session struct {
	info        SessionInfo
	session     *sdk.Session
	history     []ChatMessage
	mu          sync.RWMutex
	onEvent     func(SessionEvent) // fan-out callback set by Manager
	unsubscribe func()
}

// newSession creates a Session from an SDK session.
func newSession(name, model string, sdkSession *sdk.Session, workingDir string) *Session {
	s := &Session{
		info: SessionInfo{
			Name:         name,
			Model:        model,
			SessionID:    sdkSession.SessionID,
			WorkingDir:   workingDir,
			CreatedAt:    time.Now(),
			State:        StateIdle,
			LastActivity: time.Now(),
		},
		session: sdkSession,
		history: make([]ChatMessage, 0),
	}

	// Subscribe to SDK events.
	s.unsubscribe = sdkSession.On(s.handleSDKEvent)
	return s
}

// Info returns a snapshot of the session info.
func (s *Session) Info() SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info := s.info
	info.MessageCount = len(s.history)
	return info
}

// History returns a copy of all chat messages.
func (s *Session) History() []ChatMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChatMessage, len(s.history))
	copy(out, s.history)
	return out
}

// SendPrompt sends a user prompt to the session.
func (s *Session) SendPrompt(ctx context.Context, prompt string) error {
	s.mu.Lock()
	s.info.State = StateProcessing
	s.info.LastActivity = time.Now()
	s.history = append(s.history, ChatMessage{
		Role:      "user",
		Content:   prompt,
		Timestamp: time.Now(),
	})
	name := s.info.Name
	s.mu.Unlock()

	// Broadcast user message to other clients.
	content := prompt
	s.emit(SessionEvent{
		Type:        EventUserMessage,
		SessionName: name,
		Data:        EventData{Content: &content},
	})

	_, err := s.session.Send(ctx, sdk.MessageOptions{
		Prompt: prompt,
	})
	if err != nil {
		s.mu.Lock()
		s.info.State = StateError
		s.mu.Unlock()
		return fmt.Errorf("sending prompt: %w", err)
	}
	return nil
}

// Destroy cleans up the SDK session.
func (s *Session) Destroy() {
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	if s.session != nil {
		_ = s.session.Destroy()
	}
}

// handleSDKEvent dispatches SDK events to our event model.
func (s *Session) handleSDKEvent(evt sdk.SessionEvent) {
	s.mu.Lock()
	s.info.LastActivity = time.Now()
	name := s.info.Name
	s.mu.Unlock()

	switch evt.Type {
	case sdk.AssistantTurnStart:
		s.mu.Lock()
		s.info.State = StateProcessing
		s.info.ToolCallCount = 0
		s.mu.Unlock()
		s.emit(SessionEvent{Type: EventTurnStart, SessionName: name})

	case sdk.AssistantMessageDelta:
		if evt.Data.DeltaContent != nil {
			content := *evt.Data.DeltaContent
			s.emit(SessionEvent{
				Type:        EventContentDelta,
				SessionName: name,
				Data:        EventData{Content: &content},
			})
		}

	case sdk.AssistantMessage:
		if evt.Data.Content != nil {
			s.mu.Lock()
			s.history = append(s.history, ChatMessage{
				Role:      "assistant",
				Content:   *evt.Data.Content,
				Timestamp: time.Now(),
			})
			s.mu.Unlock()
		}

	case sdk.AssistantIntent:
		if evt.Data.Intent != nil {
			intent := *evt.Data.Intent
			s.mu.Lock()
			s.info.Intent = intent
			s.mu.Unlock()
			s.emit(SessionEvent{
				Type:        EventIntentChanged,
				SessionName: name,
				Data:        EventData{Intent: &intent},
			})
		}

	case sdk.ToolExecutionStart:
		var toolName string
		if evt.Data.ToolName != nil {
			toolName = *evt.Data.ToolName
		}
		var callID string
		if evt.Data.ToolCallID != nil {
			callID = *evt.Data.ToolCallID
		}
		var inputStr string
		if evt.Data.Arguments != nil {
			if b, err := json.Marshal(evt.Data.Arguments); err == nil {
				inputStr = string(b)
			}
		}
		s.mu.Lock()
		s.info.ToolCallCount++
		s.mu.Unlock()
		s.emit(SessionEvent{
			Type:        EventToolStart,
			SessionName: name,
			Data:        EventData{ToolName: &toolName, ToolCallID: &callID, ToolInput: &inputStr},
		})

	case sdk.ToolExecutionComplete:
		var callID string
		if evt.Data.ToolCallID != nil {
			callID = *evt.Data.ToolCallID
		}
		var result string
		if evt.Data.Result != nil {
			result = fmt.Sprintf("%v", *evt.Data.Result)
		}
		success := true
		if evt.Data.Success != nil {
			success = *evt.Data.Success
		}
		s.emit(SessionEvent{
			Type:        EventToolComplete,
			SessionName: name,
			Data:        EventData{ToolCallID: &callID, ToolResult: &result, ToolSuccess: &success},
		})

	case sdk.ToolExecutionProgress:
		var callID string
		if evt.Data.ToolCallID != nil {
			callID = *evt.Data.ToolCallID
		}
		var msg string
		if evt.Data.ProgressMessage != nil {
			msg = *evt.Data.ProgressMessage
		}
		s.emit(SessionEvent{
			Type:        EventToolProgress,
			SessionName: name,
			Data:        EventData{ToolCallID: &callID, ProgressMessage: &msg},
		})

	case sdk.ToolExecutionPartialResult:
		var callID string
		if evt.Data.ToolCallID != nil {
			callID = *evt.Data.ToolCallID
		}
		var partial string
		if evt.Data.PartialOutput != nil {
			partial = *evt.Data.PartialOutput
		}
		s.emit(SessionEvent{
			Type:        EventToolPartialResult,
			SessionName: name,
			Data:        EventData{ToolCallID: &callID, PartialOutput: &partial},
		})

	case sdk.AssistantReasoningDelta:
		if evt.Data.ReasoningText != nil {
			content := *evt.Data.ReasoningText
			var rid string
			if evt.Data.ReasoningID != nil {
				rid = *evt.Data.ReasoningID
			}
			s.emit(SessionEvent{
				Type:        EventReasoningDelta,
				SessionName: name,
				Data:        EventData{Content: &content, ReasoningID: &rid},
			})
		}

	case sdk.AssistantUsage:
		var inputTokens, outputTokens int
		if evt.Data.InputTokens != nil {
			inputTokens = int(*evt.Data.InputTokens)
		}
		if evt.Data.OutputTokens != nil {
			outputTokens = int(*evt.Data.OutputTokens)
		}
		var model string
		if evt.Data.Model != nil {
			model = *evt.Data.Model
		}
		s.emit(SessionEvent{
			Type:        EventUsageInfo,
			SessionName: name,
			Data:        EventData{InputTokens: &inputTokens, OutputTokens: &outputTokens, Model: &model},
		})

	case sdk.AssistantTurnEnd:
		s.mu.Lock()
		s.info.State = StateIdle
		s.mu.Unlock()
		s.emit(SessionEvent{Type: EventTurnEnd, SessionName: name})

	case sdk.SessionIdle:
		s.mu.Lock()
		s.info.State = StateIdle
		s.mu.Unlock()
		s.emit(SessionEvent{Type: EventSessionIdle, SessionName: name})

	case sdk.SessionError:
		var errMsg string
		if evt.Data.Message != nil {
			errMsg = *evt.Data.Message
		}
		s.mu.Lock()
		s.info.State = StateError
		s.mu.Unlock()
		slog.Warn("copilot session error", "session", name, "error", errMsg)
		s.emit(SessionEvent{
			Type:        EventSessionError,
			SessionName: name,
			Data:        EventData{ErrorMessage: &errMsg},
		})

	// --- Subagent lifecycle ---

	case sdk.SubagentStarted:
		var agentName, displayName, desc, toolCallID string
		if evt.Data.AgentName != nil {
			agentName = *evt.Data.AgentName
		}
		if evt.Data.AgentDisplayName != nil {
			displayName = *evt.Data.AgentDisplayName
		}
		if evt.Data.AgentDescription != nil {
			desc = *evt.Data.AgentDescription
		}
		if evt.Data.ToolCallID != nil {
			toolCallID = *evt.Data.ToolCallID
		}
		s.emit(SessionEvent{
			Type:        EventSubagentStart,
			SessionName: name,
			Data: EventData{
				AgentName:        &agentName,
				AgentDisplayName: &displayName,
				AgentDescription: &desc,
				ParentToolCallID: &toolCallID,
			},
		})

	case sdk.SubagentCompleted:
		var toolCallID string
		if evt.Data.ToolCallID != nil {
			toolCallID = *evt.Data.ToolCallID
		}
		var summary string
		if evt.Data.Summary != nil {
			summary = *evt.Data.Summary
		}
		s.emit(SessionEvent{
			Type:        EventSubagentComplete,
			SessionName: name,
			Data:        EventData{ParentToolCallID: &toolCallID, Summary: &summary},
		})

	case sdk.SubagentFailed:
		var toolCallID string
		if evt.Data.ToolCallID != nil {
			toolCallID = *evt.Data.ToolCallID
		}
		var errMsg string
		if evt.Data.Error != nil {
			errMsg = fmt.Sprintf("%v", *evt.Data.Error)
		}
		s.emit(SessionEvent{
			Type:        EventSubagentFailed,
			SessionName: name,
			Data:        EventData{ParentToolCallID: &toolCallID, ErrorMessage: &errMsg},
		})

	case sdk.SubagentSelected:
		var agentName, displayName string
		if evt.Data.AgentName != nil {
			agentName = *evt.Data.AgentName
		}
		if evt.Data.AgentDisplayName != nil {
			displayName = *evt.Data.AgentDisplayName
		}
		s.emit(SessionEvent{
			Type:        EventSubagentSelected,
			SessionName: name,
			Data:        EventData{AgentName: &agentName, AgentDisplayName: &displayName},
		})

	case sdk.SubagentDeselected:
		s.emit(SessionEvent{Type: EventSubagentDeselected, SessionName: name})

	// --- Session lifecycle ---

	case sdk.SessionTitleChanged:
		var title string
		if evt.Data.Title != nil {
			title = *evt.Data.Title
		}
		s.emit(SessionEvent{
			Type:        EventTitleChanged,
			SessionName: name,
			Data:        EventData{Title: &title},
		})

	case sdk.SessionCompactionStart:
		s.emit(SessionEvent{Type: EventCompactionStart, SessionName: name})

	case sdk.SessionCompactionComplete:
		success := true
		if evt.Data.Success != nil {
			success = *evt.Data.Success
		}
		var summary string
		if evt.Data.SummaryContent != nil {
			summary = *evt.Data.SummaryContent
		}
		s.emit(SessionEvent{
			Type:        EventCompactionComplete,
			SessionName: name,
			Data:        EventData{Success: &success, Summary: &summary},
		})

	case sdk.SessionPlanChanged:
		var summary string
		if evt.Data.Summary != nil {
			summary = *evt.Data.Summary
		}
		s.emit(SessionEvent{
			Type:        EventPlanChanged,
			SessionName: name,
			Data:        EventData{Summary: &summary},
		})

	case sdk.SessionTaskComplete:
		var summary string
		if evt.Data.Summary != nil {
			summary = *evt.Data.Summary
		}
		s.emit(SessionEvent{
			Type:        EventTaskComplete,
			SessionName: name,
			Data:        EventData{Summary: &summary},
		})

	case sdk.SessionContextChanged:
		s.emit(SessionEvent{Type: EventContextChanged, SessionName: name})

	case sdk.SessionModelChange:
		var newModel, prevModel string
		if evt.Data.NewModel != nil {
			newModel = *evt.Data.NewModel
		}
		if evt.Data.PreviousModel != nil {
			prevModel = *evt.Data.PreviousModel
		}
		s.emit(SessionEvent{
			Type:        EventModelChange,
			SessionName: name,
			Data:        EventData{NewModel: &newModel, PreviousModel: &prevModel},
		})

	case sdk.SessionModeChanged:
		var newMode, prevMode string
		if evt.Data.NewMode != nil {
			newMode = *evt.Data.NewMode
		}
		if evt.Data.PreviousMode != nil {
			prevMode = *evt.Data.PreviousMode
		}
		s.emit(SessionEvent{
			Type:        EventModeChanged,
			SessionName: name,
			Data:        EventData{NewMode: &newMode, PreviousMode: &prevMode},
		})

	case sdk.SessionWarning:
		var warnType, msg string
		if evt.Data.WarningType != nil {
			warnType = *evt.Data.WarningType
		}
		if evt.Data.Message != nil {
			msg = *evt.Data.Message
		}
		s.emit(SessionEvent{
			Type:        EventSessionWarning,
			SessionName: name,
			Data:        EventData{WarningType: &warnType, ErrorMessage: &msg},
		})

	case sdk.SessionInfo:
		var infoType, msg string
		if evt.Data.InfoType != nil {
			infoType = *evt.Data.InfoType
		}
		if evt.Data.Message != nil {
			msg = *evt.Data.Message
		}
		s.emit(SessionEvent{
			Type:        EventSessionInfo,
			SessionName: name,
			Data:        EventData{InfoType: &infoType, Content: &msg},
		})

	// --- User input / elicitation ---

	case sdk.UserInputRequested:
		var reqID, question string
		var choices []string
		allowFreeform := true
		if evt.Data.RequestID != nil {
			reqID = *evt.Data.RequestID
		}
		if evt.Data.Question != nil {
			question = *evt.Data.Question
		}
		if evt.Data.Message != nil && question == "" {
			question = *evt.Data.Message
		}
		if evt.Data.Choices != nil {
			choices = evt.Data.Choices
		}
		if evt.Data.AllowFreeform != nil {
			allowFreeform = *evt.Data.AllowFreeform
		}
		s.emit(SessionEvent{
			Type:        EventUserInputRequested,
			SessionName: name,
			Data: EventData{
				RequestID:    &reqID,
				Question:     &question,
				Choices:      choices,
				AllowFreeform: &allowFreeform,
			},
		})

	case sdk.UserInputCompleted:
		var reqID string
		if evt.Data.RequestID != nil {
			reqID = *evt.Data.RequestID
		}
		s.emit(SessionEvent{
			Type:        EventUserInputCompleted,
			SessionName: name,
			Data:        EventData{RequestID: &reqID},
		})

	case sdk.ElicitationRequested:
		var reqID, msg string
		if evt.Data.RequestID != nil {
			reqID = *evt.Data.RequestID
		}
		if evt.Data.Message != nil {
			msg = *evt.Data.Message
		}
		s.emit(SessionEvent{
			Type:        EventElicitationRequested,
			SessionName: name,
			Data:        EventData{RequestID: &reqID, Content: &msg},
		})

	case sdk.ElicitationCompleted:
		var reqID string
		if evt.Data.RequestID != nil {
			reqID = *evt.Data.RequestID
		}
		s.emit(SessionEvent{
			Type:        EventElicitationCompleted,
			SessionName: name,
			Data:        EventData{RequestID: &reqID},
		})

	// --- Permissions ---

	case sdk.PermissionRequested:
		var reqID, kind, toolName string
		if evt.Data.RequestID != nil {
			reqID = *evt.Data.RequestID
		}
		if evt.Data.PermissionRequest != nil {
			kind = string(evt.Data.PermissionRequest.Kind)
		}
		if evt.Data.ToolName != nil {
			toolName = *evt.Data.ToolName
		}
		s.emit(SessionEvent{
			Type:        EventPermissionRequested,
			SessionName: name,
			Data: EventData{
				RequestID:          &reqID,
				PermissionKind:     &kind,
				PermissionToolName: &toolName,
			},
		})

	case sdk.PermissionCompleted:
		var reqID string
		if evt.Data.RequestID != nil {
			reqID = *evt.Data.RequestID
		}
		s.emit(SessionEvent{
			Type:        EventPermissionCompleted,
			SessionName: name,
			Data:        EventData{RequestID: &reqID},
		})

	// --- Hooks & skills ---

	case sdk.HookStart:
		var hookID, hookType string
		if evt.Data.HookInvocationID != nil {
			hookID = *evt.Data.HookInvocationID
		}
		if evt.Data.HookType != nil {
			hookType = *evt.Data.HookType
		}
		s.emit(SessionEvent{
			Type:        EventHookStart,
			SessionName: name,
			Data:        EventData{HookID: &hookID, HookType: &hookType},
		})

	case sdk.HookEnd:
		var hookID string
		if evt.Data.HookInvocationID != nil {
			hookID = *evt.Data.HookInvocationID
		}
		success := true
		if evt.Data.Success != nil {
			success = *evt.Data.Success
		}
		s.emit(SessionEvent{
			Type:        EventHookEnd,
			SessionName: name,
			Data:        EventData{HookID: &hookID, Success: &success},
		})

	case sdk.SkillInvoked:
		var skillName string
		if evt.Data.Name != nil {
			skillName = *evt.Data.Name
		}
		s.emit(SessionEvent{
			Type:        EventSkillInvoked,
			SessionName: name,
			Data:        EventData{SkillName: &skillName},
		})

	default:
		slog.Debug("unhandled SDK event", "type", evt.Type, "session", name)
	}
}

// emit sends an event to the Manager's fan-out callback.
func (s *Session) emit(evt SessionEvent) {
	s.mu.RLock()
	fn := s.onEvent
	s.mu.RUnlock()
	if fn != nil {
		fn(evt)
	}
}
