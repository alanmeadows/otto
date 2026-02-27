package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

const maxJSONRetries = 2

// ParseJSONResponse attempts to parse a JSON response from LLM output.
// If the raw response is not valid JSON, it tries to extract JSON and
// optionally retries via the same session.
func ParseJSONResponse[T any](ctx context.Context, client Client, sessionID string, rawResponse string) (T, error) {
	var zero T

	// Try direct unmarshal
	if err := json.Unmarshal([]byte(rawResponse), &zero); err == nil {
		return zero, nil
	}

	// Try stripping markdown fences and preamble
	cleaned := stripMarkdownJSON(rawResponse)
	var result T
	if err := json.Unmarshal([]byte(cleaned), &result); err == nil {
		return result, nil
	}

	// Retry via session if client is available
	if client != nil && sessionID != "" {
		for i := 0; i < maxJSONRetries; i++ {
			slog.Debug("retrying JSON parse via session", "attempt", i+1, "session", sessionID)

			resp, err := client.SendPrompt(ctx, sessionID,
				"Your previous response was not valid JSON. Please return ONLY the JSON array/object as specified, with no other text, no markdown fences, no explanation.")
			if err != nil {
				continue
			}

			// Try direct parse
			if err := json.Unmarshal([]byte(resp.Content), &result); err == nil {
				return result, nil
			}

			// Try cleaned
			cleaned = stripMarkdownJSON(resp.Content)
			if err := json.Unmarshal([]byte(cleaned), &result); err == nil {
				return result, nil
			}
		}
	}

	return zero, fmt.Errorf("failed to parse JSON response after %d retries: %s", maxJSONRetries, truncate(rawResponse, 200))
}

// stripMarkdownJSON removes markdown code fences and leading/trailing non-JSON text.
func stripMarkdownJSON(s string) string {
	s = strings.TrimSpace(s)

	// Remove ```json ... ``` fences
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)\\n?```")
	if matches := re.FindStringSubmatch(s); len(matches) > 1 {
		s = strings.TrimSpace(matches[1])
	}

	// Find first { or [ and last } or ]
	startObj := strings.IndexByte(s, '{')
	startArr := strings.IndexByte(s, '[')

	start := -1
	isArray := false

	switch {
	case startObj >= 0 && startArr >= 0:
		if startArr < startObj {
			start = startArr
			isArray = true
		} else {
			start = startObj
		}
	case startObj >= 0:
		start = startObj
	case startArr >= 0:
		start = startArr
		isArray = true
	}

	if start < 0 {
		return s
	}

	var end int
	if isArray {
		end = strings.LastIndexByte(s, ']')
	} else {
		end = strings.LastIndexByte(s, '}')
	}

	if end <= start {
		return s
	}

	return s[start : end+1]
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
