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
		Name:        "Reflect",
		Description: "Reflect on the current session, capturing learnings, mistakes, and successful patterns. This information is saved to .forge/learnings/ for future self-improvement.",
		ReadOnly:    false,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "Brief summary of what was accomplished in this session",
				},
				"mistakes": map[string]any{
					"type":        "array",
					"description": "List of mistakes made or things that could have been done better",
					"items": map[string]any{
						"type": "string",
					},
				},
				"successes": map[string]any{
					"type":        "array",
					"description": "List of patterns or approaches that worked well",
					"items": map[string]any{
						"type": "string",
					},
				},
				"suggestions": map[string]any{
					"type":        "array",
					"description": "Ideas for future improvement or things to remember",
					"items": map[string]any{
						"type": "string",
					},
				},
			},
			"required": []string{"summary"},
		},
		Handler: executeReflect,
	}
}

func executeReflect(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	summary, err := requireString(input, "summary")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}

	mistakes := extractStringArray(input, "mistakes")
	successes := extractStringArray(input, "successes")
	suggestions := extractStringArray(input, "suggestions")

	outPath, err := writeReflection(ctx.CWD, summary, mistakes, successes, suggestions)
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}

	return textResult(fmt.Sprintf("Reflection saved to %s", outPath)), nil
}

// SaveReflection writes a reflection file directly (no tool registry needed).
// Used by the loop's auto-reflection on session completion.
func SaveReflection(cwd, summary string) error {
	_, err := writeReflection(cwd, summary, nil, nil, nil)
	return err
}

// writeReflection formats and persists a reflection file, returning its path.
func writeReflection(cwd, summary string, mistakes, successes, suggestions []string) (string, error) {
	now := time.Now()

	var entry strings.Builder
	fmt.Fprintf(&entry, "# Session Reflection - %s\n\n", now.Format("2006-01-02 15:04"))
	fmt.Fprintf(&entry, "**Summary:** %s\n\n", summary)

	if len(mistakes) > 0 {
		entry.WriteString("**Mistakes & Improvements:**\n")
		for _, m := range mistakes {
			fmt.Fprintf(&entry, "- %s\n", m)
		}
		entry.WriteString("\n")
	}

	if len(successes) > 0 {
		entry.WriteString("**Successful Patterns:**\n")
		for _, s := range successes {
			fmt.Fprintf(&entry, "- %s\n", s)
		}
		entry.WriteString("\n")
	}

	if len(suggestions) > 0 {
		entry.WriteString("**Future Suggestions:**\n")
		for _, s := range suggestions {
			fmt.Fprintf(&entry, "- %s\n", s)
		}
		entry.WriteString("\n")
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

	commitLearning(cwd, outPath)

	return outPath, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// commitLearning stages, commits, and pushes a learning file (and .gitattributes).
// Best-effort: logs and returns silently on failure (non-git dirs, no remote, etc.).
//
//	git rev-parse --git-dir   (bail if not a repo)
//	git add -- <file> .gitattributes
//	git commit -m "..." --no-verify
//	git push                  (only if a remote tracking branch exists)
func commitLearning(cwd, learningPath string) {
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
