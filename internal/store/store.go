package store

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"
)

// Document represents a markdown file with YAML frontmatter.
type Document struct {
	Frontmatter map[string]any
	Body        string
}

// ReadDocument reads a markdown file with YAML frontmatter.
func ReadDocument(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading document %s: %w", path, err)
	}

	var matter map[string]any
	body, err := frontmatter.Parse(strings.NewReader(string(data)), &matter)
	if err != nil {
		// If no frontmatter, entire content is the body.
		// Log at debug level since this is common for plain markdown files.
		slog.Debug("no frontmatter found in document", "path", path, "error", err)
		return &Document{
			Frontmatter: make(map[string]any),
			Body:        string(data),
		}, nil
	}

	return &Document{
		Frontmatter: matter,
		Body:        string(body),
	}, nil
}

// WriteDocument writes a markdown file with YAML frontmatter.
func WriteDocument(path string, doc *Document) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}

	var buf bytes.Buffer

	// Write frontmatter if non-empty
	if len(doc.Frontmatter) > 0 {
		buf.WriteString("---\n")
		fm, err := yaml.Marshal(doc.Frontmatter)
		if err != nil {
			return fmt.Errorf("marshaling frontmatter: %w", err)
		}
		buf.Write(fm)
		buf.WriteString("---\n\n")
	}

	buf.WriteString(doc.Body)

	return atomicWriteFile(path, buf.Bytes(), 0644)
}

// ReadBody reads just the body of a markdown file (ignoring frontmatter).
func ReadBody(path string) (string, error) {
	doc, err := ReadDocument(path)
	if err != nil {
		return "", err
	}
	return doc.Body, nil
}

// WriteBody writes just a markdown body to a file (no frontmatter).
func WriteBody(path string, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}
	return atomicWriteFile(path, []byte(body), 0644)
}

// atomicWriteFile writes data to a temp file then renames it into place,
// preventing partial writes on crash or disk-full.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Exists checks if a file exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
