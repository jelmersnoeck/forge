package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/stretchr/testify/require"
)

func TestIsReviewCommand(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input string
		want  bool
	}{
		"exact match": {
			input: "/review",
			want:  true,
		},
		"with base flag": {
			input: "/review --base main",
			want:  true,
		},
		"with whitespace": {
			input: "  /review  ",
			want:  true,
		},
		"not a review command": {
			input: "review this code",
			want:  false,
		},
		"empty string": {
			input: "",
			want:  false,
		},
		"similar but not review": {
			input: "/reviews",
			want:  false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := isReviewCommand(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

func TestParseReviewBase(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input string
		want  string
	}{
		"no base flag": {
			input: "/review",
			want:  "",
		},
		"with base main": {
			input: "/review --base main",
			want:  "main",
		},
		"with base develop": {
			input: "/review --base develop",
			want:  "develop",
		},
		"base flag at end without value": {
			input: "/review --base",
			want:  "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := parseReviewBase(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

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

func TestSlashCommandNames(t *testing.T) {
	r := require.New(t)

	names := slashCommandNames()
	r.Contains(names, "/review", "should include /review")

	// All names start with /
	for _, name := range names {
		r.True(strings.HasPrefix(name, "/"), "command %q must start with /", name)
	}
}

func TestSlashCommandNamesExcludesHidden(t *testing.T) {
	r := require.New(t)

	names := slashCommandNames()
	for _, cmd := range slashCommands {
		if cmd.Hidden {
			r.NotContains(names, cmd.Name, "hidden command %q should not appear", cmd.Name)
		}
	}
}

func TestTrySlashComplete(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input     string
		wantDone  bool
		wantValue string
	}{
		"completes partial slash command": {
			input:     "/r",
			wantDone:  true,
			wantValue: "/review",
		},
		"completes single-char prefix": {
			input:     "/rev",
			wantDone:  true,
			wantValue: "/review",
		},
		"no completion for non-slash input": {
			input:     "hello",
			wantDone:  false,
			wantValue: "hello",
		},
		"no completion for empty input": {
			input:     "",
			wantDone:  false,
			wantValue: "",
		},
		"no completion for unknown command": {
			input:     "/xyz",
			wantDone:  false,
			wantValue: "/xyz",
		},
		"exact match still completes": {
			input:     "/review",
			wantDone:  true,
			wantValue: "/review",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ta := textarea.New()
			ta.SetValue(tc.input)
			m := &model{textArea: ta}
			done := m.trySlashComplete()
			r.Equal(tc.wantDone, done)
			r.Equal(tc.wantValue, m.textArea.Value())
		})
	}
}

func TestExtractPRURL(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		text string
		want string
	}{
		"PR URL in text": {
			text: "Draft PR created: https://github.com/jelmersnoeck/forge/pull/134\n\nBranch: jelmer/foo -> main",
			want: "https://github.com/jelmersnoeck/forge/pull/134",
		},
		"PR URL inline": {
			text: "Check out https://github.com/abed/dreamatorium/pull/7 for the timeline fix",
			want: "https://github.com/abed/dreamatorium/pull/7",
		},
		"no PR URL": {
			text: "Troy and Abed in the morning!",
			want: "",
		},
		"github URL but not a PR": {
			text: "See https://github.com/greendale/repo/issues/42",
			want: "",
		},
		"empty string": {
			text: "",
			want: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := extractPRURL(tc.text)
			r.Equal(tc.want, got)
		})
	}
}
