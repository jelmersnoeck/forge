// Package context loads CLAUDE.md, skills, agents, rules, and settings.
package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jelmersnoeck/forge/internal/config"
	"github.com/jelmersnoeck/forge/internal/spec"
	"github.com/jelmersnoeck/forge/internal/types"
	"gopkg.in/yaml.v3"
)

// Loader crawls the filesystem for context files.
type Loader struct {
	cwd string
}

// NewLoader creates a new context loader.
func NewLoader(cwd string) *Loader {
	return &Loader{cwd: cwd}
}

// Load discovers and loads context from the specified sources.
// Sources: "user", "project", "local"
func (l *Loader) Load(sources []string) (types.ContextBundle, error) {
	bundle := types.ContextBundle{
		AgentDefinitions: make(map[string]types.AgentDefinition),
		Settings:         types.MergedSettings{},
	}

	for _, source := range sources {
		switch source {
		case "user":
			if err := l.loadUserContext(&bundle); err != nil {
				return bundle, fmt.Errorf("load user context: %w", err)
			}

		case "project":
			if err := l.loadProjectContext(&bundle); err != nil {
				return bundle, fmt.Errorf("load project context: %w", err)
			}

			// Load parent directories (walk upward from cwd)
			if err := l.loadParentContext(&bundle); err != nil {
				return bundle, fmt.Errorf("load parent context: %w", err)
			}

		case "local":
			if err := l.loadLocalContext(&bundle); err != nil {
				return bundle, fmt.Errorf("load local context: %w", err)
			}
		}
	}

	return bundle, nil
}

// LoadSkillContent reads the content of a skill by name.
func (l *Loader) LoadSkillContent(name string) (string, error) {
	// Search in user and project skill directories
	searchPaths := []string{
		filepath.Join(os.Getenv("HOME"), ".claude", "skills", name, "SKILL.md"),
		filepath.Join(l.cwd, ".claude", "skills", name, "SKILL.md"),
	}

	for _, path := range searchPaths {
		content, err := os.ReadFile(path)
		if err == nil {
			return string(content), nil
		}
	}

	return "", fmt.Errorf("skill not found: %s", name)
}

func (l *Loader) loadUserContext(bundle *types.ContextBundle) error {
	home := os.Getenv("HOME")
	if home == "" {
		return nil // No home directory, skip
	}

	claudeDir := filepath.Join(home, ".claude")

	// Load user CLAUDE.md
	if content, err := os.ReadFile(filepath.Join(home, "CLAUDE.md")); err == nil {
		bundle.ClaudeMD = append(bundle.ClaudeMD, types.ClaudeMDEntry{
			Path:    filepath.Join(home, "CLAUDE.md"),
			Content: string(content),
			Level:   "user",
		})
	}

	// Load user AGENTS.md
	if content, err := os.ReadFile(filepath.Join(home, "AGENTS.md")); err == nil {
		bundle.AgentsMD = append(bundle.AgentsMD, types.AgentsMDEntry{
			Path:    filepath.Join(home, "AGENTS.md"),
			Content: string(content),
			Level:   "user",
		})
	}

	// Load rules from ~/.claude/rules/
	rulesDir := filepath.Join(claudeDir, "rules")
	if entries, err := os.ReadDir(rulesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			path := filepath.Join(rulesDir, entry.Name())
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			bundle.Rules = append(bundle.Rules, types.RuleEntry{
				Path:    path,
				Content: string(content),
				Level:   "user",
			})
		}
	}

	// Load user settings
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := l.mergeSettings(bundle, settingsPath); err != nil {
		return err
	}

	// Discover skills
	if err := l.discoverSkills(bundle, filepath.Join(claudeDir, "skills"), "user"); err != nil {
		return err
	}

	return nil
}

func (l *Loader) loadParentContext(bundle *types.ContextBundle) error {
	// Walk up from cwd to root, loading CLAUDE.md from each directory
	dir := l.cwd
	root := filepath.Dir(l.cwd) // Start from parent of cwd

	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // Reached root
		}

		claudePath := filepath.Join(parent, "CLAUDE.md")
		if content, err := os.ReadFile(claudePath); err == nil {
			bundle.ClaudeMD = append(bundle.ClaudeMD, types.ClaudeMDEntry{
				Path:    claudePath,
				Content: string(content),
				Level:   "parent",
			})
		}

		agentsPath := filepath.Join(parent, "AGENTS.md")
		if content, err := os.ReadFile(agentsPath); err == nil {
			bundle.AgentsMD = append(bundle.AgentsMD, types.AgentsMDEntry{
				Path:    agentsPath,
				Content: string(content),
				Level:   "parent",
			})
		}

		// Stop if we've reached root or gone past the original cwd parent
		if parent == root || parent == "/" {
			break
		}

		dir = parent
	}

	return nil
}

func (l *Loader) loadProjectContext(bundle *types.ContextBundle) error {
	// Load CLAUDE.md from cwd or .claude/
	claudePath := filepath.Join(l.cwd, "CLAUDE.md")
	if content, err := os.ReadFile(claudePath); err == nil {
		bundle.ClaudeMD = append(bundle.ClaudeMD, types.ClaudeMDEntry{
			Path:    claudePath,
			Content: string(content),
			Level:   "project",
		})
	} else {
		// Try .claude/CLAUDE.md
		claudePath = filepath.Join(l.cwd, ".claude", "CLAUDE.md")
		if content, err := os.ReadFile(claudePath); err == nil {
			bundle.ClaudeMD = append(bundle.ClaudeMD, types.ClaudeMDEntry{
				Path:    claudePath,
				Content: string(content),
				Level:   "project",
			})
		}
	}

	// Load AGENTS.md from cwd or .claude/ (read-only — never written to by Reflect)
	agentsPath := filepath.Join(l.cwd, "AGENTS.md")
	if content, err := os.ReadFile(agentsPath); err == nil {
		bundle.AgentsMD = append(bundle.AgentsMD, types.AgentsMDEntry{
			Path:    agentsPath,
			Content: string(content),
			Level:   "project",
		})
	} else {
		// Try .claude/AGENTS.md
		agentsPath = filepath.Join(l.cwd, ".claude", "AGENTS.md")
		if content, err := os.ReadFile(agentsPath); err == nil {
			bundle.AgentsMD = append(bundle.AgentsMD, types.AgentsMDEntry{
				Path:    agentsPath,
				Content: string(content),
				Level:   "project",
			})
		}
	}

	// Load .forge/learnings/*.md (generated by Reflect tool)
	if err := l.loadLearnings(bundle); err != nil {
		return err
	}

	claudeDir := filepath.Join(l.cwd, ".claude")

	// Load rules
	rulesDir := filepath.Join(claudeDir, "rules")
	if entries, err := os.ReadDir(rulesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			path := filepath.Join(rulesDir, entry.Name())
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			bundle.Rules = append(bundle.Rules, types.RuleEntry{
				Path:    path,
				Content: string(content),
				Level:   "project",
			})
		}
	}

	// Load project settings
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := l.mergeSettings(bundle, settingsPath); err != nil {
		return err
	}

	// Discover skills
	if err := l.discoverSkills(bundle, filepath.Join(claudeDir, "skills"), "project"); err != nil {
		return err
	}

	// Load agents
	agentsDir := filepath.Join(claudeDir, "agents")
	if entries, err := os.ReadDir(agentsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			path := filepath.Join(agentsDir, entry.Name())
			agent, err := l.parseAgentFile(path)
			if err != nil {
				continue
			}

			bundle.AgentDefinitions[agent.Name] = agent
		}
	}

	// Load specs
	cfg, _ := config.Load(l.cwd)
	specsDir := spec.FindSpecsDir(l.cwd, cfg)
	specs, err := spec.LoadSpecs(specsDir)
	if err != nil {
		return fmt.Errorf("load specs: %w", err)
	}
	bundle.Specs = append(bundle.Specs, specs...)

	return nil
}

func (l *Loader) loadLocalContext(bundle *types.ContextBundle) error {
	// Load CLAUDE.local.md
	localPath := filepath.Join(l.cwd, "CLAUDE.local.md")
	if content, err := os.ReadFile(localPath); err == nil {
		bundle.ClaudeMD = append(bundle.ClaudeMD, types.ClaudeMDEntry{
			Path:    localPath,
			Content: string(content),
			Level:   "local",
		})
	}

	// Load AGENTS.local.md
	agentsLocalPath := filepath.Join(l.cwd, "AGENTS.local.md")
	if content, err := os.ReadFile(agentsLocalPath); err == nil {
		bundle.AgentsMD = append(bundle.AgentsMD, types.AgentsMDEntry{
			Path:    agentsLocalPath,
			Content: string(content),
			Level:   "local",
		})
	}

	// Load local settings
	localSettingsPath := filepath.Join(l.cwd, ".claude", "settings.local.json")
	if err := l.mergeSettings(bundle, localSettingsPath); err != nil {
		return err
	}

	return nil
}

// loadLearnings reads individual learning files from .forge/learnings/.
func (l *Loader) loadLearnings(bundle *types.ContextBundle) error {
	dir := filepath.Join(l.cwd, ".forge", "learnings")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // directory doesn't exist yet — that's fine
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		bundle.AgentsMD = append(bundle.AgentsMD, types.AgentsMDEntry{
			Path:    path,
			Content: string(content),
			Level:   "project",
		})
	}

	return nil
}

func (l *Loader) discoverSkills(bundle *types.ContextBundle, skillsDir, level string) error {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil // Skills directory doesn't exist, that's fine
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		skill, err := l.parseSkillFile(skillPath)
		if err != nil {
			continue
		}

		bundle.SkillDescriptions = append(bundle.SkillDescriptions, skill)
	}

	return nil
}

func (l *Loader) parseSkillFile(path string) (types.SkillDescription, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return types.SkillDescription{}, err
	}

	// Parse YAML frontmatter
	frontmatter, _, err := parseFrontmatter(string(content))
	if err != nil {
		return types.SkillDescription{}, err
	}

	name, _ := frontmatter["name"].(string)
	description, _ := frontmatter["description"].(string)
	isUserInvocable, _ := frontmatter["isUserInvocable"].(bool)

	return types.SkillDescription{
		Name:            name,
		Description:     description,
		Path:            path,
		IsUserInvocable: isUserInvocable,
	}, nil
}

func (l *Loader) parseAgentFile(path string) (types.AgentDefinition, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return types.AgentDefinition{}, err
	}

	// Parse YAML frontmatter
	frontmatter, body, err := parseFrontmatter(string(content))
	if err != nil {
		return types.AgentDefinition{}, err
	}

	agent := types.AgentDefinition{
		Prompt: body,
	}

	if name, ok := frontmatter["name"].(string); ok {
		agent.Name = name
	}

	if description, ok := frontmatter["description"].(string); ok {
		agent.Description = description
	}

	if model, ok := frontmatter["model"].(string); ok {
		agent.Model = model
	}

	if maxTurns, ok := frontmatter["maxTurns"].(int); ok {
		agent.MaxTurns = maxTurns
	}

	if tools, ok := frontmatter["tools"].([]any); ok {
		for _, t := range tools {
			if toolName, ok := t.(string); ok {
				agent.Tools = append(agent.Tools, toolName)
			}
		}
	}

	if disallowedTools, ok := frontmatter["disallowedTools"].([]any); ok {
		for _, t := range disallowedTools {
			if toolName, ok := t.(string); ok {
				agent.DisallowedTools = append(agent.DisallowedTools, toolName)
			}
		}
	}

	return agent, nil
}

func (l *Loader) mergeSettings(bundle *types.ContextBundle, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil // Settings file doesn't exist, that's fine
	}

	var settings types.MergedSettings
	if err := json.Unmarshal(content, &settings); err != nil {
		return fmt.Errorf("parse settings %s: %w", path, err)
	}

	// Merge into bundle (later settings override earlier ones)
	if settings.Model != "" {
		bundle.Settings.Model = settings.Model
	}

	if settings.Permissions != nil {
		if bundle.Settings.Permissions == nil {
			bundle.Settings.Permissions = &types.PermissionConfig{}
		}
		bundle.Settings.Permissions.Allow = append(bundle.Settings.Permissions.Allow, settings.Permissions.Allow...)
		bundle.Settings.Permissions.Deny = append(bundle.Settings.Permissions.Deny, settings.Permissions.Deny...)
	}

	if settings.Env != nil {
		if bundle.Settings.Env == nil {
			bundle.Settings.Env = make(map[string]string)
		}
		for k, v := range settings.Env {
			bundle.Settings.Env[k] = v
		}
	}

	return nil
}

// parseFrontmatter extracts YAML frontmatter from markdown content.
// Returns frontmatter map, body content, and error.
func parseFrontmatter(content string) (map[string]any, string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return nil, content, nil // No frontmatter
	}

	// Find closing ---
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			endIdx = i
			break
		}
	}

	if endIdx == -1 {
		return nil, content, nil // No closing delimiter
	}

	// Parse YAML
	yamlContent := strings.Join(lines[1:endIdx], "\n")
	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(yamlContent), &frontmatter); err != nil {
		return nil, "", fmt.Errorf("parse yaml frontmatter: %w", err)
	}

	// Body is everything after the closing ---
	body := strings.Join(lines[endIdx+1:], "\n")
	body = strings.TrimSpace(body)

	return frontmatter, body, nil
}
