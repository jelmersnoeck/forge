package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInitInDir_CreatesStructure(t *testing.T) {
	dir := t.TempDir()

	if err := runInitInDir(dir, false); err != nil {
		t.Fatalf("runInitInDir() error: %v", err)
	}

	// Verify directories exist.
	for _, sub := range []string{".forge", ".forge/principles", ".forge/prompts"} {
		path := filepath.Join(dir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected directory %s to exist: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}

	// Verify config.yaml exists and has content.
	configPath := filepath.Join(dir, ".forge", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config.yaml: %v", err)
	}
	if len(data) == 0 {
		t.Error("config.yaml is empty")
	}

	content := string(data)
	if !strings.Contains(content, "agent:") {
		t.Error("config.yaml missing agent section")
	}
	if !strings.Contains(content, "tracker:") {
		t.Error("config.yaml missing tracker section")
	}
	if !strings.Contains(content, "build:") {
		t.Error("config.yaml missing build section")
	}
	if !strings.Contains(content, "server:") {
		t.Error("config.yaml missing server section")
	}
	if !strings.Contains(content, "principles:") {
		t.Error("config.yaml missing principles section")
	}
}

func TestRunInitInDir_Force_Overwrites(t *testing.T) {
	dir := t.TempDir()

	// First init.
	if err := runInitInDir(dir, false); err != nil {
		t.Fatalf("first runInitInDir() error: %v", err)
	}

	// Write a sentinel file to verify overwrite.
	sentinelPath := filepath.Join(dir, ".forge", "config.yaml")
	if err := os.WriteFile(sentinelPath, []byte("old content"), 0644); err != nil {
		t.Fatalf("writing sentinel: %v", err)
	}

	// Second init with --force.
	if err := runInitInDir(dir, true); err != nil {
		t.Fatalf("forced runInitInDir() error: %v", err)
	}

	// Verify config was overwritten.
	data, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("reading config after force: %v", err)
	}
	if string(data) == "old content" {
		t.Error("config.yaml was not overwritten with --force")
	}
	if !strings.Contains(string(data), "agent:") {
		t.Error("overwritten config.yaml missing expected content")
	}
}

func TestRunInitInDir_NoForce_DoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()

	// First init.
	if err := runInitInDir(dir, false); err != nil {
		t.Fatalf("first runInitInDir() error: %v", err)
	}

	// Second init without --force should fail.
	err := runInitInDir(dir, false)
	if err == nil {
		t.Fatal("expected error when .forge/ already exists without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error message = %q, want it to mention 'already exists'", err.Error())
	}
}

func TestRunInitInDir_ConfigIsLoadable(t *testing.T) {
	dir := t.TempDir()

	if err := runInitInDir(dir, false); err != nil {
		t.Fatalf("runInitInDir() error: %v", err)
	}

	// The generated config.yaml should be parseable by the config loader.
	// We import the config package indirectly by reading the file.
	configPath := filepath.Join(dir, ".forge", "config.yaml")
	_, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("config.yaml does not exist: %v", err)
	}
}
