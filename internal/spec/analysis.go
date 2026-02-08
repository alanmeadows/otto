package spec

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CodebaseSummary holds the analysis results for a project directory.
type CodebaseSummary struct {
	Archetype     string   // "go-cli", "go-controller", "node", etc.
	Language      string   // "go", "typescript", etc.
	ProjectLayout []string // top-level dirs
	Dependencies  []string // key deps with versions
	LoggingLib    string
	TestFramework string
	ErrorStyle    string
	ConfigLoading string
	HasDockerfile bool
	HasMakefile   bool
	HasCI         bool
}

// AnalyzeCodebase analyzes a repository directory and returns a summary.
func AnalyzeCodebase(repoDir string) (*CodebaseSummary, error) {
	summary := &CodebaseSummary{}

	// Detect archetype and language
	summary.detectArchetype(repoDir)

	// List top-level directories
	summary.listTopLevelDirs(repoDir)

	// Parse dependencies
	summary.parseDependencies(repoDir)

	// Scan Go imports for patterns
	if summary.Language == "go" {
		summary.scanGoPatterns(repoDir)
	}

	// Check for common files
	summary.HasDockerfile = fileExists(repoDir, "Dockerfile")
	summary.HasMakefile = fileExists(repoDir, "Makefile")
	summary.HasCI = dirExists(repoDir, ".github", "workflows") ||
		fileExists(repoDir, ".gitlab-ci.yml") ||
		dirExists(repoDir, ".circleci")

	return summary, nil
}

// String renders the summary as a human-readable string.
func (s *CodebaseSummary) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Archetype: %s\n", s.Archetype)
	fmt.Fprintf(&b, "Language: %s\n", s.Language)
	fmt.Fprintf(&b, "Project Layout: %s\n", strings.Join(s.ProjectLayout, ", "))

	if len(s.Dependencies) > 0 {
		fmt.Fprintf(&b, "Key Dependencies:\n")
		for _, d := range s.Dependencies {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
	}

	if s.LoggingLib != "" {
		fmt.Fprintf(&b, "Logging: %s\n", s.LoggingLib)
	}
	if s.TestFramework != "" {
		fmt.Fprintf(&b, "Test Framework: %s\n", s.TestFramework)
	}
	if s.ErrorStyle != "" {
		fmt.Fprintf(&b, "Error Style: %s\n", s.ErrorStyle)
	}
	if s.ConfigLoading != "" {
		fmt.Fprintf(&b, "Config Loading: %s\n", s.ConfigLoading)
	}

	fmt.Fprintf(&b, "Dockerfile: %v\n", s.HasDockerfile)
	fmt.Fprintf(&b, "Makefile: %v\n", s.HasMakefile)
	fmt.Fprintf(&b, "CI: %v\n", s.HasCI)

	return b.String()
}

func (s *CodebaseSummary) detectArchetype(repoDir string) {
	switch {
	case fileExists(repoDir, "go.mod"):
		s.Language = "go"
		// Determine Go sub-archetype
		if dirExists(repoDir, "cmd") {
			s.Archetype = "go-cli"
		} else if dirExists(repoDir, "controllers") || dirExists(repoDir, "api") {
			s.Archetype = "go-controller"
		} else if dirExists(repoDir, "pkg") || dirExists(repoDir, "internal") {
			s.Archetype = "go-library"
		} else {
			s.Archetype = "go"
		}
	case fileExists(repoDir, "package.json"):
		s.Language = "typescript"
		if fileExists(repoDir, "next.config.js") || fileExists(repoDir, "next.config.mjs") {
			s.Archetype = "nextjs"
		} else if fileExists(repoDir, "tsconfig.json") {
			s.Archetype = "node-ts"
		} else {
			s.Archetype = "node"
		}
	case fileExists(repoDir, "Cargo.toml"):
		s.Language = "rust"
		s.Archetype = "rust"
	case fileExists(repoDir, "pyproject.toml") || fileExists(repoDir, "setup.py") || fileExists(repoDir, "requirements.txt"):
		s.Language = "python"
		s.Archetype = "python"
	case fileExists(repoDir, "pom.xml") || fileExists(repoDir, "build.gradle"):
		s.Language = "java"
		s.Archetype = "java"
	default:
		s.Language = "unknown"
		s.Archetype = "unknown"
	}
}

func (s *CodebaseSummary) listTopLevelDirs(repoDir string) {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			s.ProjectLayout = append(s.ProjectLayout, e.Name())
		}
	}
	sort.Strings(s.ProjectLayout)
}

func (s *CodebaseSummary) parseDependencies(repoDir string) {
	goModPath := filepath.Join(repoDir, "go.mod")
	if f, err := os.Open(goModPath); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		inRequire := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "require (") || strings.HasPrefix(line, "require(") {
				inRequire = true
				continue
			}
			if inRequire && line == ")" {
				inRequire = false
				continue
			}
			if inRequire {
				// Skip indirect deps
				if strings.Contains(line, "// indirect") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					s.Dependencies = append(s.Dependencies, parts[0]+" "+parts[1])
				}
			}
			// Single-line require
			if strings.HasPrefix(line, "require ") && !strings.Contains(line, "(") {
				parts := strings.Fields(line)
				if len(parts) >= 3 {
					s.Dependencies = append(s.Dependencies, parts[1]+" "+parts[2])
				}
			}
		}
		return
	}

	pkgPath := filepath.Join(repoDir, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		// Simple extraction — look for "dependencies" keys
		content := string(data)
		if strings.Contains(content, "dependencies") {
			s.Dependencies = append(s.Dependencies, "(see package.json)")
		}
	}
}

func (s *CodebaseSummary) scanGoPatterns(repoDir string) {
	importCounts := map[string]int{}

	_ = filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip hidden dirs and vendor
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())

			// Detect imports
			if strings.Contains(line, `"log/slog"`) {
				importCounts["slog"]++
			}
			if strings.Contains(line, `"go.uber.org/zap"`) {
				importCounts["zap"]++
			}
			if strings.Contains(line, `"github.com/go-logr/logr"`) {
				importCounts["logr"]++
			}
			if strings.Contains(line, `"github.com/rs/zerolog"`) {
				importCounts["zerolog"]++
			}
			if strings.Contains(line, `"github.com/charmbracelet/log"`) {
				importCounts["charmbracelet/log"]++
			}

			// Test frameworks
			if strings.Contains(line, `"github.com/stretchr/testify`) {
				importCounts["testify"]++
			}
			if strings.Contains(line, `"github.com/onsi/ginkgo`) {
				importCounts["ginkgo"]++
			}
			if strings.Contains(line, `"github.com/onsi/gomega`) {
				importCounts["gomega"]++
			}

			// Error patterns
			if strings.Contains(line, "fmt.Errorf") {
				importCounts["fmt.Errorf"]++
			}
			if strings.Contains(line, "errors.New") {
				importCounts["errors.New"]++
			}
			if strings.Contains(line, "pkg/errors") {
				importCounts["pkg/errors"]++
			}

			// Config loading
			if strings.Contains(line, `"github.com/spf13/viper"`) {
				importCounts["viper"]++
			}
			if strings.Contains(line, `"encoding/json"`) || strings.Contains(line, "jsonc") {
				importCounts["json"]++
			}
		}

		return nil
	})

	// Determine logging lib
	s.LoggingLib = detectBestMatch(importCounts, []string{"slog", "zap", "logr", "zerolog", "charmbracelet/log"})

	// Determine test framework
	s.TestFramework = detectBestMatch(importCounts, []string{"testify", "ginkgo", "gomega"})
	if s.TestFramework == "" {
		s.TestFramework = "stdlib"
	}

	// Determine error style
	s.ErrorStyle = detectBestMatch(importCounts, []string{"fmt.Errorf", "errors.New", "pkg/errors"})

	// Determine config loading
	s.ConfigLoading = detectBestMatch(importCounts, []string{"viper", "json"})
}

func detectBestMatch(counts map[string]int, candidates []string) string {
	best := ""
	bestCount := 0
	for _, c := range candidates {
		if counts[c] > bestCount {
			best = c
			bestCount = counts[c]
		}
	}
	return best
}

func fileExists(dir string, names ...string) bool {
	path := filepath.Join(append([]string{dir}, names...)...)
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(dir string, names ...string) bool {
	path := filepath.Join(append([]string{dir}, names...)...)
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
