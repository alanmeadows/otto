package ado

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/alanmeadows/otto/internal/provider"
)

// RunWorkflow executes a workflow action on the pull request.
func (b *Backend) RunWorkflow(ctx context.Context, pr *provider.PRInfo, action provider.WorkflowAction) error {
	switch action {
	case provider.WorkflowSubmit:
		return provider.ErrUnsupported
	case provider.WorkflowAutoComplete:
		return b.workflowAutoComplete(ctx, pr)
	case provider.WorkflowCreateWorkItem:
		return b.workflowCreateWorkItem(ctx, pr)
	case provider.WorkflowAddressBot:
		return b.workflowAddressBot(ctx, pr)
	default:
		return fmt.Errorf("unknown workflow action: %d", action)
	}
}

// workflowAutoComplete sets the PR to auto-complete when all policies pass.
func (b *Backend) workflowAutoComplete(ctx context.Context, pr *provider.PRInfo) error {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)
	repo := b.resolveRepo(pr)

	// Get current user identity.
	identity, err := b.getCurrentUser(ctx, org)
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests/%s",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repo), pr.ID)

	update := map[string]any{
		"autoCompleteSetBy": map[string]string{
			"id": identity.ID,
		},
		"completionOptions": map[string]any{
			"mergeStrategy":       "squash",
			"deleteSourceBranch":  true,
			"transitionWorkItems": true,
		},
	}

	resp, err := b.doRequest(ctx, http.MethodPatch, path, update)
	if err != nil {
		return fmt.Errorf("failed to set auto-complete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return b.parseError(resp)
	}

	slog.Info("auto-complete enabled for PR", "prID", pr.ID)
	return nil
}

// workflowCreateWorkItem creates a Task work item linked to the PR.
func (b *Backend) workflowCreateWorkItem(ctx context.Context, pr *provider.PRInfo) error {
	org := b.resolveOrg(pr)
	project := b.resolveProject(pr)

	// Create work item with JSON Patch content type.
	path := fmt.Sprintf("/%s/%s/_apis/wit/workitems/$Task",
		url.PathEscape(org), url.PathEscape(project))

	ops := []adoWorkItemPatchOp{
		{
			Op:    "add",
			Path:  "/fields/System.Title",
			Value: fmt.Sprintf("Follow-up: %s", pr.Title),
		},
		{
			Op:   "add",
			Path: "/fields/System.Description",
			Value: fmt.Sprintf("Auto-created from PR #%s: %s\n\n%s",
				pr.ID, pr.Title, pr.URL),
		},
		{
			Op:   "add",
			Path: "/relations/-",
			Value: map[string]any{
				"rel": "ArtifactLink",
				"url": pr.URL,
				"attributes": map[string]string{
					"name": "Pull Request",
				},
			},
		},
	}

	resp, err := b.doRequestWithContentType(ctx, http.MethodPost, path, ops, "application/json-patch+json")
	if err != nil {
		return fmt.Errorf("failed to create work item: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return b.parseError(resp)
	}

	var wi adoWorkItem
	if err := json.NewDecoder(resp.Body).Decode(&wi); err != nil {
		slog.Warn("work item created but could not decode response", "error", err)
		return nil
	}

	slog.Info("work item created", "workItemID", wi.ID, "prID", pr.ID)
	return nil
}

// workflowAddressBot identifies MerlinBot comment threads and logs them
// for future LLM-based resolution.
func (b *Backend) workflowAddressBot(ctx context.Context, pr *provider.PRInfo) error {
	comments, err := b.GetComments(ctx, pr)
	if err != nil {
		return fmt.Errorf("failed to get comments for bot detection: %w", err)
	}

	var botThreads []provider.Comment
	for _, c := range comments {
		if c.Author == "MerlinBot" && !c.IsResolved {
			botThreads = append(botThreads, c)
		}
	}

	if len(botThreads) == 0 {
		slog.Info("no unresolved MerlinBot threads found", "prID", pr.ID)
		return nil
	}

	slog.Info("found unresolved MerlinBot threads",
		"prID", pr.ID,
		"count", len(botThreads))

	for _, t := range botThreads {
		slog.Debug("MerlinBot thread",
			"threadID", t.ThreadID,
			"body", t.Body,
			"filePath", t.FilePath,
			"line", t.Line)
	}

	return nil
}

// getCurrentUser retrieves the authenticated user's identity.
func (b *Backend) getCurrentUser(ctx context.Context, org string) (*adoIdentity, error) {
	path := fmt.Sprintf("/%s/_apis/connectiondata", url.PathEscape(org))

	resp, err := b.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, b.parseError(resp)
	}

	var connData adoConnectionData
	if err := json.NewDecoder(resp.Body).Decode(&connData); err != nil {
		return nil, fmt.Errorf("failed to decode connection data: %w", err)
	}

	return &connData.AuthenticatedUser, nil
}
