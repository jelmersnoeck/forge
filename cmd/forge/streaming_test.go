package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func newTestModel() *model {
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	return &model{
		output:         []string{},
		renderer:       renderer,
		streamStartIdx: -1,
		width:          80,
		taskTrackers:   make(map[string]*taskTracker),
	}
}

func makeTextEvent(content string) types.OutboundEvent {
	return types.OutboundEvent{
		Type:    "text",
		Content: content,
	}
}

func TestFlushRawText_CompleteLine(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	// Simulate text event with a complete line
	m.textBuf = "Hello, Greendale!\n"
	m.streamBuf = "Hello, Greendale!\n"
	m.streamStartIdx = 0

	m.flushRawText()

	r.Equal([]string{"Hello, Greendale!"}, m.output)
	r.Equal("", m.textBuf, "textBuf should be empty after flushing complete line")
}

func TestFlushRawText_PartialLine(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	// Simulate text event with no trailing newline (partial line)
	m.textBuf = "Troy and Abed"
	m.streamBuf = "Troy and Abed"
	m.streamStartIdx = 0

	m.flushRawText()

	r.Empty(m.output, "partial line should not be added to output")
	r.Equal("Troy and Abed", m.textBuf, "partial line should remain in textBuf")
}

func TestFlushRawText_MultipleLines(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	m.textBuf = "line one\nline two\npartial"
	m.streamBuf = m.textBuf
	m.streamStartIdx = 0

	m.flushRawText()

	r.Equal([]string{"line one", "line two"}, m.output)
	r.Equal("partial", m.textBuf)
}

func TestFlushRawText_EmptyBuf(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	m.flushRawText()

	r.Empty(m.output)
	r.Equal("", m.textBuf)
}

func TestFlushText_EmptyStream(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	m.flushText()

	r.Empty(m.output)
	r.Equal(-1, m.streamStartIdx)
	r.Equal("", m.streamBuf)
}

func TestFlushText_ReplacesRawLines(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	// Simulate: some pre-existing output, then raw streaming lines
	m.output = []string{"[header]", "raw line 1", "raw line 2"}
	m.streamStartIdx = 1
	m.streamBuf = "**bold text**"

	m.flushText()

	// Raw lines should be replaced with glamour-rendered output
	r.Equal("[header]", m.output[0], "pre-existing output should be preserved")
	r.Greater(len(m.output), 1, "should have rendered content")
	// The glamour output for **bold text** should contain the bold text
	joined := strings.Join(m.output[1:], "\n")
	r.Contains(joined, "bold text", "rendered output should contain the text")
	r.Equal(-1, m.streamStartIdx, "stream should be reset")
	r.Equal("", m.streamBuf, "streamBuf should be cleared")
}

func TestFlushText_IncludesPartialLine(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	// Simulate: partial line in textBuf, full text in streamBuf
	m.output = []string{"raw line 1"}
	m.streamStartIdx = 0
	m.streamBuf = "Hello world"
	m.textBuf = "" // already flushed as raw

	m.flushText()

	joined := strings.Join(m.output, "\n")
	r.Contains(joined, "Hello world")
	r.Equal(-1, m.streamStartIdx)
}

func TestFlushText_PRURLExtraction(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	m.output = []string{"raw"}
	m.streamStartIdx = 0
	m.streamBuf = "PR: https://github.com/greendale/repo/pull/42"

	m.flushText()

	r.Equal("https://github.com/greendale/repo/pull/42", m.prURL)
}

func TestFlushText_ScrollOffsetAdjustment(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	// Simulate: user scrolled up, then flush replaces lines
	m.output = []string{"line0", "raw1", "raw2", "raw3"}
	m.streamStartIdx = 1
	m.streamBuf = "short"
	m.scrollOffset = 2

	m.flushText()

	// 3 raw lines replaced with glamour output (likely fewer lines for "short")
	// scrollOffset should be adjusted
	r.GreaterOrEqual(m.scrollOffset, 0, "scrollOffset should not go negative")
}

func TestStreamingFlow_TextThenToolUse(t *testing.T) {
	r := require.New(t)
	m := newTestModel()
	m.width = 80

	// Simulate a full streaming flow:
	// 1. Text events arrive and accumulate
	m.handleEvent(makeTextEvent("Hello "))
	m.handleEvent(makeTextEvent("world\n"))
	m.handleEvent(makeTextEvent("second line"))

	r.Equal("Hello world\nsecond line", m.streamBuf)
	r.Equal("Hello world\nsecond line", m.textBuf)
	r.Equal(0, m.streamStartIdx)

	// 2. Tick fires — raw text displayed
	m.flushRawText()
	r.Equal([]string{"Hello world"}, m.output)
	r.Equal("second line", m.textBuf)

	// 3. Tool use event — triggers flushText
	m.flushText()

	// Raw lines replaced with glamour output
	joined := strings.Join(m.output, "\n")
	r.Contains(joined, "Hello world")
	r.Contains(joined, "second line")
	r.Equal(-1, m.streamStartIdx)
	r.Equal("", m.streamBuf)
}

func TestStreamingFlow_MultipleBlocks(t *testing.T) {
	r := require.New(t)
	m := newTestModel()
	m.width = 80

	// First text block
	m.handleEvent(makeTextEvent("block one"))
	m.flushText()
	block1End := len(m.output)

	// Second text block (after a tool_use)
	m.handleEvent(makeTextEvent("block two"))
	r.Equal(block1End, m.streamStartIdx, "new block should start after previous output")
	m.flushText()

	joined := strings.Join(m.output, "\n")
	r.Contains(joined, "block one")
	r.Contains(joined, "block two")
}

func TestFlushText_NoTicksBefore(t *testing.T) {
	r := require.New(t)
	m := newTestModel()
	m.width = 80

	// Text arrives but no tick fires before flushText
	m.handleEvent(makeTextEvent("Dean Pelton says **hello**"))

	r.Equal("Dean Pelton says **hello**", m.textBuf)
	r.Equal("Dean Pelton says **hello**", m.streamBuf)
	r.Equal(0, m.streamStartIdx)
	r.Empty(m.output, "no tick means no raw lines in output yet")

	// Direct flushText without flushRawText
	m.flushText()

	r.Equal(-1, m.streamStartIdx)
	r.Equal("", m.streamBuf)
	r.Equal("", m.textBuf)

	joined := strings.Join(m.output, "\n")
	r.Contains(joined, "Dean Pelton")
	r.Contains(joined, "hello")
}

func TestFlushText_NoStreamStartIdx(t *testing.T) {
	r := require.New(t)
	m := newTestModel()

	// streamBuf has content but streamStartIdx was never set (shouldn't happen
	// in practice, but defensive). The fallback leaves raw lines in place.
	m.streamBuf = "some text"
	m.streamStartIdx = -1
	m.textBuf = ""

	m.flushText()

	r.Equal("", m.streamBuf)
	r.Equal(-1, m.streamStartIdx)
}
