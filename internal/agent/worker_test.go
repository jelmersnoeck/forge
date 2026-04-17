package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractPipelineHint(t *testing.T) {
	tests := map[string]struct {
		metadata map[string]any
		want     string
	}{
		"nil metadata": {
			metadata: nil,
			want:     "auto",
		},
		"empty metadata": {
			metadata: map[string]any{},
			want:     "auto",
		},
		"ideate hint": {
			metadata: map[string]any{"pipeline_hint": "ideate"},
			want:     "ideate",
		},
		"code hint": {
			metadata: map[string]any{"pipeline_hint": "code"},
			want:     "code",
		},
		"auto hint": {
			metadata: map[string]any{"pipeline_hint": "auto"},
			want:     "auto",
		},
		"invalid hint defaults to auto": {
			metadata: map[string]any{"pipeline_hint": "streets-ahead"},
			want:     "auto",
		},
		"wrong type defaults to auto": {
			metadata: map[string]any{"pipeline_hint": 42},
			want:     "auto",
		},
		"empty string defaults to auto": {
			metadata: map[string]any{"pipeline_hint": ""},
			want:     "auto",
		},
		"other metadata ignored": {
			metadata: map[string]any{
				"source":        "linear",
				"pipeline_hint": "code",
			},
			want: "code",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, extractPipelineHint(tc.metadata))
		})
	}
}
