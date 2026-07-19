package executor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// CheckRunID uniquely identifies a check run for deduplication.
// Format: "{namespace}/{whcName}:{checkType}"
type CheckRunID string

// NewCheckRunID constructs a CheckRunID from its components.
func NewCheckRunID(namespace, whcName, checkType string) CheckRunID {
	return CheckRunID(fmt.Sprintf("%s/%s:%s", namespace, whcName, checkType))
}

// ParseCheckRunID extracts namespace, WHC name, and check type from a CheckRunID.
func ParseCheckRunID(id CheckRunID) (namespace, whcName, checkType string, err error) {
	s := string(id)
	slashIdx := strings.Index(s, "/")
	if slashIdx < 0 {
		return "", "", "", fmt.Errorf("invalid CheckRunID format: missing '/': %s", s)
	}
	colonIdx := strings.LastIndex(s, ":")
	if colonIdx < 0 || colonIdx <= slashIdx {
		return "", "", "", fmt.Errorf("invalid CheckRunID format: missing ':': %s", s)
	}
	namespace = s[:slashIdx]
	whcName = s[slashIdx+1 : colonIdx]
	checkType = s[colonIdx+1:]
	return namespace, whcName, checkType, nil
}

// HasPrefix returns true if the CheckRunID starts with the given prefix.
// Useful for cancelling all runs belonging to a specific WHC.
func (id CheckRunID) HasPrefix(prefix string) bool {
	return strings.HasPrefix(string(id), prefix)
}

// activeRun tracks a single running check execution.
type activeRun struct {
	cancel context.CancelFunc
}

// CheckExecutor manages the lifecycle of long-running check executions.
// It implements manager.Runnable (Start(ctx) error) and provides:
// - Deduplication of submissions by CheckRunID
// - Panic recovery for all goroutines
// - Graceful shutdown tied to the controller manager's lifecycle
// - Per-run context cancellation
type CheckExecutor struct {
	mu         sync.RWMutex
	activeRuns map[CheckRunID]*activeRun
	wg         sync.WaitGroup

	// managerCtx is set when Start is called; it lives as long as the operator.
	managerCtx context.Context

	logger logr.Logger

	// shutdownTimeout is how long to wait for active runs on shutdown.
	shutdownTimeout time.Duration

	// started signals that Start has been called and managerCtx is available.
	started chan struct{}
}

// NewCheckExecutor creates a new CheckExecutor.
func NewCheckExecutor() *CheckExecutor {
	return &CheckExecutor{
		activeRuns:      make(map[CheckRunID]*activeRun),
		shutdownTimeout: 5 * time.Minute,
		started:         make(chan struct{}),
		logger:          logf.Log.WithName("CheckExecutor"),
	}
}

// Start implements manager.Runnable. It stores the manager context,
// then blocks until the context is cancelled (operator shutdown).
// On shutdown, it waits for active goroutines to complete (with timeout).
func (e *CheckExecutor) Start(ctx context.Context) error {
	e.managerCtx = ctx
	close(e.started) // signal that the executor is ready to accept submissions

	e.logger.Info("CheckExecutor started")

	// Block until the manager context is done (operator shutting down)
	<-ctx.Done()

	e.logger.Info("CheckExecutor stopping, waiting for active runs to complete",
		"activeRuns", e.ActiveRunCount(),
		"timeout", e.shutdownTimeout.String(),
	)

	// Wait for active goroutines to finish, with a timeout
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		e.logger.Info("All active runs completed")
	case <-time.After(e.shutdownTimeout):
		e.logger.Info("Shutdown timeout reached, some runs may not have completed",
			"remainingRuns", e.ActiveRunCount(),
		)
	}

	return nil
}

// Submit submits a function for execution, identified by the given CheckRunID.
// If a run with the same ID is already active, the submission is skipped (deduplicated).
// The function receives a context that is cancelled when either the manager shuts down
// or Cancel(id) is called.
// Returns true if the function was submitted, false if it was deduplicated.
func (e *CheckExecutor) Submit(id CheckRunID, fn func(ctx context.Context)) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.activeRuns[id]; exists {
		e.logger.V(1).Info("Submission deduplicated, run already active", "checkRunID", id)
		return false
	}

	// Create a cancellable context derived from the manager context
	runCtx, cancel := context.WithCancel(e.managerCtx)

	e.activeRuns[id] = &activeRun{
		cancel: cancel,
	}

	e.wg.Add(1)
	go e.safeRun(runCtx, id, fn)

	e.logger.Info("Check run submitted", "checkRunID", id)
	return true
}

// IsRunning returns true if a run with the given ID is currently active.
func (e *CheckExecutor) IsRunning(id CheckRunID) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, exists := e.activeRuns[id]
	return exists
}

// Cancel cancels the context of a specific run, signaling it to stop.
func (e *CheckExecutor) Cancel(id CheckRunID) {
	e.mu.RLock()
	run, exists := e.activeRuns[id]
	e.mu.RUnlock()

	if exists {
		e.logger.Info("Cancelling check run", "checkRunID", id)
		run.cancel()
	}
}

// CancelByPrefix cancels all runs whose ID starts with the given prefix.
// Useful for cancelling all runs belonging to a specific WHC (prefix = "namespace/name:").
func (e *CheckExecutor) CancelByPrefix(prefix string) {
	e.mu.RLock()
	var toCancel []CheckRunID
	for id := range e.activeRuns {
		if id.HasPrefix(prefix) {
			toCancel = append(toCancel, id)
		}
	}
	e.mu.RUnlock()

	for _, id := range toCancel {
		e.Cancel(id)
	}
}

// ActiveRunCount returns the number of currently active runs.
func (e *CheckExecutor) ActiveRunCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.activeRuns)
}

// WaitUntilStarted blocks until the executor's Start method has been called.
// This is useful for components that need to submit work but may be initialized
// before the manager starts.
func (e *CheckExecutor) WaitUntilStarted() {
	<-e.started
}

// safeRun executes the function with panic recovery and cleanup.
func (e *CheckExecutor) safeRun(ctx context.Context, id CheckRunID, fn func(ctx context.Context)) {
	defer e.wg.Done()
	defer e.removeRun(id)
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error(fmt.Errorf("panic: %v", r), "Recovered panic in check run", "checkRunID", id)
		}
	}()

	fn(ctx)
}

// removeRun removes a run from the active set and cancels its context.
func (e *CheckExecutor) removeRun(id CheckRunID) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if run, exists := e.activeRuns[id]; exists {
		run.cancel() // ensure context resources are freed
		delete(e.activeRuns, id)
		e.logger.V(1).Info("Check run completed and removed", "checkRunID", id)
	}
}
