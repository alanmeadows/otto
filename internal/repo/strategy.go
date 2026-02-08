package repo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/alanmeadows/otto/internal/config"
)

// Strategy defines how otto manages branches for a repository.
type Strategy interface {
	CreateBranch(baseBranch, name string) (workDir string, err error)
	SwitchTo(name string) (workDir string, err error)
	Remove(name string, force bool) error
	List() ([]BranchInfo, error)
	CurrentBranch() (string, error)
}

// BranchInfo holds information about a branch.
type BranchInfo struct {
	Name      string
	WorkDir   string
	IsCurrent bool
}

// TemplateData provides data for branch name templating.
type TemplateData struct {
	Name string
}

// NewStrategy creates the appropriate strategy for a repo config.
func NewStrategy(repo config.RepoConfig) Strategy {
	switch repo.GitStrategy {
	case config.GitStrategyWorktree:
		return &WorktreeStrategy{repo: repo}
	case config.GitStrategyBranch:
		return &BranchStrategy{repo: repo}
	case config.GitStrategyHandsOff:
		return &HandsOffStrategy{repo: repo}
	default:
		return &HandsOffStrategy{repo: repo}
	}
}

// renderBranchName renders a branch name from a template and name.
func renderBranchName(tmpl, name string) (string, error) {
	if tmpl == "" {
		tmpl = "otto/{{.Name}}"
	}
	t, err := template.New("branch").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing branch template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, TemplateData{Name: name}); err != nil {
		return "", fmt.Errorf("executing branch template: %w", err)
	}
	return buf.String(), nil
}

// ReverseBranchTemplate extracts the logical name from a full branch name
// by reversing the template pattern.
func ReverseBranchTemplate(tmpl, fullBranch string) (string, error) {
	if tmpl == "" {
		tmpl = "otto/{{.Name}}"
	}

	// Replace {{.Name}} or {{.name}} with a marker to split on
	const marker = "\x00CAPTURE\x00"
	pattern := strings.Replace(tmpl, "{{.Name}}", marker, 1)
	pattern = strings.Replace(pattern, "{{.name}}", marker, 1)

	parts := strings.SplitN(pattern, marker, 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("template %q does not contain {{.Name}} placeholder", tmpl)
	}

	prefix := parts[0]
	suffix := parts[1]

	if !strings.HasPrefix(fullBranch, prefix) {
		return "", fmt.Errorf("branch %q does not match template prefix %q", fullBranch, prefix)
	}
	if suffix != "" && !strings.HasSuffix(fullBranch, suffix) {
		return "", fmt.Errorf("branch %q does not match template suffix %q", fullBranch, suffix)
	}

	name := strings.TrimPrefix(fullBranch, prefix)
	if suffix != "" {
		name = strings.TrimSuffix(name, suffix)
	}

	if name == "" {
		return "", fmt.Errorf("extracted empty name from branch %q with template %q", fullBranch, tmpl)
	}

	return name, nil
}

// --- WorktreeStrategy ---

// WorktreeStrategy manages branches using git worktrees.
type WorktreeStrategy struct {
	repo config.RepoConfig
}

func (s *WorktreeStrategy) CreateBranch(baseBranch, name string) (string, error) {
	branchName, err := renderBranchName(s.repo.BranchTemplate, name)
	if err != nil {
		return "", err
	}

	worktreeDir := s.repo.WorktreeDir
	if worktreeDir == "" {
		worktreeDir = filepath.Join(filepath.Dir(s.repo.PrimaryDir), "worktrees")
	}
	workDir := filepath.Join(worktreeDir, name)

	args := []string{"worktree", "add", workDir, "-b", branchName}
	if baseBranch != "" {
		args = append(args, baseBranch)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = s.repo.PrimaryDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add: %s: %w", string(out), err)
	}

	return workDir, nil
}

func (s *WorktreeStrategy) SwitchTo(name string) (string, error) {
	worktreeDir := s.repo.WorktreeDir
	if worktreeDir == "" {
		worktreeDir = filepath.Join(filepath.Dir(s.repo.PrimaryDir), "worktrees")
	}
	workDir := filepath.Join(worktreeDir, name)

	if _, err := os.Stat(workDir); err != nil {
		return "", fmt.Errorf("worktree %q does not exist: %w", workDir, err)
	}

	return workDir, nil
}

func (s *WorktreeStrategy) Remove(name string, force bool) error {
	worktreeDir := s.repo.WorktreeDir
	if worktreeDir == "" {
		worktreeDir = filepath.Join(filepath.Dir(s.repo.PrimaryDir), "worktrees")
	}
	workDir := filepath.Join(worktreeDir, name)

	args := []string{"worktree", "remove", workDir}
	if force {
		args = append(args, "--force")
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = s.repo.PrimaryDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", string(out), err)
	}
	return nil
}

func (s *WorktreeStrategy) List() ([]BranchInfo, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = s.repo.PrimaryDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}

	return parseWorktreeList(string(out)), nil
}

func (s *WorktreeStrategy) CurrentBranch() (string, error) {
	return getCurrentBranch(s.repo.PrimaryDir)
}

// parseWorktreeList parses the porcelain output of git worktree list.
func parseWorktreeList(output string) []BranchInfo {
	var infos []BranchInfo
	var current BranchInfo

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if current.WorkDir != "" {
				infos = append(infos, current)
				current = BranchInfo{}
			}
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			current.WorkDir = strings.TrimPrefix(line, "worktree ")
		}
		if strings.HasPrefix(line, "branch ") {
			ref := strings.TrimPrefix(line, "branch ")
			current.Name = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	if current.WorkDir != "" {
		infos = append(infos, current)
	}

	return infos
}

// --- BranchStrategy ---

// BranchStrategy manages branches in the primary directory.
type BranchStrategy struct {
	repo config.RepoConfig
}

func (s *BranchStrategy) CreateBranch(baseBranch, name string) (string, error) {
	branchName, err := renderBranchName(s.repo.BranchTemplate, name)
	if err != nil {
		return "", err
	}

	args := []string{"checkout", "-b", branchName}
	if baseBranch != "" {
		args = append(args, baseBranch)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = s.repo.PrimaryDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git checkout -b: %s: %w", string(out), err)
	}

	return s.repo.PrimaryDir, nil
}

func (s *BranchStrategy) SwitchTo(name string) (string, error) {
	branchName, err := renderBranchName(s.repo.BranchTemplate, name)
	if err != nil {
		return "", err
	}

	cmd := exec.Command("git", "checkout", branchName)
	cmd.Dir = s.repo.PrimaryDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git checkout: %s: %w", string(out), err)
	}

	return s.repo.PrimaryDir, nil
}

func (s *BranchStrategy) Remove(name string, force bool) error {
	branchName, err := renderBranchName(s.repo.BranchTemplate, name)
	if err != nil {
		return err
	}

	flag := "-d"
	if force {
		flag = "-D"
	}

	cmd := exec.Command("git", "branch", flag, branchName)
	cmd.Dir = s.repo.PrimaryDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch %s: %s: %w", flag, string(out), err)
	}
	return nil
}

func (s *BranchStrategy) List() ([]BranchInfo, error) {
	cmd := exec.Command("git", "branch", "--list")
	cmd.Dir = s.repo.PrimaryDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch --list: %w", err)
	}

	var infos []BranchInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		isCurrent := strings.HasPrefix(line, "* ")
		name := strings.TrimPrefix(line, "* ")
		name = strings.TrimSpace(name)
		infos = append(infos, BranchInfo{
			Name:      name,
			WorkDir:   s.repo.PrimaryDir,
			IsCurrent: isCurrent,
		})
	}

	return infos, nil
}

func (s *BranchStrategy) CurrentBranch() (string, error) {
	return getCurrentBranch(s.repo.PrimaryDir)
}

// --- HandsOffStrategy ---

// HandsOffStrategy is read-only — does not create or remove branches.
type HandsOffStrategy struct {
	repo config.RepoConfig
}

func (s *HandsOffStrategy) CreateBranch(baseBranch, name string) (string, error) {
	return "", fmt.Errorf("hands-off strategy does not create branches")
}

func (s *HandsOffStrategy) SwitchTo(name string) (string, error) {
	return s.repo.PrimaryDir, nil
}

func (s *HandsOffStrategy) Remove(name string, force bool) error {
	return fmt.Errorf("hands-off strategy does not remove branches")
}

func (s *HandsOffStrategy) List() ([]BranchInfo, error) {
	branch, err := getCurrentBranch(s.repo.PrimaryDir)
	if err != nil {
		return nil, err
	}
	return []BranchInfo{
		{Name: branch, WorkDir: s.repo.PrimaryDir, IsCurrent: true},
	}, nil
}

func (s *HandsOffStrategy) CurrentBranch() (string, error) {
	return getCurrentBranch(s.repo.PrimaryDir)
}

// getCurrentBranch gets the current branch name.
func getCurrentBranch(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --abbrev-ref HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
