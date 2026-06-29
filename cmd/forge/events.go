package main

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/jelmersnoeck/forge/internal/runtime/cost"
	"github.com/jelmersnoeck/forge/internal/types"
)

// EventResult carries state derived from handling an event that the model
// applies after the handler runs.
type EventResult struct {
	ModelName string // non-empty if a model/usage event set it
	PRURL     string // non-empty if a PR URL was detected/emitted
}

// EventHandler turns inbound OutboundEvents into mutations of the output buffer,
// task trackers, and cost accumulator. It also owns the streaming-text state
// (raw partial lines + the accumulated block for glamour rendering) because the
// flush logic must read and adjust the OutputBuffer's scroll offset in place.
type EventHandler struct {
	out       *OutputBuffer
	tasks     *TaskTrackerSet
	cost      *CostAccumulator
	track     *cost.Tracker
	rend      *glamour.TermRenderer
	width     int
	sessionID string
	prURL     string

	textBuf        string // not-yet-displayed raw text (partial line between ticks)
	streamBuf      string // full accumulated text block for glamour rendering at flush
	streamStartIdx int    // index in output where raw streaming lines began (-1 = inactive)
}

// NewEventHandler wires an EventHandler to the shared subsystems.
func NewEventHandler(out *OutputBuffer, tasks *TaskTrackerSet, costAcc *CostAccumulator, track *cost.Tracker, rend *glamour.TermRenderer, width int, sessionID string) *EventHandler {
	return &EventHandler{
		out:            out,
		tasks:          tasks,
		cost:           costAcc,
		track:          track,
		rend:           rend,
		width:          width,
		sessionID:      sessionID,
		streamStartIdx: -1,
	}
}

// SetWidth updates the wrapping width.
func (h *EventHandler) SetWidth(w int) { h.width = w }

// SetRenderer swaps the glamour renderer (after a window resize).
func (h *EventHandler) SetRenderer(r *glamour.TermRenderer) { h.rend = r }

// HasPendingText reports whether there is an unflushed partial line.
func (h *EventHandler) HasPendingText() bool { return h.textBuf != "" }

// Streaming reports whether a text block is mid-stream (affects the "working"
// indicator: no indicator while text is actively streaming).
func (h *EventHandler) Streaming() bool { return h.streamBuf != "" }

// PRURL returns the detected PR URL, if any.
func (h *EventHandler) PRURL() string { return h.prURL }

// Handle processes a single event and returns derived state for the model.
func (h *EventHandler) Handle(event types.OutboundEvent) EventResult {
	switch event.Type {
	case "model":
		h.cost.SetModel(event.Content)

	case "text":
		// Mark where raw streaming lines begin in output (first text event per block).
		if h.streamStartIdx == -1 {
			h.streamStartIdx = h.out.Len()
		}
		h.streamBuf += event.Content
		h.textBuf += event.Content

	case "tool_use":
		h.flushText()
		// Suppress repeated TaskGet/AgentGet lines — the inline task
		// progress display handles these via task_status events.
		switch event.ToolName {
		case "TaskGet", "AgentGet", "TaskOutput":
			// Don't render a line; the task_status event updates the tracker.
		default:
			if event.Content != "" {
				// Wrap long tool content to terminal width
				maxWidth := h.width - 10 // account for prefix and margins
				if maxWidth < 40 {
					maxWidth = 40
				}
				wrapped := wrapText(event.Content, maxWidth)
				prefix := toolStyle.Render("  ["+event.ToolName+"]") + " "
				for i, line := range wrapped {
					if i == 0 {
						h.out.Append(prefix + dimStyle.Render(line))
					} else {
						// Indent continuation lines
						h.out.Append("    " + dimStyle.Render(line))
					}
				}
			} else {
				h.out.Append(toolStyle.Render("  [" + event.ToolName + "]"))
			}
		}

	case "tool_progress":
		// Progress is surfaced via the model's toolProgress field; no output
		// mutation here. The model reads event.Content directly.

	case "queued_task_result":
		h.flushText()
		maxWidth := h.width - 14 // account for prefix
		if maxWidth < 40 {
			maxWidth = 40
		}
		wrapped := wrapText(event.Content, maxWidth)
		for i, line := range wrapped {
			if i == 0 {
				h.out.Append(queueStyle.Render("  [queued] ") + dimStyle.Render(line))
			} else {
				h.out.Append("            " + dimStyle.Render(line))
			}
		}

	case "queued_task_error":
		h.flushText()
		maxWidth := h.width - 20 // account for prefix
		if maxWidth < 40 {
			maxWidth = 40
		}
		wrapped := wrapText(event.Content, maxWidth)
		for i, line := range wrapped {
			if i == 0 {
				h.out.Append(errorStyle.Render("  [queued error] ") + line)
			} else {
				h.out.Append("                  " + line)
			}
		}

	case "queue_immediate":
		h.flushText()
		maxWidth := h.width - 24 // account for prefix
		if maxWidth < 40 {
			maxWidth = 40
		}
		wrapped := wrapText(event.Content, maxWidth)
		for i, line := range wrapped {
			if i == 0 {
				h.out.Append(queueStyle.Render("  ⏱  Queued immediate: ") + dimStyle.Render(line))
			} else {
				h.out.Append("                        " + dimStyle.Render(line))
			}
		}

	case "queue_on_complete":
		h.flushText()
		maxWidth := h.width - 27 // account for prefix
		if maxWidth < 40 {
			maxWidth = 40
		}
		wrapped := wrapText(event.Content, maxWidth)
		for i, line := range wrapped {
			if i == 0 {
				h.out.Append(queueStyle.Render("  ⏱  Queued on complete: ") + dimStyle.Render(line))
			} else {
				h.out.Append("                           " + dimStyle.Render(line))
			}
		}

	case "steering":
		h.flushText()
		h.out.Append(dimStyle.Render("  [steering message injected]"))

	case "usage":
		if warning := h.cost.Record(event, h.track, h.sessionID); warning != "" {
			h.out.Append(dimStyle.Render(warning))
		}

	case "error":
		h.flushText()
		maxWidth := h.width - 8 // account for "error: "
		if maxWidth < 40 {
			maxWidth = 40
		}
		wrapped := wrapText(event.Content, maxWidth)
		for i, line := range wrapped {
			if i == 0 {
				h.out.Append(errorStyle.Render("error: ") + line)
			} else {
				h.out.Append("       " + line)
			}
		}

	case "interrupted":
		h.flushText()
		h.out.Append(dimStyle.Render("interrupted by user"))

	case "warning":
		h.flushText()
		maxWidth := h.width - 11 // account for "warning: "
		if maxWidth < 40 {
			maxWidth = 40
		}
		wrapped := wrapText(event.Content, maxWidth)
		for i, line := range wrapped {
			if i == 0 {
				h.out.Append(errorStyle.Render("warning: ") + line)
			} else {
				h.out.Append("         " + line)
			}
		}

	case "done":
		h.flushText()
		// Finalize any remaining task trackers (agent done, no more polling).
		h.out.Append(h.tasks.FinalizeAll()...)
		h.out.Append("")

	case "phase_start":
		h.flushText()
		h.out.Append("")
		h.out.Append(headerStyle.Render("  [phase] ") + event.Content + " starting")

	case "phase_complete":
		h.flushText()
		h.out.Append(dimStyle.Render("  [phase] ") + event.Content)
		h.out.Append("")

	case "phase_handoff":
		h.flushText()
		h.out.Append(dimStyle.Render("  [phase] ") + event.Content)

	case "intent_classified":
		h.flushText()
		switch parseClassifiedIntent(event.Content) {
		case "question":
			h.out.Append(dimStyle.Render("  answering question..."))
		case "investigate":
			h.out.Append(dimStyle.Render("  investigating..."))
		case "triage":
			h.out.Append(dimStyle.Render("  triaging — investigating and filing an issue..."))
		}
		// "task" is silent — the phase_start events provide the display.

	case "classification_error":
		h.flushText()
		h.out.Append(dimStyle.Render("  " + event.Content))

	case "ideation_start":
		h.flushText()
		h.out.Append(headerStyle.Render("  [ideation] ") + event.Content)

	case "ideation_candidate":
		// Quiet — candidates are internal to the pipeline

	case "clarification_start":
		h.flushText()
		h.out.Append(dimStyle.Render("  [clarify] ") + event.Content)

	case "clarification_question":
		h.flushText()
		h.out.Append(dimStyle.Render("  [question] ") + event.Content)

	case "planning_start":
		h.flushText()
		h.out.Append(dimStyle.Render("  [planning] ") + event.Content)

	case "planning_selection":
		h.flushText()
		h.out.Append(headerStyle.Render("  [plan] ") + event.Content)

	case "staleness_warning":
		h.flushText()
		h.out.Append(dimStyle.Render("  [staleness] ") + event.Content)

	case "staleness_error":
		h.flushText()
		h.out.Append(errorStyle.Render("  [staleness] ") + event.Content)

	case "review_start":
		h.flushText()
		h.out.Append(headerStyle.Render("  [review] ") + event.Content)

	case "review_finding":
		h.flushText()
		h.out.Append(formatReviewFinding(event.Content, h.width))

	case "review_agent_done":
		// Quiet — individual agent completions don't need display

	case "review_provider_summary":
		h.flushText()
		h.out.Append("")
		for _, line := range strings.Split(event.Content, "\n") {
			h.out.Append("  " + dimStyle.Render(line))
		}

	case "review_summary":
		h.flushText()
		h.out.Append("")
		h.out.Append(headerStyle.Render("  Review Summary"))
		for _, line := range strings.Split(event.Content, "\n") {
			h.out.Append("  " + line)
		}
		h.out.Append("")

	case "review_error":
		h.flushText()
		h.out.Append(errorStyle.Render("  [review error] ") + event.Content)

	case "pr_url":
		h.prURL = event.Content

	case "pr_monitor":
		h.flushText()
		h.out.Append(dimStyle.Render("  [pr] ") + event.Content)

	case "task_status":
		if summary, _ := h.tasks.HandleStatus(event.Content); summary != "" {
			h.out.Append(summary)
		}
	}

	_, modelName := h.cost.Summary()
	return EventResult{ModelName: modelName, PRURL: h.prURL}
}

// flushRawText appends new raw text lines to output during streaming.
// Holds incomplete lines (no trailing newline) in textBuf for the next tick.
func (h *EventHandler) flushRawText() {
	text := h.textBuf
	if text == "" {
		return
	}

	// Split into lines; keep partial last line in textBuf
	lines := strings.Split(text, "\n")
	if len(lines) > 0 {
		// Last element is either "" (text ended with \n) or a partial line
		h.textBuf = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}

	h.out.Append(lines...)
}

// flushText renders the entire accumulated text block through glamour,
// replacing the raw streaming lines in output with the rendered result.
func (h *EventHandler) flushText() {
	// Flush any remaining partial line first
	if h.textBuf != "" {
		h.out.Append(h.textBuf)
		h.textBuf = ""
	}

	text := h.streamBuf
	startIdx := h.streamStartIdx

	// Reset streaming state
	h.streamBuf = ""
	h.streamStartIdx = -1

	if text == "" {
		return
	}

	// Sniff for GitHub PR URLs if we don't already have one.
	if h.prURL == "" {
		h.prURL = extractPRURL(text)
	}

	rendered, err := h.rend.Render(text)
	if err != nil || startIdx < 0 {
		// Glamour failed or no stream start recorded — raw lines are already
		// in output from flushRawText, so just leave them.
		return
	}

	// Replace raw streaming lines with glamour-rendered result.
	renderedLines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")

	// Calculate line count change for scroll offset adjustment.
	rawLineCount := h.out.Len() - startIdx
	newLineCount := len(renderedLines)

	// Replace: keep output[:startIdx], append rendered lines
	h.out.SetLines(append(h.out.Lines()[:startIdx], renderedLines...))

	// Adjust scroll offset so viewport stays stable if user scrolled up.
	if offset := h.out.Offset(); offset > 0 {
		delta := newLineCount - rawLineCount
		offset += delta
		if offset < 0 {
			offset = 0
		}
		h.out.SetOffset(offset)
	}
}

// parseClassifiedIntent extracts the intent from an intent_classified event's
// Content. The orchestrator emits a JSON object
// {"intent":"...","size":"...","spec_match":"..."}; older/direct callers may
// emit the bare intent string. This handles both: JSON is parsed for its
// intent field, otherwise the trimmed raw content is returned.
func parseClassifiedIntent(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") {
		var obj struct {
			Intent string `json:"intent"`
		}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			return obj.Intent
		}
	}
	return trimmed
}
