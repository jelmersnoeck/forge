package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

// prMonitorInterval is how often the monitor checks PR health.
const prMonitorInterval = 5 * time.Minute

// PRInfo holds the current state of a PR for the working branch.
type PRInfo struct {
	Number       int
	URL          string
	Branch       string
	Base         string
	State        string // "OPEN", "CLOSED", "MERGED"
	ChecksOK     bool
	NeedsRebase  bool
	FailedChecks []string
}

// prCheckSource is the sentinel Source value for internal PR health check
// messages. The worker's main loop uses this to distinguish PR checks from
// real user messages, ensuring git operations are serialized.
const prCheckSource = "pr_check_internal"

// prHealthMonitor periodically enqueues PR health checks into the hub.
// The actual check runs in the worker's main loop (via handlePRCheck),
// guaranteeing no concurrent git access with conversation turns.
//
// Flow: monitor goroutine enqueues a synthetic message every 5 min →
// hub queue → worker pulls it → handlePRCheck runs git ops inline.
func (w *Worker) prHealthMonitor(ctx context.Context) {
	// Verify gh is available before starting the loop.
	if _, err := exec.LookPath("gh"); err != nil {
		log.Printf("[agent:%s] pr-monitor: gh CLI not found, disabling PR health monitor", w.sessionID)
		w.hub.PublishEvent(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: w.sessionID,
			Type:      "pr_monitor",
			Content:   "PR health monitor disabled: gh CLI not found on PATH.",
			Timestamp: time.Now().UnixMilli(),
		})
		return
	}

	// Enqueue the first check immediately so rebase/CI issues surface at
	// startup, not after a 5-minute wait.
	w.enqueuePRCheck()

	ticker := time.NewTicker(prMonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.enqueuePRCheck()
		}
	}
}

// enqueuePRCheck pushes a synthetic message into the hub that triggers a
// PR health check when the worker processes it. Skips enqueue if a check
// message is already pending (backpressure).
func (w *Worker) enqueuePRCheck() {
	if !w.prCheckPending.CompareAndSwap(false, true) {
		log.Printf("[agent:%s] pr-monitor: skipping enqueue, previous check still pending", w.sessionID)
		return
	}
	log.Printf("[agent:%s] pr-monitor: enqueuing PR health check", w.sessionID)
	w.hub.PushMessage(types.InboundMessage{
		Source:    prCheckSource,
		Text:      "", // not user-visible
		Timestamp: time.Now().UnixMilli(),
	})
}

// handlePRCheck runs a PR health check inline in the worker's main loop.
// Returns true if the PR is in a terminal state (merged/closed).
// Because this runs in the worker loop (not a background goroutine),
// it is serialized with all other message processing — no TOCTOU race.
func (w *Worker) handlePRCheck(ctx context.Context) bool {
	// Clear the pending flag so the monitor goroutine can enqueue again.
	w.prCheckPending.Store(false)

	needsFix, fixMsg, terminal := w.prHealthCheck(ctx)
	if needsFix && fixMsg != "" {
		w.hub.PushMessage(types.InboundMessage{
			SessionID: w.sessionID,
			Text:      fixMsg,
			User:      "pr-monitor",
			Source:    "pr_monitor",
			Timestamp: time.Now().UnixMilli(),
		})
	}
	return terminal
}

// prHealthCheck performs a single health check cycle.
// Returns whether the agent should investigate, a message to send, and whether
// the PR is in a terminal state (merged/closed).
func (w *Worker) prHealthCheck(ctx context.Context) (needsFix bool, fixMsg string, terminal bool) {
	emit := func(event types.OutboundEvent) {
		if event.ID == "" {
			event.ID = uuid.New().String()
		}
		if event.SessionID == "" {
			event.SessionID = w.sessionID
		}
		if event.Timestamp == 0 {
			event.Timestamp = time.Now().UnixMilli()
		}
		w.hub.PublishEvent(event)
	}

	info, err := getPRInfo(w.cwd)
	if err != nil {
		// No PR or network error — skip silently.
		log.Printf("[agent:%s] pr-monitor: %v", w.sessionID, err)
		return false, "", false
	}

	switch info.State {
	case "MERGED":
		emit(types.OutboundEvent{
			Type:    "pr_monitor",
			Content: fmt.Sprintf("PR #%d has been merged.", info.Number),
		})
		return false, "", true
	case "CLOSED":
		emit(types.OutboundEvent{
			Type:    "pr_monitor",
			Content: fmt.Sprintf("PR #%d has been closed.", info.Number),
		})
		return false, "", true
	}

	// Notify the CLI about the PR URL so it can display it in the status bar.
	if info.URL != "" {
		emit(types.OutboundEvent{
			Type:    "pr_url",
			Content: info.URL,
		})
	}

	// Check if rebase is needed.
	if info.NeedsRebase {
		emit(types.OutboundEvent{
			Type:    "pr_monitor",
			Content: fmt.Sprintf("PR #%d: base branch %s has advanced, needs rebase.", info.Number, info.Base),
		})

		msg := fmt.Sprintf(
			"[PR Health Monitor] PR #%d needs to be rebased onto %s. "+
				"The base branch has new commits that aren't in our branch.\n\n"+
				"Please run the rebase:\n"+
				"1. Run `git fetch origin %s && git rebase origin/%s`\n"+
				"2. If there are conflicts, resolve them (examine the conflict markers, fix each file, `git add` the resolved files, then `git rebase --continue`)\n"+
				"3. Once the rebase is complete, force-push with `git push --force-with-lease origin HEAD`\n"+
				"4. Run the tests to make sure nothing broke",
			info.Number, info.Base, info.Base, info.Base,
		)
		return true, msg, false
	}

	// Check CI status.
	if !info.ChecksOK && len(info.FailedChecks) > 0 {
		failures := strings.Join(info.FailedChecks, "\n  - ")
		emit(types.OutboundEvent{
			Type:    "pr_monitor",
			Content: fmt.Sprintf("PR #%d: CI checks failing:\n  - %s", info.Number, failures),
		})

		msg := fmt.Sprintf(
			"[PR Health Monitor] CI checks are failing on PR #%d. "+
				"Please investigate and fix the following failures:\n\n"+
				"  - %s\n\n"+
				"Look at the test output, identify root causes, and fix them. "+
				"Run the relevant tests locally to verify your fixes before pushing.",
			info.Number, failures,
		)
		return true, msg, false
	}

	log.Printf("[agent:%s] pr-monitor: PR #%d is healthy", w.sessionID, info.Number)
	return false, "", false
}

// getPRInfo queries GitHub for the current branch's PR status.
func getPRInfo(cwd string) (*PRInfo, error) {
	// Get current branch.
	branch, err := tools.GitOutput(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("get current branch: %w", err)
	}

	// Query PR for this branch via gh.
	out, err := tools.GHOutput(cwd, "pr", "view", "--json",
		"number,url,headRefName,baseRefName,state,statusCheckRollup")
	if err != nil {
		return nil, fmt.Errorf("no PR found for branch %s", branch)
	}

	var pr struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
		State       string `json:"state"`
		Checks      []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		return nil, fmt.Errorf("parse PR JSON: %w", err)
	}

	info := &PRInfo{
		Number:   pr.Number,
		URL:      pr.URL,
		Branch:   pr.HeadRefName,
		Base:     pr.BaseRefName,
		State:    pr.State,
		ChecksOK: true,
	}

	// Check if base branch has advanced past our merge base.
	if info.State == "OPEN" {
		info.NeedsRebase = checkNeedsRebase(cwd, info.Base)
	}

	// Evaluate check status.
	for _, check := range pr.Checks {
		switch {
		case check.Status == "COMPLETED" && check.Conclusion == "FAILURE":
			info.ChecksOK = false
			info.FailedChecks = append(info.FailedChecks, check.Name)
		case check.Status == "COMPLETED" && check.Conclusion == "TIMED_OUT":
			info.ChecksOK = false
			info.FailedChecks = append(info.FailedChecks, check.Name+" (timed out)")
		}
		// PENDING/IN_PROGRESS/QUEUED — don't treat as failure.
	}

	return info, nil
}

// checkNeedsRebase returns true if origin/<base> has commits not in HEAD.
func checkNeedsRebase(cwd, base string) bool {
	// Fetch latest base.
	if _, _, err := tools.GitOutputFull(cwd, "fetch", "origin", base); err != nil {
		return false
	}

	// Count commits on origin/<base> not reachable from HEAD.
	count, err := tools.GitOutput(cwd, "rev-list", "--count", "HEAD..origin/"+base)
	if err != nil {
		return false
	}
	return count != "0"
}
