// Package agent defines the Agent interface for LLM execution backends.
//
// Forge delegates all LLM work (planning, coding, reviewing) to agents.
// Each backend (Claude Code, OpenCode, HTTP) implements this interface
// by shelling out to CLI tools or making API calls.
package agent

import "context"

// Agent executes prompts and returns structured output.
type Agent interface {
	// Run executes a prompt with the given request parameters.
	Run(ctx context.Context, req Request) (*Response, error)
}

// Mode defines the operational mode for an agent invocation.
type Mode string

const (
	ModePlan   Mode = "plan"   // Read-only, outputs structured plan.
	ModeCode   Mode = "code"   // Full access, writes code, runs tests.
	ModeReview Mode = "review" // Read-only, outputs structured findings.
)

// ToolPermissions controls what actions an agent is allowed to perform.
type ToolPermissions struct {
	Read    bool // Can read files.
	Write   bool // Can write files.
	Execute bool // Can run commands.
	Network bool // Can make network calls.
}

// Request contains everything needed for an agent invocation.
type Request struct {
	Prompt       string          // The assembled prompt.
	WorkDir      string          // Working directory (repo checkout).
	Mode         Mode            // plan | code | review.
	Permissions  ToolPermissions // What the agent can do.
	OutputFormat string          // json | text | stream.
	Model        string          // Optional model override.
}

// Response contains the structured output from an agent invocation.
type Response struct {
	Output   string   // Raw text output from the agent.
	ExitCode int      // Process exit code (for CLI-based agents).
	Cost     *Cost    // Optional token/cost information.
	Duration float64  // Execution time in seconds.
	Error    string   // Error message if the agent failed.
	Files    []string // Files modified (for code mode).
}

// Cost tracks token usage and API costs for an agent run.
type Cost struct {
	InputTokens  int     // Tokens consumed in the prompt.
	OutputTokens int     // Tokens generated in the response.
	TotalCost    float64 // Estimated cost in USD.
}
