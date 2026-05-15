package bridge

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventDedup_SeenAndRecord(t *testing.T) {
	d := NewEventDedup()

	require.False(t, d.Seen("s1", "evt-1"))

	d.Record("s1", "evt-1")
	require.True(t, d.Seen("s1", "evt-1"))
	require.False(t, d.Seen("s1", "evt-2"))

	// Different session
	require.False(t, d.Seen("s2", "evt-1"))
}

func TestEventDedup_EmptyEventID(t *testing.T) {
	d := NewEventDedup()

	// Empty event IDs are never "seen" and recording them is a no-op
	d.Record("s1", "")
	require.False(t, d.Seen("s1", ""))
}

func TestEventDedup_RingBufferOverflow(t *testing.T) {
	d := NewEventDedup()

	// Fill the buffer past capacity
	for i := 0; i < dedupBufferSize+10; i++ {
		d.Record("s1", string(rune('A'+i)))
	}

	// The earliest entries should be evicted
	// Entry 0 ('A') was overwritten by entry 256+0
	require.False(t, d.Seen("s1", string(rune('A'))))

	// Recent entries should still be present
	last := string(rune('A' + dedupBufferSize + 9))
	require.True(t, d.Seen("s1", last))
}

func TestEventDedup_Drop(t *testing.T) {
	d := NewEventDedup()

	d.Record("s1", "evt-1")
	require.True(t, d.Seen("s1", "evt-1"))

	d.Drop("s1")
	require.False(t, d.Seen("s1", "evt-1"))
}

func TestRingBuffer_Contains(t *testing.T) {
	r := newRingBuffer(4)

	r.Add("a")
	r.Add("b")
	r.Add("c")

	require.True(t, r.Contains("a"))
	require.True(t, r.Contains("b"))
	require.True(t, r.Contains("c"))
	require.False(t, r.Contains("d"))

	// Fill and overflow
	r.Add("d")
	r.Add("e") // overwrites "a"

	require.False(t, r.Contains("a"))
	require.True(t, r.Contains("b"))
	require.True(t, r.Contains("e"))
}
