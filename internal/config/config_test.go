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

func TestLoad_RepoMap(t *testing.T) {
	r := require.New(t)
	t.Setenv("HOME", t.TempDir()) // isolate from real user config

	dir := t.TempDir()
	forgeDir := filepath.Join(dir, ".forge")
	r.NoError(os.MkdirAll(forgeDir, 0o755))
	r.NoError(os.WriteFile(filepath.Join(forgeDir, "config.json"),
		[]byte(`{"repoMap":{"enabled":true,"tokenBudget":500,"maxFiles":10}}`), 0o644))

	got, err := Load(dir)
	r.NoError(err)
	r.True(got.RepoMap.Enabled)
	r.Equal(500, got.RepoMap.TokenBudget)
	r.Equal(10, got.RepoMap.MaxFiles)
}

// TestLoad_RepoMap_ProjectDisablesUser verifies an explicit "enabled": false in
// the project config overrides an enabling user config (project-over-user for
// the boolean flag).
func TestLoad_RepoMap_ProjectDisablesUser(t *testing.T) {
	r := require.New(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	userForge := filepath.Join(home, ".forge")
	r.NoError(os.MkdirAll(userForge, 0o755))
	r.NoError(os.WriteFile(filepath.Join(userForge, "config.json"),
		[]byte(`{"repoMap":{"enabled":true}}`), 0o644))

	dir := t.TempDir()
	projForge := filepath.Join(dir, ".forge")
	r.NoError(os.MkdirAll(projForge, 0o755))
	r.NoError(os.WriteFile(filepath.Join(projForge, "config.json"),
		[]byte(`{"repoMap":{"enabled":false}}`), 0o644))

	got, err := Load(dir)
	r.NoError(err)
	r.False(got.RepoMap.Enabled)
}
