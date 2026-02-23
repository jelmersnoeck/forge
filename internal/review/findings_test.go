package review

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/principles"
)

func TestHasCriticalFindings(t *testing.T) {
	tests := []struct {
		name     string
		findings []Finding
		want     bool
	}{
		{
			name:     "empty findings",
			findings: nil,
			want:     false,
		},
		{
			name: "only info findings",
			findings: []Finding{
				{Severity: principles.SeverityInfo, Message: "info1"},
				{Severity: principles.SeverityInfo, Message: "info2"},
			},
			want: false,
		},
		{
			name: "only warning findings",
			findings: []Finding{
				{Severity: principles.SeverityWarning, Message: "warn1"},
			},
			want: false,
		},
		{
			name: "single critical finding",
			findings: []Finding{
				{Severity: principles.SeverityCritical, Message: "crit1"},
			},
			want: true,
		},
		{
			name: "mixed with critical",
			findings: []Finding{
				{Severity: principles.SeverityInfo, Message: "info1"},
				{Severity: principles.SeverityWarning, Message: "warn1"},
				{Severity: principles.SeverityCritical, Message: "crit1"},
			},
			want: true,
		},
		{
			name: "mixed without critical",
			findings: []Finding{
				{Severity: principles.SeverityInfo, Message: "info1"},
				{Severity: principles.SeverityWarning, Message: "warn1"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasCriticalFindings(tt.findings)
			if got != tt.want {
				t.Errorf("HasCriticalFindings() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeduplicate(t *testing.T) {
	tests := []struct {
		name     string
		findings []Finding
		wantLen  int
	}{
		{
			name:     "empty findings",
			findings: nil,
			wantLen:  0,
		},
		{
			name: "no duplicates",
			findings: []Finding{
				{File: "a.go", Line: 1, PrincipleID: "sec-001", Message: "msg1"},
				{File: "b.go", Line: 2, PrincipleID: "sec-002", Message: "msg2"},
			},
			wantLen: 2,
		},
		{
			name: "exact duplicates same file line principle",
			findings: []Finding{
				{File: "a.go", Line: 10, PrincipleID: "sec-001", Message: "msg1", Reviewer: "r1"},
				{File: "a.go", Line: 10, PrincipleID: "sec-001", Message: "msg2", Reviewer: "r2"},
			},
			wantLen: 1,
		},
		{
			name: "same file different line",
			findings: []Finding{
				{File: "a.go", Line: 10, PrincipleID: "sec-001"},
				{File: "a.go", Line: 20, PrincipleID: "sec-001"},
			},
			wantLen: 2,
		},
		{
			name: "same file same line different principle",
			findings: []Finding{
				{File: "a.go", Line: 10, PrincipleID: "sec-001"},
				{File: "a.go", Line: 10, PrincipleID: "sec-002"},
			},
			wantLen: 2,
		},
		{
			name: "multiple duplicates mixed",
			findings: []Finding{
				{File: "a.go", Line: 1, PrincipleID: "sec-001"},
				{File: "a.go", Line: 1, PrincipleID: "sec-001"},
				{File: "b.go", Line: 2, PrincipleID: "sec-002"},
				{File: "b.go", Line: 2, PrincipleID: "sec-002"},
				{File: "c.go", Line: 3, PrincipleID: "sec-003"},
			},
			wantLen: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Deduplicate(tt.findings)
			if len(got) != tt.wantLen {
				t.Errorf("Deduplicate() returned %d findings, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestDeduplicateKeepsFirst(t *testing.T) {
	findings := []Finding{
		{File: "a.go", Line: 10, PrincipleID: "sec-001", Message: "first", Reviewer: "r1"},
		{File: "a.go", Line: 10, PrincipleID: "sec-001", Message: "second", Reviewer: "r2"},
	}
	got := Deduplicate(findings)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(got))
	}
	if got[0].Message != "first" {
		t.Errorf("expected first finding to be kept, got message=%q", got[0].Message)
	}
}

func TestFilterBySeverity(t *testing.T) {
	all := []Finding{
		{Severity: principles.SeverityInfo, Message: "info"},
		{Severity: principles.SeverityWarning, Message: "warn"},
		{Severity: principles.SeverityCritical, Message: "crit"},
	}

	tests := []struct {
		name      string
		threshold principles.Severity
		wantLen   int
		wantMsgs  []string
	}{
		{
			name:      "info threshold returns all",
			threshold: principles.SeverityInfo,
			wantLen:   3,
			wantMsgs:  []string{"info", "warn", "crit"},
		},
		{
			name:      "warning threshold returns warning and critical",
			threshold: principles.SeverityWarning,
			wantLen:   2,
			wantMsgs:  []string{"warn", "crit"},
		},
		{
			name:      "critical threshold returns only critical",
			threshold: principles.SeverityCritical,
			wantLen:   1,
			wantMsgs:  []string{"crit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterBySeverity(all, tt.threshold)
			if len(got) != tt.wantLen {
				t.Errorf("FilterBySeverity(%s) returned %d findings, want %d", tt.threshold, len(got), tt.wantLen)
			}
			for i, msg := range tt.wantMsgs {
				if i < len(got) && got[i].Message != msg {
					t.Errorf("FilterBySeverity(%s)[%d].Message = %q, want %q", tt.threshold, i, got[i].Message, msg)
				}
			}
		})
	}
}

func TestCoveredPrinciples(t *testing.T) {
	tests := []struct {
		name     string
		findings []Finding
		want     []string
	}{
		{
			name:     "empty findings",
			findings: nil,
			want:     nil,
		},
		{
			name: "unique principle IDs",
			findings: []Finding{
				{PrincipleID: "sec-001"},
				{PrincipleID: "arch-001"},
				{PrincipleID: "test-001"},
			},
			want: []string{"sec-001", "arch-001", "test-001"},
		},
		{
			name: "duplicate principle IDs",
			findings: []Finding{
				{PrincipleID: "sec-001"},
				{PrincipleID: "sec-001"},
				{PrincipleID: "arch-001"},
			},
			want: []string{"sec-001", "arch-001"},
		},
		{
			name: "empty principle ID skipped",
			findings: []Finding{
				{PrincipleID: "sec-001"},
				{PrincipleID: ""},
				{PrincipleID: "arch-001"},
			},
			want: []string{"sec-001", "arch-001"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CoveredPrinciples(tt.findings)
			if len(got) != len(tt.want) {
				t.Fatalf("CoveredPrinciples() returned %d IDs, want %d: got=%v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("CoveredPrinciples()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
