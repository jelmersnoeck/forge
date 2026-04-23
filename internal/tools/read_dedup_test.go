package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReadDedup(t *testing.T) {
	tests := map[string]struct {
		run func(t *testing.T, dir string)
	}{
		"second read returns stub when file unchanged": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "troy.txt")
				r.NoError(os.WriteFile(path, []byte("Troy Barnes\nAbed Nadir"), 0644))

				state := types.NewReadState()
				ctx := types.ToolContext{ReadState: state}
				input := map[string]any{"file_path": path}
				tool := ReadTool()

				// First read: full content
				res1, err := tool.Handler(input, ctx)
				r.NoError(err)
				r.False(res1.IsError)
				r.Contains(res1.Content[0].Text, "Troy Barnes")

				// State should be populated
				_, ok := state.Get(path)
				r.True(ok)

				// Second read: stub
				res2, err := tool.Handler(input, ctx)
				r.NoError(err)
				r.False(res2.IsError)
				r.Equal(FileUnchangedStub, res2.Content[0].Text)
			},
		},
		"read returns fresh content after file modified on disk": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "britta.txt")
				r.NoError(os.WriteFile(path, []byte("Britta Perry"), 0644))

				state := types.NewReadState()
				ctx := types.ToolContext{ReadState: state}
				input := map[string]any{"file_path": path}
				tool := ReadTool()

				// First read
				_, err := tool.Handler(input, ctx)
				r.NoError(err)

				// Modify file (need to wait 1s for mtime granularity on some filesystems)
				time.Sleep(1100 * time.Millisecond)
				r.NoError(os.WriteFile(path, []byte("Britta is the worst"), 0644))

				// Second read: should get fresh content, not stub
				res, err := tool.Handler(input, ctx)
				r.NoError(err)
				r.False(res.IsError)
				r.Contains(res.Content[0].Text, "Britta is the worst")
				r.NotEqual(FileUnchangedStub, res.Content[0].Text)
			},
		},
		"different offset skips dedup": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "jeff.txt")
				lines := strings.Repeat("Winger speech line\n", 50)
				r.NoError(os.WriteFile(path, []byte(lines), 0644))

				state := types.NewReadState()
				ctx := types.ToolContext{ReadState: state}
				tool := ReadTool()

				// Read from offset 1
				_, err := tool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)

				// Read from offset 10 — different params, no dedup
				res, err := tool.Handler(map[string]any{"file_path": path, "offset": float64(10)}, ctx)
				r.NoError(err)
				r.False(res.IsError)
				r.NotEqual(FileUnchangedStub, res.Content[0].Text)
				r.Contains(res.Content[0].Text, "10\tWinger speech line")
			},
		},
		"different limit skips dedup": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "chang.txt")
				r.NoError(os.WriteFile(path, []byte("Senor Chang\nBen Chang\nKevin"), 0644))

				state := types.NewReadState()
				ctx := types.ToolContext{ReadState: state}
				tool := ReadTool()

				// Read with default limit
				_, err := tool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)

				// Read with explicit limit=1 — different params, no dedup
				res, err := tool.Handler(map[string]any{"file_path": path, "limit": float64(1)}, ctx)
				r.NoError(err)
				r.False(res.IsError)
				r.NotEqual(FileUnchangedStub, res.Content[0].Text)
			},
		},
		"edit invalidates read state": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "annie.txt")
				r.NoError(os.WriteFile(path, []byte("Annie Edison"), 0644))

				state := types.NewReadState()
				ctx := types.ToolContext{ReadState: state}

				// Read the file
				readTool := ReadTool()
				_, err := readTool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				_, ok := state.Get(path)
				r.True(ok)

				// Edit the file
				editTool := EditTool()
				_, err = editTool.Handler(map[string]any{
					"file_path":  path,
					"old_string": "Edison",
					"new_string": "Adderall",
				}, ctx)
				r.NoError(err)

				// State should be invalidated
				_, ok = state.Get(path)
				r.False(ok)

				// Next read should return fresh content
				res, err := readTool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				r.Contains(res.Content[0].Text, "Annie Adderall")
				r.NotEqual(FileUnchangedStub, res.Content[0].Text)
			},
		},
		"write invalidates read state": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "dean.txt")
				r.NoError(os.WriteFile(path, []byte("Dean Pelton"), 0644))

				state := types.NewReadState()
				ctx := types.ToolContext{ReadState: state}

				// Read the file
				readTool := ReadTool()
				_, err := readTool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				_, ok := state.Get(path)
				r.True(ok)

				// Overwrite the file
				writeTool := WriteTool()
				_, err = writeTool.Handler(map[string]any{
					"file_path": path,
					"content":   "Dean-a-ling-a-ling!",
				}, ctx)
				r.NoError(err)

				// State should be invalidated
				_, ok = state.Get(path)
				r.False(ok)

				// Next read should return fresh content
				res, err := readTool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				r.Contains(res.Content[0].Text, "Dean-a-ling-a-ling!")
			},
		},
		"nil ReadState is safe": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "shirley.txt")
				r.NoError(os.WriteFile(path, []byte("That's nice"), 0644))

				ctx := types.ToolContext{} // nil ReadState
				tool := ReadTool()

				// Should work fine — just never dedup
				res1, err := tool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				r.Contains(res1.Content[0].Text, "That's nice")

				res2, err := tool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				r.Contains(res2.Content[0].Text, "That's nice")
				// Both reads return full content (no dedup without state)
				r.NotEqual(FileUnchangedStub, res2.Content[0].Text)
			},
		},
		"images are never deduped": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "human_being.png")
				pngData := []byte{
					0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
					0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
					0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
					0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
					0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
					0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
					0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xDD, 0x8D,
					0xB4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
					0x44, 0xAE, 0x42, 0x60, 0x82,
				}
				r.NoError(os.WriteFile(path, pngData, 0644))

				state := types.NewReadState()
				ctx := types.ToolContext{ReadState: state}
				tool := ReadTool()

				// Read image twice — should get full data both times
				res1, err := tool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				r.Equal("image", res1.Content[0].Type)

				res2, err := tool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				r.Equal("image", res2.Content[0].Type)

				// Images should not appear in read state
				_, ok := state.Get(path)
				r.False(ok)
			},
		},
		"bash-modified file detected via mtime": {
			run: func(t *testing.T, dir string) {
				r := require.New(t)
				path := filepath.Join(dir, "pierce.txt")
				r.NoError(os.WriteFile(path, []byte("Pierce Hawthorne"), 0644))

				state := types.NewReadState()
				ctx := types.ToolContext{ReadState: state}
				tool := ReadTool()

				// First read
				_, err := tool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)

				// Simulate bash modifying the file (no explicit invalidation)
				// Artificially set a stale mtime in the state to simulate
				// the file being modified by an external process.
				entry, _ := state.Get(path)
				entry.MtimeUnix = entry.MtimeUnix - 10
				state.Set(path, entry)

				// Next read should return fresh content (mtime mismatch)
				res, err := tool.Handler(map[string]any{"file_path": path}, ctx)
				r.NoError(err)
				r.Contains(res.Content[0].Text, "Pierce Hawthorne")
				r.NotEqual(FileUnchangedStub, res.Content[0].Text)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			tc.run(t, dir)
		})
	}
}
