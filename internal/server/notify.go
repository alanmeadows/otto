package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/alanmeadows/otto/internal/config"
)

// notifyHTTPClient is a dedicated HTTP client for notifications,
// isolated from http.DefaultClient to avoid global state mutation.
var notifyHTTPClient = &http.Client{Timeout: 15 * time.Second}

// NotificationEvent represents the type of event that triggers a notification.
type NotificationEvent string

const (
	EventPRGreen        NotificationEvent = "pr_green"
	EventPRFailed       NotificationEvent = "pr_failed"
	EventSpecComplete   NotificationEvent = "spec_complete"
	EventCommentHandled NotificationEvent = "comment_handled"
)

// NotificationPayload carries details about a notification event.
type NotificationPayload struct {
	Event       NotificationEvent
	Title       string            // PR title or spec name
	URL         string            // Link to PR or spec
	Status      string            // "green", "failed", etc.
	FixAttempts int               // Number of fix attempts (for PR events)
	MaxAttempts int               // Max fix attempts configured
	Error       string            // Error summary for failures
	Extra       map[string]string // Additional context
}

// Notify sends a notification to the configured Teams webhook.
// Returns nil immediately if no webhook is configured or if the event is filtered out.
func Notify(ctx context.Context, cfg *config.NotificationsConfig, payload NotificationPayload) error {
	if cfg.TeamsWebhookURL == "" {
		return nil
	}

	// Check event filtering: if Events is non-empty, only notify for listed events.
	if len(cfg.Events) > 0 {
		allowed := false
		for _, e := range cfg.Events {
			if e == string(payload.Event) {
				allowed = true
				break
			}
		}
		if !allowed {
			slog.Debug("notification event filtered out", "event", string(payload.Event))
			return nil
		}
	}

	card := buildAdaptiveCard(payload)

	body, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshaling notification payload: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.TeamsWebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	slog.Debug("sending notification", "event", string(payload.Event), "title", payload.Title)

	resp, err := notifyHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending notification: %w", err)
	}
	defer resp.Body.Close()

	// Drain the body so the connection can be reused.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification webhook returned status %d: %s", resp.StatusCode, string(respBody))
	}

	slog.Debug("notification sent successfully", "event", string(payload.Event))
	return nil
}

// buildAdaptiveCard constructs an Adaptive Card wrapped in the Power Automate envelope.
func buildAdaptiveCard(payload NotificationPayload) map[string]any {
	// Determine header icon and title.
	headerText := string(payload.Event)
	switch payload.Event {
	case EventPRGreen:
		headerText = "✅ PR Passed"
	case EventPRFailed:
		headerText = "❌ PR Failed"
	case EventSpecComplete:
		headerText = "📋 Spec Complete"
	case EventCommentHandled:
		headerText = "💬 Comment Handled"
	}

	// Build facts.
	facts := []map[string]any{}
	if payload.Title != "" {
		facts = append(facts, map[string]any{"title": "Title", "value": payload.Title})
	}
	if payload.Status != "" {
		facts = append(facts, map[string]any{"title": "Status", "value": payload.Status})
	}
	if payload.FixAttempts > 0 || payload.MaxAttempts > 0 {
		facts = append(facts, map[string]any{
			"title": "Fix Attempts",
			"value": fmt.Sprintf("%d / %d", payload.FixAttempts, payload.MaxAttempts),
		})
	}
	for k, v := range payload.Extra {
		facts = append(facts, map[string]any{"title": k, "value": v})
	}

	// Build card body.
	cardBody := []map[string]any{
		{
			"type":   "TextBlock",
			"size":   "Medium",
			"weight": "Bolder",
			"text":   headerText,
		},
	}

	if len(facts) > 0 {
		cardBody = append(cardBody, map[string]any{
			"type":  "FactSet",
			"facts": facts,
		})
	}

	if payload.Error != "" {
		cardBody = append(cardBody, map[string]any{
			"type":   "TextBlock",
			"text":   fmt.Sprintf("⚠️ %s", payload.Error),
			"color":  "Attention",
			"wrap":   true,
			"weight": "Bolder",
		})
	}

	// Build actions.
	var actions []map[string]any
	if payload.URL != "" {
		actions = append(actions, map[string]any{
			"type":  "Action.OpenUrl",
			"title": "Open",
			"url":   payload.URL,
		})
	}

	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body":    cardBody,
	}
	if len(actions) > 0 {
		card["actions"] = actions
	}

	// Wrap in Power Automate envelope.
	return map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     card,
			},
		},
	}
}
