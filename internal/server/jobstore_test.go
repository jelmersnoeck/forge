package server

import (
	"testing"
)

// TestJobStoreContract verifies the MemoryJobStore satisfies JobStore.
func TestJobStoreContract_Memory(t *testing.T) {
	var _ JobStore = NewMemoryJobStore()
}
