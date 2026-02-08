package opencode

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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

func (s *SDKLLMClient) SendPrompt(ctx context.Context, sessionID string, prompt string, model ModelRef, directory string) (*PromptResponse, error) {
	slog.Debug("sending prompt", "session", sessionID, "model", model.String())

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
	if err != nil {
		return nil, fmt.Errorf("sending prompt: %w", err)
	}

	content := extractTextContent(resp)

	return &PromptResponse{
		Content: content,
	}, nil
}

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
