package phase

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPRMetrics_RecordAndRead(t *testing.T) {
	r := require.New(t)

	m := &PRMetrics{}

	r.Equal(int64(0), m.PushFailures())
	r.Equal(int64(0), m.InvalidURLResults())

	m.RecordPushFailure()
	m.RecordPushFailure()
	m.RecordInvalidURLResult()

	r.Equal(int64(2), m.PushFailures())
	r.Equal(int64(1), m.InvalidURLResults())
}

func TestPRMetrics_Snapshot(t *testing.T) {
	r := require.New(t)

	m := &PRMetrics{}
	m.RecordPushFailure()
	m.RecordInvalidURLResult()
	m.RecordInvalidURLResult()

	snap := m.Snapshot()
	r.Equal(int64(1), snap.PushFailures)
	r.Equal(int64(2), snap.InvalidURLResults)

	// Snapshot is a copy — further increments don't affect it.
	m.RecordPushFailure()
	r.Equal(int64(1), snap.PushFailures)
	r.Equal(int64(2), m.PushFailures())
}

func TestPRMetrics_Reset(t *testing.T) {
	r := require.New(t)

	m := &PRMetrics{}
	m.RecordPushFailure()
	m.RecordInvalidURLResult()
	r.Equal(int64(1), m.PushFailures())

	m.Reset()
	r.Equal(int64(0), m.PushFailures())
	r.Equal(int64(0), m.InvalidURLResults())
}

func TestPRMetrics_ConcurrentAccess(t *testing.T) {
	r := require.New(t)

	m := &PRMetrics{}
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.RecordPushFailure()
		}()
		go func() {
			defer wg.Done()
			m.RecordInvalidURLResult()
		}()
	}
	wg.Wait()

	r.Equal(int64(100), m.PushFailures())
	r.Equal(int64(100), m.InvalidURLResults())
}

func TestPRMetricsInstance_ReturnsSingleton(t *testing.T) {
	r := require.New(t)

	a := PRMetricsInstance()
	b := PRMetricsInstance()
	r.Same(a, b)
}
