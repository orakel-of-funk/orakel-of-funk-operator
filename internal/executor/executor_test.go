package executor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startExecutor(t *testing.T) (*CheckExecutor, context.CancelFunc) {
	t.Helper()
	exec := NewCheckExecutor()
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := exec.Start(ctx); err != nil {
			t.Errorf("executor Start returned error: %v", err)
		}
	}()

	// Wait for the executor to be ready
	exec.WaitUntilStarted()
	return exec, cancel
}

func TestCheckRunID(t *testing.T) {
	id := NewCheckRunID("my-namespace", "my-whc", "baseline")
	assert.Equal(t, CheckRunID("my-namespace/my-whc:baseline"), id)

	ns, name, checkType, err := ParseCheckRunID(id)
	require.NoError(t, err)
	assert.Equal(t, "my-namespace", ns)
	assert.Equal(t, "my-whc", name)
	assert.Equal(t, "baseline", checkType)
}

func TestCheckRunID_ParseErrors(t *testing.T) {
	_, _, _, err := ParseCheckRunID(CheckRunID("invalid"))
	assert.Error(t, err)

	_, _, _, err = ParseCheckRunID(CheckRunID("ns/name"))
	assert.Error(t, err)
}

func TestCheckRunID_HasPrefix(t *testing.T) {
	id := NewCheckRunID("ns", "whc", "baseline")
	assert.True(t, id.HasPrefix("ns/whc:"))
	assert.False(t, id.HasPrefix("other/"))
}

func TestSubmit_Deduplication(t *testing.T) {
	exec, cancel := startExecutor(t)
	defer cancel()

	var counter atomic.Int32
	blocker := make(chan struct{})

	id := NewCheckRunID("ns", "whc", "check1")

	// First submission - should execute
	submitted := exec.Submit(id, func(ctx context.Context) {
		counter.Add(1)
		<-blocker // block until released
	})
	assert.True(t, submitted)

	// Give it a moment to start
	time.Sleep(10 * time.Millisecond)

	// Second submission with same ID - should be deduplicated
	submitted = exec.Submit(id, func(ctx context.Context) {
		counter.Add(1)
	})
	assert.False(t, submitted)

	// Release the first run
	close(blocker)
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, int32(1), counter.Load())
	assert.False(t, exec.IsRunning(id))
}

func TestSubmit_AfterCompletion(t *testing.T) {
	exec, cancel := startExecutor(t)
	defer cancel()

	var counter atomic.Int32
	id := NewCheckRunID("ns", "whc", "check1")

	// First run
	exec.Submit(id, func(ctx context.Context) {
		counter.Add(1)
	})
	time.Sleep(50 * time.Millisecond)

	// After completion, same ID can be submitted again
	submitted := exec.Submit(id, func(ctx context.Context) {
		counter.Add(1)
	})
	assert.True(t, submitted)
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, int32(2), counter.Load())
}

func TestSubmit_PanicRecovery(t *testing.T) {
	exec, cancel := startExecutor(t)
	defer cancel()

	id := NewCheckRunID("ns", "whc", "panicker")

	exec.Submit(id, func(ctx context.Context) {
		panic("test panic")
	})

	time.Sleep(50 * time.Millisecond)

	// Executor should still be functional after panic
	assert.False(t, exec.IsRunning(id))
	assert.Equal(t, 0, exec.ActiveRunCount())

	// Can submit new work
	var ran atomic.Bool
	exec.Submit(NewCheckRunID("ns", "whc", "after-panic"), func(ctx context.Context) {
		ran.Store(true)
	})
	time.Sleep(50 * time.Millisecond)
	assert.True(t, ran.Load())
}

func TestCancel(t *testing.T) {
	exec, cancel := startExecutor(t)
	defer cancel()

	var cancelled atomic.Bool
	id := NewCheckRunID("ns", "whc", "cancellable")

	exec.Submit(id, func(ctx context.Context) {
		<-ctx.Done()
		cancelled.Store(true)
	})

	time.Sleep(10 * time.Millisecond)
	assert.True(t, exec.IsRunning(id))

	exec.Cancel(id)
	time.Sleep(50 * time.Millisecond)

	assert.True(t, cancelled.Load())
	assert.False(t, exec.IsRunning(id))
}

func TestCancelByPrefix(t *testing.T) {
	exec, cancel := startExecutor(t)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(3)

	ids := []CheckRunID{
		NewCheckRunID("ns", "whc1", "baseline"),
		NewCheckRunID("ns", "whc1", "user"),
		NewCheckRunID("ns", "whc2", "baseline"),
	}

	for _, id := range ids {
		exec.Submit(id, func(ctx context.Context) {
			<-ctx.Done()
			wg.Done()
		})
	}

	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, 3, exec.ActiveRunCount())

	// Cancel all runs for whc1
	exec.CancelByPrefix("ns/whc1:")

	// Wait for the cancelled ones to finish
	time.Sleep(50 * time.Millisecond)

	// whc2 should still be running
	assert.True(t, exec.IsRunning(ids[2]))
	assert.False(t, exec.IsRunning(ids[0]))
	assert.False(t, exec.IsRunning(ids[1]))

	// Cleanup
	exec.Cancel(ids[2])
	wg.Wait()
}

func TestGracefulShutdown(t *testing.T) {
	exec := NewCheckExecutor()
	exec.shutdownTimeout = 2 * time.Second

	ctx, cancel := context.WithCancel(context.Background())

	startDone := make(chan struct{})
	go func() {
		exec.Start(ctx)
		close(startDone)
	}()
	exec.WaitUntilStarted()

	var completed atomic.Bool
	exec.Submit(NewCheckRunID("ns", "whc", "slow"), func(ctx context.Context) {
		time.Sleep(100 * time.Millisecond)
		completed.Store(true)
	})

	// Trigger shutdown
	cancel()

	// Start should return after the goroutine completes
	select {
	case <-startDone:
		assert.True(t, completed.Load())
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return within timeout")
	}
}

func TestContextCancelledOnManagerShutdown(t *testing.T) {
	exec, cancel := startExecutor(t)

	var gotCancelled atomic.Bool
	exec.Submit(NewCheckRunID("ns", "whc", "check"), func(ctx context.Context) {
		<-ctx.Done()
		gotCancelled.Store(true)
	})

	time.Sleep(10 * time.Millisecond)

	// Simulate manager shutdown
	cancel()
	time.Sleep(50 * time.Millisecond)

	assert.True(t, gotCancelled.Load())
}

func TestMultipleConcurrentSubmissions(t *testing.T) {
	exec, cancel := startExecutor(t)
	defer cancel()

	var counter atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		id := NewCheckRunID("ns", "whc", fmt.Sprintf("check-%d", i))
		exec.Submit(id, func(ctx context.Context) {
			counter.Add(1)
			wg.Done()
		})
	}

	wg.Wait()
	assert.Equal(t, int32(20), counter.Load())
	assert.Equal(t, 0, exec.ActiveRunCount())
}
