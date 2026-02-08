package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed *.md
var builtinFS embed.FS

// Load returns the prompt template for the given name.
// Checks user override at ~/.config/otto/prompts/<name> first.
func Load(name string) (*template.Template, error) {
	// Check user override
	configDir, err := os.UserConfigDir()
	if err == nil {
		userPath := filepath.Join(configDir, "otto", "prompts", name)
		if data, err := os.ReadFile(userPath); err == nil {
			return template.New(name).Parse(string(data))
		}
	}

	// Fall back to embedded
	data, err := builtinFS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("loading prompt template %s: %w", name, err)
	}
	return template.New(name).Parse(string(data))
}

// Execute loads a template and executes it with the given data map.
func Execute(name string, data map[string]string) (string, error) {
	tmpl, err := Load(name)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing prompt template %s: %w", name, err)
	}
	return buf.String(), nil
}

// List returns the names of all available prompt templates.
func List() ([]string, error) {
	entries, err := builtinFS.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
