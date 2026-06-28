package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/jelmersnoeck/forge/internal/types"
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

func TestIsModelCommand(t *testing.T) {
	tests := map[string]struct {
		input string
		want  bool
	}{
		"exact match": {
			input: "/model", want: true,
		},
		"with argument": {
			input: "/model sonnet", want: true,
		},
		"with whitespace": {
			input: "  /model opus  ", want: true,
		},
		"not a model command": {
			input: "what model are you?", want: false,
		},
		"empty string": {
			input: "", want: false,
		},
		"similar but not model": {
			input: "/models", want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, isModelCommand(tc.input))
		})
	}
}

func TestParseModelArg(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"no argument": {
			input: "/model", want: "",
		},
		"sonnet": {
			input: "/model sonnet", want: "sonnet",
		},
		"full model ID": {
			input: "/model claude-sonnet-4-20250514", want: "claude-sonnet-4-20250514",
		},
		"with extra whitespace": {
			input: "  /model  opus  ", want: "opus",
		},
		"list subcommand": {
			input: "/model list", want: "list",
		},
		"list with whitespace": {
			input: "  /model  list  ", want: "list",
		},
		"global flag stripped after name": {
			input: "/model opus --global", want: "opus",
		},
		"global flag stripped before name": {
			input: "/model --global opus", want: "opus",
		},
		"global flag alone": {
			input: "/model --global", want: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, parseModelArg(tc.input))
		})
	}
}

func TestParseModelGlobal(t *testing.T) {
	tests := map[string]struct {
		input string
		want  bool
	}{
		"no flag":             {input: "/model opus", want: false},
		"flag after name":     {input: "/model opus --global", want: true},
		"flag before name":    {input: "/model --global opus", want: true},
		"flag alone":          {input: "/model --global", want: true},
		"bare model":          {input: "/model", want: false},
		"not the global flag": {input: "/model --globalish", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, parseModelGlobal(tc.input))
		})
	}
}

func TestShortModelName(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"full sonnet": {
			input: "claude-sonnet-4-20250514", want: "sonnet-4",
		},
		"full opus": {
			input: "claude-opus-4-6", want: "opus-4-6",
		},
		"full haiku": {
			input: "claude-haiku-4-20250506", want: "haiku-4",
		},
		"short alias": {
			input: "sonnet", want: "sonnet",
		},
		"no claude prefix": {
			input: "gpt-4", want: "gpt-4",
		},
		"empty": {
			input: "", want: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, shortModelName(tc.input))
		})
	}
}

func TestRenderModelList(t *testing.T) {
	tests := map[string]struct {
		input []types.ProviderModels
		check func(r *require.Assertions, lines []string)
	}{
		"empty providers": {
			input: nil,
			check: func(r *require.Assertions, lines []string) {
				r.Len(lines, 1)
				r.Contains(lines[0], "No providers configured")
				r.Contains(lines[0], "ANTHROPIC_API_KEY")
			},
		},
		"anthropic models": {
			input: []types.ProviderModels{{
				Provider: "Anthropic",
				Models: []types.ModelEntry{
					{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4"},
					{ID: "claude-sonnet-4-20250514", DisplayName: "Claude Sonnet 4"},
				},
			}},
			check: func(r *require.Assertions, lines []string) {
				joined := strings.Join(lines, "\n")
				r.Contains(joined, "Available models:")
				r.Contains(joined, "Anthropic")
				r.Contains(joined, "claude-opus-4-6")
				r.Contains(joined, "Claude Opus 4")
				r.Contains(joined, "claude-sonnet-4-20250514")
			},
		},
		"openai models no display name": {
			input: []types.ProviderModels{{
				Provider: "OpenAI",
				Models: []types.ModelEntry{
					{ID: "gpt-4.1"},
					{ID: "gpt-4.1-mini"},
				},
			}},
			check: func(r *require.Assertions, lines []string) {
				joined := strings.Join(lines, "\n")
				r.Contains(joined, "OpenAI")
				r.Contains(joined, "gpt-4.1")
				r.Contains(joined, "gpt-4.1-mini")
			},
		},
		"claude CLI aliases": {
			input: []types.ProviderModels{{
				Provider: "Claude CLI",
			}},
			check: func(r *require.Assertions, lines []string) {
				joined := strings.Join(lines, "\n")
				r.Contains(joined, "Claude CLI (aliases)")
				r.Contains(joined, "opus")
				r.Contains(joined, "→")
				r.Contains(joined, "claude-opus-4-6")
				r.Contains(joined, "sonnet")
				r.Contains(joined, "haiku")
			},
		},
		"provider error": {
			input: []types.ProviderModels{{
				Provider: "Anthropic",
				Error:    "authentication failed",
			}},
			check: func(r *require.Assertions, lines []string) {
				joined := strings.Join(lines, "\n")
				r.Contains(joined, "Anthropic")
				r.Contains(joined, "Error: authentication failed")
			},
		},
		"mixed providers": {
			input: []types.ProviderModels{
				{
					Provider: "Anthropic",
					Models: []types.ModelEntry{
						{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4"},
					},
				},
				{
					Provider: "OpenAI",
					Error:    "request timed out",
				},
				{
					Provider: "Claude CLI",
				},
			},
			check: func(r *require.Assertions, lines []string) {
				joined := strings.Join(lines, "\n")
				r.Contains(joined, "Anthropic")
				r.Contains(joined, "claude-opus-4-6")
				r.Contains(joined, "OpenAI")
				r.Contains(joined, "Error: request timed out")
				r.Contains(joined, "Claude CLI (aliases)")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			lines := renderModelList(tc.input)
			tc.check(r, lines)
		})
	}
}

func TestBuildModelChoices(t *testing.T) {
	r := require.New(t)

	providers := []types.ProviderModels{
		{Provider: "Claude CLI"},
		{Provider: "Anthropic", Models: []types.ModelEntry{
			{ID: "claude-sonnet-4-20250514", DisplayName: "Sonnet 4"},
			{ID: "claude-opus-4-6"},
		}},
		{Provider: "Broken", Error: "boom"},
	}

	choices := buildModelChoices(providers)

	// Claude CLI aliases first (opus, sonnet, haiku), then Anthropic IDs.
	r.Equal("opus", choices[0].id)
	r.Equal("sonnet", choices[1].id)
	r.Equal("haiku", choices[2].id)
	r.Equal("claude-sonnet-4-20250514", choices[3].id)
	r.Contains(choices[3].label, "Sonnet 4")
	r.Equal("claude-opus-4-6", choices[4].id)

	// Errored provider contributes nothing.
	for _, c := range choices {
		r.NotContains(c.label, "boom")
	}
}

func TestBuildModelChoices_empty(t *testing.T) {
	r := require.New(t)
	r.Empty(buildModelChoices(nil))
}

func TestApplyModelSwitch_globalPersists(t *testing.T) {
	r := require.New(t)
	t.Setenv("HOME", t.TempDir())

	m := model{interactiveMode: true}
	newM, cmd := m.applyModelSwitch("sonnet", true)
	r.NotNil(cmd)

	// Persisted to ~/.forge/config.toml.
	path := filepath.Join(os.Getenv("HOME"), ".forge", "config.toml")
	data, err := os.ReadFile(path)
	r.NoError(err)
	r.Contains(string(data), "sonnet")

	// Output notes the global save.
	joined := strings.Join(newM.output, "\n")
	r.Contains(joined, "Saved model.default")
}

func TestApplyModelSwitch_sessionOnly(t *testing.T) {
	r := require.New(t)
	t.Setenv("HOME", t.TempDir())

	m := model{interactiveMode: true}
	newM, cmd := m.applyModelSwitch("opus", false)
	r.NotNil(cmd)

	// No config file written when not global.
	path := filepath.Join(os.Getenv("HOME"), ".forge", "config.toml")
	_, err := os.Stat(path)
	r.True(os.IsNotExist(err))

	joined := strings.Join(newM.output, "\n")
	r.NotContains(joined, "Saved model.default")
	r.Contains(joined, "Switching model to opus")
}

func TestApplyModelSwitch_gatewayRejected(t *testing.T) {
	r := require.New(t)
	m := model{interactiveMode: false}
	newM, cmd := m.applyModelSwitch("opus", true)
	r.Nil(cmd)
	joined := strings.Join(newM.output, "\n")
	r.Contains(joined, "not supported in gateway mode")
}
