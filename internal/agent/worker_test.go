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

func TestResolveModelAlias(t *testing.T) {
	tests := map[string]struct {
		name        string
		isClaudeCLI bool
		want        string
	}{
		"opus alias": {
			name: "opus", isClaudeCLI: false,
			want: "claude-opus-4-6",
		},
		"sonnet alias": {
			name: "sonnet", isClaudeCLI: false,
			want: "claude-sonnet-4-20250514",
		},
		"haiku alias": {
			name: "haiku", isClaudeCLI: false,
			want: "claude-haiku-4-20250506",
		},
		"full model ID passes through": {
			name: "claude-sonnet-4-20250514", isClaudeCLI: false,
			want: "claude-sonnet-4-20250514",
		},
		"unknown name passes through": {
			name: "gpt-4", isClaudeCLI: false,
			want: "gpt-4",
		},
		"claude CLI skips alias resolution": {
			name: "sonnet", isClaudeCLI: true,
			want: "sonnet",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, ResolveModelAlias(tc.name, tc.isClaudeCLI))
		})
	}
}

func TestWorkerResolveModel(t *testing.T) {
	tests := map[string]struct {
		override      string
		settingsModel string
		isClaudeCLI   bool
		want          string
	}{
		"override takes priority": {
			override: "sonnet", settingsModel: "claude-opus-4-6", isClaudeCLI: false,
			want: "claude-sonnet-4-20250514",
		},
		"settings fallback": {
			override: "", settingsModel: "claude-opus-4-6", isClaudeCLI: false,
			want: "claude-opus-4-6",
		},
		"default when no override or settings": {
			override: "", settingsModel: "", isClaudeCLI: false,
			want: "claude-opus-4-6",
		},
		"claude CLI passes alias through": {
			override: "sonnet", settingsModel: "", isClaudeCLI: true,
			want: "sonnet",
		},
		"settings with non-claude prefix ignored for Anthropic": {
			override: "", settingsModel: "opus[1m]", isClaudeCLI: false,
			want: "claude-opus-4-6",
		},
		"settings with non-claude prefix used for Claude CLI": {
			override: "", settingsModel: "opus[1m]", isClaudeCLI: true,
			want: "opus[1m]",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			w := &Worker{modelOverride: tc.override}
			r.Equal(tc.want, w.resolveModel(tc.settingsModel, tc.isClaudeCLI, "claude-opus-4-6"))
		})
	}
}

func TestWorkerSetModelConcurrent(t *testing.T) {
	r := require.New(t)
	w := &Worker{}
	r.Equal("", w.ModelOverride())

	w.SetModel("sonnet")
	r.Equal("sonnet", w.ModelOverride())

	w.SetModel("opus")
	r.Equal("opus", w.ModelOverride())
}
