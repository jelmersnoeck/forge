package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListTemplates(t *testing.T) {
	infos, err := ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates() error: %v", err)
	}

	if len(infos) == 0 {
		t.Fatal("ListTemplates() returned no templates")
	}

	// Verify expected templates exist.
	expected := map[string]bool{
		"go-security":  false,
		"go-style":     false,
		"web-security": false,
		"api-design":   false,
	}

	for _, info := range infos {
		if _, ok := expected[info.Name]; ok {
			expected[info.Name] = true
		}

		// Every template must have basic metadata.
		if info.Name == "" {
			t.Error("template has empty name")
		}
		if info.Version == "" {
			t.Errorf("template %q has empty version", info.Name)
		}
		if info.Description == "" {
			t.Errorf("template %q has empty description", info.Name)
		}
		if info.Principles == 0 {
			t.Errorf("template %q has no principles", info.Name)
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("expected template %q not found", name)
		}
	}
}

func TestListTemplates_MetadataComplete(t *testing.T) {
	infos, err := ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates() error: %v", err)
	}

	for _, info := range infos {
		t.Run(info.Name, func(t *testing.T) {
			if info.Version != "1.0.0" {
				t.Errorf("version = %q, want %q", info.Version, "1.0.0")
			}
			if info.Principles < 3 {
				t.Errorf("principles = %d, want >= 3", info.Principles)
			}
		})
	}
}

func TestGetTemplate(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "go-security", wantErr: false},
		{name: "go-style", wantErr: false},
		{name: "web-security", wantErr: false},
		{name: "api-design", wantErr: false},
		{name: "nonexistent", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := GetTemplate(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Error("GetTemplate() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("GetTemplate() error: %v", err)
			}
			if set.Name != tt.name {
				t.Errorf("Name = %q, want %q", set.Name, tt.name)
			}
			if len(set.Principles) == 0 {
				t.Error("Principles is empty")
			}

			// Validate principle structure.
			for _, p := range set.Principles {
				if p.ID == "" {
					t.Error("principle has empty ID")
				}
				if p.Title == "" {
					t.Errorf("principle %s has empty title", p.ID)
				}
				if p.Category == "" {
					t.Errorf("principle %s has empty category", p.ID)
				}
				if p.Severity == "" {
					t.Errorf("principle %s has empty severity", p.ID)
				}
				if p.Description == "" {
					t.Errorf("principle %s has empty description", p.ID)
				}
				if p.Check == "" {
					t.Errorf("principle %s has empty check", p.ID)
				}
			}
		})
	}
}

func TestGetTemplate_PrincipleIDs(t *testing.T) {
	tests := []struct {
		name      string
		wantIDs   []string
	}{
		{
			name:    "go-security",
			wantIDs: []string{"sec-001", "sec-002", "sec-003", "sec-004", "sec-005"},
		},
		{
			name:    "go-style",
			wantIDs: []string{"style-001", "style-002", "style-003", "style-004", "style-005"},
		},
		{
			name:    "web-security",
			wantIDs: []string{"web-001", "web-002", "web-003", "web-004", "web-005"},
		},
		{
			name:    "api-design",
			wantIDs: []string{"api-001", "api-002", "api-003", "api-004", "api-005"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := GetTemplate(tt.name)
			if err != nil {
				t.Fatalf("GetTemplate() error: %v", err)
			}

			gotIDs := make(map[string]bool)
			for _, p := range set.Principles {
				gotIDs[p.ID] = true
			}

			for _, wantID := range tt.wantIDs {
				if !gotIDs[wantID] {
					t.Errorf("missing principle ID %q", wantID)
				}
			}
		})
	}
}

func TestInstallTemplate(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, "principles")

	if err := InstallTemplate("go-security", destDir); err != nil {
		t.Fatalf("InstallTemplate() error: %v", err)
	}

	// Verify file was created.
	destPath := filepath.Join(destDir, "go-security.yaml")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading installed template: %v", err)
	}

	if len(data) == 0 {
		t.Error("installed template file is empty")
	}

	// Verify content is valid YAML with expected fields.
	content := string(data)
	if !strings.Contains(content, "name: go-security") {
		t.Error("installed template missing name field")
	}
	if !strings.Contains(content, "sec-001") {
		t.Error("installed template missing expected principle ID")
	}
}

func TestInstallTemplate_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, "nested", "deep", "principles")

	if err := InstallTemplate("go-style", destDir); err != nil {
		t.Fatalf("InstallTemplate() error: %v", err)
	}

	destPath := filepath.Join(destDir, "go-style.yaml")
	if _, err := os.Stat(destPath); err != nil {
		t.Fatalf("installed template not found: %v", err)
	}
}

func TestInstallTemplate_NoOverwrite(t *testing.T) {
	dir := t.TempDir()

	// First install should succeed.
	if err := InstallTemplate("go-security", dir); err != nil {
		t.Fatalf("first InstallTemplate() error: %v", err)
	}

	// Second install should fail (no overwrite).
	err := InstallTemplate("go-security", dir)
	if err == nil {
		t.Fatal("expected error on duplicate install, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want it to mention 'already exists'", err.Error())
	}
}

func TestInstallTemplate_NotFound(t *testing.T) {
	dir := t.TempDir()

	err := InstallTemplate("nonexistent", dir)
	if err == nil {
		t.Fatal("expected error for nonexistent template, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to mention 'not found'", err.Error())
	}
}

func TestInstallTemplate_AllTemplates(t *testing.T) {
	infos, err := ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates() error: %v", err)
	}

	for _, info := range infos {
		t.Run(info.Name, func(t *testing.T) {
			dir := t.TempDir()
			if err := InstallTemplate(info.Name, dir); err != nil {
				t.Fatalf("InstallTemplate(%q) error: %v", info.Name, err)
			}

			destPath := filepath.Join(dir, info.Name+".yaml")
			data, err := os.ReadFile(destPath)
			if err != nil {
				t.Fatalf("reading installed template: %v", err)
			}
			if len(data) == 0 {
				t.Error("installed file is empty")
			}
		})
	}
}
