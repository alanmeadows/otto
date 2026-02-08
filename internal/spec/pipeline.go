package spec

import "fmt"

// CheckPrerequisites verifies that all prerequisite artifacts exist for a given command.
// Returns nil if all prerequisites are satisfied, or an error describing what is missing
// and which command to run next.
func CheckPrerequisites(spec *Spec, command string) error {
	switch command {
	case "requirements":
		// No prerequisites — first step in the pipeline.
		return nil

	case "research":
		if !spec.HasRequirements() {
			return fmt.Errorf("research requires requirements.md — run 'otto spec requirements' first")
		}
		return nil

	case "design":
		if !spec.HasRequirements() {
			return fmt.Errorf("design requires requirements.md — run 'otto spec requirements' first")
		}
		if !spec.HasResearch() {
			return fmt.Errorf("design requires research.md — run 'otto spec research' first")
		}
		return nil

	case "task-generate":
		if !spec.HasRequirements() {
			return fmt.Errorf("task-generate requires requirements.md — run 'otto spec requirements' first")
		}
		if !spec.HasResearch() {
			return fmt.Errorf("task-generate requires research.md — run 'otto spec research' first")
		}
		if !spec.HasDesign() {
			return fmt.Errorf("task-generate requires design.md — run 'otto spec design' first")
		}
		return nil

	case "execute":
		if !spec.HasRequirements() {
			return fmt.Errorf("execute requires requirements.md — run 'otto spec requirements' first")
		}
		if !spec.HasResearch() {
			return fmt.Errorf("execute requires research.md — run 'otto spec research' first")
		}
		if !spec.HasDesign() {
			return fmt.Errorf("execute requires design.md — run 'otto spec design' first")
		}
		if !spec.HasTasks() {
			return fmt.Errorf("execute requires tasks.md — run 'otto spec task generate' first")
		}
		return nil

	case "run":
		// Exempt from pipeline enforcement — ad-hoc escape hatch.
		return nil

	default:
		return fmt.Errorf("unknown command %q", command)
	}
}
