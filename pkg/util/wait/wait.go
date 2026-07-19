// Package wait provides context-aware polling utilities for waiting on Kubernetes resource states.
package wait

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DefaultPollInterval is the default interval between polling attempts.
	DefaultPollInterval = 5 * time.Second
)

// WaitForPodsAssigned polls until at least one pod matching the selector exists in the namespace
// and all matching pods have been assigned to a node (pod.Spec.NodeName != "").
// Returns the pod list once all pods are assigned, or an error if the timeout is reached or context is cancelled.
func WaitForPodsAssigned(ctx context.Context, cl client.Client, namespace string, selector labels.Selector, timeout time.Duration) (*corev1.PodList, error) {
	var pods *corev1.PodList

	err := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, timeout, true, func(ctx context.Context) (done bool, err error) {
		podList := &corev1.PodList{}
		if err := cl.List(ctx, podList, &client.ListOptions{
			Namespace:     namespace,
			LabelSelector: selector,
		}); err != nil {
			return false, nil // retry on transient errors
		}

		if len(podList.Items) == 0 {
			return false, nil // no pods yet
		}

		for _, pod := range podList.Items {
			if pod.Spec.NodeName == "" {
				return false, nil // not all assigned
			}
		}

		pods = podList
		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("waiting for pods to be assigned in namespace %s: %w", namespace, err)
	}

	return pods, nil
}

// WaitForWorkloadUpdated polls until the workload's UpdatedReplicas matches the desired replica count,
// indicating all pods have been rolled out with the latest spec.
// The checkFn should return (true, nil) when the workload is considered updated.
func WaitForWorkloadUpdated(ctx context.Context, checkFn func(ctx context.Context) (bool, error), timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, timeout, true, func(ctx context.Context) (done bool, err error) {
		return checkFn(ctx)
	})

	if err != nil {
		return fmt.Errorf("waiting for workload to be updated: %w", err)
	}

	return nil
}

// WaitForCondition polls a generic condition function until it returns true or the timeout is reached.
// This is a convenience wrapper around wait.PollUntilContextTimeout.
func WaitForCondition(ctx context.Context, interval, timeout time.Duration, condFn func(ctx context.Context) (bool, error)) error {
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (done bool, err error) {
		return condFn(ctx)
	})
}

// SleepWithContext sleeps for the specified duration but returns early if the context is cancelled.
// Returns ctx.Err() if the context was cancelled, nil otherwise.
func SleepWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
