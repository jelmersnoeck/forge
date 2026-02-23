package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplatesListCmd(t *testing.T) {
	// Capture output by executing the command.
	buf := new(bytes.Buffer)
	templatesListCmd.SetOut(buf)
	templatesListCmd.SetErr(buf)

	// Reset format flag to default.
	templatesOutputFormat = "table"

	if err := templatesListCmd.RunE(templatesListCmd, nil); err != nil {
		t.Fatalf("templates list error: %v", err)
	}

	// The output goes to os.Stdout in the actual implementation,
	// so we test the underlying function directly instead.
}

func TestRunTemplatesList_Table(t *testing.T) {
	// Redirect stdout to capture output.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	templatesOutputFormat = "table"
	err := runTemplatesList(nil, nil)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runTemplatesList() error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Check header.
	if !strings.Contains(output, "NAME") {
		t.Error("table output missing NAME header")
	}
	if !strings.Contains(output, "VERSION") {
		t.Error("table output missing VERSION header")
	}
	if !strings.Contains(output, "DESCRIPTION") {
		t.Error("table output missing DESCRIPTION header")
	}

	// Check known templates appear.
	for _, name := range []string{"go-security", "go-style", "web-security", "api-design"} {
		if !strings.Contains(output, name) {
			t.Errorf("table output missing template %q", name)
		}
	}
}

func TestRunTemplatesList_JSON(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	templatesOutputFormat = "json"
	err := runTemplatesList(nil, nil)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runTemplatesList() json error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should be valid JSON.
	if !strings.HasPrefix(strings.TrimSpace(output), "[") {
		t.Errorf("json output does not start with [: %s", output[:50])
	}
	if !strings.Contains(output, `"name"`) {
		t.Error("json output missing name field")
	}
	if !strings.Contains(output, `"go-security"`) {
		t.Error("json output missing go-security template")
	}
}

func TestRunTemplatesInstall(t *testing.T) {
	dir := t.TempDir()

	// Change to temp directory so the install uses it.
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Create .forge directory structure.
	os.MkdirAll(filepath.Join(dir, ".forge", "principles"), 0755)

	// Redirect stdout.
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := runTemplatesInstall(nil, []string{"go-security"})

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runTemplatesInstall() error: %v", err)
	}

	// Verify file was created.
	destPath := filepath.Join(dir, ".forge", "principles", "go-security.yaml")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading installed template: %v", err)
	}
	if len(data) == 0 {
		t.Error("installed template is empty")
	}
	if !strings.Contains(string(data), "sec-001") {
		t.Error("installed template missing expected principle ID")
	}
}

func TestRunTemplatesInstall_NotFound(t *testing.T) {
	dir := t.TempDir()

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	err := runTemplatesInstall(nil, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent template, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to mention 'not found'", err.Error())
	}
}

func TestTemplatesCmd_Registered(t *testing.T) {
	// Verify the templates command is registered on rootCmd.
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "templates" {
			found = true
			break
		}
	}
	if !found {
		t.Error("templates command not registered on rootCmd")
	}
}

func TestTemplatesSubcommands(t *testing.T) {
	subCmds := templatesCmd.Commands()
	names := make(map[string]bool)
	for _, cmd := range subCmds {
		names[cmd.Name()] = true
	}

	if !names["list"] {
		t.Error("missing 'list' subcommand")
	}
	if !names["install"] {
		t.Error("missing 'install' subcommand")
	}
}
