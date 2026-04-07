// Package envutil loads .env files into the process environment.
package envutil

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnv reads .env from each dir in order, applying the first one found.
// Falls back to the executable's directory if no dirs match.
// Existing env vars are never overridden.
func LoadEnv(dirs ...string) {
	candidates := make([]string, 0, len(dirs)+1)
	for _, d := range dirs {
		candidates = append(candidates, filepath.Join(d, ".env"))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}

	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq < 0 {
				continue
			}
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, val)
			}
		}
		_ = f.Close()
		return
	}
}
