package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBashTool(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T, dir string) (map[string]any, types.ToolContext)
		want  func(*testing.T, types.ToolResult, error)
	}{
		"simple echo": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				return map[string]any{"command": "echo 'Troy Barnes'"}, types.ToolContext{
					Ctx: context.Background(),
					CWD: dir,
				}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "Troy Barnes")
			},
		},
		"working directory": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				return map[string]any{"command": "pwd"}, types.ToolContext{
					Ctx: context.Background(),
					CWD: dir,
				}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "TestBashTool")
			},
		},
		"nonzero exit code": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				return map[string]any{"command": "exit 42"}, types.ToolContext{
					Ctx: context.Background(),
					CWD: dir,
				}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
			},
		},
		"stderr output": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				return map[string]any{"command": "echo 'error message' >&2"}, types.ToolContext{
					Ctx: context.Background(),
					CWD: dir,
				}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "error message")
			},
		},
		"combined stdout and stderr": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				cmd := "echo 'stdout'; echo 'stderr' >&2"
				return map[string]any{"command": cmd}, types.ToolContext{
					Ctx: context.Background(),
					CWD: dir,
				}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "stdout")
				r.Contains(result.Content[0].Text, "stderr")
			},
		},
		"timeout": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				return map[string]any{
						"command": "sleep 10",
						"timeout": float64(100), // 100ms
					}, types.ToolContext{
						Ctx: context.Background(),
						CWD: dir,
					}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "timed out")
			},
		},
		"max timeout clamped": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				// Request 10 minutes, should be clamped to 600s
				return map[string]any{
						"command": "echo 'test'",
						"timeout": float64(10 * 60 * 1000), // 10 minutes
					}, types.ToolContext{
						Ctx: context.Background(),
						CWD: dir,
					}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "test")
			},
		},
		"file operations": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				cmd := "echo 'Greendale Community College' > greendale.txt && cat greendale.txt"
				return map[string]any{"command": cmd}, types.ToolContext{
					Ctx: context.Background(),
					CWD: dir,
				}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "Greendale Community College")
			},
		},
		"multiline output": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				cmd := "echo -e 'Troy\\nAbed\\nAnnie'"
				return map[string]any{"command": cmd}, types.ToolContext{
					Ctx: context.Background(),
					CWD: dir,
				}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				lines := strings.Split(result.Content[0].Text, "\n")
				r.True(len(lines) >= 3)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input, ctx := tc.setup(t, dir)
			tool := BashTool()
			result, err := tool.Handler(input, ctx)
			tc.want(t, result, err)
		})
	}
}

func TestBashToolWorkingDirectory(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	// Create a file in the temp dir
	testFile := filepath.Join(dir, "human_being.txt")
	err := os.WriteFile(testFile, []byte("mascot"), 0644)
	r.NoError(err)

	// Run a command that lists files
	tool := BashTool()
	result, err := tool.Handler(
		map[string]any{"command": "ls"},
		types.ToolContext{
			Ctx: context.Background(),
			CWD: dir,
		},
	)

	r.NoError(err)
	r.False(result.IsError)
	r.Contains(result.Content[0].Text, "human_being.txt")
}

func TestBashToolInteractiveCommands(t *testing.T) {
	tests := map[string]struct {
		command      string
		shouldBlock  bool
		errorMessage string
	}{
		"vim editor": {
			command:      "vim file.txt",
			shouldBlock:  true,
			errorMessage: "vim",
		},
		"less pager": {
			command:      "less file.txt",
			shouldBlock:  true,
			errorMessage: "less",
		},
		"python REPL": {
			command:      "python",
			shouldBlock:  true,
			errorMessage: "REPL",
		},
		"python script is OK": {
			command:     "python script.py",
			shouldBlock: false,
		},
		"python -c is OK": {
			command:     "python -c 'print(\"hello\")'",
			shouldBlock: false,
		},
		"node REPL": {
			command:      "node",
			shouldBlock:  true,
			errorMessage: "REPL",
		},
		"node script is OK": {
			command:     "node script.js",
			shouldBlock: false,
		},
		"npm init interactive": {
			command:      "npm init",
			shouldBlock:  true,
			errorMessage: "npm",
		},
		"npm init -y is OK": {
			command:     "npm init -y",
			shouldBlock: false,
		},
		"docker exec -it": {
			command:      "docker exec -it container bash",
			shouldBlock:  true,
			errorMessage: "docker",
		},
		"docker exec without -it is OK": {
			command:     "docker exec container ls",
			shouldBlock: false,
		},
		"cat file is OK": {
			command:     "cat file.txt",
			shouldBlock: false,
		},
		"git commit with message is OK": {
			command:     "git commit -m 'message'",
			shouldBlock: false,
		},
		"mysql with -e is OK": {
			command:     "mysql -e 'SELECT * FROM users'",
			shouldBlock: false,
		},
		"top command": {
			command:      "top",
			shouldBlock:  true,
			errorMessage: "top",
		},
		"piped command is OK": {
			command:     "vim file.txt | cat",
			shouldBlock: false,
		},
		"redirected input is OK": {
			command:     "python < script.py",
			shouldBlock: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir := t.TempDir()
			tool := BashTool()

			result, err := tool.Handler(
				map[string]any{"command": tc.command},
				types.ToolContext{
					Ctx: context.Background(),
					CWD: dir,
				},
			)

			r.NoError(err)

			if tc.shouldBlock {
				r.True(result.IsError, "Expected command '%s' to be blocked", tc.command)
				r.Contains(result.Content[0].Text, "Interactive command detected")
				if tc.errorMessage != "" {
					r.Contains(strings.ToLower(result.Content[0].Text), strings.ToLower(tc.errorMessage))
				}
			} else {
				// Non-interactive commands may fail for other reasons (file not found, etc.)
				// but should not be blocked by interactive detection
				if result.IsError {
					r.NotContains(result.Content[0].Text, "Interactive command detected",
						"Command '%s' should not be blocked as interactive", tc.command)
				}
			}
		})
	}
}

func TestCheckInteractiveCommand(t *testing.T) {
	tests := map[string]struct {
		command string
		isValid bool // true if command should be allowed (empty warning)
	}{
		"vim":                       {command: "vim file.txt", isValid: false},
		"vi":                        {command: "vi file.txt", isValid: false},
		"nano":                      {command: "nano file.txt", isValid: false},
		"less":                      {command: "less file.txt", isValid: false},
		"python REPL":               {command: "python", isValid: false},
		"python3 REPL":              {command: "python3", isValid: false},
		"python script":             {command: "python script.py", isValid: true},
		"python -c":                 {command: "python -c 'print(1)'", isValid: true},
		"node REPL":                 {command: "node", isValid: false},
		"node script":               {command: "node app.js", isValid: true},
		"npm init":                  {command: "npm init", isValid: false},
		"npm init -y":               {command: "npm init -y", isValid: true},
		"npm install":               {command: "npm install", isValid: true},
		"docker exec -it":           {command: "docker exec -it container bash", isValid: false},
		"docker exec":               {command: "docker exec container ls", isValid: true},
		"sudo vim":                  {command: "sudo vim file.txt", isValid: false},
		"cat":                       {command: "cat file.txt", isValid: true},
		"echo":                      {command: "echo hello", isValid: true},
		"git commit -m":             {command: "git commit -m 'msg'", isValid: true},
		"mysql -e":                  {command: "mysql -e 'SELECT 1'", isValid: true},
		"piped vim":                 {command: "echo test | vim -", isValid: true},
		"redirected python":         {command: "python < script.py", isValid: true},
		"background python":         {command: "python script.py &", isValid: true},
		"force flag":                {command: "vim -f file.txt", isValid: true},
		"batch mode":                {command: "rails new app --batch", isValid: true},
		"kubectl exec -it":          {command: "kubectl exec -it pod -- bash", isValid: false},
		"kubectl exec":              {command: "kubectl exec pod -- ls", isValid: true},
		"empty":                     {command: "", isValid: true},
		"whitespace":                {command: "   ", isValid: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			warning := checkInteractiveCommand(tc.command)
			
			if tc.isValid {
				r.Empty(warning, "Command '%s' should be allowed but got warning: %s", tc.command, warning)
			} else {
				r.NotEmpty(warning, "Command '%s' should be blocked", tc.command)
			}
		})
	}
}
