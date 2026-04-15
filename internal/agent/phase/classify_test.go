package phase

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseIntent(t *testing.T) {
	tests := map[string]struct {
		input string
		want  Intent
	}{
		"question": {
			input: `{"intent": "question"}`,
			want:  IntentQuestion,
		},
		"task": {
			input: `{"intent": "task"}`,
			want:  IntentTask,
		},
		"question with whitespace": {
			input: `  {"intent": "question"}  `,
			want:  IntentQuestion,
		},
		"garbage defaults to task": {
			input: "I'm not sure what you mean",
			want:  IntentTask,
		},
		"empty string defaults to task": {
			input: "",
			want:  IntentTask,
		},
		"unknown intent value defaults to task": {
			input: `{"intent": "greendale"}`,
			want:  IntentTask,
		},
		"valid JSON but no intent field": {
			input: `{"category": "question"}`,
			want:  IntentTask,
		},
		"malformed JSON defaults to task": {
			input: `{"intent": `,
			want:  IntentTask,
		},
		"JSON with extra fields still works": {
			input: `{"intent": "question", "confidence": 0.9}`,
			want:  IntentQuestion,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := parseIntent(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

func TestClassifyIntentEmptyPrompt(t *testing.T) {
	r := require.New(t)
	// Empty prompt should skip classification and return task.
	got := ClassifyIntent(t.Context(), nil, "")
	r.Equal(IntentTask, got)
}

func TestClassifyIntentWhitespacePrompt(t *testing.T) {
	r := require.New(t)
	got := ClassifyIntent(t.Context(), nil, "   ")
	r.Equal(IntentTask, got)
}
