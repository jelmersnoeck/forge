package phase

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseIntent(t *testing.T) {
	tests := map[string]struct {
		input   string
		want    Intent
		wantErr bool
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
			input:   "I'm not sure what you mean",
			want:    IntentTask,
			wantErr: true,
		},
		"empty string defaults to task": {
			input:   "",
			want:    IntentTask,
			wantErr: true,
		},
		"unknown intent value defaults to task": {
			input:   `{"intent": "greendale"}`,
			want:    IntentTask,
			wantErr: true,
		},
		"valid JSON but no intent field": {
			input:   `{"category": "question"}`,
			want:    IntentTask,
			wantErr: true,
		},
		"malformed JSON defaults to task": {
			input:   `{"intent": `,
			want:    IntentTask,
			wantErr: true,
		},
		"JSON with extra fields still works": {
			input: `{"intent": "question", "confidence": 0.9}`,
			want:  IntentQuestion,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got, err := parseIntent(tc.input)
			r.Equal(tc.want, got)
			if tc.wantErr {
				r.Error(err)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestClassifyIntentEmptyPrompt(t *testing.T) {
	r := require.New(t)
	// Empty prompt should skip classification and return task.
	got, err := ClassifyIntent(t.Context(), nil, "")
	r.NoError(err)
	r.Equal(IntentTask, got)
}

func TestClassifyIntentWhitespacePrompt(t *testing.T) {
	r := require.New(t)
	got, err := ClassifyIntent(t.Context(), nil, "   ")
	r.NoError(err)
	r.Equal(IntentTask, got)
}

func TestTruncateAtWordBoundary(t *testing.T) {
	tests := map[string]struct {
		input  string
		maxLen int
		want   string
	}{
		"short string unchanged": {
			input:  "how does caching work",
			maxLen: 100,
			want:   "how does caching work",
		},
		"truncates at word boundary": {
			input:  "how does the caching layer work in production",
			maxLen: 25,
			want:   "how does the caching...",
		},
		"single massive word hard-cuts": {
			input:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			maxLen: 10,
			want:   "aaaaaaaaaa...",
		},
		"exact length unchanged": {
			input:  "hello",
			maxLen: 5,
			want:   "hello",
		},
		"trims trailing whitespace before ellipsis": {
			input:  "Troy Barnes is a football star at Greendale Community College",
			maxLen: 30,
			want:   "Troy Barnes is a football...",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := truncateAtWordBoundary(tc.input, tc.maxLen)
			r.Equal(tc.want, got)
		})
	}
}

func TestClassificationModelsNotEmpty(t *testing.T) {
	r := require.New(t)
	r.NotEmpty(classificationModels, "classificationModels must have at least one model")
	for _, m := range classificationModels {
		r.NotEmpty(m, "classification model must not be empty string")
	}
}
