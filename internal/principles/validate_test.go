package principles

import (
	"strings"
	"testing"
)

func validPrinciple() Principle {
	return Principle{
		ID:          "sec-001",
		Category:    CategorySecurity,
		Severity:    SeverityCritical,
		Title:       "No hardcoded secrets",
		Description: "Never embed secrets in code.",
		Rationale:   "Secrets in code end up in version control.",
		Check:       "Scan for string literals.",
		Examples: []Example{
			{Type: "bad", Code: `const key = "abc"`, Explanation: "Hardcoded."},
		},
	}
}

func TestValidatePrinciple_Valid(t *testing.T) {
	p := validPrinciple()
	if err := ValidatePrinciple(p); err != nil {
		t.Errorf("expected valid principle to pass, got: %v", err)
	}
}

func TestValidatePrinciple_InvalidIDFormat(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"no dash", "sec001"},
		{"uppercase", "SEC-001"},
		{"no number", "sec-abc"},
		{"short number", "sec-01"},
		{"long number", "sec-0001"},
		{"leading dash", "-sec-001"},
		{"empty", ""},
		{"just number", "001"},
		{"just category", "sec"},
		{"special chars", "sec_001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPrinciple()
			p.ID = tt.id
			err := ValidatePrinciple(p)
			if err == nil {
				t.Errorf("expected error for ID %q, got nil", tt.id)
			}
		})
	}
}

func TestValidatePrinciple_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Principle)
		wantMsg string
	}{
		{
			name:    "missing id",
			modify:  func(p *Principle) { p.ID = "" },
			wantMsg: "id is required",
		},
		{
			name:    "missing title",
			modify:  func(p *Principle) { p.Title = "" },
			wantMsg: "title is required",
		},
		{
			name:    "missing description",
			modify:  func(p *Principle) { p.Description = "" },
			wantMsg: "description is required",
		},
		{
			name:    "missing rationale",
			modify:  func(p *Principle) { p.Rationale = "" },
			wantMsg: "rationale is required",
		},
		{
			name:    "missing check",
			modify:  func(p *Principle) { p.Check = "" },
			wantMsg: "check is required",
		},
		{
			name:    "missing severity",
			modify:  func(p *Principle) { p.Severity = "" },
			wantMsg: "severity is required",
		},
		{
			name:    "missing category",
			modify:  func(p *Principle) { p.Category = "" },
			wantMsg: "category is required",
		},
		{
			name:    "missing examples",
			modify:  func(p *Principle) { p.Examples = nil },
			wantMsg: "at least one example is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPrinciple()
			tt.modify(&p)
			err := ValidatePrinciple(p)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestValidatePrinciple_InvalidSeverity(t *testing.T) {
	p := validPrinciple()
	p.Severity = "high"
	err := ValidatePrinciple(p)
	if err == nil {
		t.Fatal("expected error for invalid severity, got nil")
	}
	if !strings.Contains(err.Error(), "severity") {
		t.Errorf("error %q does not mention severity", err.Error())
	}
}

func TestValidatePrinciple_InvalidCategory(t *testing.T) {
	p := validPrinciple()
	p.Category = "unknown"
	err := ValidatePrinciple(p)
	if err == nil {
		t.Fatal("expected error for invalid category, got nil")
	}
	if !strings.Contains(err.Error(), "category") {
		t.Errorf("error %q does not mention category", err.Error())
	}
}

func TestValidatePrinciple_MultipleErrors(t *testing.T) {
	p := Principle{} // Everything missing.
	err := ValidatePrinciple(p)
	if err == nil {
		t.Fatal("expected error for empty principle, got nil")
	}

	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}

	// Should have errors for: id, title, description, rationale, check,
	// severity, category, examples = 8 errors minimum.
	if len(ve.Errors) < 8 {
		t.Errorf("expected at least 8 errors, got %d: %v", len(ve.Errors), ve.Errors)
	}
}

func TestValidateSet_Valid(t *testing.T) {
	set := &PrincipleSet{
		Name:    "test",
		Version: "1.0.0",
		Principles: []Principle{
			validPrinciple(),
		},
	}
	if err := ValidateSet(set); err != nil {
		t.Errorf("expected valid set to pass, got: %v", err)
	}
}

func TestValidateSet_MissingName(t *testing.T) {
	set := &PrincipleSet{
		Version: "1.0.0",
		Principles: []Principle{
			validPrinciple(),
		},
	}
	err := ValidateSet(set)
	if err == nil {
		t.Fatal("expected error for missing set name, got nil")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error %q does not mention name", err.Error())
	}
}

func TestValidateSet_InvalidPrinciple(t *testing.T) {
	bad := validPrinciple()
	bad.Severity = "bogus"

	set := &PrincipleSet{
		Name:    "test",
		Version: "1.0.0",
		Principles: []Principle{
			validPrinciple(),
			bad,
		},
	}
	err := ValidateSet(set)
	if err == nil {
		t.Fatal("expected error for invalid principle in set, got nil")
	}
	if !strings.Contains(err.Error(), "sec-001") {
		t.Errorf("error %q does not reference the principle ID", err.Error())
	}
}

func TestValidatePrinciple_ValidSeverities(t *testing.T) {
	for _, sev := range []Severity{SeverityCritical, SeverityWarning, SeverityInfo} {
		t.Run(string(sev), func(t *testing.T) {
			p := validPrinciple()
			p.Severity = sev
			if err := ValidatePrinciple(p); err != nil {
				t.Errorf("severity %q should be valid, got: %v", sev, err)
			}
		})
	}
}

func TestValidatePrinciple_ValidCategories(t *testing.T) {
	for _, cat := range []Category{CategorySecurity, CategoryArchitecture, CategorySimplicity, CategoryTesting, CategoryPerformance} {
		t.Run(string(cat), func(t *testing.T) {
			p := validPrinciple()
			p.Category = cat
			if err := ValidatePrinciple(p); err != nil {
				t.Errorf("category %q should be valid, got: %v", cat, err)
			}
		})
	}
}

func TestValidationError_ErrorString(t *testing.T) {
	ve := &ValidationError{
		Errors: []string{"error one", "error two"},
	}
	s := ve.Error()
	if !strings.Contains(s, "2 error(s)") {
		t.Errorf("error string %q does not mention count", s)
	}
	if !strings.Contains(s, "error one") {
		t.Errorf("error string %q does not contain first error", s)
	}
	if !strings.Contains(s, "error two") {
		t.Errorf("error string %q does not contain second error", s)
	}
}
