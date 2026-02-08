package spec

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// SpecList lists all specs with their artifact status and task completion.
func SpecList(w io.Writer, repoDir string) error {
	specs, err := ListSpecs(repoDir)
	if err != nil {
		return err
	}

	if len(specs) == 0 {
		fmt.Fprintln(w, "No specs found.")
		return nil
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	cellStyle := lipgloss.NewStyle().Padding(0, 1)

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
		Headers("Slug", "R", "Re", "D", "T", "Q", "Progress").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return cellStyle
		})

	for _, s := range specs {
		r := indicator(s.HasRequirements())
		re := indicator(s.HasResearch())
		d := indicator(s.HasDesign())
		tk := indicator(s.HasTasks())
		q := indicator(s.HasQuestions())

		progress := taskProgress(&s)

		t = t.Row(s.Slug, r, re, d, tk, q, progress)
	}

	fmt.Fprintln(w, t.String())
	return nil
}

// indicator returns a check or dash for artifact presence.
func indicator(exists bool) string {
	if exists {
		return "✓"
	}
	return "·"
}

// taskProgress returns a completion string like "3/10 (30%)" for a spec.
func taskProgress(s *Spec) string {
	if !s.HasTasks() {
		return "-"
	}

	tasks, err := ParseTasks(s.TasksPath)
	if err != nil {
		return "err"
	}

	if len(tasks) == 0 {
		return "0/0"
	}

	completed := 0
	for _, t := range tasks {
		if t.Status == TaskStatusCompleted {
			completed++
		}
	}

	pct := (completed * 100) / len(tasks)
	return fmt.Sprintf("%d/%d (%d%%)", completed, len(tasks), pct)
}

// FormatSpecSummary returns a one-line summary of a spec's status.
func FormatSpecSummary(s *Spec) string {
	var parts []string
	if s.HasRequirements() {
		parts = append(parts, "R")
	}
	if s.HasResearch() {
		parts = append(parts, "Re")
	}
	if s.HasDesign() {
		parts = append(parts, "D")
	}
	if s.HasTasks() {
		parts = append(parts, "T")
	}
	if s.HasQuestions() {
		parts = append(parts, "Q")
	}

	status := strings.Join(parts, ",")
	if status == "" {
		status = "empty"
	}

	return fmt.Sprintf("%s [%s]", s.Slug, status)
}
