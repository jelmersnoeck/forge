package planner

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadWorkstream(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		check   func(t *testing.T, ws *Workstream)
	}{
		{
			name: "valid workstream",
			yaml: `id: ws-auth
goal: "Implement authentication"
tracker: github
repo: org/repo
status: pending
phases:
  - name: "Foundation"
    issues:
      - title: "Add user model"
        description: "Create User struct and DB schema"
        labels:
          - feature
        status: pending
      - title: "Add auth middleware"
        description: "JWT middleware"
        depends_on:
          - "Add user model"
        status: pending
  - name: "Integration"
    issues:
      - title: "Add login endpoint"
        description: "POST /login"
        depends_on:
          - "Add auth middleware"
        status: completed
`,
			wantErr: false,
			check: func(t *testing.T, ws *Workstream) {
				if ws.ID != "ws-auth" {
					t.Errorf("ID = %q, want %q", ws.ID, "ws-auth")
				}
				if ws.Goal != "Implement authentication" {
					t.Errorf("Goal = %q, want %q", ws.Goal, "Implement authentication")
				}
				if len(ws.Phases) != 2 {
					t.Fatalf("Phases = %d, want 2", len(ws.Phases))
				}
				if ws.Phases[0].Name != "Foundation" {
					t.Errorf("Phase[0].Name = %q, want %q", ws.Phases[0].Name, "Foundation")
				}
				if len(ws.Phases[0].Issues) != 2 {
					t.Fatalf("Phase[0].Issues = %d, want 2", len(ws.Phases[0].Issues))
				}
				if ws.Phases[0].Issues[1].DependsOn[0] != "Add user model" {
					t.Errorf("DependsOn = %v, want [Add user model]", ws.Phases[0].Issues[1].DependsOn)
				}
				// Completed status should be preserved.
				if ws.Phases[1].Issues[0].Status != StatusCompleted {
					t.Errorf("Status = %q, want %q", ws.Phases[1].Issues[0].Status, StatusCompleted)
				}
			},
		},
		{
			name:    "missing id",
			yaml:    `goal: "test"`,
			wantErr: true,
		},
		{
			name:    "missing goal",
			yaml:    `id: ws-test`,
			wantErr: true,
		},
		{
			name:    "invalid yaml",
			yaml:    `{{{invalid`,
			wantErr: true,
		},
		{
			name: "default status to pending",
			yaml: `id: ws-test
goal: "test goal"
phases:
  - name: "Phase 1"
    issues:
      - title: "Issue 1"
        description: "desc"
`,
			wantErr: false,
			check: func(t *testing.T, ws *Workstream) {
				if ws.Phases[0].Issues[0].Status != StatusPending {
					t.Errorf("Status = %q, want %q", ws.Phases[0].Issues[0].Status, StatusPending)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "workstream.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatal(err)
			}

			ws, err := LoadWorkstream(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadWorkstream() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil && ws != nil {
				tt.check(t, ws)
			}
		})
	}
}

func TestLoadWorkstream_FileNotFound(t *testing.T) {
	_, err := LoadWorkstream("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestSaveWorkstream(t *testing.T) {
	ws := &Workstream{
		ID:        "ws-test",
		Goal:      "Test goal",
		Tracker:   "github",
		Repo:      "org/repo",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Status:    StatusPending,
		Phases: []Phase{
			{
				Name: "Phase 1",
				Issues: []WorkstreamIssue{
					{
						Title:       "Issue A",
						Description: "Description A",
						Labels:      []string{"feature"},
						Status:      StatusPending,
					},
					{
						Title:       "Issue B",
						Description: "Description B",
						DependsOn:   []string{"Issue A"},
						Status:      StatusPending,
					},
				},
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "ws.yaml")

	if err := SaveWorkstream(ws, path); err != nil {
		t.Fatalf("SaveWorkstream() error = %v", err)
	}

	// Load back and verify round-trip.
	loaded, err := LoadWorkstream(path)
	if err != nil {
		t.Fatalf("LoadWorkstream() error = %v", err)
	}

	if loaded.ID != ws.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, ws.ID)
	}
	if loaded.Goal != ws.Goal {
		t.Errorf("Goal = %q, want %q", loaded.Goal, ws.Goal)
	}
	if len(loaded.Phases) != 1 {
		t.Fatalf("Phases = %d, want 1", len(loaded.Phases))
	}
	if len(loaded.Phases[0].Issues) != 2 {
		t.Fatalf("Issues = %d, want 2", len(loaded.Phases[0].Issues))
	}
	if loaded.Phases[0].Issues[1].DependsOn[0] != "Issue A" {
		t.Errorf("DependsOn = %v, want [Issue A]", loaded.Phases[0].Issues[1].DependsOn)
	}
}

func TestSaveWorkstream_BadPath(t *testing.T) {
	ws := &Workstream{ID: "test", Goal: "test"}
	err := SaveWorkstream(ws, "/nonexistent/dir/ws.yaml")
	if err == nil {
		t.Fatal("expected error for bad path")
	}
}

func TestAllIssues(t *testing.T) {
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Title: "A", Status: StatusPending},
				{Title: "B", Status: StatusPending},
			}},
			{Name: "P2", Issues: []WorkstreamIssue{
				{Title: "C", Status: StatusPending},
			}},
		},
	}

	issues := ws.AllIssues()
	if len(issues) != 3 {
		t.Fatalf("AllIssues() = %d, want 3", len(issues))
	}
	if issues[0].Title != "A" {
		t.Errorf("issues[0].Title = %q, want %q", issues[0].Title, "A")
	}
	if issues[2].Title != "C" {
		t.Errorf("issues[2].Title = %q, want %q", issues[2].Title, "C")
	}
}

func TestFindIssue(t *testing.T) {
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Ref: "#1", Title: "Issue One", Status: StatusPending},
				{Title: "Issue Two", Status: StatusPending},
			}},
		},
	}

	// Find by ref.
	found := ws.FindIssue("#1")
	if found == nil || found.Title != "Issue One" {
		t.Errorf("FindIssue(#1) = %v, want Issue One", found)
	}

	// Find by title.
	found = ws.FindIssue("Issue Two")
	if found == nil || found.Title != "Issue Two" {
		t.Errorf("FindIssue(Issue Two) = %v, want Issue Two", found)
	}

	// Not found.
	found = ws.FindIssue("Nonexistent")
	if found != nil {
		t.Errorf("FindIssue(Nonexistent) = %v, want nil", found)
	}
}

func TestIssueID(t *testing.T) {
	// With ref.
	issue := &WorkstreamIssue{Ref: "#42", Title: "My Issue"}
	if issue.IssueID() != "#42" {
		t.Errorf("IssueID() = %q, want %q", issue.IssueID(), "#42")
	}

	// Without ref, falls back to title.
	issue = &WorkstreamIssue{Title: "My Issue"}
	if issue.IssueID() != "My Issue" {
		t.Errorf("IssueID() = %q, want %q", issue.IssueID(), "My Issue")
	}
}
