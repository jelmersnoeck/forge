package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatIssuePrompt(t *testing.T) {
	tests := map[string]struct {
		issue ghIssue
		want  string
	}{
		"full issue with comments": {
			issue: ghIssue{
				Title: "Fix the Darkest Timeline",
				Body:  "Evil Abed has taken over the study room.\nWe need to stop him.",
				URL:   "https://github.com/greendale/community/issues/42",
				Comments: []ghComment{
					{
						Author:    ghAuthor{Login: "troy-barnes"},
						Body:      "I'll get the felt goatees!",
						CreatedAt: "2026-04-01T10:00:00Z",
					},
					{
						Author:    ghAuthor{Login: "abed-nadir"},
						Body:      "Cool. Cool cool cool.",
						CreatedAt: "2026-04-01T11:00:00Z",
					},
				},
			},
			want: "Implement the following GitHub issue.\n\n" +
				"Issue: https://github.com/greendale/community/issues/42\n\n" +
				"# Fix the Darkest Timeline\n\n" +
				"Evil Abed has taken over the study room.\nWe need to stop him.\n\n" +
				"## Comments\n\n" +
				"**@troy-barnes** (2026-04-01T10:00:00Z):\nI'll get the felt goatees!\n\n" +
				"**@abed-nadir** (2026-04-01T11:00:00Z):\nCool. Cool cool cool.",
		},
		"no comments": {
			issue: ghIssue{
				Title: "Add paintball mode",
				Body:  "We need a competitive paintball feature.",
				URL:   "https://github.com/greendale/community/issues/7",
			},
			want: "Implement the following GitHub issue.\n\n" +
				"Issue: https://github.com/greendale/community/issues/7\n\n" +
				"# Add paintball mode\n\n" +
				"We need a competitive paintball feature.",
		},
		"empty body": {
			issue: ghIssue{
				Title: "Fix something",
				Body:  "",
				URL:   "https://github.com/greendale/community/issues/99",
			},
			want: "Implement the following GitHub issue.\n\n" +
				"Issue: https://github.com/greendale/community/issues/99\n\n" +
				"# Fix something",
		},
		"empty body with comments": {
			issue: ghIssue{
				Title: "Fix something",
				Body:  "",
				URL:   "https://github.com/greendale/community/issues/99",
				Comments: []ghComment{
					{
						Author:    ghAuthor{Login: "dean-pelton"},
						Body:      "I hope this doesn't awaken anything in me.",
						CreatedAt: "2026-06-01T09:00:00Z",
					},
				},
			},
			want: "Implement the following GitHub issue.\n\n" +
				"Issue: https://github.com/greendale/community/issues/99\n\n" +
				"# Fix something\n\n" +
				"## Comments\n\n" +
				"**@dean-pelton** (2026-06-01T09:00:00Z):\nI hope this doesn't awaken anything in me.",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := formatIssuePrompt(tc.issue)
			r.Equal(tc.want, got)
		})
	}
}

func TestExtractIssueNumber(t *testing.T) {
	tests := map[string]struct {
		input string
		want  int
	}{
		"plain number":       {input: "42", want: 42},
		"hash prefix":        {input: "#42", want: 42},
		"full URL":           {input: "https://github.com/owner/repo/issues/42", want: 42},
		"URL with fragment":  {input: "https://github.com/o/r/issues/99#comment-123", want: 99},
		"URL with query":     {input: "https://github.com/o/r/issues/7?foo=bar", want: 7},
		"cross-repo URL":     {input: "https://github.com/other-org/other-repo/issues/123", want: 123},
		"not a number":       {input: "foo", want: 0},
		"empty":              {input: "", want: 0},
		"URL without number": {input: "https://github.com/o/r/issues/", want: 0},
		"zero":               {input: "0", want: 0},
		"spaces with hash":   {input: " #7 ", want: 7},
		"negative":           {input: "-1", want: 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := extractIssueNumber(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

func TestNormalizeIssueRef(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"plain number":       {input: "42", want: "42"},
		"hash prefix":        {input: "#42", want: "42"},
		"full URL":           {input: "https://github.com/owner/repo/issues/42", want: "https://github.com/owner/repo/issues/42"},
		"URL with fragment":  {input: "https://github.com/o/r/issues/42#comment-123", want: "https://github.com/o/r/issues/42#comment-123"},
		"hash with spaces":   {input: " #7 ", want: "7"},
		"number with spaces": {input: " 7 ", want: "7"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := normalizeIssueRef(tc.input)
			r.Equal(tc.want, got)
		})
	}
}
