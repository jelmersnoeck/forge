package principles

import (
	"fmt"
	"regexp"
	"strings"
)

// idPattern matches principle IDs like "sec-001", "arch-003".
var idPattern = regexp.MustCompile(`^[a-z]+-\d{3}$`)

// knownCategories is the set of valid principle categories.
var knownCategories = map[Category]bool{
	CategorySecurity:     true,
	CategoryArchitecture: true,
	CategorySimplicity:   true,
	CategoryTesting:      true,
	CategoryPerformance:  true,
}

// knownSeverities is the set of valid principle severities.
var knownSeverities = map[Severity]bool{
	SeverityCritical: true,
	SeverityWarning:  true,
	SeverityInfo:     true,
}

// ValidationError collects multiple validation problems.
type ValidationError struct {
	Errors []string
}

func (ve *ValidationError) Error() string {
	return fmt.Sprintf("validation failed with %d error(s):\n  - %s",
		len(ve.Errors), strings.Join(ve.Errors, "\n  - "))
}

// add appends an error message.
func (ve *ValidationError) add(msg string) {
	ve.Errors = append(ve.Errors, msg)
}

// ValidateSet validates an entire principle set, returning all errors found.
func ValidateSet(set *PrincipleSet) error {
	ve := &ValidationError{}

	if set.Name == "" {
		ve.add("principle set name is required")
	}

	for i, p := range set.Principles {
		prefix := fmt.Sprintf("principle[%d]", i)
		if p.ID != "" {
			prefix = fmt.Sprintf("principle[%d] (%s)", i, p.ID)
		}
		validatePrinciple(prefix, p, ve)
	}

	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}

// ValidatePrinciple validates a single principle, returning all errors found.
func ValidatePrinciple(p Principle) error {
	ve := &ValidationError{}
	validatePrinciple("principle", p, ve)
	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}

func validatePrinciple(prefix string, p Principle, ve *ValidationError) {
	// Required fields.
	if p.ID == "" {
		ve.add(fmt.Sprintf("%s: id is required", prefix))
	} else if !idPattern.MatchString(p.ID) {
		ve.add(fmt.Sprintf("%s: id %q does not match pattern {category}-{number} (e.g. sec-001)", prefix, p.ID))
	}

	if p.Title == "" {
		ve.add(fmt.Sprintf("%s: title is required", prefix))
	}
	if p.Description == "" {
		ve.add(fmt.Sprintf("%s: description is required", prefix))
	}
	if p.Rationale == "" {
		ve.add(fmt.Sprintf("%s: rationale is required", prefix))
	}
	if p.Check == "" {
		ve.add(fmt.Sprintf("%s: check is required", prefix))
	}

	// Severity validation.
	if p.Severity == "" {
		ve.add(fmt.Sprintf("%s: severity is required", prefix))
	} else if !knownSeverities[p.Severity] {
		ve.add(fmt.Sprintf("%s: severity %q is not valid (must be critical, warning, or info)", prefix, p.Severity))
	}

	// Category validation.
	if p.Category == "" {
		ve.add(fmt.Sprintf("%s: category is required", prefix))
	} else if !knownCategories[p.Category] {
		ve.add(fmt.Sprintf("%s: category %q is not known", prefix, p.Category))
	}

	// Examples: at least one required.
	if len(p.Examples) == 0 {
		ve.add(fmt.Sprintf("%s: at least one example is required", prefix))
	}
}
