package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotify_NoWebhook(t *testing.T) {
	cfg := &config.NotificationsConfig{
		TeamsWebhookURL: "",
	}
	err := Notify(t.Context(), cfg, NotificationPayload{
		Event: EventPRGreen,
		Title: "Test PR",
	})
	assert.NoError(t, err)
}

func TestNotify_EventFiltering(t *testing.T) {
	// Set up a server that would record if it was called.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.NotificationsConfig{
		TeamsWebhookURL: srv.URL,
		Events:          []string{"pr_green"}, // Only allow pr_green
	}

	// Send a pr_failed event — should be filtered out.
	err := Notify(t.Context(), cfg, NotificationPayload{
		Event: EventPRFailed,
		Title: "Test PR",
	})
	assert.NoError(t, err)
	assert.False(t, called, "webhook should not be called for filtered event")
}

func TestNotify_EventFilteringEmptyAllowed(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.NotificationsConfig{
		TeamsWebhookURL: srv.URL,
		Events:          []string{}, // Empty = allow all
	}

	err := Notify(t.Context(), cfg, NotificationPayload{
		Event: EventPRFailed,
		Title: "Test PR",
	})
	assert.NoError(t, err)
	assert.True(t, called, "webhook should be called when Events is empty (allow all)")
}

func TestNotify_SendsRequest(t *testing.T) {
	var receivedBody []byte
	var receivedContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.NotificationsConfig{
		TeamsWebhookURL: srv.URL,
	}

	err := Notify(t.Context(), cfg, NotificationPayload{
		Event:  EventPRGreen,
		Title:  "My PR Title",
		URL:    "https://example.com/pr/1",
		Status: "green",
	})
	require.NoError(t, err)

	assert.Equal(t, "application/json", receivedContentType)

	// Parse and validate the Adaptive Card structure.
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(receivedBody, &envelope))

	assert.Equal(t, "message", envelope["type"])

	attachments, ok := envelope["attachments"].([]any)
	require.True(t, ok)
	require.Len(t, attachments, 1)

	attachment := attachments[0].(map[string]any)
	assert.Equal(t, "application/vnd.microsoft.card.adaptive", attachment["contentType"])

	content := attachment["content"].(map[string]any)
	assert.Equal(t, "AdaptiveCard", content["type"])
	assert.Equal(t, "1.4", content["version"])
}

func TestBuildAdaptiveCard_PRGreen(t *testing.T) {
	payload := NotificationPayload{
		Event:  EventPRGreen,
		Title:  "Add feature X",
		URL:    "https://example.com/pr/42",
		Status: "green",
	}

	card := buildAdaptiveCard(payload)

	// Check envelope.
	assert.Equal(t, "message", card["type"])

	attachments := card["attachments"].([]map[string]any)
	require.Len(t, attachments, 1)

	content := attachments[0]["content"].(map[string]any)
	assert.Equal(t, "AdaptiveCard", content["type"])
	assert.Equal(t, "1.4", content["version"])

	// Check body has header with green icon.
	body := content["body"].([]map[string]any)
	require.NotEmpty(t, body)
	assert.Equal(t, "✅ PR Passed", body[0]["text"])

	// Check facts include title and status.
	factSet := body[1]
	assert.Equal(t, "FactSet", factSet["type"])
	facts := factSet["facts"].([]map[string]any)
	assert.GreaterOrEqual(t, len(facts), 2)

	// Check action button exists.
	actions := content["actions"].([]map[string]any)
	require.Len(t, actions, 1)
	assert.Equal(t, "Action.OpenUrl", actions[0]["type"])
	assert.Equal(t, "https://example.com/pr/42", actions[0]["url"])
}

func TestBuildAdaptiveCard_PRFailed(t *testing.T) {
	payload := NotificationPayload{
		Event:       EventPRFailed,
		Title:       "Fix bug Y",
		Status:      "failed",
		FixAttempts: 5,
		MaxAttempts: 5,
		Error:       "Exhausted fix attempts",
	}

	card := buildAdaptiveCard(payload)

	attachments := card["attachments"].([]map[string]any)
	content := attachments[0]["content"].(map[string]any)
	body := content["body"].([]map[string]any)

	// Check header.
	assert.Equal(t, "❌ PR Failed", body[0]["text"])

	// Check error text block exists.
	hasError := false
	for _, block := range body {
		if text, ok := block["text"].(string); ok {
			if text == "⚠️ Exhausted fix attempts" {
				hasError = true
				assert.Equal(t, "Attention", block["color"])
			}
		}
	}
	assert.True(t, hasError, "card should include error text block")
}

func TestBuildAdaptiveCard_WithURL(t *testing.T) {
	payload := NotificationPayload{
		Event: EventCommentHandled,
		Title: "Some PR",
		URL:   "https://example.com/pr/99",
	}

	card := buildAdaptiveCard(payload)

	attachments := card["attachments"].([]map[string]any)
	content := attachments[0]["content"].(map[string]any)

	actions, ok := content["actions"].([]map[string]any)
	require.True(t, ok, "actions should be present when URL is set")
	require.Len(t, actions, 1)
	assert.Equal(t, "Action.OpenUrl", actions[0]["type"])
	assert.Equal(t, "https://example.com/pr/99", actions[0]["url"])
}

func TestBuildAdaptiveCard_NoURL(t *testing.T) {
	payload := NotificationPayload{
		Event: EventPRGreen,
		Title: "No URL PR",
	}

	card := buildAdaptiveCard(payload)

	attachments := card["attachments"].([]map[string]any)
	content := attachments[0]["content"].(map[string]any)

	_, hasActions := content["actions"]
	assert.False(t, hasActions, "actions should not be present when URL is empty")
}

func TestNotify_WebhookErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream connect error"))
	}))
	defer srv.Close()

	cfg := &config.NotificationsConfig{
		TeamsWebhookURL: srv.URL,
	}

	err := Notify(t.Context(), cfg, NotificationPayload{
		Event: EventPRGreen,
		Title: "Test PR",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
	assert.Contains(t, err.Error(), "upstream connect error")
}

func TestNotify_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.NotificationsConfig{
		TeamsWebhookURL: srv.URL,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := Notify(ctx, cfg, NotificationPayload{
		Event: EventPRGreen,
		Title: "Test PR",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}
