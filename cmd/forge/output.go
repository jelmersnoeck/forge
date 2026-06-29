package main

import "strings"

// OutputBuffer owns the scrollable scrollback area: the rendered lines, the
// current scroll offset (lines up from the bottom), and whether new content
// should auto-scroll the viewport to the bottom.
type OutputBuffer struct {
	lines      []string
	offset     int
	autoScroll bool
}

// NewOutputBuffer returns a buffer that starts auto-scrolled to the bottom.
func NewOutputBuffer() *OutputBuffer {
	return &OutputBuffer{
		lines:      []string{},
		autoScroll: true,
	}
}

// Append adds lines to the buffer.
func (b *OutputBuffer) Append(lines ...string) {
	b.lines = append(b.lines, lines...)
}

// Lines returns the underlying slice (not a copy).
func (b *OutputBuffer) Lines() []string { return b.lines }

// SetLines replaces the underlying slice. Used by stream-replace logic that
// rewrites a trailing range of the buffer.
func (b *OutputBuffer) SetLines(lines []string) { b.lines = lines }

// Len returns the number of lines in the buffer.
func (b *OutputBuffer) Len() int { return len(b.lines) }

// Offset returns the current scroll offset (lines up from the bottom).
func (b *OutputBuffer) Offset() int { return b.offset }

// SetOffset sets the scroll offset directly. Callers are responsible for
// clamping; flushText uses this to keep the viewport stable on stream replace.
func (b *OutputBuffer) SetOffset(n int) { b.offset = n }

// AutoScroll reports whether the buffer auto-scrolls to the bottom on new content.
func (b *OutputBuffer) AutoScroll() bool { return b.autoScroll }

// ResetScrollIfAuto snaps the offset back to 0 when auto-scroll is enabled.
func (b *OutputBuffer) ResetScrollIfAuto() {
	if b.autoScroll {
		b.offset = 0
	}
}

// ScrollUp scrolls up by n lines, clamped to maxOffset, and disables auto-scroll.
// maxOffset is the largest valid offset for the current viewport height.
func (b *OutputBuffer) ScrollUp(n, maxOffset int) {
	if maxOffset > 0 && b.offset < maxOffset {
		b.offset += n
		if b.offset > maxOffset {
			b.offset = maxOffset
		}
		b.autoScroll = false
	}
}

// ScrollDown scrolls down by n lines, clamped to 0. When the offset reaches 0
// auto-scroll is re-enabled.
func (b *OutputBuffer) ScrollDown(n int) {
	if b.offset > 0 {
		b.offset -= n
		if b.offset < 0 {
			b.offset = 0
		}
		if b.offset == 0 {
			b.autoScroll = true
		}
	}
}

// View returns the bottom-anchored slice of lines that fits in height terminal
// rows, accounting for the current scroll offset. When the buffer fits entirely
// (or height is non-positive) all lines are joined.
func (b *OutputBuffer) View(height int) string {
	if len(b.lines) > height {
		endIdx := len(b.lines) - b.offset
		startIdx := endIdx - height
		if startIdx < 0 {
			startIdx = 0
		}
		return strings.Join(b.lines[startIdx:endIdx], "\n")
	}
	return strings.Join(b.lines, "\n")
}
