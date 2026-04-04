// Package env loads .env files into the process environment.
package env

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotenv reads a .env file from dir (falling back to the binary's
// directory). Existing env vars are never overridden.
func LoadDotenv(dir string) {
	candidates := []string{
		filepath.Join(dir, ".env"),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}

	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()

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
				os.Setenv(key, val)
			}
		}
		return
	}
}
