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

func newTestCtx(t *testing.T, dir string) types.ToolContext {
	t.Helper()
	return types.ToolContext{
		Ctx:       context.Background(),
		CWD:       dir,
		ReadState: make(map[string]types.ReadFileEntry),
		Emit:      func(types.OutboundEvent) {},
	}
}

func TestReadHandler_BlocksEnvFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("SECRET=changme"), 0644)

	tests := map[string]struct {
		path string
	}{
		".env":            {path: envPath},
		".env.local":      {path: filepath.Join(dir, ".env.local")},
		".env.production": {path: filepath.Join(dir, ".env.production")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := readHandler(map[string]any{"file_path": tc.path}, newTestCtx(t, dir))
			r.NoError(err)
			r.True(result.IsError)
			r.Contains(result.Content[0].Text, "blocked")
		})
	}
}

func TestReadHandler_AllowsNonEnvFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	safePath := filepath.Join(dir, "config.yaml")
	os.WriteFile(safePath, []byte("key: value"), 0644)

	result, err := readHandler(map[string]any{"file_path": safePath}, newTestCtx(t, dir))
	r.NoError(err)
	r.False(result.IsError)
	r.Contains(result.Content[0].Text, "key: value")
}

func TestWriteHandler_BlocksEnvFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	tests := map[string]struct {
		filename string
	}{
		".env":            {filename: ".env"},
		".env.local":      {filename: ".env.local"},
		".env.production": {filename: ".env.production"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, tc.filename)
			result, err := writeHandler(map[string]any{
				"file_path": path,
				"content":   "SECRET=oops",
			}, newTestCtx(t, dir))
			r.NoError(err)
			r.True(result.IsError)
			r.Contains(result.Content[0].Text, "blocked")

			// File must not exist
			_, statErr := os.Stat(path)
			r.True(os.IsNotExist(statErr))
		})
	}
}

func TestEditHandler_BlocksEnvFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("OLD=value"), 0644)

	result, err := editHandler(map[string]any{
		"file_path":  envPath,
		"old_string": "OLD",
		"new_string": "NEW",
	}, newTestCtx(t, dir))
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "blocked")

	// File must be unchanged
	data, _ := os.ReadFile(envPath)
	r.Equal("OLD=value", string(data))
}

func TestBashHandler_BlocksEnvFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("SECRET=hunter2"), 0644)

	tests := map[string]struct {
		command string
	}{
		"cat .env":     {command: "cat .env"},
		"source .env":  {command: "source .env"},
		"head .env":    {command: "head -1 .env"},
		"grep in .env": {command: "grep SECRET .env"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := bashHandler(map[string]any{"command": tc.command}, newTestCtx(t, dir))
			r.NoError(err)
			r.True(result.IsError)
			r.Contains(result.Content[0].Text, "blocked")
		})
	}
}

func TestBashHandler_AllowsSafeCommands(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	result, err := bashHandler(map[string]any{"command": "echo greendale"}, newTestCtx(t, dir))
	r.NoError(err)
	r.False(result.IsError)
	r.Contains(result.Content[0].Text, "greendale")
}

func TestGlobHandler_FiltersEnvFiles(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	// Create a mix of files
	for _, name := range []string{".env", ".env.local", "config.yaml", "main.go"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644)
	}

	result, err := globHandler(map[string]any{"pattern": "*"}, newTestCtx(t, dir))
	r.NoError(err)
	r.False(result.IsError)

	output := result.Content[0].Text
	r.NotContains(output, ".env")
	r.Contains(output, "config.yaml")
	r.Contains(output, "main.go")
}

func TestGrepHandler_BlocksEnvFilePath(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("SECRET=hunter2"), 0644)

	result, err := grepHandler(map[string]any{
		"pattern": "SECRET",
		"path":    envPath,
	}, newTestCtx(t, dir))
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "blocked")
}

func TestGrepHandler_ExcludesEnvFromDirectorySearch(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, ".env"), []byte("DEAN_PELTON=secret"), 0644)
	os.WriteFile(filepath.Join(dir, "config.go"), []byte("DEAN_PELTON=public"), 0644)

	result, err := grepHandler(map[string]any{
		"pattern":     "DEAN_PELTON",
		"path":        dir,
		"output_mode": "content",
	}, newTestCtx(t, dir))
	r.NoError(err)

	// Should find config.go but not .env
	output := result.Content[0].Text
	r.NotContains(output, "secret")
	if !result.IsError {
		r.Contains(output, "public")
	}
}

func TestTaskCreate_BlocksEnvFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	result, err := handleTaskCreate(map[string]any{
		"description": "read secrets",
		"command":     "cat .env",
	}, newTestCtx(t, dir))
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "blocked")
}

func TestQueueImmediate_BlocksEnvFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	result, err := handleQueueImmediate(map[string]any{
		"command": "cat .env.production",
	}, newTestCtx(t, dir))
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "blocked")
}

func TestQueueOnComplete_BlocksEnvFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	result, err := handleQueueOnComplete(map[string]any{
		"command": "source .env.local",
	}, newTestCtx(t, dir))
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "blocked")
}

func TestEnvFileBlock_NoOverrides(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("PIERCE_HAWTHORNE=moist"), 0644)

	// Even with all possible context variations, .env is blocked
	ctx := newTestCtx(t, dir)
	ctx.CWD = dir

	result, err := readHandler(map[string]any{"file_path": envPath}, ctx)
	r.NoError(err)
	r.True(result.IsError)

	// Ensure the error message is clear and doesn't leak file contents
	r.NotContains(strings.ToLower(result.Content[0].Text), "moist")
	r.Contains(result.Content[0].Text, "blocked")
}
