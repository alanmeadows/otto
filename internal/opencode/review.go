package opencode

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alanmeadows/otto/internal/prompts"
)

// ReviewPipeline orchestrates multi-model critical review of LLM-generated artifacts.
type ReviewPipeline struct {
	client    LLMClient
	primary   ModelRef
	secondary ModelRef
	tertiary  *ModelRef
	directory string
	maxCycles int
}

// ReviewConfig holds configuration for the review pipeline.
type ReviewConfig struct {
	Primary   ModelRef
	Secondary ModelRef
	Tertiary  *ModelRef
	MaxCycles int // default 1, max 2
}

// NewReviewPipeline creates a new multi-model review pipeline.
func NewReviewPipeline(client LLMClient, directory string, cfg ReviewConfig) *ReviewPipeline {
	maxCycles := cfg.MaxCycles
	if maxCycles <= 0 {
		maxCycles = 1
	}
	if maxCycles > 2 {
		maxCycles = 2
	}
	return &ReviewPipeline{
		client:    client,
		primary:   cfg.Primary,
		secondary: cfg.Secondary,
		tertiary:  cfg.Tertiary,
		directory: directory,
		maxCycles: maxCycles,
	}
}

// Review runs the multi-model review pipeline and returns the final artifact.
// The pipeline: primary generates → secondary critiques → (optional) tertiary critiques → primary refines.
//
// Currently runs exactly maxCycles iterations. Future improvement: use Levenshtein distance
// between pass 1 and pass 4 output — if delta exceeds 20% of artifact length, iterate.
// Alternatively, delegate to LLM: "Did the review feedback result in material changes? Reply YES/NO."
func (p *ReviewPipeline) Review(ctx context.Context, prompt string, contextData map[string]string) (string, error) {
	var artifact string
	var err error

	for cycle := 0; cycle < p.maxCycles; cycle++ {
		slog.Info("review pipeline", "cycle", cycle+1, "max_cycles", p.maxCycles)

		// Pass 1: Primary generates (or refines on cycle > 0)
		currentPrompt := prompt
		if len(contextData) > 0 {
			for k, v := range contextData {
				currentPrompt += fmt.Sprintf("\n\n## %s\n\n%s", k, v)
			}
		}
		if cycle > 0 && artifact != "" {
			currentPrompt = fmt.Sprintf("Here is the current version of the artifact:\n\n%s\n\nPlease refine and improve it based on any issues you identify.\n\nOriginal instructions:\n%s", artifact, prompt)
		}

		artifact, err = p.generate(ctx, p.primary, currentPrompt)
		if err != nil {
			return "", fmt.Errorf("primary generation (cycle %d): %w", cycle+1, err)
		}
		slog.Debug("primary generated artifact", "length", len(artifact))

		// Pass 2: Secondary critiques
		critique1, err := p.critique(ctx, p.secondary, artifact)
		if err != nil {
			slog.Warn("secondary critique failed, continuing with primary output", "error", err)
			continue
		}
		slog.Debug("secondary critique received", "length", len(critique1))

		// Pass 3: (Optional) Tertiary critiques
		var critique2 string
		if p.tertiary != nil {
			critique2, err = p.critique(ctx, *p.tertiary, artifact)
			if err != nil {
				slog.Warn("tertiary critique failed, continuing without", "error", err)
			} else {
				slog.Debug("tertiary critique received", "length", len(critique2))
			}
		}

		// Pass 4: Primary incorporates feedback
		artifact, err = p.refine(ctx, p.primary, artifact, critique1, critique2)
		if err != nil {
			return "", fmt.Errorf("primary refinement (cycle %d): %w", cycle+1, err)
		}
		slog.Debug("primary refined artifact", "length", len(artifact))
	}

	return artifact, nil
}

// generate creates an artifact using the given model.
func (p *ReviewPipeline) generate(ctx context.Context, model ModelRef, prompt string) (string, error) {
	session, err := p.client.CreateSession(ctx, "generate", p.directory)
	if err != nil {
		return "", err
	}
	defer p.client.DeleteSession(ctx, session.ID, p.directory)

	resp, err := p.client.SendPrompt(ctx, session.ID, prompt, model, p.directory)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// critique reviews an artifact using the given model with the review.md prompt.
func (p *ReviewPipeline) critique(ctx context.Context, model ModelRef, artifact string) (string, error) {
	session, err := p.client.CreateSession(ctx, "critique", p.directory)
	if err != nil {
		return "", err
	}
	defer p.client.DeleteSession(ctx, session.ID, p.directory)

	// Build review prompt from template
	reviewPrompt, err := prompts.Execute("review.md", map[string]string{
		"artifact":      artifact,
		"artifact_type": "document",
	})
	if err != nil {
		// Fallback to inline prompt if template fails — log the error since
		// the embedded template should always be available.
		slog.Warn("failed to load review.md template, using inline fallback", "error", err)
		reviewPrompt = fmt.Sprintf("Critically review the following artifact. Identify gaps, errors, inconsistencies, missing considerations, and areas for improvement. Be specific and actionable.\n\n---\n\n%s", artifact)
	}

	resp, err := p.client.SendPrompt(ctx, session.ID, reviewPrompt, model, p.directory)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// refine incorporates review feedback into the artifact.
func (p *ReviewPipeline) refine(ctx context.Context, model ModelRef, artifact, critique1, critique2 string) (string, error) {
	session, err := p.client.CreateSession(ctx, "refine", p.directory)
	if err != nil {
		return "", err
	}
	defer p.client.DeleteSession(ctx, session.ID, p.directory)

	prompt := fmt.Sprintf(`Here is an artifact that has been critically reviewed. Incorporate ALL valid feedback and produce the final, improved version.

## Original Artifact

%s

## Review Feedback #1

%s`, artifact, critique1)

	if critique2 != "" {
		prompt += fmt.Sprintf("\n\n## Review Feedback #2\n\n%s", critique2)
	}

	prompt += "\n\n## Instructions\n\nProduce the complete, final version of the artifact incorporating all valid feedback. Output ONLY the artifact content — no preamble, no commentary."

	resp, err := p.client.SendPrompt(ctx, session.ID, prompt, model, p.directory)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
