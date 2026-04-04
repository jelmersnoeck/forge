// Package spec handles loading and parsing feature specifications.
//
// Specs are markdown files with YAML frontmatter stored in .forge/specs/.
// Each spec defines a feature's intent, behavior, constraints, and
// interfaces — acting as the source of truth for implementation and
// acceptance testing.
//
//	.forge/specs/
//	  ├── worktree-isolation.md
//	  ├── mcp-client.md
//	  └── spec-driven-dev.md
//
// Format:
//
//	---
//	id: feature-slug
//	status: active
//	---
//	# Summary (max 15 words)
//	## Description
//	## Context
//	## Behavior
//	## Constraints
//	## Interfaces
//	## Edge Cases
package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jelmersnoeck/forge/internal/config"
	"github.com/jelmersnoeck/forge/internal/types"
	"gopkg.in/yaml.v3"
)

// DefaultSpecsDir is the default location for spec files.
const DefaultSpecsDir = ".forge/specs"

// ParseSpec reads and parses a spec markdown file into a SpecDocument.
func ParseSpec(path string) (types.SpecDocument, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return types.SpecDocument{}, fmt.Errorf("read spec: %w", err)
	}

	return parseSpecContent(string(content), path)
}

func parseSpecContent(content, path string) (types.SpecDocument, error) {
	frontmatter, body, err := parseFrontmatter(content)
	if err != nil {
		return types.SpecDocument{}, fmt.Errorf("parse frontmatter: %w", err)
	}

	doc := types.SpecDocument{Path: path}

	if frontmatter != nil {
		doc.ID, _ = frontmatter["id"].(string)
		doc.Status, _ = frontmatter["status"].(string)
	}

	if doc.Status == "" {
		doc.Status = "draft"
	}

	sections := parseSections(body)
	doc.Header = sections["_header"]
	doc.Description = sections["description"]
	doc.Context = sections["context"]
	doc.Behavior = sections["behavior"]
	doc.Constraints = sections["constraints"]
	doc.Interfaces = sections["interfaces"]
	doc.EdgeCases = sections["edge cases"]

	return doc, nil
}

// parseSections splits markdown body into sections keyed by lowercase h2 heading.
// Content before the first h2 is stored under "_header".
//
//	# This Is The Header        → "_header": "This Is The Header"
//	## Description               → "description": "..."
//	## Edge Cases                → "edge cases": "..."
func parseSections(body string) map[string]string {
	sections := make(map[string]string)
	lines := strings.Split(body, "\n")

	var currentKey string
	var buf strings.Builder

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "# ") && currentKey == "":
			sections["_header"] = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			continue
		case strings.HasPrefix(line, "## "):
			if currentKey != "" {
				sections[currentKey] = strings.TrimSpace(buf.String())
			}
			currentKey = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			buf.Reset()
			continue
		}

		if currentKey != "" {
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}

	if currentKey != "" {
		sections[currentKey] = strings.TrimSpace(buf.String())
	}

	return sections
}

// LoadSpecs reads all spec files from the given directory.
// Returns an empty slice (not error) if the directory doesn't exist.
func LoadSpecs(dir string) ([]types.SpecEntry, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read specs dir: %w", err)
	}

	var specs []types.SpecEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		doc, err := ParseSpec(path)
		if err != nil {
			continue
		}

		content, _ := os.ReadFile(path)
		specs = append(specs, types.SpecEntry{
			Path:    path,
			Content: string(content),
			ID:      doc.ID,
			Status:  doc.Status,
			Header:  doc.Header,
		})
	}

	return specs, nil
}

// FindSpecsDir resolves the specs directory. If the config has an override,
// it's used (resolved relative to cwd). Otherwise returns the default
// .forge/specs under cwd.
func FindSpecsDir(cwd string, cfg config.ForgeConfig) string {
	if cfg.SpecsDir != "" {
		if filepath.IsAbs(cfg.SpecsDir) {
			return cfg.SpecsDir
		}
		return filepath.Join(cwd, cfg.SpecsDir)
	}
	return filepath.Join(cwd, DefaultSpecsDir)
}

// parseFrontmatter extracts YAML frontmatter from markdown content.
func parseFrontmatter(content string) (map[string]any, string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return nil, content, nil
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			endIdx = i
			break
		}
	}

	if endIdx == -1 {
		return nil, content, nil
	}

	yamlContent := strings.Join(lines[1:endIdx], "\n")
	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(yamlContent), &frontmatter); err != nil {
		return nil, "", fmt.Errorf("parse yaml frontmatter: %w", err)
	}

	body := strings.Join(lines[endIdx+1:], "\n")
	body = strings.TrimSpace(body)

	return frontmatter, body, nil
}
