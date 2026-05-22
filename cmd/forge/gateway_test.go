package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildChildArgs(t *testing.T) {
	tests := map[string]struct {
		input []string
		want  []string
	}{
		"strips -daemon from args": {
			input: []string{"gateway", "-daemon", "-port", "4000"},
			want:  []string{"gateway", "-port", "4000"},
		},
		"strips --daemon from args": {
			input: []string{"gateway", "--daemon", "-pid-file", "/tmp/forge.pid"},
			want:  []string{"gateway", "-pid-file", "/tmp/forge.pid"},
		},
		"daemon at end": {
			input: []string{"gateway", "-port", "4000", "-daemon"},
			want:  []string{"gateway", "-port", "4000"},
		},
		"no daemon flag": {
			input: []string{"gateway", "-port", "4000"},
			want:  []string{"gateway", "-port", "4000"},
		},
		"daemon only": {
			input: []string{"gateway", "-daemon"},
			want:  []string{"gateway"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := buildChildArgs(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

func TestResolveDaemonPath(t *testing.T) {
	tests := map[string]struct {
		flagValue   string
		envRunDir   string
		sessionsDir string
		filename    string
		want        string
	}{
		"flag value takes precedence": {
			flagValue:   "/custom/path/forge.pid",
			envRunDir:   "/run/dir",
			sessionsDir: "/sessions",
			filename:    "forge.pid",
			want:        "/custom/path/forge.pid",
		},
		"FORGE_RUN_DIR if no flag": {
			flagValue:   "",
			envRunDir:   "/home/user/.forge/run",
			sessionsDir: "/tmp/forge/sessions",
			filename:    "forge.pid",
			want:        "/home/user/.forge/run/forge.pid",
		},
		"sessionsDir as final fallback": {
			flagValue:   "",
			envRunDir:   "",
			sessionsDir: "/tmp/forge/sessions",
			filename:    "forge.log",
			want:        "/tmp/forge/sessions/forge.log",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			// Set/unset FORGE_RUN_DIR for each test case
			if tc.envRunDir != "" {
				t.Setenv("FORGE_RUN_DIR", tc.envRunDir)
			} else {
				t.Setenv("FORGE_RUN_DIR", "")
			}
			got := resolveDaemonPath(tc.flagValue, tc.filename, tc.sessionsDir)
			r.Equal(tc.want, got)
		})
	}
}

func TestCheckPIDFile_NoPIDFile(t *testing.T) {
	r := require.New(t)
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "forge.pid")

	err := checkPIDFile(pidFile)
	r.NoError(err, "should succeed when no PID file exists")
}

func TestCheckPIDFile_StalePID(t *testing.T) {
	r := require.New(t)
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "forge.pid")

	// Write a PID that definitely isn't running. PID 2147483647 is unlikely
	// to be alive on any system.
	err := os.WriteFile(pidFile, []byte("2147483647\n"), 0o644)
	r.NoError(err)

	err = checkPIDFile(pidFile)
	r.NoError(err, "should succeed and remove stale PID file")

	// PID file should be removed
	_, err = os.Stat(pidFile)
	r.True(os.IsNotExist(err), "stale PID file should be removed")
}

func TestCheckPIDFile_AlivePID(t *testing.T) {
	r := require.New(t)
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "forge.pid")

	// Use the current process PID — guaranteed alive
	myPID := os.Getpid()
	err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", myPID)), 0o644)
	r.NoError(err)

	err = checkPIDFile(pidFile)
	r.Error(err, "should refuse when a live process owns the PID file")
	r.Contains(err.Error(), "already running")
	r.Contains(err.Error(), fmt.Sprintf("pid %d", myPID))
}

func TestCheckPIDFile_CorruptFile(t *testing.T) {
	r := require.New(t)
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "forge.pid")

	err := os.WriteFile(pidFile, []byte("not-a-number\n"), 0o644)
	r.NoError(err)

	err = checkPIDFile(pidFile)
	r.NoError(err, "should remove corrupt PID file and proceed")

	_, err = os.Stat(pidFile)
	r.True(os.IsNotExist(err), "corrupt PID file should be removed")
}

func TestAppendIfMissing(t *testing.T) {
	tests := map[string]struct {
		args  []string
		flag  string
		value string
		want  []string
	}{
		"appends when missing": {
			args:  []string{"gateway"},
			flag:  "-pid-file",
			value: "/tmp/forge.pid",
			want:  []string{"gateway", "-pid-file", "/tmp/forge.pid"},
		},
		"no-op when present": {
			args:  []string{"gateway", "-pid-file", "/other.pid"},
			flag:  "-pid-file",
			value: "/tmp/forge.pid",
			want:  []string{"gateway", "-pid-file", "/other.pid"},
		},
		"detects = form": {
			args:  []string{"gateway", "-pid-file=/other.pid"},
			flag:  "-pid-file",
			value: "/tmp/forge.pid",
			want:  []string{"gateway", "-pid-file=/other.pid"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := appendIfMissing(tc.args, tc.flag, tc.value)
			r.Equal(tc.want, got)
		})
	}
}

func TestWritePIDFile(t *testing.T) {
	r := require.New(t)
	tmp := t.TempDir()
	// Nested path to test mkdir -p behavior
	pidFile := filepath.Join(tmp, "nested", "dir", "forge.pid")

	err := writePIDFile(pidFile)
	r.NoError(err)

	data, err := os.ReadFile(pidFile)
	r.NoError(err)
	r.Contains(string(data), fmt.Sprintf("%d", os.Getpid()))
}
