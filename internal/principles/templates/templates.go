// Package templates provides built-in principle set templates that can be
// listed, retrieved, and installed into a project's .forge/principles directory.
package templates

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jelmersnoeck/forge/internal/principles"
	"gopkg.in/yaml.v3"
)

//go:embed *.yaml
var templateFS embed.FS

// TemplateInfo describes an available principle template.
type TemplateInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Principles  int    `json:"principles"` // Number of principles in the set.
}

// ListTemplates returns metadata for all available built-in templates.
func ListTemplates() ([]TemplateInfo, error) {
	entries, err := templateFS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("reading embedded templates: %w", err)
	}

	var infos []TemplateInfo
	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}

		set, err := loadEmbedded(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("loading template %s: %w", entry.Name(), err)
		}

		infos = append(infos, TemplateInfo{
			Name:        set.Name,
			Version:     set.Version,
			Description: set.Description,
			Principles:  len(set.Principles),
		})
	}

	return infos, nil
}

// GetTemplate returns a principle set by template name.
func GetTemplate(name string) (*principles.PrincipleSet, error) {
	filename := name + ".yaml"

	// Check if file exists in embedded FS.
	data, err := templateFS.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("template %q not found: %w", name, err)
	}

	var set principles.PrincipleSet
	if err := yaml.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("parsing template %q: %w", name, err)
	}

	return &set, nil
}

// InstallTemplate copies a built-in template to the destination directory.
// The file is written as <name>.yaml. If the file already exists, it returns
// an error.
func InstallTemplate(name string, destDir string) error {
	filename := name + ".yaml"

	// Read from embedded FS.
	data, err := templateFS.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("template %q not found: %w", name, err)
	}

	// Ensure destination directory exists.
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", destDir, err)
	}

	destPath := filepath.Join(destDir, filename)

	// Check if file already exists.
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("template file already exists: %s", destPath)
	}

	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("writing template to %s: %w", destPath, err)
	}

	return nil
}

// loadEmbedded loads a PrincipleSet from the embedded filesystem.
func loadEmbedded(filename string) (*principles.PrincipleSet, error) {
	data, err := templateFS.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filename, err)
	}

	var set principles.PrincipleSet
	if err := yaml.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	return &set, nil
}

// isYAML returns true if the filename has a .yaml or .yml extension.
func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}
