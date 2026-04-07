package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeSlug(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input string
		want  string
	}{
		"clean slug": {
			input: "fix-auth-timeout",
			want:  "fix-auth-timeout",
		},
		"uppercase": {
			input: "Fix-Auth-Timeout",
			want:  "fix-auth-timeout",
		},
		"spaces become dashes": {
			input: "fix auth timeout",
			want:  "fix-auth-timeout",
		},
		"special chars removed": {
			input: "fix: auth timeout!",
			want:  "fix-auth-timeout",
		},
		"leading trailing dashes trimmed": {
			input: "--fix-auth--",
			want:  "fix-auth",
		},
		"collapsed dashes": {
			input: "fix---auth---timeout",
			want:  "fix-auth-timeout",
		},
		"very long slug truncated": {
			input: "this-is-a-very-long-slug-that-should-be-truncated-at-the-maximum-allowed-length",
			want:  "this-is-a-very-long-slug-that-should-be",
		},
		"empty string": {
			input: "",
			want:  "",
		},
		"only special chars": {
			input: "!@#$%",
			want:  "",
		},
		"backtick wrapped": {
			input: "`fix-auth-timeout`",
			want:  "fix-auth-timeout",
		},
		"newlines and whitespace": {
			input: "\n  fix-auth-timeout  \n",
			want:  "fix-auth-timeout",
		},
		"unicode chars removed": {
			input: "fix-authöTimeout",
			want:  "fix-auth-timeout",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := sanitizeSlug(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

func TestFallbackSessionName(t *testing.T) {
	r := require.New(t)

	// Generate multiple names and verify format
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		name := fallbackSessionName()
		r.Regexp(`^[a-z]+-[a-z]+$`, name, "fallback name should be adjective-noun: %s", name)
		seen[name] = true
	}

	// With 20x20=400 combos and 50 tries, we should see some variety
	r.Greater(len(seen), 5, "expected variety in fallback names")
}

func TestGenerateSessionName_EmptyPrompt(t *testing.T) {
	r := require.New(t)

	// Empty prompt should use fallback (no API call)
	name := generateSessionName("")
	r.Regexp(`^[a-z]+-[a-z]+$`, name, "empty prompt should produce fallback name: %s", name)
}

func TestGenerateSessionName_NoAPIKey(t *testing.T) {
	r := require.New(t)

	// Save and clear API key
	t.Setenv("ANTHROPIC_API_KEY", "")

	name := generateSessionName("Fix the authentication timeout in the login flow")
	r.Regexp(`^[a-z]+-[a-z]+$`, name, "missing API key should produce fallback name: %s", name)
}
