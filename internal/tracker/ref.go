package tracker

import (
	"fmt"
	"regexp"
	"strings"
)

// IssueRef is a parsed issue reference that identifies a work item
// across different tracker systems.
type IssueRef struct {
	Tracker string // "github", "jira", "linear", "file".
	Org     string // GitHub org.
	Repo    string // GitHub repo.
	Project string // Jira/Linear project/team.
	ID      string // Issue ID or number.
	Path    string // File path (for file tracker).
}

// String returns the canonical string representation of the reference.
func (r *IssueRef) String() string {
	switch r.Tracker {
	case "github":
		if r.Org != "" && r.Repo != "" {
			return fmt.Sprintf("gh:%s/%s#%s", r.Org, r.Repo, r.ID)
		}
		return "#" + r.ID
	case "jira":
		return fmt.Sprintf("jira:%s-%s", r.Project, r.ID)
	case "linear":
		return fmt.Sprintf("linear:%s-%s", r.Project, r.ID)
	case "file":
		return r.Path
	default:
		return r.ID
	}
}

var (
	// github://org/repo#123 or gh:org/repo#123
	githubFullRe = regexp.MustCompile(`^(?:github://|gh:)([^/]+)/([^#]+)#(\d+)$`)
	// #123
	githubShortRe = regexp.MustCompile(`^#(\d+)$`)
	// jira://PROJECT-456 or jira:PROJECT-456
	jiraRe = regexp.MustCompile(`^(?:jira://|jira:)([A-Z][A-Z0-9]+)-(\d+)$`)
	// Bare JIRA-style: PROJECT-456
	jiraBareRe = regexp.MustCompile(`^([A-Z][A-Z0-9]+)-(\d+)$`)
	// linear://TEAM-789 or lin:TEAM-789 or linear:TEAM-789
	linearRe = regexp.MustCompile(`^(?:linear://|lin(?:ear)?:)([A-Z][A-Z0-9]+)-(\d+)$`)
	// file://path or ./path
	fileRe = regexp.MustCompile(`^(?:file://)?(\..*)$`)
)

// ParseIssueRef parses an issue reference string into a structured IssueRef.
// Supported formats:
//
//	github://org/repo#123, gh:org/repo#123, #123
//	jira://PROJECT-456, jira:PROJECT-456, PROJECT-456
//	linear://TEAM-789, lin:TEAM-789
//	file://./path, ./path
func ParseIssueRef(ref string) (*IssueRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("empty issue reference")
	}

	if m := githubFullRe.FindStringSubmatch(ref); m != nil {
		return &IssueRef{Tracker: "github", Org: m[1], Repo: m[2], ID: m[3]}, nil
	}
	if m := githubShortRe.FindStringSubmatch(ref); m != nil {
		return &IssueRef{Tracker: "github", ID: m[1]}, nil
	}
	if m := jiraRe.FindStringSubmatch(ref); m != nil {
		return &IssueRef{Tracker: "jira", Project: m[1], ID: m[2]}, nil
	}
	if m := linearRe.FindStringSubmatch(ref); m != nil {
		return &IssueRef{Tracker: "linear", Project: m[1], ID: m[2]}, nil
	}
	if m := fileRe.FindStringSubmatch(ref); m != nil {
		return &IssueRef{Tracker: "file", Path: m[1]}, nil
	}
	if m := jiraBareRe.FindStringSubmatch(ref); m != nil {
		return &IssueRef{Tracker: "jira", Project: m[1], ID: m[2]}, nil
	}

	return nil, fmt.Errorf("unrecognized issue reference format: %q", ref)
}
