package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFindMatches(t *testing.T) {
	tests := map[string]struct {
		content      string
		old          string
		disableFuzzy bool
		wantOK       bool
		wantStrat    matchStrategy
		wantCount    int
	}{
		"exact single": {
			content:   "Troy: Cool beans",
			old:       "Cool",
			wantOK:    true,
			wantStrat: matchExact,
			wantCount: 1,
		},
		"exact multiple": {
			content:   "pop pop pop",
			old:       "pop",
			wantOK:    true,
			wantStrat: matchExact,
			wantCount: 3,
		},
		"no match at all": {
			content: "Study group meeting",
			old:     "Pierce",
			wantOK:  false,
		},
		"trailing whitespace variant": {
			content:   "func main() {\n\tabed := 1   \n}\n",
			old:       "func main() {\n\tabed := 1\n}",
			wantOK:    true,
			wantStrat: matchWhitespace,
			wantCount: 1,
		},
		"leading indentation variant tabs vs spaces": {
			content:   "func f() {\n\treturn 42\n}\n",
			old:       "func f() {\n    return 42\n}",
			wantOK:    true,
			wantStrat: matchWhitespace,
			wantCount: 1,
		},
		"disable fuzzy skips whitespace tier": {
			content:      "func f() {\n\treturn 42\n}\n",
			old:          "func f() {\n    return 42\n}",
			disableFuzzy: true,
			wantOK:       false,
		},
		"exact wins over whitespace variant elsewhere": {
			content:   "x := 1\n\ty := 1\n",
			old:       "x := 1",
			wantOK:    true,
			wantStrat: matchExact,
			wantCount: 1,
		},
		"two whitespace matches": {
			content:   "a\tb\nmid\n  a\tb\nend\n",
			old:       "a b",
			wantOK:    true,
			wantStrat: matchWhitespace,
			wantCount: 2,
		},
		"crlf file matches lf old": {
			content:   "alpha\r\nbeta\r\ngamma\r\n",
			old:       "alpha\nbeta\ngamma",
			wantOK:    true,
			wantStrat: matchWhitespace,
			wantCount: 1,
		},
		"empty content": {
			content: "",
			old:     "anything",
			wantOK:  false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			ranges, strat, ok := findMatches(tc.content, tc.old, tc.disableFuzzy)
			r.Equal(tc.wantOK, ok)
			if !tc.wantOK {
				return
			}
			r.Equal(tc.wantStrat, strat)
			r.Len(ranges, tc.wantCount)
			// Ranges must be valid and non-overlapping ascending.
			prevEnd := -1
			for _, rg := range ranges {
				r.GreaterOrEqual(rg[0], 0)
				r.LessOrEqual(rg[1], len(tc.content))
				r.LessOrEqual(rg[0], rg[1])
				r.GreaterOrEqual(rg[0], prevEnd)
				prevEnd = rg[1]
			}
		})
	}
}

func TestNearMissDiagnostic(t *testing.T) {
	r := require.New(t)

	diag := nearMissDiagnostic("func greendale() {\n\treturn campus\n}\n", "func greendale() {\n\treturn library\n}")
	r.NotEmpty(diag)
	r.Contains(diag, "campus")
	r.Contains(diag, "library")

	empty := nearMissDiagnostic("", "anything")
	r.Contains(empty, "empty")
}

func TestEditWhitespaceMatching(t *testing.T) {
	tests := map[string]struct {
		content string
		input   map[string]any
		want    func(*testing.T, types.ToolResult, error, string)
	}{
		"trailing whitespace normalized match": {
			content: "func main() {\n\tabed := 1   \n}\n",
			input: map[string]any{
				"old_string": "func main() {\n\tabed := 1\n}",
				"new_string": "func main() {\n\tabed := 2\n}",
			},
			want: func(t *testing.T, result types.ToolResult, err error, after string) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "whitespace-normalized match")
				r.Equal("func main() {\n\tabed := 2\n}\n", after)
			},
		},
		"leading indentation difference reuses file": {
			content: "func f() {\n\treturn 42\n}\n",
			input: map[string]any{
				"old_string": "func f() {\n    return 42\n}",
				"new_string": "func f() {\n    return 7\n}",
			},
			want: func(t *testing.T, result types.ToolResult, err error, after string) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "whitespace-normalized match")
				r.Contains(after, "return 7")
			},
		},
		"two whitespace matches without replace_all": {
			content: "a\tb\nmid\n  a\tb\nend\n",
			input: map[string]any{
				"old_string": "a b",
				"new_string": "qux",
			},
			want: func(t *testing.T, result types.ToolResult, err error, after string) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "replace_all")
				r.Equal("a\tb\nmid\n  a\tb\nend\n", after)
			},
		},
		"disable fuzzy with only whitespace variant": {
			content: "func f() {\n\treturn 42\n}\n",
			input: map[string]any{
				"old_string":    "func f() {\n    return 42\n}",
				"new_string":    "x",
				"disable_fuzzy": true,
			},
			want: func(t *testing.T, result types.ToolResult, err error, after string) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "not found")
				r.Equal("func f() {\n\treturn 42\n}\n", after)
			},
		},
		"not found includes diagnostic": {
			content: "func greendale() {\n\treturn campus\n}\n",
			input: map[string]any{
				"old_string": "func greendale() {\n\treturn library\n}",
				"new_string": "x",
			},
			want: func(t *testing.T, result types.ToolResult, err error, after string) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "not found in")
				r.Contains(result.Content[0].Text, "campus")
			},
		},
		"crlf file preserved on write": {
			content: "alpha\r\nbeta\r\ngamma\r\n",
			input: map[string]any{
				"old_string": "alpha\nbeta\ngamma",
				"new_string": "alpha\ndelta\ngamma",
			},
			want: func(t *testing.T, result types.ToolResult, err error, after string) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(after, "delta")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "subject.txt")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0644))
			input := tc.input
			input["file_path"] = path
			tool := EditTool()
			result, err := tool.Handler(input, types.ToolContext{})
			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			tc.want(t, result, err, string(data))
		})
	}
}
