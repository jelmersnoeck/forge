package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// atomicTempPrefix is the prefix for temp files created during atomic writes.
// It is identifiable and hidden from typical globs so orphans can be cleaned up.
const atomicTempPrefix = ".forge-write-"

// atomicWrite writes content to path atomically. It writes to a temp file in the
// same directory as path, fsyncs it to disk, sets the requested permissions, then
// renames it over the target. On POSIX, rename is atomic: a concurrent reader sees
// either the complete old file or the complete new file, never a partial write.
//
// On any failure before the rename, the temp file is removed and path is left
// unchanged.
//
// Cleanup is best-effort: on an error path it closes then removes the temp file.
// If tmp.Close fails (e.g. NFS or a held file lock), the subsequent os.Remove may
// not reclaim the file; the Close error is folded into the returned error so it is
// not silently swallowed, but the orphan may persist until the OS releases it
// (cleanupOrphanTempFiles handles such leftovers on a later run).
//
// atomicWrite does not log. All write, sync, chmod, rename, and cleanup-close
// failures are returned to the caller, which is responsible for capturing and
// surfacing them. In this package writeHandler/editHandler wrap the returned
// error via errResultf so it reaches the LLM as a tool result.
func atomicWrite(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, atomicTempPrefix+"*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// cleanup closes and removes the temp file after an error before rename. It
	// returns the close error (if any) so the caller can surface it: a failed
	// Close can itself be the real IO error, and on some filesystems (NFS, or
	// when a lock is held) the subsequent os.Remove may not reclaim the file.
	cleanup := func() error {
		closeErr := tmp.Close()
		_ = os.Remove(tmpPath)
		return closeErr
	}

	if _, err := tmp.Write(content); err != nil {
		if closeErr := cleanup(); closeErr != nil {
			return fmt.Errorf("write temp file: %w (cleanup close: %v)", err, closeErr)
		}
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		if closeErr := cleanup(); closeErr != nil {
			return fmt.Errorf("sync temp file: %w (cleanup close: %v)", err, closeErr)
		}
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// targetPerm returns the permission bits to apply to path. If path already exists
// its current mode is preserved; otherwise fallback is used.
func targetPerm(path string, fallback os.FileMode) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return fallback
}

// cleanupOrphanTempFiles removes leftover atomic-write temp files directly in dir.
// These are left behind only if a process crashed mid-write. It returns the number
// of files removed. Per-file remove errors are ignored (best-effort); only a failure
// to read the directory is returned.
func cleanupOrphanTempFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), atomicTempPrefix) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}
