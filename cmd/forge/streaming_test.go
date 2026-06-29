package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// newTestHandler builds an EventHandler wired to fresh subsystems for testing
// the streaming/flush logic in isolation.
func newTestHandler() *EventHandler {
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	out := NewOutputBuffer()
	tasks := NewTaskTrackerSet()
	costAcc := &CostAccumulator{}
	return NewEventHandler(out, tasks, costAcc, nil, renderer, 80, "")
}

func makeTextEvent(content string) types.OutboundEvent {
	return types.OutboundEvent{
		Type:    "text",
		Content: content,
	}
}

func TestFlushRawText_CompleteLine(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// Simulate text event with a complete line
	h.textBuf = "Hello, Greendale!\n"
	h.streamBuf = "Hello, Greendale!\n"
	h.streamStartIdx = 0

	h.flushRawText()

	r.Equal([]string{"Hello, Greendale!"}, h.out.Lines())
	r.Equal("", h.textBuf, "textBuf should be empty after flushing complete line")
}

func TestFlushRawText_PartialLine(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// Simulate text event with no trailing newline (partial line)
	h.textBuf = "Troy and Abed"
	h.streamBuf = "Troy and Abed"
	h.streamStartIdx = 0

	h.flushRawText()

	r.Empty(h.out.Lines(), "partial line should not be added to output")
	r.Equal("Troy and Abed", h.textBuf, "partial line should remain in textBuf")
}

func TestFlushRawText_MultipleLines(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	h.textBuf = "line one\nline two\npartial"
	h.streamBuf = h.textBuf
	h.streamStartIdx = 0

	h.flushRawText()

	r.Equal([]string{"line one", "line two"}, h.out.Lines())
	r.Equal("partial", h.textBuf)
}

func TestFlushRawText_EmptyBuf(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	h.flushRawText()

	r.Empty(h.out.Lines())
	r.Equal("", h.textBuf)
}

func TestFlushText_EmptyStream(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	h.flushText()

	r.Empty(h.out.Lines())
	r.Equal(-1, h.streamStartIdx)
	r.Equal("", h.streamBuf)
}

func TestFlushText_ReplacesRawLines(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// Simulate: some pre-existing output, then raw streaming lines
	h.out.SetLines([]string{"[header]", "raw line 1", "raw line 2"})
	h.streamStartIdx = 1
	h.streamBuf = "**bold text**"

	h.flushText()

	// Raw lines should be replaced with glamour-rendered output
	lines := h.out.Lines()
	r.Equal("[header]", lines[0], "pre-existing output should be preserved")
	r.Greater(len(lines), 1, "should have rendered content")
	// The glamour output for **bold text** should contain the bold text
	joined := strings.Join(lines[1:], "\n")
	r.Contains(joined, "bold text", "rendered output should contain the text")
	r.Equal(-1, h.streamStartIdx, "stream should be reset")
	r.Equal("", h.streamBuf, "streamBuf should be cleared")
}

func TestFlushText_IncludesPartialLine(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// Simulate: partial line in textBuf, full text in streamBuf
	h.out.SetLines([]string{"raw line 1"})
	h.streamStartIdx = 0
	h.streamBuf = "Hello world"
	h.textBuf = "" // already flushed as raw

	h.flushText()

	joined := strings.Join(h.out.Lines(), "\n")
	r.Contains(joined, "Hello world")
	r.Equal(-1, h.streamStartIdx)
}

func TestFlushText_PRURLExtraction(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	h.out.SetLines([]string{"raw"})
	h.streamStartIdx = 0
	h.streamBuf = "PR: https://github.com/greendale/repo/pull/42"

	h.flushText()

	r.Equal("https://github.com/greendale/repo/pull/42", h.prURL)
}

func TestFlushText_ScrollOffsetAdjustment(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// Simulate: user scrolled up, then flush replaces lines
	h.out.SetLines([]string{"line0", "raw1", "raw2", "raw3"})
	h.streamStartIdx = 1
	h.streamBuf = "short"
	h.out.SetOffset(2)

	h.flushText()

	// 3 raw lines replaced with glamour output (likely fewer lines for "short")
	// scrollOffset should be adjusted
	r.GreaterOrEqual(h.out.Offset(), 0, "scrollOffset should not go negative")
}

func TestStreamingFlow_TextThenToolUse(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// Simulate a full streaming flow:
	// 1. Text events arrive and accumulate
	h.Handle(makeTextEvent("Hello "))
	h.Handle(makeTextEvent("world\n"))
	h.Handle(makeTextEvent("second line"))

	r.Equal("Hello world\nsecond line", h.streamBuf)
	r.Equal("Hello world\nsecond line", h.textBuf)
	r.Equal(0, h.streamStartIdx)

	// 2. Tick fires — raw text displayed
	h.flushRawText()
	r.Equal([]string{"Hello world"}, h.out.Lines())
	r.Equal("second line", h.textBuf)

	// 3. Tool use event — triggers flushText
	h.flushText()

	// Raw lines replaced with glamour output
	joined := strings.Join(h.out.Lines(), "\n")
	r.Contains(joined, "Hello world")
	r.Contains(joined, "second line")
	r.Equal(-1, h.streamStartIdx)
	r.Equal("", h.streamBuf)
}

func TestStreamingFlow_MultipleBlocks(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// First text block
	h.Handle(makeTextEvent("block one"))
	h.flushText()
	block1End := h.out.Len()

	// Second text block (after a tool_use)
	h.Handle(makeTextEvent("block two"))
	r.Equal(block1End, h.streamStartIdx, "new block should start after previous output")
	h.flushText()

	joined := strings.Join(h.out.Lines(), "\n")
	r.Contains(joined, "block one")
	r.Contains(joined, "block two")
}

func TestFlushText_NoTicksBefore(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// Text arrives but no tick fires before flushText
	h.Handle(makeTextEvent("Dean Pelton says **hello**"))

	r.Equal("Dean Pelton says **hello**", h.textBuf)
	r.Equal("Dean Pelton says **hello**", h.streamBuf)
	r.Equal(0, h.streamStartIdx)
	r.Empty(h.out.Lines(), "no tick means no raw lines in output yet")

	// Direct flushText without flushRawText
	h.flushText()

	r.Equal(-1, h.streamStartIdx)
	r.Equal("", h.streamBuf)
	r.Equal("", h.textBuf)

	joined := strings.Join(h.out.Lines(), "\n")
	r.Contains(joined, "Dean Pelton")
	r.Contains(joined, "hello")
}

func TestFlushText_NoStreamStartIdx(t *testing.T) {
	r := require.New(t)
	h := newTestHandler()

	// streamBuf has content but streamStartIdx was never set (shouldn't happen
	// in practice, but defensive). The fallback leaves raw lines in place.
	h.streamBuf = "some text"
	h.streamStartIdx = -1
	h.textBuf = ""

	h.flushText()

	r.Equal("", h.streamBuf)
	r.Equal(-1, h.streamStartIdx)
}
