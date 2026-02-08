package spec

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
)

// SpecRun runs an ad-hoc prompt against a spec's full context.
// It does NOT use the review pipeline or delete the session.
func SpecRun(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
	prompt string,
) (string, error) {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return "", err
	}

	// Read all artifacts as context.
	var contextBuf strings.Builder

	requirementsMD := readArtifact(spec.RequirementsPath)
	if requirementsMD != "" {
		contextBuf.WriteString("## Requirements\n\n")
		contextBuf.WriteString(requirementsMD)
		contextBuf.WriteString("\n\n")
	}

	researchMD := readArtifact(spec.ResearchPath)
	if researchMD != "" {
		contextBuf.WriteString("## Research\n\n")
		contextBuf.WriteString(researchMD)
		contextBuf.WriteString("\n\n")
	}

	designMD := readArtifact(spec.DesignPath)
	if designMD != "" {
		contextBuf.WriteString("## Design\n\n")
		contextBuf.WriteString(designMD)
		contextBuf.WriteString("\n\n")
	}

	tasksMD := readArtifact(spec.TasksPath)
	if tasksMD != "" {
		contextBuf.WriteString("## Tasks\n\n")
		contextBuf.WriteString(tasksMD)
		contextBuf.WriteString("\n\n")
	}

	questionsMD := readArtifact(spec.QuestionsPath)
	if questionsMD != "" {
		contextBuf.WriteString("## Questions\n\n")
		contextBuf.WriteString(questionsMD)
		contextBuf.WriteString("\n\n")
	}

	// Build combined prompt.
	fullPrompt := fmt.Sprintf(`Here is the full context for this specification:

%s
## User Request

%s`, contextBuf.String(), prompt)

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)

	// Create session.
	session, err := client.CreateSession(ctx, "spec-run", repoDir)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}

	// Send prompt.
	resp, err := client.SendPrompt(ctx, session.ID, fullPrompt, primaryModel, repoDir)
	if err != nil {
		return "", fmt.Errorf("sending prompt: %w", err)
	}

	// Intentionally do NOT delete session — user may continue interacting.
	slog.Info("spec run completed", "spec", spec.Slug, "session", session.ID)

	return resp.Content, nil
}
