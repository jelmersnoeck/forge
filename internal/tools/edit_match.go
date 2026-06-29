package tools

import (
	"fmt"
	"strings"
)

// matchStrategy identifies which tier produced a match (for result messaging).
type matchStrategy int

const (
	matchExact matchStrategy = iota
	matchWhitespace
)

// findMatches returns the byte ranges in content matching old according to the
// first successful tier, the strategy used, and ok=false if no tier matched.
//
// Tiers are attempted in order and never mixed: if the exact tier finds at
// least one match, the whitespace tier is never consulted. When disableFuzzy
// is true only the exact tier runs.
func findMatches(content, old string, disableFuzzy bool) (ranges [][2]int, strat matchStrategy, ok bool) {
	if r := exactMatches(content, old); len(r) > 0 {
		return r, matchExact, true
	}
	if disableFuzzy {
		return nil, matchExact, false
	}
	if r := whitespaceMatches(content, old); len(r) > 0 {
		return r, matchWhitespace, true
	}
	return nil, matchExact, false
}

// exactMatches returns every non-overlapping byte range where old occurs
// verbatim in content.
func exactMatches(content, old string) [][2]int {
	if old == "" {
		return nil
	}
	var ranges [][2]int
	offset := 0
	for {
		idx := strings.Index(content[offset:], old)
		if idx < 0 {
			break
		}
		start := offset + idx
		end := start + len(old)
		ranges = append(ranges, [2]int{start, end})
		offset = end
	}
	return ranges
}

// lineSpan records the byte range and content of a single physical line in the
// source (excluding its trailing line break).
type lineSpan struct {
	start int // byte offset of first character
	end   int // byte offset just past the last character (before the break)
}

// splitLines breaks content into line spans plus the normalized form of each
// line. A line break is "\n" or "\r\n"; the break itself is not part of the
// span. A trailing break produces no extra empty line, matching strings.Split
// semantics on the content sans final newline only when one exists.
func splitLines(content string) (spans []lineSpan, normalized []string) {
	i := 0
	n := len(content)
	for i <= n {
		start := i
		for i < n && content[i] != '\n' {
			i++
		}
		end := i
		// Strip a trailing '\r' so CRLF and LF compare equal.
		lineEnd := end
		if lineEnd > start && content[lineEnd-1] == '\r' {
			lineEnd--
		}
		spans = append(spans, lineSpan{start: start, end: lineEnd})
		normalized = append(normalized, normalizeLine(content[start:lineEnd]))
		if i == n {
			break
		}
		i++ // skip the '\n'
	}
	return spans, normalized
}

// normalizeLine trims leading and trailing whitespace and collapses internal
// runs of spaces/tabs to a single space. This makes lines that differ only in
// indentation or internal whitespace compare equal.
func normalizeLine(line string) string {
	line = strings.TrimRight(line, " \t\r")
	line = strings.TrimLeft(line, " \t")
	var b strings.Builder
	prevSpace := false
	for _, r := range line {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}

// whitespaceMatches finds contiguous runs of file lines whose normalized form
// equals the normalized form of old's lines. Returns the byte range covering
// each matched block (from the first matched line's start to the last matched
// line's end, excluding the trailing line break).
func whitespaceMatches(content, old string) [][2]int {
	fileSpans, fileNorm := splitLines(content)
	_, oldNorm := splitLines(old)

	if len(oldNorm) == 0 || len(oldNorm) > len(fileNorm) {
		return nil
	}

	// Ambiguity guard: if every normalized line is empty, the block carries no
	// distinguishing content and would map onto arbitrary blank regions. Refuse
	// rather than guess (spec: never guess silently).
	if allEmpty(oldNorm) {
		return nil
	}

	var ranges [][2]int
	i := 0
	for i+len(oldNorm) <= len(fileNorm) {
		if !blockEqual(fileNorm[i:i+len(oldNorm)], oldNorm) {
			i++
			continue
		}
		start := fileSpans[i].start
		end := fileSpans[i+len(oldNorm)-1].end
		ranges = append(ranges, [2]int{start, end})
		i += len(oldNorm) // non-overlapping
	}
	return ranges
}

// allEmpty reports whether every normalized line in the block is empty.
func allEmpty(lines []string) bool {
	for _, l := range lines {
		if l != "" {
			return false
		}
	}
	return true
}

// blockEqual reports whether two slices of normalized lines are element-wise
// equal.
func blockEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// nearMissDiagnostic builds a capped unified-diff-style snippet comparing old
// against the most similar line-block in content. Used in not-found errors.
func nearMissDiagnostic(content, old string) string {
	if strings.TrimSpace(content) == "" {
		return "file is empty"
	}
	fileSpans, fileNorm := splitLines(content)
	oldLines := strings.Split(strings.ReplaceAll(old, "\r\n", "\n"), "\n")
	_, oldNorm := splitLines(old)

	window := len(oldNorm)
	if window == 0 {
		return ""
	}

	bestStart := 0
	bestScore := -1.0
	limit := len(fileNorm) - window
	if limit < 0 {
		limit = 0
	}
	for i := 0; i <= limit; i++ {
		end := i + window
		if end > len(fileNorm) {
			end = len(fileNorm)
		}
		score := blockSimilarity(fileNorm[i:end], oldNorm)
		if score > bestScore {
			bestScore = score
			bestStart = i
		}
	}

	end := bestStart + window
	if end > len(fileSpans) {
		end = len(fileSpans)
	}
	var foundLines []string
	for i := bestStart; i < end; i++ {
		s := fileSpans[i]
		foundLines = append(foundLines, content[s.start:s.end])
	}

	return formatDiff(oldLines, foundLines)
}

// blockSimilarity scores two equal-length-ish normalized line blocks by the
// fraction of positionally matching lines.
func blockSimilarity(a, b []string) float64 {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 0
	}
	matches := 0
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			matches++
		}
	}
	return float64(matches) / float64(maxLen)
}

// formatDiff renders a unified-diff-style snippet of expected vs found, capped
// at ~40 lines total.
func formatDiff(expected, found []string) string {
	const cap = 40
	var b strings.Builder
	b.WriteString("closest match found (- expected, + file):\n")
	written := 0
	for _, line := range expected {
		if written >= cap {
			break
		}
		fmt.Fprintf(&b, "- %s\n", line)
		written++
	}
	for _, line := range found {
		if written >= cap {
			break
		}
		fmt.Fprintf(&b, "+ %s\n", line)
		written++
	}
	return strings.TrimRight(b.String(), "\n")
}
