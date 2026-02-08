package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alanmeadows/otto/internal/store"
)

// Task represents a single task parsed from tasks.md.
type Task struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Status        string   `json:"status"`
	ParallelGroup int      `json:"parallel_group"`
	DependsOn     []string `json:"depends_on"`
	Description   string   `json:"description"`
	Files         []string `json:"files"`
	RetryCount    int      `json:"retry_count"`
}

// Valid task statuses.
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
	TaskStatusSkipped   = "skipped"
)

// validStatuses is the set of allowed statuses.
var validStatuses = map[string]bool{
	TaskStatusPending:   true,
	TaskStatusRunning:   true,
	TaskStatusCompleted: true,
	TaskStatusFailed:    true,
	TaskStatusSkipped:   true,
}

// Regex patterns for parsing task fields from markdown.
var (
	taskHeaderRe   = regexp.MustCompile(`^##\s+Task\s+\d+:\s*(.+)$`)
	fieldIDRe      = regexp.MustCompile(`^\s*-\s+\*\*id\*\*:\s*(.+)$`)
	fieldStatusRe  = regexp.MustCompile(`^\s*-\s+\*\*status\*\*:\s*(.+)$`)
	fieldGroupRe   = regexp.MustCompile(`^\s*-\s+\*\*parallel_group\*\*:\s*(\d+)`)
	fieldDependsRe = regexp.MustCompile(`^\s*-\s+\*\*depends_on\*\*:\s*(.+)$`)
	fieldDescRe    = regexp.MustCompile(`^\s*-\s+\*\*description\*\*:\s*(.+)$`)
	fieldFilesRe   = regexp.MustCompile(`^\s*-\s+\*\*files\*\*:\s*(.+)$`)
	fieldRetryRe   = regexp.MustCompile(`^\s*-\s+\*\*retry_count\*\*:\s*(\d+)`)
	dependsListRe  = regexp.MustCompile(`task-\d+`)
)

// ParseTasks parses the markdown task format from a tasks.md file.
func ParseTasks(tasksPath string) ([]Task, error) {
	content, err := store.ReadBody(tasksPath)
	if err != nil {
		return nil, fmt.Errorf("reading tasks file: %w", err)
	}

	return ParseTasksFromString(content)
}

// ParseTasksFromString parses tasks from a markdown string.
func ParseTasksFromString(content string) ([]Task, error) {
	lines := strings.Split(content, "\n")
	var tasks []Task
	var current *Task

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Check for task header
		if m := taskHeaderRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				tasks = append(tasks, *current)
			}
			current = &Task{
				Title:     strings.TrimSpace(m[1]),
				DependsOn: []string{},
				Files:     []string{},
			}
			continue
		}

		if current == nil {
			continue
		}

		// Parse fields
		if m := fieldIDRe.FindStringSubmatch(line); m != nil {
			current.ID = strings.TrimSpace(m[1])
			continue
		}

		if m := fieldStatusRe.FindStringSubmatch(line); m != nil {
			status := strings.TrimSpace(m[1])
			if !validStatuses[status] {
				return nil, fmt.Errorf("invalid status %q for task %q", status, current.Title)
			}
			current.Status = status
			continue
		}

		if m := fieldGroupRe.FindStringSubmatch(line); m != nil {
			g, err := strconv.Atoi(m[1])
			if err != nil {
				return nil, fmt.Errorf("invalid parallel_group %q: %w", m[1], err)
			}
			current.ParallelGroup = g
			continue
		}

		if m := fieldDependsRe.FindStringSubmatch(line); m != nil {
			raw := strings.TrimSpace(m[1])
			deps := dependsListRe.FindAllString(raw, -1)
			if deps != nil {
				current.DependsOn = deps
			}
			continue
		}

		if m := fieldDescRe.FindStringSubmatch(line); m != nil {
			current.Description = strings.TrimSpace(m[1])
			continue
		}

		if m := fieldFilesRe.FindStringSubmatch(line); m != nil {
			raw := strings.TrimSpace(m[1])
			current.Files = parseJSONStringArray(raw)
			continue
		}

		if m := fieldRetryRe.FindStringSubmatch(line); m != nil {
			r, _ := strconv.Atoi(m[1])
			current.RetryCount = r
			continue
		}
	}

	// Don't forget the last task
	if current != nil {
		tasks = append(tasks, *current)
	}

	// Validate all tasks have IDs
	for i, t := range tasks {
		if t.ID == "" {
			return nil, fmt.Errorf("task %d (%q) has no id", i+1, t.Title)
		}
		if t.Status == "" {
			tasks[i].Status = TaskStatusPending
		}
	}

	return tasks, nil
}

// parseJSONStringArray attempts to parse a JSON array of strings.
// Falls back to splitting on commas if JSON parsing fails.
func parseJSONStringArray(raw string) []string {
	var result []string
	if err := json.Unmarshal([]byte(raw), &result); err == nil {
		return result
	}

	// Fallback: strip brackets and split
	raw = strings.Trim(raw, "[]")
	parts := strings.Split(raw, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// UpdateTaskStatus updates the status of a task in tasks.md with file-level locking.
func UpdateTaskStatus(tasksPath string, taskID string, status string) error {
	if !validStatuses[status] {
		return fmt.Errorf("invalid status %q", status)
	}

	return store.WithLock(tasksPath, 5*time.Second, func() error {
		content, err := store.ReadBody(tasksPath)
		if err != nil {
			return fmt.Errorf("reading tasks: %w", err)
		}

		// Find and replace the status line for the given task ID
		lines := strings.Split(content, "\n")
		foundID := false
		updated := false

		for i, line := range lines {
			if m := fieldIDRe.FindStringSubmatch(line); m != nil {
				if strings.TrimSpace(m[1]) == taskID {
					foundID = true
					continue
				}
				// New task ID block — reset flag
				if foundID && !updated {
					return fmt.Errorf("task %q found but has no status field", taskID)
				}
				foundID = false
			}

			if foundID && !updated {
				if fieldStatusRe.MatchString(line) {
					lines[i] = fmt.Sprintf("- **status**: %s", status)
					updated = true
				}
			}
		}

		if !foundID && !updated {
			return fmt.Errorf("task %q not found", taskID)
		}
		if foundID && !updated {
			return fmt.Errorf("task %q found but has no status field", taskID)
		}

		return store.WriteBody(tasksPath, strings.Join(lines, "\n"))
	})
}

// GetRunnableTasks returns tasks with status "pending" whose dependencies are all "completed".
func GetRunnableTasks(tasks []Task) []Task {
	statusMap := make(map[string]string)
	for _, t := range tasks {
		statusMap[t.ID] = t.Status
	}

	var runnable []Task
	for _, t := range tasks {
		if t.Status != TaskStatusPending {
			continue
		}

		allDepsComplete := true
		for _, dep := range t.DependsOn {
			if statusMap[dep] != TaskStatusCompleted {
				allDepsComplete = false
				break
			}
		}

		if allDepsComplete {
			runnable = append(runnable, t)
		}
	}

	return runnable
}

// BuildPhases groups tasks by parallel_group, ordered by group number.
// Validates that all depends_on references for tasks in phase N are satisfied
// by tasks in phases < N.
func BuildPhases(tasks []Task) ([][]Task, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	// Group by parallel_group
	groups := make(map[int][]Task)
	for _, t := range tasks {
		groups[t.ParallelGroup] = append(groups[t.ParallelGroup], t)
	}

	// Get sorted group numbers
	var groupNums []int
	for g := range groups {
		groupNums = append(groupNums, g)
	}
	sort.Ints(groupNums)

	// Build task-to-group mapping
	taskGroup := make(map[string]int)
	for _, t := range tasks {
		taskGroup[t.ID] = t.ParallelGroup
	}

	// Validate dependencies
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			depGroup, exists := taskGroup[dep]
			if !exists {
				return nil, fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
			if depGroup >= t.ParallelGroup {
				return nil, fmt.Errorf(
					"task %q (group %d) depends on task %q (group %d) — dependencies must be in earlier groups",
					t.ID, t.ParallelGroup, dep, depGroup,
				)
			}
		}
	}

	// Build ordered phases
	var phases [][]Task
	for _, g := range groupNums {
		phases = append(phases, groups[g])
	}

	return phases, nil
}
