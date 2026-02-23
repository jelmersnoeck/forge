package planner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jelmersnoeck/forge/internal/tracker"
)

// CreateIssues creates tracker issues for all items in a workstream plan.
// It sets dependencies via tracker.Link(), updates WorkstreamIssue.Ref with
// the created issue identifiers, and adds the "forge" label and workstream
// ID to each issue body.
func (p *Planner) CreateIssues(ctx context.Context, ws *Workstream) error {
	slog.Info("creating issues for workstream", "workstream_id", ws.ID, "tracker", ws.Tracker)

	// First pass: create all issues and collect ref mappings.
	// We create issues in phase order so dependencies (earlier phases) exist first.
	refMap := make(map[string]string) // title -> created ref

	for i := range ws.Phases {
		phase := &ws.Phases[i]
		for j := range phase.Issues {
			issue := &phase.Issues[j]

			body := formatIssueBody(ws, phase, issue)

			labels := append([]string{"forge"}, issue.Labels...)

			req := &tracker.CreateIssueRequest{
				Title:        issue.Title,
				Body:         body,
				Labels:       labels,
				Repo:         ws.Repo,
				WorkstreamID: ws.ID,
			}

			created, err := p.tracker.CreateIssue(ctx, req)
			if err != nil {
				return fmt.Errorf("creating issue %q: %w", issue.Title, err)
			}

			issue.Ref = created.ID
			refMap[issue.Title] = created.ID

			slog.Info("created issue",
				"title", issue.Title,
				"ref", created.ID,
				"url", created.URL,
			)
		}
	}

	// Second pass: create dependency links.
	for _, issue := range ws.AllIssues() {
		for _, depTitle := range issue.DependsOn {
			depRef, ok := refMap[depTitle]
			if !ok {
				slog.Warn("dependency not found in ref map",
					"issue", issue.Title,
					"depends_on", depTitle,
				)
				continue
			}

			if err := p.tracker.Link(ctx, issue.Ref, depRef, tracker.RelDependsOn); err != nil {
				slog.Warn("failed to create dependency link",
					"from", issue.Ref,
					"to", depRef,
					"error", err,
				)
				// Non-fatal: some trackers may not support linking.
			}
		}
	}

	slog.Info("issue creation complete",
		"workstream_id", ws.ID,
		"total_created", len(refMap),
	)

	return nil
}

// formatIssueBody builds the issue body with workstream metadata.
func formatIssueBody(ws *Workstream, phase *Phase, issue *WorkstreamIssue) string {
	var sb strings.Builder

	sb.WriteString(issue.Description)
	sb.WriteString("\n\n---\n")
	sb.WriteString(fmt.Sprintf("**Workstream:** `%s`\n", ws.ID))
	sb.WriteString(fmt.Sprintf("**Phase:** %s\n", phase.Name))

	if len(issue.DependsOn) > 0 {
		sb.WriteString(fmt.Sprintf("**Depends on:** %s\n", strings.Join(issue.DependsOn, ", ")))
	}

	sb.WriteString("\n_Created by [Forge](https://github.com/jelmersnoeck/forge)_\n")

	return sb.String()
}
