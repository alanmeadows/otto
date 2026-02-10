package opencode

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	opencode "github.com/sst/opencode-sdk-go"
)

// SDKLLMClient wraps the OpenCode SDK to implement LLMClient.
type SDKLLMClient struct {
	client *opencode.Client
}

// NewSDKLLMClient creates an LLMClient backed by the real OpenCode SDK.
func NewSDKLLMClient(client *opencode.Client) *SDKLLMClient {
	return &SDKLLMClient{client: client}
}

func (s *SDKLLMClient) CreateSession(ctx context.Context, title string, directory string) (*SessionInfo, error) {
	slog.Debug("creating OpenCode session", "title", title, "directory", directory)

	session, err := s.client.Session.New(ctx, opencode.SessionNewParams{
		Title:     opencode.F(title),
		Directory: opencode.F(directory),
	})
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	return &SessionInfo{
		ID:    session.ID,
		Title: session.Title,
	}, nil
}

// promptResult carries the outcome from Session.Prompt() executed in a goroutine.
type promptResult struct {
	resp *opencode.SessionPromptResponse
	err  error
}

func (s *SDKLLMClient) SendPrompt(ctx context.Context, sessionID string, prompt string, model ModelRef, directory string) (*PromptResponse, error) {
	slog.Debug("sending prompt with event-driven idle detection", "session", sessionID, "model", model.String())

	// Create a cancellable context so we can stop the SSE stream when done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Step 1: Start SSE event stream BEFORE sending the prompt so we don't miss
	// the session.idle event. The stream is scoped to the directory.
	eventStream := s.client.Event.ListStreaming(ctx, opencode.EventListParams{
		Directory: opencode.F(directory),
	})

	// Channel for SSE-detected completion
	idleCh := make(chan struct{}, 1)
	sseErrCh := make(chan error, 1)

	// Track SSE activity for the periodic heartbeat log
	var lastActivity atomic_time
	lastActivity.Store(time.Now())

	// Step 2: Monitor SSE events in a goroutine for session.idle / session.error
	go func() {
		defer eventStream.Close()
		var eventCount int
		var lastLogTime time.Time
		for eventStream.Next() {
			evt := eventStream.Current()
			lastActivity.Store(time.Now())

			switch evt.Type {
			case opencode.EventListResponseTypeSessionIdle:
				idle, ok := evt.AsUnion().(opencode.EventListResponseEventSessionIdle)
				if ok && idle.Properties.SessionID == sessionID {
					slog.Debug("received session.idle event", "session", sessionID, "total_events", eventCount)
					select {
					case idleCh <- struct{}{}:
					default:
					}
					return
				}

			case opencode.EventListResponseTypeSessionError:
				errEvt, ok := evt.AsUnion().(opencode.EventListResponseEventSessionError)
				if ok && errEvt.Properties.SessionID == sessionID {
					errName := string(errEvt.Properties.Error.Name)
					slog.Error("received session.error event", "session", sessionID, "error", errName)
					select {
					case sseErrCh <- fmt.Errorf("session error: %s", errName):
					default:
					}
					return
				}

			case opencode.EventListResponseTypeMessagePartUpdated:
				partEvt, ok := evt.AsUnion().(opencode.EventListResponseEventMessagePartUpdated)
				if ok && partEvt.Properties.Part.SessionID == sessionID {
					eventCount++
					// Throttle activity logging to at most once per 5 seconds
					if time.Since(lastLogTime) >= 5*time.Second {
						slog.Debug("session activity", "session", sessionID,
							"part_type", partEvt.Properties.Part.Type,
							"events_received", eventCount)
						lastLogTime = time.Now()
					}
				}
			}
		}
		// Stream ended (context cancelled or server disconnect)
		if err := eventStream.Err(); err != nil {
			slog.Debug("SSE stream ended", "session", sessionID, "error", err)
		}
	}()

	// Step 3: Send prompt in a goroutine (this is the blocking call that may hang).
	promptCh := make(chan promptResult, 1)
	go func() {
		resp, err := s.client.Session.Prompt(ctx, sessionID, opencode.SessionPromptParams{
			Parts: opencode.F([]opencode.SessionPromptParamsPartUnion{
				opencode.TextPartInputParam{
					Type: opencode.F(opencode.TextPartInputTypeText),
					Text: opencode.F(prompt),
				},
			}),
			Model: opencode.F(opencode.SessionPromptParamsModel{
				ProviderID: opencode.F(model.ProviderID),
				ModelID:    opencode.F(model.ModelID),
			}),
			Directory: opencode.F(directory),
		})
		promptCh <- promptResult{resp: resp, err: err}
	}()

	slog.Debug("prompt sent, racing Prompt() vs SSE session.idle", "session", sessionID)

	// Step 4: Periodic activity logging
	activityTicker := time.NewTicker(30 * time.Second)
	defer activityTicker.Stop()

	// Step 5: Wait for whichever signal arrives first.
	for {
		select {
		case result := <-promptCh:
			// The blocking Prompt() call returned normally.
			if result.err != nil {
				return nil, fmt.Errorf("sending prompt: %w", result.err)
			}
			slog.Debug("Prompt() returned normally", "session", sessionID)
			content := extractTextContent(result.resp)
			return &PromptResponse{Content: content}, nil

		case <-idleCh:
			// SSE detected session.idle before Prompt() returned.
			// This is the fix for the hanging Prompt() issue — the session
			// completed its work but the HTTP response never arrived.
			slog.Info("session.idle detected (Prompt() was still blocking), fetching response from messages", "session", sessionID)
			cancel() // cancel context to unblock the stuck Prompt() goroutine
			return s.fetchLatestResponse(context.Background(), sessionID, directory)

		case err := <-sseErrCh:
			// SSE detected a session error.
			cancel()
			return nil, err

		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for session completion: %w", ctx.Err())

		case <-activityTicker.C:
			la := lastActivity.Load()
			slog.Debug("still waiting for session completion", "session", sessionID, "last_activity_ago", time.Since(la).Round(time.Second))
		}
	}
}

// atomic_time is a thread-safe time.Time wrapper.
type atomic_time struct {
	mu sync.Mutex
	t  time.Time
}

func (a *atomic_time) Store(t time.Time) {
	a.mu.Lock()
	a.t = t
	a.mu.Unlock()
}

func (a *atomic_time) Load() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.t
}

// extractTextContent extracts text from a SessionPromptResponse by iterating
// over the Parts and concatenating all TextPart text values.
func extractTextContent(resp *opencode.SessionPromptResponse) string {
	if resp == nil {
		return ""
	}

	var texts []string
	for _, part := range resp.Parts {
		if part.Type == "text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n")
	}

	return ""
}

// fetchLatestResponse retrieves messages from the session and extracts the last
// assistant response content. Called after session.idle is detected.
func (s *SDKLLMClient) fetchLatestResponse(ctx context.Context, sessionID string, directory string) (*PromptResponse, error) {
	msgs, err := s.client.Session.Messages(ctx, sessionID, opencode.SessionMessagesParams{
		Directory: opencode.F(directory),
	})
	if err != nil {
		return nil, fmt.Errorf("fetching messages after session.idle: %w", err)
	}

	if msgs == nil {
		return &PromptResponse{Content: ""}, nil
	}

	// Find the last assistant message and extract its text parts
	var lastContent string
	for _, m := range *msgs {
		if m.Info.Role == opencode.MessageRoleAssistant {
			content := extractMessageContent(m)
			if content != "" {
				lastContent = content
			}
		}
	}

	slog.Debug("extracted response after session.idle", "session", sessionID, "content_length", len(lastContent))
	return &PromptResponse{Content: lastContent}, nil
}

// GetMessages retrieves all messages from a session.
func (s *SDKLLMClient) GetMessages(ctx context.Context, sessionID string, directory string) ([]Message, error) {
	msgs, err := s.client.Session.Messages(ctx, sessionID, opencode.SessionMessagesParams{
		Directory: opencode.F(directory),
	})
	if err != nil {
		return nil, fmt.Errorf("getting messages: %w", err)
	}

	if msgs == nil {
		return nil, nil
	}

	var result []Message
	for _, m := range *msgs {
		result = append(result, Message{
			Role:    string(m.Info.Role),
			Content: extractMessageContent(m),
		})
	}
	return result, nil
}

func (s *SDKLLMClient) DeleteSession(ctx context.Context, sessionID string, directory string) error {
	slog.Debug("deleting session", "session", sessionID)
	_, err := s.client.Session.Delete(ctx, sessionID, opencode.SessionDeleteParams{
		Directory: opencode.F(directory),
	})
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (s *SDKLLMClient) AbortSession(ctx context.Context, sessionID string, directory string) error {
	_, err := s.client.Session.Abort(ctx, sessionID, opencode.SessionAbortParams{
		Directory: opencode.F(directory),
	})
	if err != nil {
		return fmt.Errorf("aborting session: %w", err)
	}
	return nil
}

// extractMessageContent extracts text content from a SessionMessagesResponse
// by iterating over its Parts and concatenating all text parts.
func extractMessageContent(msg opencode.SessionMessagesResponse) string {
	var texts []string
	for _, part := range msg.Parts {
		if part.Type == "text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n")
	}
	return ""
}
