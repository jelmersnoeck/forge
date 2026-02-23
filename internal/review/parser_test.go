package review

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/principles"
)

func TestParseFindings(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantLen int
		wantErr bool
		check   func(t *testing.T, findings []Finding)
	}{
		{
			name:    "empty output",
			output:  "",
			wantLen: 0,
		},
		{
			name:    "whitespace only",
			output:  "   \n\t  ",
			wantLen: 0,
		},
		{
			name:    "single JSON object",
			output:  `{"file":"main.go","line":10,"principle_id":"sec-001","severity":"critical","message":"SQL injection","suggestion":"Use parameterized queries"}`,
			wantLen: 1,
			check: func(t *testing.T, findings []Finding) {
				f := findings[0]
				if f.File != "main.go" {
					t.Errorf("File = %q, want %q", f.File, "main.go")
				}
				if f.Line != 10 {
					t.Errorf("Line = %d, want %d", f.Line, 10)
				}
				if f.PrincipleID != "sec-001" {
					t.Errorf("PrincipleID = %q, want %q", f.PrincipleID, "sec-001")
				}
				if f.Severity != principles.SeverityCritical {
					t.Errorf("Severity = %q, want %q", f.Severity, principles.SeverityCritical)
				}
			},
		},
		{
			name: "JSON array of findings",
			output: `[
				{"file":"a.go","line":1,"principle_id":"sec-001","severity":"critical","message":"issue1"},
				{"file":"b.go","line":2,"principle_id":"arch-001","severity":"warning","message":"issue2"}
			]`,
			wantLen: 2,
		},
		{
			name:    "empty JSON array",
			output:  `[]`,
			wantLen: 0,
		},
		{
			name: "JSON embedded in markdown code fence",
			output: "Here are my findings:\n\n```json\n" +
				`[{"file":"main.go","line":5,"principle_id":"sec-001","severity":"warning","message":"found issue"}]` +
				"\n```\n\nThat's all.",
			wantLen: 1,
			check: func(t *testing.T, findings []Finding) {
				if findings[0].File != "main.go" {
					t.Errorf("File = %q, want %q", findings[0].File, "main.go")
				}
			},
		},
		{
			name: "JSON embedded in explanatory text",
			output: `I reviewed the diff and found the following issues:

{"file":"handler.go","line":42,"principle_id":"sec-002","severity":"critical","message":"Missing auth check"}

The above issue is critical and should be fixed before merging.`,
			wantLen: 1,
			check: func(t *testing.T, findings []Finding) {
				if findings[0].Line != 42 {
					t.Errorf("Line = %d, want %d", findings[0].Line, 42)
				}
			},
		},
		{
			name:    "completely malformed output",
			output:  "This is just plain text with no JSON at all.",
			wantErr: true,
		},
		{
			name:    "invalid JSON that looks like JSON",
			output:  `{"file": "main.go", "line": broken}`,
			wantErr: true,
		},
		{
			name:    "JSON object with only empty fields is not a finding",
			output:  `{"foo":"bar"}`,
			wantErr: true,
		},
		{
			name: "array embedded in text with surrounding prose",
			output: `Based on my analysis:

[
  {"file":"config.go","line":15,"principle_id":"sec-001","severity":"info","message":"Consider using env vars"},
  {"file":"db.go","line":88,"principle_id":"sec-002","severity":"critical","message":"Hardcoded credentials"}
]

These findings cover security principles sec-001 and sec-002.`,
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := ParseFindings(tt.output)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseFindings() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(findings) != tt.wantLen {
				t.Fatalf("ParseFindings() returned %d findings, want %d", len(findings), tt.wantLen)
			}
			if tt.check != nil {
				tt.check(t, findings)
			}
		})
	}
}
