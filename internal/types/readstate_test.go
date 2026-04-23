package types

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadState_ConcurrentAccess(t *testing.T) {
	tests := map[string]struct {
		run func(t *testing.T)
	}{
		"concurrent reads of different files": {
			run: func(t *testing.T) {
				r := require.New(t)
				rs := NewReadState()

				var wg sync.WaitGroup
				for i := range 100 {
					wg.Add(1)
					go func(n int) {
						defer wg.Done()
						path := "/tmp/file" + string(rune('a'+n%26))
						rs.Set(path, ReadFileEntry{MtimeUnix: int64(n), Offset: 1, Limit: 2000})
						_, _ = rs.Get(path)
					}(i)
				}
				wg.Wait()

				// At least some entries should exist
				_, ok := rs.Get("/tmp/filea")
				r.True(ok)
			},
		},
		"concurrent reads of same file": {
			run: func(t *testing.T) {
				r := require.New(t)
				rs := NewReadState()

				entry := ReadFileEntry{MtimeUnix: 1234, Offset: 1, Limit: 2000}
				rs.Set("/tmp/troy.go", entry)

				var wg sync.WaitGroup
				for range 100 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						got, ok := rs.Get("/tmp/troy.go")
						if ok {
							_ = got.MtimeUnix
						}
						// Last-writer-wins: all write the same value
						rs.Set("/tmp/troy.go", entry)
					}()
				}
				wg.Wait()

				got, ok := rs.Get("/tmp/troy.go")
				r.True(ok)
				r.Equal(entry, got)
			},
		},
		"concurrent set and delete": {
			run: func(t *testing.T) {
				rs := NewReadState()

				var wg sync.WaitGroup
				for i := range 100 {
					wg.Add(1)
					go func(n int) {
						defer wg.Done()
						path := "/tmp/abed.go"
						if n%2 == 0 {
							rs.Set(path, ReadFileEntry{MtimeUnix: int64(n)})
						} else {
							rs.Delete(path)
						}
					}(i)
				}
				wg.Wait()
				// No panic = success
			},
		},
		"nil ReadState Get returns zero false": {
			run: func(t *testing.T) {
				r := require.New(t)
				var rs *ReadState
				entry, ok := rs.Get("/tmp/anything")
				r.False(ok)
				r.Equal(ReadFileEntry{}, entry)
			},
		},
		"nil ReadState Set is no-op": {
			run: func(t *testing.T) {
				var rs *ReadState
				rs.Set("/tmp/anything", ReadFileEntry{MtimeUnix: 42})
				// No panic = success
			},
		},
		"nil ReadState Delete is no-op": {
			run: func(t *testing.T) {
				var rs *ReadState
				rs.Delete("/tmp/anything")
				// No panic = success
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.run(t)
		})
	}
}
