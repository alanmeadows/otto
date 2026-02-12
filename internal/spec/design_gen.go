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

// SpecDesign generates or refines the design document for a spec.
func SpecDesign(
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

	if err := CheckPrerequisites(spec, "design"); err != nil {
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

	// Read all existing artifacts
	requirementsMD := readArtifact(spec.RequirementsPath)
	researchMD := readArtifact(spec.ResearchPath)
	existingDesignMD := readArtifact(spec.DesignPath)
	tasksMD := readArtifact(spec.TasksPath)
	questionsMD := readArtifact(spec.QuestionsPath)

	summary, err := AnalyzeCodebase(repoDir)
	if err != nil {
		return fmt.Errorf("analyzing codebase: %w", err)
	}

	// Build data map for template
	data := map[string]string{
		"requirements_md":  requirementsMD,
		"research_md":      researchMD,
		"codebase_summary": summary.String(),
	}
	if existingDesignMD != "" {
		data["existing_design_md"] = existingDesignMD
	}
	if tasksMD != "" {
		data["tasks_md"] = tasksMD
	}
	if questionsMD != "" {
		data["questions_md"] = questionsMD
	}

	rendered, err := prompts.Execute("design.md", data)
	if err != nil {
		return fmt.Errorf("rendering design prompt: %w", err)
	}

	pipeline := buildReviewPipeline(client, repoDir, cfg)

	contextData := map[string]string{
		"Requirements": requirementsMD,
		"Research":     researchMD,
	}
	if summary.String() != "" {
		contextData["Codebase Summary"] = summary.String()
	}

	result, stats, err := pipeline.Review(ctx, rendered, contextData)
	if err != nil {
		return fmt.Errorf("review pipeline: %w", err)
	}

	printReviewStats("Design", stats)

	// Split questions from design output before writing.
	designContent, _ := SplitQuestions(result)

	if err := store.WriteBody(spec.DesignPath, designContent); err != nil {
		return fmt.Errorf("writing design: %w", err)
	}

	// Extract and append questions from the output.
	ExtractAndAppendQuestions(result, spec)

	// Auto-resolve any new questions.
	ResolveAndReport(ctx, client, cfg, repoDir, spec, "design")

	slog.Info("design updated", "spec", spec.Slug)
	return nil
}
