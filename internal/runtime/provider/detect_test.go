package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoDetect(t *testing.T) {
	tests := map[string]struct {
		envAnthropic string
		envOpenAI    string
		wantName     string // empty means "don't care, just not anthropic/openai"
	}{
		"anthropic key wins": {
			envAnthropic: "sk-ant-test",
			envOpenAI:    "sk-oai-test",
			wantName:     "anthropic",
		},
		"openai fallback": {
			envAnthropic: "",
			envOpenAI:    "sk-oai-test",
			wantName:     "openai",
		},
		"anthropic takes priority": {
			envAnthropic: "sk-ant-test",
			envOpenAI:    "",
			wantName:     "anthropic",
		},
		"whitespace-only anthropic key ignored": {
			envAnthropic: "  \t\n  ",
			envOpenAI:    "sk-oai-test",
			wantName:     "openai",
		},
		"whitespace-only both keys skips to next provider": {
			envAnthropic: "  ",
			envOpenAI:    "  ",
			// Falls through to claude-cli if on PATH; otherwise not found.
			// We only assert that whitespace keys are not used as anthropic/openai.
		},
		"key with leading/trailing whitespace trimmed": {
			envAnthropic: "  sk-ant-test  ",
			wantName:     "anthropic",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			t.Setenv("ANTHROPIC_API_KEY", tc.envAnthropic)
			t.Setenv("OPENAI_API_KEY", tc.envOpenAI)

			result := AutoDetect()
			if tc.wantName != "" {
				r.True(result.Found)
				r.Equal(tc.wantName, result.Name)
				r.NotNil(result.Provider, "Provider should be non-nil when Found is true")
			} else {
				// No specific provider expected; just verify whitespace keys
				// weren't treated as valid anthropic/openai credentials.
				r.NotEqual("anthropic", result.Name)
				r.NotEqual("openai", result.Name)
			}
		})
	}
}

func TestFromName(t *testing.T) {
	tests := map[string]struct {
		name    string
		envKey  string
		envVal  string
		wantNil bool
	}{
		"anthropic with key": {
			name:   "anthropic",
			envKey: "ANTHROPIC_API_KEY",
			envVal: "sk-ant-test",
		},
		"anthropic without key": {
			name:    "anthropic",
			envKey:  "ANTHROPIC_API_KEY",
			envVal:  "",
			wantNil: true,
		},
		"openai with key": {
			name:   "openai",
			envKey: "OPENAI_API_KEY",
			envVal: "sk-oai-test",
		},
		"openai without key": {
			name:    "openai",
			envKey:  "OPENAI_API_KEY",
			envVal:  "",
			wantNil: true,
		},
		"unknown provider": {
			name:    "dean-pelton",
			wantNil: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			if tc.envKey != "" {
				t.Setenv(tc.envKey, tc.envVal)
			}

			p := FromName(tc.name)
			if tc.wantNil {
				r.Nil(p)
			} else {
				r.NotNil(p)
			}
		})
	}
}

func TestValidateAPIKey(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"clean key":            {input: "sk-ant-test", want: "sk-ant-test"},
		"leading whitespace":   {input: "  sk-ant-test", want: "sk-ant-test"},
		"trailing whitespace":  {input: "sk-ant-test  ", want: "sk-ant-test"},
		"both sides":           {input: "  sk-ant-test  ", want: "sk-ant-test"},
		"tabs and newlines":    {input: "\t sk-ant-test \n", want: "sk-ant-test"},
		"whitespace only":      {input: "  \t\n  ", want: ""},
		"empty":                {input: "", want: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, validateAPIKey(tc.input))
		})
	}
}

func TestFromNameOrFallback(t *testing.T) {
	tests := map[string]struct {
		name   string
		envKey string
		envVal string
	}{
		"anthropic":        {name: "anthropic", envKey: "ANTHROPIC_API_KEY", envVal: "sk-ant-test"},
		"anthropic no key": {name: "anthropic", envKey: "ANTHROPIC_API_KEY", envVal: ""},
		"openai":           {name: "openai", envKey: "OPENAI_API_KEY", envVal: "sk-oai-test"},
		"openai no key":    {name: "openai", envKey: "OPENAI_API_KEY", envVal: ""},
		"unknown":          {name: "senor-chang"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			if tc.envKey != "" {
				t.Setenv(tc.envKey, tc.envVal)
			}

			// FromNameOrFallback always returns non-nil
			p := FromNameOrFallback(tc.name)
			r.NotNil(p)
		})
	}
}
