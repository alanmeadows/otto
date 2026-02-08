package spec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeCodebase_GoProject(t *testing.T) {
	dir := t.TempDir()

	// Create go.mod
	goMod := `module example.com/test

go 1.21

require (
	github.com/spf13/cobra v1.8.0
	github.com/stretchr/testify v1.9.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
)
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644))

	// Create cmd directory
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd", "app"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmd", "app", "main.go"), []byte(`package main

import (
	"log/slog"
	"fmt"
)

func main() {
	slog.Info("hello")
	fmt.Errorf("test: %w", nil)
}
`), 0644))

	// Create internal directory
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "pkg"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "pkg", "util.go"), []byte(`package pkg

import (
	"encoding/json"
	"fmt"
)

func Load() error {
	_ = json.Unmarshal(nil, nil)
	return fmt.Errorf("not implemented")
}
`), 0644))

	// Create Dockerfile
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM golang:1.21\n"), 0644))

	// Create Makefile
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build ./...\n"), 0644))

	// Create CI dir
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte("name: CI\n"), 0644))

	summary, err := AnalyzeCodebase(dir)
	require.NoError(t, err)

	assert.Equal(t, "go-cli", summary.Archetype)
	assert.Equal(t, "go", summary.Language)
	assert.Contains(t, summary.ProjectLayout, "cmd")
	assert.Contains(t, summary.ProjectLayout, "internal")
	assert.True(t, summary.HasDockerfile)
	assert.True(t, summary.HasMakefile)
	assert.True(t, summary.HasCI)

	// Check dependencies parsed — should have cobra and testify
	assert.GreaterOrEqual(t, len(summary.Dependencies), 2)

	// Check logging detected
	assert.Equal(t, "slog", summary.LoggingLib)

	// Check error style
	assert.Equal(t, "fmt.Errorf", summary.ErrorStyle)

	// Check config loading
	assert.Equal(t, "json", summary.ConfigLoading)

	// Test String() method
	str := summary.String()
	assert.Contains(t, str, "go-cli")
	assert.Contains(t, str, "Logging: slog")
}

func TestAnalyzeCodebase_NodeProject(t *testing.T) {
	dir := t.TempDir()

	pkgJSON := `{
  "name": "test-app",
  "version": "1.0.0",
  "dependencies": {
    "express": "^4.18.0"
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}"), 0644))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0755))

	summary, err := AnalyzeCodebase(dir)
	require.NoError(t, err)

	assert.Equal(t, "node-ts", summary.Archetype)
	assert.Equal(t, "typescript", summary.Language)
	assert.Contains(t, summary.ProjectLayout, "src")
}

func TestAnalyzeCodebase_RustProject(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(`[package]
name = "test"
version = "0.1.0"
`), 0644))

	summary, err := AnalyzeCodebase(dir)
	require.NoError(t, err)

	assert.Equal(t, "rust", summary.Archetype)
	assert.Equal(t, "rust", summary.Language)
}

func TestAnalyzeCodebase_PythonProject(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"test\"\n"), 0644))

	summary, err := AnalyzeCodebase(dir)
	require.NoError(t, err)

	assert.Equal(t, "python", summary.Archetype)
	assert.Equal(t, "python", summary.Language)
}

func TestAnalyzeCodebase_UnknownProject(t *testing.T) {
	dir := t.TempDir()

	summary, err := AnalyzeCodebase(dir)
	require.NoError(t, err)

	assert.Equal(t, "unknown", summary.Archetype)
	assert.Equal(t, "unknown", summary.Language)
	assert.False(t, summary.HasDockerfile)
	assert.False(t, summary.HasMakefile)
	assert.False(t, summary.HasCI)
}

func TestCodebaseSummaryString(t *testing.T) {
	s := &CodebaseSummary{
		Archetype:     "go-cli",
		Language:      "go",
		ProjectLayout: []string{"cmd", "internal", "pkg"},
		Dependencies:  []string{"cobra v1.8.0", "testify v1.9.0"},
		LoggingLib:    "slog",
		TestFramework: "testify",
		ErrorStyle:    "fmt.Errorf",
		ConfigLoading: "json",
		HasDockerfile: true,
		HasMakefile:   true,
		HasCI:         true,
	}

	str := s.String()
	assert.Contains(t, str, "Archetype: go-cli")
	assert.Contains(t, str, "Language: go")
	assert.Contains(t, str, "cmd, internal, pkg")
	assert.Contains(t, str, "cobra v1.8.0")
	assert.Contains(t, str, "Logging: slog")
	assert.Contains(t, str, "Test Framework: testify")
	assert.Contains(t, str, "Error Style: fmt.Errorf")
	assert.Contains(t, str, "Config Loading: json")
	assert.Contains(t, str, "Dockerfile: true")
	assert.Contains(t, str, "Makefile: true")
	assert.Contains(t, str, "CI: true")
}
