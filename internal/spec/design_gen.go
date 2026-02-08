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
) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	if err := CheckPrerequisites(spec, "design"); err != nil {
		return err
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

	result, err := pipeline.Review(ctx, rendered, contextData)
	if err != nil {
		return fmt.Errorf("review pipeline: %w", err)
	}

	if err := store.WriteBody(spec.DesignPath, result); err != nil {
		return fmt.Errorf("writing design: %w", err)
	}

	slog.Info("design updated", "spec", spec.Slug)
	return nil
}
