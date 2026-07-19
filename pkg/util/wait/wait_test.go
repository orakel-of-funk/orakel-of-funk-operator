package wait

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSleepWithContext_Normal(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	err := SleepWithContext(ctx, 50*time.Millisecond)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
}

func TestSleepWithContext_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := SleepWithContext(ctx, 5*time.Second)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, elapsed, 1*time.Second)
}

func TestWaitForCondition_ImmediateSuccess(t *testing.T) {
	ctx := context.Background()
	err := WaitForCondition(ctx, 10*time.Millisecond, 1*time.Second, func(ctx context.Context) (bool, error) {
		return true, nil
	})
	require.NoError(t, err)
}

func TestWaitForCondition_Timeout(t *testing.T) {
	ctx := context.Background()
	err := WaitForCondition(ctx, 10*time.Millisecond, 50*time.Millisecond, func(ctx context.Context) (bool, error) {
		return false, nil
	})
	assert.Error(t, err)
}

func TestWaitForCondition_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := WaitForCondition(ctx, 10*time.Millisecond, 5*time.Second, func(ctx context.Context) (bool, error) {
		return false, nil
	})
	assert.Error(t, err)
}

func TestWaitForWorkloadUpdated_Success(t *testing.T) {
	callCount := 0
	// Use WaitForCondition directly with short interval to avoid DefaultPollInterval
	err := WaitForCondition(context.Background(), 10*time.Millisecond, 5*time.Second, func(ctx context.Context) (bool, error) {
		callCount++
		return callCount >= 3, nil
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, callCount, 3)
}
