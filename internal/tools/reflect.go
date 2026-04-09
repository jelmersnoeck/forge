package tools

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

const (
	learningsDir           = ".forge/learnings"
	gitattributesLearnings = ".forge/learnings/** linguist-generated=true"
)

// ReflectTool returns the tool definition for session reflection.
func ReflectTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name: "Reflect",
		Description: `Save actionable learnings discovered during this session. Each learning should be a specific, reusable insight — a gotcha, workaround, or discovery that would help future sessions avoid mistakes or find solutions faster.

Good learnings:
- "DuckDuckGo Instant Answer API returns HTTP 202 for bot-detected requests — use Anthropic's server-side web_search instead"
- "git rebase in worktrees requires --onto with explicit SHAs; interactive mode hangs"
- "The Anthropic SDK at v1.27.1 silently drops tool_result blocks if they're not immediately after tool_use"

Bad learnings (don't do these):
- "Implemented feature X" (that's a commit message, not a learning)
- "Tests passed" (obvious, not actionable)
- "User asked to fix a bug" (that's a summary, not an insight)`,
		ReadOnly: false,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "One-line summary of what the session accomplished (for the filename only)",
				},
				"learnings": map[string]any{
					"type":        "array",
					"description": "Actionable gotchas, workarounds, or discoveries. Each entry should be specific enough that a future agent can act on it without context.",
					"items": map[string]any{
						"type": "string",
					},
				},
			},
			"required": []string{"summary", "learnings"},
		},
		Handler: executeReflect,
	}
}

func executeReflect(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	summary, err := requireString(input, "summary")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}

	learnings := extractStringArray(input, "learnings")
	if len(learnings) == 0 {
		return types.ToolResult{IsError: true}, fmt.Errorf("learnings array is required and must contain at least one entry")
	}

	outPath, err := writeReflection(ctx.CWD, summary, learnings)
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}

	return textResult(fmt.Sprintf("Reflection saved to %s", outPath)), nil
}

// writeReflection formats and persists a reflection file, returning its path.
func writeReflection(cwd, summary string, learnings []string) (string, error) {
	now := time.Now()

	var entry strings.Builder
	fmt.Fprintf(&entry, "# Learnings - %s\n\n", now.Format("2006-01-02 15:04"))

	for _, l := range learnings {
		fmt.Fprintf(&entry, "- %s\n", l)
	}

	dir := filepath.Join(cwd, learningsDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create %s: %v", learningsDir, err)
	}

	slug := slugify(summary, 50)
	filename := fmt.Sprintf("%s-%s.md", now.Format("20060102-150405"), slug)
	outPath := filepath.Join(dir, filename)

	if err := os.WriteFile(outPath, []byte(entry.String()), 0644); err != nil {
		return "", fmt.Errorf("write learning: %v", err)
	}

	if err := ensureGitattributes(cwd); err != nil {
		return "", fmt.Errorf("update .gitattributes: %v", err)
	}

	agentsMDPath, err := ensureAgentsMD(cwd)
	if err != nil {
		return "", fmt.Errorf("update AGENTS.md: %v", err)
	}

	commitLearning(cwd, outPath, agentsMDPath)

	return outPath, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// commitLearning stages, commits, and pushes a learning file (and .gitattributes).
// If agentsMDPath is non-empty, it's staged too.
// Best-effort: logs and returns silently on failure (non-git dirs, no remote, etc.).
//
//	git rev-parse --git-dir   (bail if not a repo)
//	git add -- <file> .gitattributes [AGENTS.md]
//	git commit -m "..." --no-verify
//	git push                  (only if a remote tracking branch exists)
func commitLearning(cwd, learningPath, agentsMDPath string) {
	// Quick check: is this a git repo?
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = cwd
	if err := cmd.Run(); err != nil {
		return
	}

	// Stage the learning file and .gitattributes
	rel, err := filepath.Rel(cwd, learningPath)
	if err != nil {
		rel = learningPath
	}
	filesToAdd := []string{rel, ".gitattributes"}
	if agentsMDPath != "" {
		agentsRel, err := filepath.Rel(cwd, agentsMDPath)
		if err != nil {
			agentsRel = agentsMDPath
		}
		filesToAdd = append(filesToAdd, agentsRel)
	}
	addArgs := append([]string{"add", "--"}, filesToAdd...)
	add := exec.Command("git", addArgs...)
	add.Dir = cwd
	if err := add.Run(); err != nil {
		log.Printf("[reflect] git add failed: %v", err)
		return
	}

	commit := exec.Command("git", "commit", "-m", "forge: save session reflection", "--no-verify")
	commit.Dir = cwd
	if err := commit.Run(); err != nil {
		log.Printf("[reflect] git commit failed: %v", err)
		return
	}

	// Push if there's a remote tracking branch. No remote? No push. No drama.
	track := exec.Command("git", "rev-parse", "--abbrev-ref", "@{upstream}")
	track.Dir = cwd
	if err := track.Run(); err != nil {
		return
	}

	push := exec.Command("git", "push", "--no-verify")
	push.Dir = cwd
	if err := push.Run(); err != nil {
		log.Printf("[reflect] git push failed: %v", err)
	}
}

// slugify lowercases, replaces non-alphanum runs with hyphens, and truncates.
func slugify(s string, maxLen int) string {
	s = strings.ToLower(s)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > maxLen {
		s = s[:maxLen]
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		s = "reflection"
	}
	return s
}

const agentsMDLearningsSection = `
# Agent Learnings

Actionable discoveries from past sessions are stored in ` + "`.forge/learnings/`" + `.
Consult them when starting a task — if a learning is relevant, factor it into
your approach to avoid repeating past mistakes.
`

// ensureAgentsMD creates or appends a learnings section to the project's AGENTS.md.
// Returns the path of the created/modified file (empty string if no change was needed).
//
// Resolution order:
//  1. Root AGENTS.md exists → append section if missing
//  2. .forge/AGENTS.md exists → append section if missing
//  3. Neither exists (CLAUDE.md doesn't count) → create .forge/AGENTS.md
func ensureAgentsMD(cwd string) (string, error) {
	rootPath := filepath.Join(cwd, "AGENTS.md")
	forgePath := filepath.Join(cwd, ".forge", "AGENTS.md")

	// Try root first, then .forge/
	for _, path := range []string{rootPath, forgePath} {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(content), "# Agent Learnings") {
			return "", nil // already has the section
		}
		// Append the section
		toWrite := string(content)
		if !strings.HasSuffix(toWrite, "\n") {
			toWrite += "\n"
		}
		toWrite += agentsMDLearningsSection
		if err := os.WriteFile(path, []byte(toWrite), 0644); err != nil {
			return "", err
		}
		return path, nil
	}

	// Neither exists — create .forge/AGENTS.md
	if err := os.MkdirAll(filepath.Join(cwd, ".forge"), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(forgePath, []byte(strings.TrimLeft(agentsMDLearningsSection, "\n")), 0644); err != nil {
		return "", err
	}
	return forgePath, nil
}

// ensureGitattributes idempotently adds the learnings line to .gitattributes.
func ensureGitattributes(cwd string) error {
	path := filepath.Join(cwd, ".gitattributes")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(existing)
	if strings.Contains(content, gitattributesLearnings) {
		return nil
	}

	// Ensure trailing newline before appending.
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += gitattributesLearnings + "\n"

	return os.WriteFile(path, []byte(content), 0644)
}

func extractStringArray(input map[string]any, key string) []string {
	val, ok := input[key]
	if !ok {
		return nil
	}

	arr, ok := val.([]any)
	if !ok {
		return nil
	}

	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && s != "" {
			result = append(result, s)
		}
	}
	return result
}
