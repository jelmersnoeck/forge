package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrapText(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		text     string
		maxWidth int
		want     []string
	}{
		"empty string": {
			text:     "",
			maxWidth: 10,
			want:     []string{""},
		},
		"short text fits on one line": {
			text:     "hello",
			maxWidth: 10,
			want:     []string{"hello"},
		},
		"exact width": {
			text:     "helloworld",
			maxWidth: 10,
			want:     []string{"helloworld"},
		},
		"wrap at word boundary": {
			text:     "hello world foo bar",
			maxWidth: 15,
			want:     []string{"hello world", "foo bar"},
		},
		"wrap multiple lines": {
			text:     "this is a very long string that should wrap across multiple lines nicely",
			maxWidth: 20,
			want:     []string{"this is a very long", "string that should", "wrap across", "multiple lines", "nicely"},
		},
		"no spaces - hard break": {
			text:     "verylongwordwithoutanyspaces",
			maxWidth: 10,
			want:     []string{"verylongwo", "rdwithouta", "nyspaces"},
		},
		"mixed spaces and long words": {
			text:     "hello verylongwordhere foo",
			maxWidth: 10,
			want:     []string{"hello very", "longwordhe", "re foo"},
		},
		"zero width fallback": {
			text:     "hello world",
			maxWidth: 0,
			want:     []string{"hello world"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := wrapText(tc.text, tc.maxWidth)
			r.Equal(tc.want, got, "wrapText(%q, %d)", tc.text, tc.maxWidth)

			// Verify no line exceeds maxWidth (except when maxWidth is 0 or very small)
			if tc.maxWidth > 5 {
				for i, line := range got {
					r.LessOrEqual(len(line), tc.maxWidth, "line %d exceeds maxWidth: %q", i, line)
				}
			}

			// Verify joining the lines reconstructs the original (with normalized whitespace)
			// For text without spaces, we just verify all content is preserved
			joined := strings.Join(got, "")
			stripped := strings.ReplaceAll(joined, " ", "")
			originalStripped := strings.ReplaceAll(tc.text, " ", "")
			r.Equal(originalStripped, stripped, "content mismatch after wrap (stripped)")
		})
	}
}
