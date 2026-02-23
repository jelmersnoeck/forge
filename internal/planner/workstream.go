// Package planner implements goal decomposition, workstream management,
// dependency graph resolution, and parallel execution for Forge's
// project-level orchestration (Phase 4).
package planner

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// WorkstreamStatus indicates the current state of a workstream or issue.
type WorkstreamStatus string

const (
	StatusPending    WorkstreamStatus = "pending"
	StatusInProgress WorkstreamStatus = "in_progress"
	StatusCompleted  WorkstreamStatus = "completed"
	StatusFailed     WorkstreamStatus = "failed"
)

// Workstream represents a decomposed goal with phases and issues.
// It is the unit of project-level orchestration in Forge.
type Workstream struct {
	ID        string           `yaml:"id"`
	Goal      string           `yaml:"goal"`
	Tracker   string           `yaml:"tracker"`
	Repo      string           `yaml:"repo"`
	CreatedAt time.Time        `yaml:"created_at"`
	Phases    []Phase          `yaml:"phases"`
	Status    WorkstreamStatus `yaml:"status"`
}

// Phase groups related issues in execution order within a workstream.
type Phase struct {
	Name   string            `yaml:"name"`
	Issues []WorkstreamIssue `yaml:"issues"`
}

// WorkstreamIssue represents a single work item within a workstream phase.
type WorkstreamIssue struct {
	Ref         string           `yaml:"ref,omitempty"`
	Title       string           `yaml:"title"`
	Description string           `yaml:"description"`
	Labels      []string         `yaml:"labels,omitempty"`
	DependsOn   []string         `yaml:"depends_on,omitempty"`
	Status      WorkstreamStatus `yaml:"status"`
}

// IssueID returns a stable identifier for the issue within the workstream.
// If a tracker ref has been assigned, it returns that; otherwise it returns
// the title as a fallback identifier.
func (wi *WorkstreamIssue) IssueID() string {
	if wi.Ref != "" {
		return wi.Ref
	}
	return wi.Title
}

// AllIssues returns all issues across all phases in order.
func (ws *Workstream) AllIssues() []*WorkstreamIssue {
	var issues []*WorkstreamIssue
	for i := range ws.Phases {
		for j := range ws.Phases[i].Issues {
			issues = append(issues, &ws.Phases[i].Issues[j])
		}
	}
	return issues
}

// FindIssue locates an issue by its ref or title across all phases.
func (ws *Workstream) FindIssue(id string) *WorkstreamIssue {
	for i := range ws.Phases {
		for j := range ws.Phases[i].Issues {
			issue := &ws.Phases[i].Issues[j]
			if issue.Ref == id || issue.Title == id {
				return issue
			}
		}
	}
	return nil
}

// LoadWorkstream reads a workstream YAML file from disk.
func LoadWorkstream(path string) (*Workstream, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workstream file %s: %w", path, err)
	}

	var ws Workstream
	if err := yaml.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("parsing workstream YAML %s: %w", path, err)
	}

	if ws.ID == "" {
		return nil, fmt.Errorf("workstream file %s: missing required field 'id'", path)
	}
	if ws.Goal == "" {
		return nil, fmt.Errorf("workstream file %s: missing required field 'goal'", path)
	}

	// Default status for issues that have none set.
	for i := range ws.Phases {
		for j := range ws.Phases[i].Issues {
			if ws.Phases[i].Issues[j].Status == "" {
				ws.Phases[i].Issues[j].Status = StatusPending
			}
		}
	}

	return &ws, nil
}

// SaveWorkstream writes a workstream to a YAML file on disk.
func SaveWorkstream(ws *Workstream, path string) error {
	data, err := yaml.Marshal(ws)
	if err != nil {
		return fmt.Errorf("marshaling workstream YAML: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing workstream file %s: %w", path, err)
	}

	return nil
}
