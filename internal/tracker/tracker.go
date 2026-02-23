// Package tracker defines the Tracker interface for issue/PR management.
//
// Forge reads work items from and writes results to external trackers.
// Each backend (GitHub, Jira, Linear, File) implements this interface
// to support different project management tools.
package tracker

import "context"

// Tracker manages work items and pull requests in an external system.
type Tracker interface {
	// GetIssue fetches a work item by reference.
	GetIssue(ctx context.Context, ref string) (*Issue, error)

	// CreateIssue creates a new work item (used by the planner).
	CreateIssue(ctx context.Context, req *CreateIssueRequest) (*Issue, error)

	// CreatePR submits completed work as a pull request.
	CreatePR(ctx context.Context, req *CreatePRRequest) (*PullRequest, error)

	// Comment adds a status update or note to a work item.
	Comment(ctx context.Context, ref string, body string) error

	// Link sets a dependency relationship between two issues.
	Link(ctx context.Context, from string, to string, rel LinkRelation) error
}

// LinkRelation describes the relationship between linked issues.
type LinkRelation string

const (
	RelBlocks    LinkRelation = "blocks"
	RelDependsOn LinkRelation = "depends_on"
	RelRelates   LinkRelation = "relates_to"
)

// Issue represents a work item from any tracker.
type Issue struct {
	ID        string   // Tracker-specific ID (e.g., "123", "PROJECT-456").
	Tracker   string   // Source tracker name (e.g., "github", "jira").
	Title     string
	Body      string
	Labels    []string
	Repo      string   // Repository (GitHub).
	Project   string   // Project key (Jira/Linear).
	DependsOn []string // Issue refs this blocks on.
	Status    string
	URL       string
}

// CreateIssueRequest contains the fields needed to create a new issue.
type CreateIssueRequest struct {
	Title       string
	Body        string
	Labels      []string
	Repo        string   // For GitHub.
	Project     string   // For Jira/Linear.
	DependsOn   []string // Issue refs to link as dependencies.
	WorkstreamID string  // Optional workstream association.
}

// CreatePRRequest contains the fields needed to create a pull request.
type CreatePRRequest struct {
	Title      string
	Body       string
	Head       string // Source branch.
	Base       string // Target branch.
	Repo       string
	Labels     []string
	IssueRef   string // Link to the originating issue.
	DraftMode  bool   // Create as draft PR.
}

// PullRequest represents a created pull request.
type PullRequest struct {
	ID     string
	Number int
	URL    string
	Status string
}
