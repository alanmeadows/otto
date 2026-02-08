package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alanmeadows/otto/internal/store"
)

// Spec represents a specification and its on-disk artifacts.
type Spec struct {
	Slug             string
	Dir              string
	RequirementsPath string
	ResearchPath     string
	DesignPath       string
	TasksPath        string
	QuestionsPath    string
	HistoryDir       string
}

// specsRoot returns the path to the specs directory within a repo.
func specsRoot(repoDir string) string {
	return filepath.Join(repoDir, ".otto", "specs")
}

// populatePaths fills in all path fields for a Spec given its slug and repo root.
func populatePaths(slug, repoDir string) Spec {
	dir := filepath.Join(specsRoot(repoDir), slug)
	return Spec{
		Slug:             slug,
		Dir:              dir,
		RequirementsPath: filepath.Join(dir, "requirements.md"),
		ResearchPath:     filepath.Join(dir, "research.md"),
		DesignPath:       filepath.Join(dir, "design.md"),
		TasksPath:        filepath.Join(dir, "tasks.md"),
		QuestionsPath:    filepath.Join(dir, "questions.md"),
		HistoryDir:       filepath.Join(dir, "history"),
	}
}

// LoadSpec resolves .otto/specs/<slug>/ and returns a Spec.
// Returns an error if the spec directory does not exist.
func LoadSpec(slug, repoDir string) (*Spec, error) {
	s := populatePaths(slug, repoDir)
	info, err := os.Stat(s.Dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("spec %q not found at %s", slug, s.Dir)
	}
	return &s, nil
}

// ListSpecs lists all specs under .otto/specs/.
func ListSpecs(repoDir string) ([]Spec, error) {
	root := specsRoot(repoDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing specs: %w", err)
	}

	var specs []Spec
	for _, e := range entries {
		if e.IsDir() {
			specs = append(specs, populatePaths(e.Name(), repoDir))
		}
	}
	return specs, nil
}

// ResolveSpec resolves a spec slug with convenience matching:
//   - If slug is empty and exactly one spec exists, use it.
//   - If slug is a prefix that matches exactly one spec, use it.
//   - Otherwise return an error.
func ResolveSpec(slug, repoDir string) (*Spec, error) {
	specs, err := ListSpecs(repoDir)
	if err != nil {
		return nil, err
	}

	if slug == "" {
		if len(specs) == 0 {
			return nil, fmt.Errorf("no specs found in %s", specsRoot(repoDir))
		}
		if len(specs) == 1 {
			return &specs[0], nil
		}
		names := make([]string, len(specs))
		for i, s := range specs {
			names[i] = s.Slug
		}
		return nil, fmt.Errorf("multiple specs found, specify one with --spec: %s", strings.Join(names, ", "))
	}

	// Exact match first.
	for _, s := range specs {
		if s.Slug == slug {
			return &s, nil
		}
	}

	// Prefix match.
	var matches []Spec
	for _, s := range specs {
		if strings.HasPrefix(s.Slug, slug) {
			matches = append(matches, s)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("spec %q not found", slug)
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}

	names := make([]string, len(matches))
	for i, s := range matches {
		names[i] = s.Slug
	}
	return nil, fmt.Errorf("ambiguous spec prefix %q matches: %s", slug, strings.Join(names, ", "))
}

// CreateSpecDir creates the .otto/specs/<slug>/ and history/ directories.
func CreateSpecDir(slug, repoDir string) error {
	dir := filepath.Join(specsRoot(repoDir), slug)
	historyDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("creating spec directory: %w", err)
	}
	return nil
}

// HasRequirements returns true if requirements.md exists.
func (s *Spec) HasRequirements() bool {
	return store.Exists(s.RequirementsPath)
}

// HasResearch returns true if research.md exists.
func (s *Spec) HasResearch() bool {
	return store.Exists(s.ResearchPath)
}

// HasDesign returns true if design.md exists.
func (s *Spec) HasDesign() bool {
	return store.Exists(s.DesignPath)
}

// HasTasks returns true if tasks.md exists.
func (s *Spec) HasTasks() bool {
	return store.Exists(s.TasksPath)
}

// HasQuestions returns true if questions.md exists.
func (s *Spec) HasQuestions() bool {
	return store.Exists(s.QuestionsPath)
}

// slugRegexp matches non-alphanumeric, non-hyphen characters.
var slugRegexp = regexp.MustCompile(`[^a-z0-9-]+`)

// multiHyphen collapses multiple consecutive hyphens.
var multiHyphen = regexp.MustCompile(`-{2,}`)

// GenerateSlug sanitizes a prompt string to a kebab-case slug, truncated to 50 characters.
func GenerateSlug(prompt string) string {
	s := strings.ToLower(strings.TrimSpace(prompt))
	s = slugRegexp.ReplaceAllString(s, "-")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if len(s) > 50 {
		s = s[:50]
		// Don't end with a hyphen after truncation.
		s = strings.TrimRight(s, "-")
	}

	if s == "" {
		s = "spec"
	}
	return s
}
