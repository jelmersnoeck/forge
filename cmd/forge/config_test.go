package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunConfig_noArgs(t *testing.T) {
	r := require.New(t)
	// runConfig with no subcommand should return 1
	code := runConfig([]string{"config"})
	r.Equal(1, code)
}

func TestRunConfig_help(t *testing.T) {
	r := require.New(t)
	code := runConfig([]string{"config", "help"})
	r.Equal(0, code)
}

func TestRunConfig_unknownSubcommand(t *testing.T) {
	r := require.New(t)
	code := runConfig([]string{"config", "yeet"})
	r.Equal(1, code)
}

func TestRunConfig_setAndGet_integration(t *testing.T) {
	r := require.New(t)
	// Override HOME to isolate test from real user config
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Create the .forge directory
	r.NoError(os.MkdirAll(filepath.Join(dir, ".forge"), 0o755))

	// Set a value
	code := runConfig([]string{"config", "set", "provider.default", "claude-cli"})
	r.Equal(0, code)

	// Verify the TOML file was created
	content, err := os.ReadFile(filepath.Join(dir, ".forge", "config.toml"))
	r.NoError(err)
	r.Contains(string(content), "claude-cli")

	// Get it back
	code = runConfig([]string{"config", "get", "provider.default"})
	r.Equal(0, code)
}

func TestRunConfig_setInvalidProvider(t *testing.T) {
	r := require.New(t)
	t.Setenv("HOME", t.TempDir())

	code := runConfig([]string{"config", "set", "provider.default", "skynet"})
	r.Equal(1, code)
}

func TestRunConfig_setUnknownKey(t *testing.T) {
	r := require.New(t)
	t.Setenv("HOME", t.TempDir())

	code := runConfig([]string{"config", "set", "darkest.timeline", "activated"})
	r.Equal(1, code)
}

func TestRunConfig_getUnknownKey(t *testing.T) {
	r := require.New(t)
	t.Setenv("HOME", t.TempDir())

	code := runConfig([]string{"config", "get", "dean.pelton"})
	r.Equal(1, code)
}

func TestRunConfig_getUnset(t *testing.T) {
	r := require.New(t)
	t.Setenv("HOME", t.TempDir())

	code := runConfig([]string{"config", "get", "provider.default"})
	r.Equal(0, code)
}

func TestRunConfig_list(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Set a value first
	r.NoError(os.MkdirAll(filepath.Join(dir, ".forge"), 0o755))
	code := runConfig([]string{"config", "set", "provider.default", "openai"})
	r.Equal(0, code)

	// List
	code = runConfig([]string{"config", "list"})
	r.Equal(0, code)
}

func TestRunConfig_listEmpty(t *testing.T) {
	r := require.New(t)
	t.Setenv("HOME", t.TempDir())

	code := runConfig([]string{"config", "ls"})
	r.Equal(0, code)
}
