package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	tests := map[string]struct {
		projectJSON string
		want        ForgeConfig
	}{
		"no config files": {
			want: ForgeConfig{},
		},
		"project config with specsDir": {
			projectJSON: `{"specsDir": "specs/greendale"}`,
			want:        ForgeConfig{SpecsDir: "specs/greendale"},
		},
		"empty project config": {
			projectJSON: `{}`,
			want:        ForgeConfig{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir := t.TempDir()

			if tc.projectJSON != "" {
				forgeDir := filepath.Join(dir, ".forge")
				r.NoError(os.MkdirAll(forgeDir, 0o755))
				r.NoError(os.WriteFile(filepath.Join(forgeDir, "config.json"), []byte(tc.projectJSON), 0o644))
			}

			got, err := Load(dir)
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

func TestLoad_invalidJSON(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	forgeDir := filepath.Join(dir, ".forge")
	r.NoError(os.MkdirAll(forgeDir, 0o755))
	r.NoError(os.WriteFile(filepath.Join(forgeDir, "config.json"), []byte(`{not json`), 0o644))

	_, err := Load(dir)
	r.Error(err)
	r.Contains(err.Error(), "parse")
}
