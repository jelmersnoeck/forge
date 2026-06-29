package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAtomicWrite(t *testing.T) {
	t.Run("writes content atomically", func(t *testing.T) {
		r := require.New(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "troy.txt")

		r.NoError(atomicWrite(path, []byte("Troy Barnes"), 0644))

		data, err := os.ReadFile(path)
		r.NoError(err)
		r.Equal("Troy Barnes", string(data))
	})

	t.Run("leaves no temp files on success", func(t *testing.T) {
		r := require.New(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "abed.txt")

		r.NoError(atomicWrite(path, []byte("Abed Nadir"), 0644))

		entries, err := os.ReadDir(dir)
		r.NoError(err)
		r.Len(entries, 1)
		r.Equal("abed.txt", entries[0].Name())
	})

	t.Run("applies requested permissions", func(t *testing.T) {
		r := require.New(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "chang.txt")

		r.NoError(atomicWrite(path, []byte("Señor Chang"), 0600))

		info, err := os.Stat(path)
		r.NoError(err)
		r.Equal(os.FileMode(0600), info.Mode().Perm())
	})

	t.Run("empty content yields empty file", func(t *testing.T) {
		r := require.New(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.txt")

		r.NoError(atomicWrite(path, []byte(""), 0644))

		data, err := os.ReadFile(path)
		r.NoError(err)
		r.Len(data, 0)
	})

	t.Run("overwrite replaces content, leaves no orphans", func(t *testing.T) {
		r := require.New(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "dean.txt")
		r.NoError(os.WriteFile(path, []byte("old"), 0644))

		r.NoError(atomicWrite(path, []byte("Dean Pelton approves"), 0644))

		data, err := os.ReadFile(path)
		r.NoError(err)
		r.Equal("Dean Pelton approves", string(data))

		entries, err := os.ReadDir(dir)
		r.NoError(err)
		r.Len(entries, 1)
	})

	t.Run("error in read-only dir leaves original untouched, no temp", func(t *testing.T) {
		r := require.New(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "pierce.txt")
		r.NoError(os.WriteFile(path, []byte("Pierce Hawthorne"), 0644))
		r.NoError(os.Chmod(dir, 0500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

		err := atomicWrite(path, []byte("clobbered"), 0644)
		r.Error(err)

		// Original content preserved.
		data, readErr := os.ReadFile(path)
		r.NoError(readErr)
		r.Equal("Pierce Hawthorne", string(data))
	})

	t.Run("concurrent readers never see partial content", func(t *testing.T) {
		r := require.New(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "britta.txt")
		full := strings.Repeat("Britta is the worst. ", 5000)
		r.NoError(os.WriteFile(path, []byte("seed"), 0644))

		var wg sync.WaitGroup
		stop := make(chan struct{})
		errs := make(chan error, 1)

		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				data, err := os.ReadFile(path)
				if err != nil {
					select {
					case errs <- err:
					default:
					}
					return
				}
				s := string(data)
				// Reader must see a complete known state, never a partial one.
				if s != "seed" && s != full {
					select {
					case errs <- nil:
					default:
					}
					return
				}
			}
		}()

		for i := 0; i < 50; i++ {
			r.NoError(atomicWrite(path, []byte(full), 0644))
		}
		close(stop)
		wg.Wait()

		select {
		case e := <-errs:
			if e != nil {
				r.NoError(e)
			} else {
				t.Fatal("reader observed partial/corrupted content during atomic write")
			}
		default:
		}
	})
}

func TestCleanupOrphanTempFiles(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	// Orphan temp files from a hypothetical crash.
	r.NoError(os.WriteFile(filepath.Join(dir, atomicTempPrefix+"123"), []byte("junk"), 0644))
	r.NoError(os.WriteFile(filepath.Join(dir, atomicTempPrefix+"456"), []byte("junk"), 0644))
	// Real files and a subdir that must survive.
	r.NoError(os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("Greendale"), 0644))
	r.NoError(os.Mkdir(filepath.Join(dir, atomicTempPrefix+"dir"), 0755))

	n, err := cleanupOrphanTempFiles(dir)
	r.NoError(err)
	r.Equal(2, n)

	_, err = os.Stat(filepath.Join(dir, "keep.txt"))
	r.NoError(err)
	_, err = os.Stat(filepath.Join(dir, atomicTempPrefix+"dir"))
	r.NoError(err, "subdirectory matching prefix must not be removed")

	for _, name := range []string{atomicTempPrefix + "123", atomicTempPrefix + "456"} {
		_, err := os.Stat(filepath.Join(dir, name))
		r.True(os.IsNotExist(err))
	}
}

func TestCleanupOrphanTempFilesMissingDir(t *testing.T) {
	r := require.New(t)
	n, err := cleanupOrphanTempFiles(filepath.Join(t.TempDir(), "does-not-exist"))
	r.Error(err)
	r.Equal(0, n)
}
