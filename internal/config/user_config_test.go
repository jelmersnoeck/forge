package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadUserConfig(t *testing.T) {
	tests := map[string]struct {
		content string
		want    UserConfig
	}{
		"file does not exist": {
			want: UserConfig{},
		},
		"empty file": {
			content: "",
			want:    UserConfig{},
		},
		"provider default set": {
			content: "[provider]\ndefault = \"claude-cli\"\n",
			want:    UserConfig{Provider: ProviderConfig{Default: "claude-cli"}},
		},
		"anthropic provider": {
			content: "[provider]\ndefault = \"anthropic\"\n",
			want:    UserConfig{Provider: ProviderConfig{Default: "anthropic"}},
		},
		"openai provider": {
			content: "[provider]\ndefault = \"openai\"\n",
			want:    UserConfig{Provider: ProviderConfig{Default: "openai"}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			path := filepath.Join(t.TempDir(), "config.toml")

			if tc.content != "" {
				r.NoError(os.WriteFile(path, []byte(tc.content), 0o644))
			}

			got, err := loadUserConfigFrom(path)
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

func TestLoadUserConfig_invalidTOML(t *testing.T) {
	r := require.New(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	r.NoError(os.WriteFile(path, []byte("[provider\ndefault = broken"), 0o644))

	_, err := loadUserConfigFrom(path)
	r.Error(err)
	r.Contains(err.Error(), "parse")
}

func TestSaveUserConfig(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ".forge", "config.toml")

	cfg := UserConfig{Provider: ProviderConfig{Default: "claude-cli"}}
	r.NoError(saveUserConfigTo(path, cfg))

	got, err := loadUserConfigFrom(path)
	r.NoError(err)
	r.Equal(cfg, got)
}

func TestSaveUserConfig_createsDirectory(t *testing.T) {
	r := require.New(t)
	path := filepath.Join(t.TempDir(), "deep", "nested", "config.toml")

	cfg := UserConfig{Provider: ProviderConfig{Default: "anthropic"}}
	r.NoError(saveUserConfigTo(path, cfg))

	_, err := os.Stat(path)
	r.NoError(err)
}

func TestSetValue(t *testing.T) {
	tests := map[string]struct {
		key     string
		value   string
		wantErr string
	}{
		"valid provider anthropic": {
			key:   "provider.default",
			value: "anthropic",
		},
		"valid provider claude-cli": {
			key:   "provider.default",
			value: "claude-cli",
		},
		"valid provider openai": {
			key:   "provider.default",
			value: "openai",
		},
		"invalid provider": {
			key:     "provider.default",
			value:   "gpt-42",
			wantErr: "invalid provider",
		},
		"unknown key": {
			key:     "nonexistent.key",
			value:   "whatever",
			wantErr: "unknown config key",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			path := filepath.Join(t.TempDir(), "config.toml")

			err := setValueAt(path, tc.key, tc.value)
			if tc.wantErr != "" {
				r.Error(err)
				r.Contains(err.Error(), tc.wantErr)
				return
			}

			r.NoError(err)

			// Verify it was persisted
			got, err := getValueAt(path, tc.key)
			r.NoError(err)
			r.Equal(tc.value, got)
		})
	}
}

func TestSetValue_preservesExisting(t *testing.T) {
	r := require.New(t)
	path := filepath.Join(t.TempDir(), "config.toml")

	// Set a value
	r.NoError(setValueAt(path, "provider.default", "anthropic"))

	// Overwrite it
	r.NoError(setValueAt(path, "provider.default", "claude-cli"))

	got, err := getValueAt(path, "provider.default")
	r.NoError(err)
	r.Equal("claude-cli", got)
}

func TestGetValue_unknownKey(t *testing.T) {
	r := require.New(t)
	path := filepath.Join(t.TempDir(), "config.toml")

	_, err := getValueAt(path, "troy.barnes")
	r.Error(err)
	r.Contains(err.Error(), "unknown config key")
}

func TestGetValue_unsetReturnsEmpty(t *testing.T) {
	r := require.New(t)
	path := filepath.Join(t.TempDir(), "config.toml")

	got, err := getValueAt(path, "provider.default")
	r.NoError(err)
	r.Equal("", got)
}

func TestListValues(t *testing.T) {
	r := require.New(t)
	path := filepath.Join(t.TempDir(), "config.toml")

	r.NoError(setValueAt(path, "provider.default", "openai"))

	values, err := listValuesAt(path)
	r.NoError(err)
	r.Equal("openai", values["provider.default"])
}

func TestListValues_empty(t *testing.T) {
	r := require.New(t)
	path := filepath.Join(t.TempDir(), "config.toml")

	values, err := listValuesAt(path)
	r.NoError(err)
	r.Equal("", values["provider.default"])
}

func TestValidKeys(t *testing.T) {
	r := require.New(t)
	keys := ValidKeys()
	r.Contains(keys, "provider.default")
	r.NotEmpty(keys["provider.default"])
}
