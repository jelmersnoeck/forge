# Learnings - 2026-06-28 15:39

- GitHub sub-issues REST API requires the actual issue `id` (integer, e.g. 4763842172), not the issue `number` (e.g. 221). The `sub_issue_id` field must be an integer, not a string — using `-f sub_issue_id=221` sends a string and gets rejected. Use `--input -` with JSON to send the correct type: `gh api repos/OWNER/REPO/issues/NUMBER/sub_issues -X POST --input - <<< '{"sub_issue_id": 4763842172}'`
- To get a GitHub issue's numeric ID (not issue number) for the sub-issues API: `gh api repos/OWNER/REPO/issues/NUMBER --jq '.id'`
- GitHub sub-issues API endpoints: GET `/repos/{owner}/{repo}/issues/{number}/sub_issues` to list, POST with `{"sub_issue_id": <id>}` to add. The POST response returns the parent issue object, not the sub-issue.
