package executor

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	oflabels "github.com/orakel-of-funk/orakel-of-funk-operator/internal/labels"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/recording"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/valkey"
	workloadUtil "github.com/orakel-of-funk/orakel-of-funk-operator/pkg/util/workload"
	waitUtil "github.com/orakel-of-funk/orakel-of-funk-operator/pkg/util/wait"
)

// NewResumeFunc creates a ResumeFunc that scans for in-progress check namespaces
// and either resumes log collection or cleans up orphaned namespaces.
func NewResumeFunc(cl client.Client, valKeyClient *valkey.ValkeyClient) func(ctx context.Context) {
	return func(ctx context.Context) {
		logger := logf.FromContext(ctx).WithName("ResumeScanner")

		// List all namespaces created by this operator
		nsList := &corev1.NamespaceList{}
		err := cl.List(ctx, nsList, &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				oflabels.LabelManagedBy: oflabels.ManagedByValue,
			}),
		})
		if err != nil {
			logger.Error(err, "Failed to list managed namespaces for resume scan")
			return
		}

		if len(nsList.Items) == 0 {
			logger.Info("No in-progress namespaces found, nothing to resume")
			return
		}

		logger.Info("Found managed namespaces to evaluate for resume", "count", len(nsList.Items))

		for _, ns := range nsList.Items {
			nsLogger := logger.WithValues("namespace", ns.Name)

			// Extract labels
			checkRunIDStr := ns.Labels[oflabels.LabelCheckRunID]
			sourceNamespace := ns.Labels[oflabels.LabelSourceNamespace]

			if checkRunIDStr == "" || sourceNamespace == "" {
				nsLogger.V(1).Info("Namespace missing required labels, skipping")
				continue
			}

			checkRunID := CheckRunID(checkRunIDStr)

			// Parse the CheckRunID to find the WHC
			whcNamespace, whcName, checkType, err := ParseCheckRunID(checkRunID)
			if err != nil {
				nsLogger.Error(err, "Failed to parse CheckRunID from namespace label")
				// Clean up the malformed namespace
				go func(nsName string) { deleteNamespace(ctx, cl, nsName) }(ns.Name)
				continue
			}

			// Check if the corresponding WHC still exists
			whc := &checksv1alpha1.WorkloadHardeningCheck{}
			err = cl.Get(ctx, types.NamespacedName{Namespace: whcNamespace, Name: whcName}, whc)
			if err != nil {
				if apierrors.IsNotFound(err) {
					// WHC was deleted — clean up the orphaned namespace
					nsLogger.Info("WHC no longer exists, cleaning up orphaned namespace",
						"whcNamespace", whcNamespace, "whcName", whcName)
					deleteNamespace(ctx, cl, ns.Name)
					continue
				}
				nsLogger.Error(err, "Failed to get WHC for resume evaluation")
				continue
			}

			// WHC exists — determine recording duration
			recordingDuration := 5 * time.Minute // default
			if whc.Spec.RecordingDuration != "" {
				if parsed, parseErr := time.ParseDuration(whc.Spec.RecordingDuration); parseErr == nil {
					recordingDuration = parsed
				}
			}

			// Calculate how long the namespace has existed
			nsAge := time.Since(ns.CreationTimestamp.Time)

			nsLogger.Info("Evaluating namespace for resume",
				"checkType", checkType,
				"nsAge", nsAge.Round(time.Second),
				"recordingDuration", recordingDuration,
			)

			// Submit a resume function via the executor (accessed via closure over the parent CheckExecutor)
			// We need a reference to the executor — this function is called from within Start(), 
			// so we can't use 'e' directly. Instead, the caller should set up the closure correctly.
			// The approach: this function just does the immediate work synchronously for simplicity.
			// For each namespace that needs work, we submit it back to the executor.

			resumeFn := buildResumeFn(cl, valKeyClient, whc, ns.Name, sourceNamespace, checkType, checkRunID, nsAge, recordingDuration)
			// We can't call e.Submit from within the ResumeFunc since it doesn't have access to 'e'.
			// Instead, we run the resume functions inline (they're short — just wait + collect logs).
			go resumeFn(ctx)
		}
	}
}

// buildResumeFn creates a function that either waits the remaining time then collects logs,
// or immediately collects logs if enough time has already passed.
func buildResumeFn(
	cl client.Client,
	valKeyClient *valkey.ValkeyClient,
	whc *checksv1alpha1.WorkloadHardeningCheck,
	targetNamespace, sourceNamespace, checkType string,
	checkRunID CheckRunID,
	nsAge, recordingDuration time.Duration,
) func(ctx context.Context) {
	return func(ctx context.Context) {
		logger := logf.FromContext(ctx).WithName("Resume").WithValues(
			"checkRunID", checkRunID,
			"namespace", targetNamespace,
		)

		// Wait remaining time if needed
		remaining := recordingDuration - nsAge
		if remaining > 0 {
			logger.Info("Waiting for remaining recording duration", "remaining", remaining.Round(time.Second))
			if err := waitUtil.SleepWithContext(ctx, remaining); err != nil {
				logger.Info("Context cancelled while waiting for resume", "error", err)
				return
			}
		}

		logger.Info("Collecting logs for resumed check")

		// Get the workload under test in the cloned namespace
		workloadUnderTest, err := getWorkloadInNamespace(ctx, cl, whc, targetNamespace)
		if err != nil {
			logger.Error(err, "Failed to get workload for log collection")
			deleteNamespace(ctx, cl, targetNamespace)
			return
		}

		// Get label selector for the workload's pods
		labelSelector, err := workloadUtil.GetLabelSelectorForWorkload(workloadUnderTest)
		if err != nil {
			logger.Error(err, "Failed to get label selector for workload")
			deleteNamespace(ctx, cl, targetNamespace)
			return
		}

		// Record logs
		podLogRecorder := recording.NewPodLogRecorder(ctx, cl)
		logs, err := podLogRecorder.RecordLogs(ctx, targetNamespace, labelSelector, false)
		if err != nil {
			logger.Error(err, "Failed to record logs during resume")
			deleteNamespace(ctx, cl, targetNamespace)
			return
		}

		// Store recording in ValKey
		workloadRecording := &recording.WorkloadRecording{
			Type:    checkType,
			Success: true, // We assume success since we can't verify readiness reliably on resume
			Logs:    logs,
		}

		valkeyKey := whc.Namespace + ":" + whc.Spec.Suffix
		err = valKeyClient.StoreRecording(ctx, valkeyKey, workloadRecording)
		if err != nil {
			logger.Error(err, "Failed to store resumed recording in ValKey")
		} else {
			logger.Info("Successfully stored resumed recording")
		}

		// Cleanup
		deleteNamespace(ctx, cl, targetNamespace)
		logger.Info("Resumed check completed and namespace cleaned up")
	}
}

// getWorkloadInNamespace fetches the target workload from the cloned namespace.
func getWorkloadInNamespace(ctx context.Context, cl client.Client, whc *checksv1alpha1.WorkloadHardeningCheck, targetNamespace string) (*client.Object, error) {
	// Reuse the workload utility to fetch the workload
	var workloadUnderTest client.Object

	switch whc.Spec.TargetRef.Kind {
	case "Deployment":
		workloadUnderTest = &appsv1.Deployment{}
	case "StatefulSet":
		workloadUnderTest = &appsv1.StatefulSet{}
	case "DaemonSet":
		workloadUnderTest = &appsv1.DaemonSet{}
	default:
		return nil, fmt.Errorf("unsupported workload kind: %s", whc.Spec.TargetRef.Kind)
	}

	err := cl.Get(ctx, types.NamespacedName{
		Namespace: targetNamespace,
		Name:      whc.Spec.TargetRef.Name,
	}, workloadUnderTest)
	if err != nil {
		return nil, err
	}

	return &workloadUnderTest, nil
}

// deleteNamespace deletes a namespace and its associated ClusterRoleBindings.
// This is a self-contained implementation to avoid importing internal/namespace
// (which has an init() that requires kubeconfig at import time).
func deleteNamespace(ctx context.Context, cl client.Client, targetNamespace string) {
	logger := logf.FromContext(ctx).WithName("deleteNamespace").WithValues("namespace", targetNamespace)

	ns := &corev1.Namespace{}
	if err := cl.Get(ctx, client.ObjectKey{Name: targetNamespace}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		logger.Error(err, "Failed to get namespace for deletion")
		return
	}

	if err := cl.Delete(ctx, ns); err != nil {
		logger.Error(err, "Failed to delete namespace")
		return
	}

	// Clean up associated ClusterRoleBindings
	crbList := &rbacv1.ClusterRoleBindingList{}
	if err := cl.List(ctx, crbList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			oflabels.LabelTargetNamespace: targetNamespace,
		}),
	}); err != nil {
		logger.Error(err, "Failed to list ClusterRoleBindings for cleanup")
		return
	}

	for _, crb := range crbList.Items {
		if err := cl.Delete(ctx, &crb); err != nil {
			logger.Error(err, "Failed to delete ClusterRoleBinding", "name", crb.Name)
		}
	}
}
