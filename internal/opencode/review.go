package opencode

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/alanmeadows/otto/internal/prompts"
)

// ReviewPipeline orchestrates multi-model critical review of LLM-generated artifacts.
type ReviewPipeline struct {
	client    LLMClient
	primary   ModelRef
	secondary ModelRef
	directory string
	maxCycles int
}

// ReviewConfig holds configuration for the review pipeline.
type ReviewConfig struct {
	Primary   ModelRef
	Secondary ModelRef
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
		directory: directory,
		maxCycles: maxCycles,
	}
}

// ReviewStats captures metrics from the multi-model review pipeline.
type ReviewStats struct {
	// SecondaryCritiqueItems is the number of distinct findings raised by the secondary model.
	SecondaryCritiqueItems int
	// RefinementChangePct is the percentage of lines changed between the pre-review and post-review artifact,
	// indicating how much feedback the primary model accepted and incorporated.
	RefinementChangePct float64
}

// countCritiqueItems estimates the number of distinct findings in a critique.
// It counts structural markers commonly used by LLMs to enumerate issues:
// bullet points (- / * ), numbered items (1. ), and ### headings.
func countCritiqueItems(critique string) int {
	count := 0
	for _, line := range strings.Split(critique, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			count++
		} else if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			count++
		} else if len(trimmed) >= 3 && trimmed[0] >= '0' && trimmed[0] <= '9' && strings.Contains(trimmed[:3], ".") {
			count++
		}
	}
	return count
}

// lineDiffRatio computes the percentage of lines that differ between two texts.
func lineDiffRatio(before, after string) float64 {
	bLines := strings.Split(before, "\n")
	aLines := strings.Split(after, "\n")

	maxLen := len(bLines)
	if len(aLines) > maxLen {
		maxLen = len(aLines)
	}
	if maxLen == 0 {
		return 0
	}

	// Count matching lines (simple LCS-like diff via line set comparison).
	matched := 0
	minLen := len(bLines)
	if len(aLines) < minLen {
		minLen = len(aLines)
	}
	for i := 0; i < minLen; i++ {
		if bLines[i] == aLines[i] {
			matched++
		}
	}

	changed := maxLen - matched
	pct := float64(changed) / float64(maxLen) * 100
	return math.Round(pct*10) / 10 // one decimal place
}

// Review runs the multi-model review pipeline and returns the final artifact plus stats.
// The pipeline: primary generates → secondary critiques → primary refines.
//
// Currently runs exactly maxCycles iterations. Future improvement: use Levenshtein distance
// between pass 1 and pass 3 output — if delta exceeds 20% of artifact length, iterate.
// Alternatively, delegate to LLM: "Did the review feedback result in material changes? Reply YES/NO."
func (p *ReviewPipeline) Review(ctx context.Context, prompt string, contextData map[string]string) (string, ReviewStats, error) {
	var artifact string
	var err error
	var stats ReviewStats

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
			return "", stats, fmt.Errorf("primary generation (cycle %d): %w", cycle+1, err)
		}
		slog.Debug("primary generated artifact", "length", len(artifact))

		preReviewArtifact := artifact

		// Pass 2: Secondary critiques
		critique, err := p.critique(ctx, p.secondary, artifact)
		if err != nil {
			slog.Warn("secondary critique failed, continuing with primary output", "error", err)
			continue
		}
		slog.Debug("secondary critique received", "length", len(critique))
		stats.SecondaryCritiqueItems += countCritiqueItems(critique)

		// Pass 3: Primary incorporates feedback
		artifact, err = p.refine(ctx, p.primary, artifact, critique)
		if err != nil {
			return "", stats, fmt.Errorf("primary refinement (cycle %d): %w", cycle+1, err)
		}
		slog.Debug("primary refined artifact", "length", len(artifact))

		// Measure how much the primary changed the artifact after receiving feedback.
		stats.RefinementChangePct = lineDiffRatio(preReviewArtifact, artifact)
	}

	return artifact, stats, nil
}

// noToolsInstruction is appended to all review-pipeline prompts to prevent the
// LLM from writing files directly via OpenCode tools. The review pipeline
// expects the artifact as response text — if the LLM writes files instead,
// we capture only a summary and the actual content gets overwritten.
const noToolsInstruction = `

CRITICAL: Return ALL output directly in your response text. Do NOT use any file editing tools, shell commands, or other tools — your response text IS the deliverable. Do not write, create, or modify any files.`

// generate creates an artifact using the given model.
func (p *ReviewPipeline) generate(ctx context.Context, model ModelRef, prompt string) (string, error) {
	session, err := p.client.CreateSession(ctx, "generate", p.directory)
	if err != nil {
		return "", err
	}
	defer p.client.DeleteSession(ctx, session.ID, p.directory)

	resp, err := p.client.SendPrompt(ctx, session.ID, prompt+noToolsInstruction, model, p.directory)
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

	resp, err := p.client.SendPrompt(ctx, session.ID, reviewPrompt+noToolsInstruction, model, p.directory)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// refine incorporates review feedback into the artifact.
func (p *ReviewPipeline) refine(ctx context.Context, model ModelRef, artifact, critique string) (string, error) {
	session, err := p.client.CreateSession(ctx, "refine", p.directory)
	if err != nil {
		return "", err
	}
	defer p.client.DeleteSession(ctx, session.ID, p.directory)

	prompt := fmt.Sprintf(`Here is an artifact that has been critically reviewed. Incorporate ALL valid feedback and produce the final, improved version.

## Original Artifact

%s

## Review Feedback

%s`, artifact, critique)

	prompt += "\n\n## Instructions\n\nProduce the complete, final version of the artifact incorporating all valid feedback. Output ONLY the artifact content — no preamble, no commentary." + noToolsInstruction

	resp, err := p.client.SendPrompt(ctx, session.ID, prompt, model, p.directory)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
