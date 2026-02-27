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
	s.mu.Unlock()

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
