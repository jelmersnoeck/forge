package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseClassifiedIntent(t *testing.T) {
	tests := map[string]struct {
		content string
		want    string
	}{
		"json triage": {
			content: `{"intent":"triage","size":"","spec_match":""}`,
			want:    "triage",
		},
		"json question": {
			content: `{"intent":"question","size":"","spec_match":""}`,
			want:    "question",
		},
		"json investigate": {
			content: `{"intent":"investigate","size":"","spec_match":""}`,
			want:    "investigate",
		},
		"json task": {
			content: `{"intent":"task","size":"standard","spec_match":""}`,
			want:    "task",
		},
		"json with whitespace": {
			content: `  {"intent":"triage"}  `,
			want:    "triage",
		},
		"bare string fallback": {
			content: "investigate",
			want:    "investigate",
		},
		"malformed json falls back to raw": {
			content: `{"intent":`,
			want:    `{"intent":`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, parseClassifiedIntent(tc.content))
		})
	}
}
