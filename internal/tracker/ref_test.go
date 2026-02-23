package tracker

import (
	"testing"
)

func TestParseIssueRef(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *IssueRef
		wantErr bool
	}{
		// GitHub full format.
		{
			name:  "github full URI",
			input: "github://myorg/myrepo#123",
			want:  &IssueRef{Tracker: "github", Org: "myorg", Repo: "myrepo", ID: "123"},
		},
		{
			name:  "gh short prefix",
			input: "gh:myorg/myrepo#456",
			want:  &IssueRef{Tracker: "github", Org: "myorg", Repo: "myrepo", ID: "456"},
		},
		{
			name:  "github with hyphens in org and repo",
			input: "gh:my-org/my-repo#789",
			want:  &IssueRef{Tracker: "github", Org: "my-org", Repo: "my-repo", ID: "789"},
		},
		// GitHub short format.
		{
			name:  "github short number only",
			input: "#42",
			want:  &IssueRef{Tracker: "github", ID: "42"},
		},
		{
			name:  "github short single digit",
			input: "#1",
			want:  &IssueRef{Tracker: "github", ID: "1"},
		},
		// Jira formats.
		{
			name:  "jira full URI",
			input: "jira://PROJECT-456",
			want:  &IssueRef{Tracker: "jira", Project: "PROJECT", ID: "456"},
		},
		{
			name:  "jira short prefix",
			input: "jira:FORGE-123",
			want:  &IssueRef{Tracker: "jira", Project: "FORGE", ID: "123"},
		},
		{
			name:  "jira bare PROJECT-NNN",
			input: "PROJ-999",
			want:  &IssueRef{Tracker: "jira", Project: "PROJ", ID: "999"},
		},
		{
			name:  "jira alphanumeric project key",
			input: "jira:AB2-100",
			want:  &IssueRef{Tracker: "jira", Project: "AB2", ID: "100"},
		},
		// Linear formats.
		{
			name:  "linear full URI",
			input: "linear://TEAM-789",
			want:  &IssueRef{Tracker: "linear", Project: "TEAM", ID: "789"},
		},
		{
			name:  "lin short prefix",
			input: "lin:ENG-42",
			want:  &IssueRef{Tracker: "linear", Project: "ENG", ID: "42"},
		},
		{
			name:  "linear short prefix",
			input: "linear:DEV-100",
			want:  &IssueRef{Tracker: "linear", Project: "DEV", ID: "100"},
		},
		// File formats.
		{
			name:  "file URI",
			input: "file://./specs/feature.md",
			want:  &IssueRef{Tracker: "file", Path: "./specs/feature.md"},
		},
		{
			name:  "relative path",
			input: "./specs/feature.md",
			want:  &IssueRef{Tracker: "file", Path: "./specs/feature.md"},
		},
		{
			name:  "relative path with nested dirs",
			input: "./issues/2024/my-issue.md",
			want:  &IssueRef{Tracker: "file", Path: "./issues/2024/my-issue.md"},
		},
		// Error cases.
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: true,
		},
		{
			name:    "malformed github ref missing number",
			input:   "gh:org/repo#",
			wantErr: true,
		},
		{
			name:    "malformed hash without number",
			input:   "#",
			wantErr: true,
		},
		{
			name:    "random string",
			input:   "just-some-text",
			wantErr: true,
		},
		{
			name:    "bare number without hash",
			input:   "123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIssueRef(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseIssueRef(%q) expected error, got %+v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseIssueRef(%q) unexpected error: %v", tt.input, err)
			}
			if got.Tracker != tt.want.Tracker {
				t.Errorf("Tracker = %q, want %q", got.Tracker, tt.want.Tracker)
			}
			if got.Org != tt.want.Org {
				t.Errorf("Org = %q, want %q", got.Org, tt.want.Org)
			}
			if got.Repo != tt.want.Repo {
				t.Errorf("Repo = %q, want %q", got.Repo, tt.want.Repo)
			}
			if got.Project != tt.want.Project {
				t.Errorf("Project = %q, want %q", got.Project, tt.want.Project)
			}
			if got.ID != tt.want.ID {
				t.Errorf("ID = %q, want %q", got.ID, tt.want.ID)
			}
			if got.Path != tt.want.Path {
				t.Errorf("Path = %q, want %q", got.Path, tt.want.Path)
			}
		})
	}
}

func TestIssueRefString(t *testing.T) {
	tests := []struct {
		name string
		ref  IssueRef
		want string
	}{
		{
			name: "github full",
			ref:  IssueRef{Tracker: "github", Org: "myorg", Repo: "myrepo", ID: "123"},
			want: "gh:myorg/myrepo#123",
		},
		{
			name: "github short",
			ref:  IssueRef{Tracker: "github", ID: "42"},
			want: "#42",
		},
		{
			name: "jira",
			ref:  IssueRef{Tracker: "jira", Project: "FORGE", ID: "456"},
			want: "jira:FORGE-456",
		},
		{
			name: "linear",
			ref:  IssueRef{Tracker: "linear", Project: "TEAM", ID: "789"},
			want: "linear:TEAM-789",
		},
		{
			name: "file",
			ref:  IssueRef{Tracker: "file", Path: "./specs/feature.md"},
			want: "./specs/feature.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ref.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIssueRefStringRoundtrip(t *testing.T) {
	// Refs that should roundtrip through String() -> ParseIssueRef().
	tests := []struct {
		name  string
		input string
	}{
		{"github full", "gh:myorg/myrepo#123"},
		{"github short", "#42"},
		{"jira", "jira:FORGE-456"},
		{"linear", "linear:TEAM-789"},
		{"file", "./specs/feature.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ParseIssueRef(tt.input)
			if err != nil {
				t.Fatalf("ParseIssueRef(%q) error: %v", tt.input, err)
			}
			got := ref.String()
			if got != tt.input {
				t.Errorf("roundtrip: got %q, want %q", got, tt.input)
			}
			// Parse again to verify.
			ref2, err := ParseIssueRef(got)
			if err != nil {
				t.Fatalf("second ParseIssueRef(%q) error: %v", got, err)
			}
			if ref2.String() != tt.input {
				t.Errorf("second roundtrip: got %q, want %q", ref2.String(), tt.input)
			}
		})
	}
}
