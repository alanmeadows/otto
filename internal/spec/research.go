package spec

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/store"
)

// SpecResearch generates or refines the research document for a spec.
func SpecResearch(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
	force bool,
) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	if err := CheckPrerequisites(spec, "research"); err != nil {
		return err
	}

	// Question gating: check for unanswered questions from prior phases.
	if !force {
		unanswered, err := CheckUnansweredQuestions(spec)
		if err != nil {
			return err
		}
		if unanswered > 0 {
			return fmt.Errorf("%d unanswered question(s) — run 'otto spec questions' to resolve, or re-run with --force to skip", unanswered)
		}
	}

	// Read requirements (guaranteed to exist by prerequisites)
	requirementsMD := readArtifact(spec.RequirementsPath)

	// Read optional existing artifacts
	existingResearchMD := readArtifact(spec.ResearchPath)
	designMD := readArtifact(spec.DesignPath)
	tasksMD := readArtifact(spec.TasksPath)
	questionsMD := readArtifact(spec.QuestionsPath)

	summary, err := AnalyzeCodebase(repoDir)
	if err != nil {
		return fmt.Errorf("analyzing codebase: %w", err)
	}

	// Build data map for template
	data := map[string]string{
		"requirements_md":  requirementsMD,
		"codebase_summary": summary.String(),
	}
	if existingResearchMD != "" {
		data["existing_research_md"] = existingResearchMD
	}
	if designMD != "" {
		data["design_md"] = designMD
	}
	if tasksMD != "" {
		data["tasks_md"] = tasksMD
	}
	if questionsMD != "" {
		data["questions_md"] = questionsMD
	}

	rendered, err := prompts.Execute("research.md", data)
	if err != nil {
		return fmt.Errorf("rendering research prompt: %w", err)
	}

	pipeline := buildReviewPipeline(client, repoDir, cfg)

	contextData := map[string]string{
		"Requirements": requirementsMD,
	}
	if summary.String() != "" {
		contextData["Codebase Summary"] = summary.String()
	}

	result, stats, err := pipeline.Review(ctx, rendered, contextData)
	if err != nil {
		return fmt.Errorf("review pipeline: %w", err)
	}

	printReviewStats("Research", stats)

	// Split questions from research output before writing.
	researchContent, _ := SplitQuestions(result)

	if err := store.WriteBody(spec.ResearchPath, researchContent); err != nil {
		return fmt.Errorf("writing research: %w", err)
	}

	// Extract and append questions from the output.
	ExtractAndAppendQuestions(result, spec)

	// Auto-resolve any new questions.
	ResolveAndReport(ctx, client, cfg, repoDir, spec, "research")

	slog.Info("research updated", "spec", spec.Slug)
	return nil
}
