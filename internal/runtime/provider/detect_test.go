package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoDetect(t *testing.T) {
	tests := map[string]struct {
		envAnthropic string
		envOpenAI    string
		wantName     string
		wantFound    bool
	}{
		"anthropic key wins": {
			envAnthropic: "sk-ant-test",
			envOpenAI:    "sk-oai-test",
			wantName:     "anthropic",
			wantFound:    true,
		},
		"openai fallback": {
			envAnthropic: "",
			envOpenAI:    "sk-oai-test",
			wantName:     "openai",
			wantFound:    true,
		},
		"anthropic takes priority": {
			envAnthropic: "sk-ant-test",
			envOpenAI:    "",
			wantName:     "anthropic",
			wantFound:    true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			t.Setenv("ANTHROPIC_API_KEY", tc.envAnthropic)
			t.Setenv("OPENAI_API_KEY", tc.envOpenAI)

			result := AutoDetect()
			r.Equal(tc.wantFound, result.Found)
			if tc.wantFound {
				r.Equal(tc.wantName, result.Name)
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
