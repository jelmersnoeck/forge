// Package phase defines composable agent phases (spec-creator, coder, reviewer)
// and the software-engineer orchestrator that chains them.
package phase

import (
	"github.com/jelmersnoeck/forge/internal/review"
)

// Phase configures a conversation loop for a specific purpose.
type Phase struct {
	Name            string   // "spec", "code", "review"
	AllowedTools    []string // tools this phase can use (empty = all)
	DisallowedTools []string // tools this phase cannot use
	Model           string   // model override (empty = default)
	MaxTurns        int      // 0 = unlimited
}

// Result is the output of a completed phase.
type Result struct {
	Phase    string           // phase name
	SpecPath string           // path to spec file (spec-creator output)
	Diff     string           // git diff (coder output)
	Findings []review.Finding // review findings (reviewer output)
}

// SpecCreator returns the spec-creator phase configuration.
func SpecCreator() Phase {
	return Phase{
		Name: "spec",
		// Spec creator can read and explore, write specs, run read-only bash.
		// No editing existing files, no PRs, no sub-agents.
		DisallowedTools: []string{
			"Edit", "PRCreate",
			"Agent", "AgentGet", "AgentList", "AgentStop",
			"TaskCreate", "TaskGet", "TaskList", "TaskStop", "TaskOutput",
			"QueueImmediate", "QueueOnComplete",
			"UseMCPTool",
		},
		MaxTurns: 200,
	}
}

// Coder returns the coder phase configuration.
func Coder() Phase {
	return Phase{
		Name:     "code",
		MaxTurns: 0, // unlimited
	}
}

// Reviewer returns the reviewer phase configuration.
func Reviewer() Phase {
	return Phase{
		Name: "review",
		DisallowedTools: []string{
			"Write", "Edit", "PRCreate",
			"Agent", "AgentGet", "AgentList", "AgentStop",
			"TaskCreate", "TaskGet", "TaskList", "TaskStop", "TaskOutput",
			"QueueImmediate", "QueueOnComplete",
			"UseMCPTool",
			"Reflect",
		},
		MaxTurns: 100,
	}
}
