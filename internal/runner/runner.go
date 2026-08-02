package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/namespace"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/recording"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/valkey"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/orakel"
	securitycontextUtil "github.com/orakel-of-funk/orakel-of-funk-operator/pkg/util/securitycontext"
	workloadUtil "github.com/orakel-of-funk/orakel-of-funk-operator/pkg/util/workload"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/workloadhardeningcheck"
)

type WorkloadCheckRunner struct {
	client.Client

	valKeyClient *valkey.ValkeyClient
	logger       logr.Logger
	recorder     record.EventRecorder

	workloadHardeningCheck *workloadhardeningcheck.WorkloadHardeningCheck
	checkType              string
	conditionType          string
	targetNamespaceName    string

	checkSuccessful bool
}

// Required to convert "user" to "User", strings.ToTitle converts each rune to title case not just the first one
var titleCase = cases.Title(language.English)

// NewWorkloadCheckRunner creates a WorkloadCheckRunner with a shared client.
// The client, valKeyClient, and recorder are injected dependencies (not created internally).
func NewWorkloadCheckRunner(
	ctx context.Context,
	cl client.Client,
	valKeyClient *valkey.ValkeyClient,
	recorder record.EventRecorder,
	workloadHardeningCheck *checksv1alpha1.WorkloadHardeningCheck,
	checkType string,
) *WorkloadCheckRunner {

	return NewWorkloadCheckRunnerForNamespace(
		ctx,
		cl,
		valKeyClient,
		recorder,
		workloadHardeningCheck,
		checkType,
		"",
	)

}

// NewWorkloadCheckRunnerForNamespace creates a WorkloadCheckRunner targeting a specific namespace.
// If targetNamespaceName is empty, one will be generated from the WHC spec.
func NewWorkloadCheckRunnerForNamespace(
	ctx context.Context,
	cl client.Client,
	valKeyClient *valkey.ValkeyClient,
	recorder record.EventRecorder,
	workloadHardeningCheck *checksv1alpha1.WorkloadHardeningCheck,
	checkType string,
	targetNamespaceName string,
) *WorkloadCheckRunner {

	log := logf.FromContext(ctx).WithName("CheckRunner").WithValues("checkType", checkType)

	conditionType := titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck
	if strings.Contains(strings.ToLower(checkType), "baseline") {
		conditionType = checksv1alpha1.ConditionTypeBaseline
	}
	if strings.Contains(strings.ToLower(checkType), "final") {
		conditionType = checksv1alpha1.ConditionTypeFinalCheck
	}

	checkRunner := &WorkloadCheckRunner{
		Client:                 cl,
		logger:                 log,
		valKeyClient:           valKeyClient,
		recorder:               recorder,
		workloadHardeningCheck: &workloadhardeningcheck.WorkloadHardeningCheck{Client: cl, WorkloadHardeningCheck: *workloadHardeningCheck.DeepCopy()},
		checkType:              checkType,
		conditionType:          conditionType,
		targetNamespaceName:    targetNamespaceName,
	}

	if checkRunner.targetNamespaceName == "" {
		checkRunner.targetNamespaceName = checkRunner.generateTargetNamespaceName()
	}

	// Override checkRunner with one that also includes the targetNamespace
	checkRunner.logger = checkRunner.logger.WithValues("targetNamespace", checkRunner.targetNamespaceName)

	return checkRunner

}

// Create the target namespace name. It consists of the base namespace, the suffix set on the workload hardening check, and the check type.
func (r *WorkloadCheckRunner) generateTargetNamespaceName() string {
	base := r.workloadHardeningCheck.Namespace
	if len(base) > 45 {
		base = base[:45] // Limit the base namespace to 45 characters to ensure the total length does not exceed 63 characters
	}

	// max length: 63
	// suffix length: 8
	// checkType: variable ~10-30 characters => find abbreviation... for checkType

	namespaceName := strings.ToLower(fmt.Sprintf("%s-%s-%s", base, r.workloadHardeningCheck.Spec.Suffix, r.checkType))

	if len(namespaceName) > 63 {
		return namespaceName[:63] // Ensure the namespace name does not exceed 63 characters
	}

	return namespaceName
}

func (r *WorkloadCheckRunner) namespaceExists(ctx context.Context, namespaceName string) bool {
	targetNs := &corev1.Namespace{}
	err := r.Get(ctx, client.ObjectKey{Name: namespaceName}, targetNs)

	return !apierrors.IsNotFound(err)
}

// createCheckNamespace clones the namespace of the workload hardening check target workload into a new namespace.
func (r *WorkloadCheckRunner) createCheckNamespace(ctx context.Context) error {

	err := namespace.Clone(ctx, r.Client, r.workloadHardeningCheck.Namespace, r.targetNamespaceName, r.workloadHardeningCheck.Spec.Suffix)

	if err != nil {
		r.logger.Error(err, fmt.Sprintf("failed to clone namespace %s", r.workloadHardeningCheck.Namespace))
		return err
	}

	targetNs := &corev1.Namespace{}
	err = r.Get(ctx, client.ObjectKey{Name: r.targetNamespaceName}, targetNs)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.logger.Error(err, "target namespace not found after cloning")
			return fmt.Errorf("target namespace %s not found after cloning", r.targetNamespaceName)
		}
		// Error reading the object - requeue the request.
		r.logger.Error(err, "failed to get target namespace after cloning")
		return fmt.Errorf("failed to get target namespace %s after cloning: %w", r.targetNamespaceName, err)
	}

	// While it would be useful to set the owner reference to the workload hardening check,
	// only cluster wide resources, can claim cluster wide resources as owner.

	return nil

}

func (r *WorkloadCheckRunner) setStatusRunning(ctx context.Context, message string) {
	conditionReason := checksv1alpha1.ReasonCheckRecording
	if r.conditionType == checksv1alpha1.ConditionTypeBaseline {
		conditionReason = checksv1alpha1.ReasonBaselineRecording
	}

	r.workloadHardeningCheck.SetCondition(ctx, metav1.Condition{
		Type:    r.conditionType,
		Status:  metav1.ConditionFalse,
		Reason:  conditionReason,
		Message: message,
	})
}

// setStatusFailed sets the status of the check to failed, and sets the condition to unknown
// Those errors are considered transient, and the check can be retried later
func (r *WorkloadCheckRunner) setStatusFailed(ctx context.Context, message string) {
	conditionReason := checksv1alpha1.ReasonCheckRecordingFailed
	if strings.Contains(r.checkType, "baseline") {
		conditionReason = checksv1alpha1.ReasonBaselineRecordingFailed
	}

	r.workloadHardeningCheck.SetCondition(ctx, metav1.Condition{
		Type:    r.conditionType,
		Status:  metav1.ConditionUnknown,
		Reason:  conditionReason,
		Message: message,
	})

}

func (r *WorkloadCheckRunner) setStatusFinishedFailure(ctx context.Context, message string, failureReason string, workloadRecording recording.WorkloadRecording) {
	conditionReason := checksv1alpha1.ReasonCheckRecordingFailed
	if r.conditionType == checksv1alpha1.ConditionTypeBaseline {
		conditionReason = checksv1alpha1.ReasonBaselineRecordingFailed
	}

	err := r.workloadHardeningCheck.SetCondition(ctx, metav1.Condition{
		Type:    r.conditionType,
		Status:  metav1.ConditionTrue,
		Reason:  conditionReason,
		Message: message,
	})

	if err != nil {
		r.logger.Error(err, "Failed to set condition for check")
	}

	// Load the logs into the log orakel for analysis
	anomalies := make(map[string][]string)
	for container, logs := range workloadRecording.Logs {
		logOrakel := orakel.NewLogOrakel()
		logOrakel.LoadBaseline(logs)
		anomalies[container] = logOrakel.GetTemplates()
	}

	checkRun := checksv1alpha1.CheckRun{
		Name:                 r.checkType,
		RecordingSuccessfull: ptr.To(false),
		CheckSuccessfull:     ptr.To(false),
		SecurityContext:      workloadRecording.SecurityContextConfigurations,
		FailureReason:        failureReason,
		LogAnomalies:         anomalies,
	}

	retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, types.NamespacedName{Name: r.workloadHardeningCheck.Name, Namespace: r.workloadHardeningCheck.Namespace}, &r.workloadHardeningCheck.WorkloadHardeningCheck); err != nil {
			if apierrors.IsNotFound(err) {
				r.logger.Info("WorkloadHardeningCheck not found, skipping status update")
				return nil // If the resource is not found, we can skip the update
			}
			r.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
			return err
		}

		switch r.conditionType {
		case checksv1alpha1.ConditionTypeBaseline:
			if r.workloadHardeningCheck.WorkloadHardeningCheck.Status.BaselineRuns == nil {
				r.workloadHardeningCheck.WorkloadHardeningCheck.Status.BaselineRuns = []*checksv1alpha1.CheckRun{}
			}
			r.workloadHardeningCheck.WorkloadHardeningCheck.Status.BaselineRuns = append(r.workloadHardeningCheck.WorkloadHardeningCheck.Status.BaselineRuns, &checkRun)

		case checksv1alpha1.ConditionTypeFinalCheck:
			r.workloadHardeningCheck.WorkloadHardeningCheck.Status.FinalRun = &checkRun

		default:
			if r.workloadHardeningCheck.WorkloadHardeningCheck.Status.CheckRuns == nil {
				r.workloadHardeningCheck.WorkloadHardeningCheck.Status.CheckRuns = make(map[string]*checksv1alpha1.CheckRun)
			}
			r.workloadHardeningCheck.WorkloadHardeningCheck.Status.CheckRuns[checkRun.Name] = &checkRun
		}

		return r.Status().Update(ctx, &r.workloadHardeningCheck.WorkloadHardeningCheck)
	})

}

func (r *WorkloadCheckRunner) setStatusFinishedSuccessfully(ctx context.Context, message string, securityContext *checksv1alpha1.SecurityContextDefaults) {
	conditionReason := checksv1alpha1.ReasonCheckRecordingFinished
	if r.conditionType == checksv1alpha1.ConditionTypeBaseline {
		conditionReason = checksv1alpha1.ReasonBaselineRecordingFinished
	}

	err := r.workloadHardeningCheck.SetCondition(ctx, metav1.Condition{
		Type:    r.conditionType,
		Status:  metav1.ConditionTrue,
		Reason:  conditionReason,
		Message: message,
	})

	if err != nil {
		r.logger.Error(err, "Failed to set condition for check")
	}

	checkRun := checksv1alpha1.CheckRun{
		Name:                 r.checkType,
		RecordingSuccessfull: ptr.To(true),
		CheckSuccessfull:     ptr.To(r.checkSuccessful),
		SecurityContext:      securityContext,
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := r.Get(ctx, types.NamespacedName{Name: r.workloadHardeningCheck.WorkloadHardeningCheck.Name, Namespace: r.workloadHardeningCheck.WorkloadHardeningCheck.Namespace}, &r.workloadHardeningCheck.WorkloadHardeningCheck); err != nil {
			if apierrors.IsNotFound(err) {
				// workloadHardeningCheck resource was deleted, while a check was running
				r.logger.Info("WorkloadHardeningCheck not found, skipping status update")
				return nil // If the resource is not found, we can skip the update
			}
			r.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
		}

		// Baseline and final checks are handled differently
		switch r.conditionType {
		case checksv1alpha1.ConditionTypeBaseline:
			// Set/Update condition for baseline check
			if r.workloadHardeningCheck.WorkloadHardeningCheck.Status.BaselineRuns == nil {
				r.workloadHardeningCheck.WorkloadHardeningCheck.Status.BaselineRuns = []*checksv1alpha1.CheckRun{}
			}

			r.workloadHardeningCheck.WorkloadHardeningCheck.Status.BaselineRuns = append(r.workloadHardeningCheck.WorkloadHardeningCheck.Status.BaselineRuns, &checkRun)

		case checksv1alpha1.ConditionTypeFinalCheck:
			r.workloadHardeningCheck.WorkloadHardeningCheck.Status.FinalRun = &checkRun
		default:

			// Set/Update condition
			if r.workloadHardeningCheck.WorkloadHardeningCheck.Status.CheckRuns == nil {
				r.workloadHardeningCheck.WorkloadHardeningCheck.Status.CheckRuns = make(map[string]*checksv1alpha1.CheckRun)
			}
			r.workloadHardeningCheck.WorkloadHardeningCheck.Status.CheckRuns[checkRun.Name] = &checkRun
		}
		return r.Status().Update(ctx, &r.workloadHardeningCheck.WorkloadHardeningCheck)

	})

	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update WorkloadHardeningCheck status after recording finished")
		r.recorder.Event(
			r.workloadHardeningCheck,
			corev1.EventTypeWarning,
			conditionReason,
			fmt.Sprintf("Failed to update WorkloadHardeningCheck status after recording finished: %s", err.Error()),
		)
		return
	}

	r.recorder.Event(
		r.workloadHardeningCheck,
		corev1.EventTypeNormal,
		conditionReason,
		fmt.Sprintf(
			"Recorded %sCheck in namespace %s",
			r.checkType,
			r.targetNamespaceName,
		),
	)
}

func (r *WorkloadCheckRunner) RunCheck(ctx context.Context, securityContext *checksv1alpha1.SecurityContextDefaults) {

	// Check if the target namespace already exists, otherwise create it
	if r.namespaceExists(ctx, r.targetNamespaceName) {
		if meta.IsStatusConditionPresentAndEqual(
			r.workloadHardeningCheck.WorkloadHardeningCheck.Status.Conditions,
			r.conditionType,
			metav1.ConditionUnknown,
		) {
			r.logger.Info(
				"Target namespace already exists, and check is in unknown state indicating a previous run was not finished",
			)

			namespace.Delete(ctx, r.Client, r.targetNamespaceName)
		}

	}

	if !r.namespaceExists(ctx, r.targetNamespaceName) {

		// clone into target namespace
		err := r.createCheckNamespace(ctx)
		if err != nil {
			r.logger.Error(err, "failed to create target namespace for baseline recording")
			return
		}

		r.logger.Info("created namespace")

		time.Sleep(1 * time.Second) // Give the namespace some time to be fully created and ready
	}

	// Set condition to false, as we are about to start the check
	r.setStatusRunning(ctx, "Start target workload with updated security context")

	// Fetch the workload we want to test, make sure we fetch it from the target namespace
	workloadUnderTest, err := r.workloadHardeningCheck.GetWorkloadUnderTest(ctx, r.targetNamespaceName)
	if err != nil {
		r.logger.Error(err, "failed to get workload under test")
		r.setStatusFailed(ctx, "Failed to get workload under test")
		return
	}

	// Eg. Baseline checks, or final checks if nothing need to be applied
	if securityContext.IsEmpty() {
		r.logger.V(2).Info("Security context is empty, nothing to apply")
	} else {
		err = r.applySecurityContext(ctx, workloadUnderTest, securityContext)
		if err != nil {
			r.logger.Error(err, "failed to apply security context to workload under test")
			r.setStatusFailed(ctx, "Failed to apply security context to workload under test")
			return
		}
	}

	// We need to ensure that the pods are updated and scheduled to nodes before we can record metrics
	// they don't need to be started/running, as they could be in a crash loop
	updated, err := r.waitForUpdatedPods(ctx, workloadUnderTest)
	if err != nil || !updated {
		r.logger.Error(err, "failed to wait for updated pods")
		r.setStatusFailed(ctx, "Failed to wait for updated pods")
		conditionReason := checksv1alpha1.ReasonCheckRecordingFailed
		if r.conditionType == checksv1alpha1.ConditionTypeBaseline {
			conditionReason = checksv1alpha1.ReasonBaselineRecordingFailed
		}
		r.recorder.Event(
			r.workloadHardeningCheck,
			corev1.EventTypeWarning,
			conditionReason,
			fmt.Sprintf(
				"%sCheck: Failed to wait for updated pods in namespace %s",
				r.checkType,
				r.targetNamespaceName,
			),
		)
		return
	}

	r.logger.Info("workload is updated and ready for recording")
	r.setStatusRunning(ctx, "Recording metrics")

	startTime := metav1.Now()

	labelSelector, _ := r.workloadHardeningCheck.GetLabelSelector(ctx)

	// start recording metrics for target workload
	recordedMetrics, err := r.recordMetrics(ctx, r.targetNamespaceName, labelSelector)

	if err != nil {
		r.logger.Error(err, "failed to record metrics")
		r.setStatusFailed(ctx, "Failed to record metrics")
		return
	}

	// Record logs for the workload, since recordMetrics only returns after the duration is reached, we can asusme that we get the full logs here
	logs, err := r.recordLogs(ctx, r.targetNamespaceName, labelSelector, false) // false means we want the current logs, not the previous ones
	if err != nil {
		r.logger.Error(err, "failed to record logs")
		r.setStatusFailed(ctx, "Failed to record logs")
		return
	}

	// If pods are crashLooping, we still want to record the metrics and logs, but we will mark the check as unsuccessful
	r.checkSuccessful, _ = workloadUtil.VerifyReadiness(workloadUnderTest, r.Client)

	workloadRecording := recording.WorkloadRecording{
		Type:            r.checkType,
		PodStateRunning: r.checkSuccessful,
		StartTime:       startTime,
		EndTime:         metav1.Now(),

		RecordedMetrics:               recordedMetrics,
		SecurityContextConfigurations: securityContext,
		Logs:                          logs,
	}

	err = r.valKeyClient.StoreRecording(
		ctx,
		// prefix with original namespace to avoid conflict if suffix is reused
		r.workloadHardeningCheck.GetNamespace()+":"+r.workloadHardeningCheck.Spec.Suffix,
		&workloadRecording,
	)

	if err != nil {
		r.logger.Error(err, "failed to store workload recording in Valkey")
		r.setStatusFailed(ctx, "Failed to store workload recording in Valkey")
		conditionReason := checksv1alpha1.ReasonCheckRecordingFailed
		if r.conditionType == checksv1alpha1.ConditionTypeBaseline {
			conditionReason = checksv1alpha1.ReasonBaselineRecordingFailed
		}
		r.recorder.Event(
			r.workloadHardeningCheck,
			corev1.EventTypeWarning,
			conditionReason,
			fmt.Sprintf(
				"Failed to store %sCheck recording in Valkey",
				r.checkType,
			),
		)
		return
	}

	r.logger.Info("recorded signals")

	if r.checkSuccessful {
		r.setStatusFinishedSuccessfully(ctx, "Check finished successfully", securityContext)
	} else {
		r.setStatusFinishedFailure(ctx, "Signal recording failed", "Workload never became ready", workloadRecording)
	}

	// Cleanup: delete the check namespace after recording
	err = namespace.Delete(ctx, r.Client, r.targetNamespaceName)
	if err != nil {
		r.logger.Error(err, "failed to delete target namespace after recording")
	} else {
		r.logger.Info("deleted target namespace after recording")
	}
}

// waits for pods to be updated and scheduled to nodes, they don't need to be started/running, as they could be in a crash loop if the security context is too strict
func (r *WorkloadCheckRunner) waitForUpdatedPods(ctx context.Context, workloadUnderTest *client.Object) (bool, error) {
	// We need to ensure that the pods are updated and scheduled to nodes before we can record metrics, they don't need to be started/running, as they could be in a crash loop
	updated := false
	startTime := metav1.Now()
	targetNamespace := (*workloadUnderTest).GetNamespace()
	for !updated {
		r.Get(ctx, types.NamespacedName{Namespace: targetNamespace, Name: (*workloadUnderTest).GetName()}, *workloadUnderTest)
		updated, _ = workloadUtil.VerifyUpdated(*workloadUnderTest)

		// Timeout after 2 minutes if the workload is not updated
		if time.Since(startTime.Time) > 2*time.Minute {
			r.logger.Error(fmt.Errorf("timeout while waiting for workload to be updated"), "timeout while waiting for workload to be updated")
			r.setStatusFailed(ctx, "Timeout while waiting for workload to be updated")

			conditionReason := checksv1alpha1.ReasonCheckRecordingFailed
			if r.conditionType == checksv1alpha1.ConditionTypeBaseline {
				conditionReason = checksv1alpha1.ReasonBaselineRecordingFailed
			}

			r.recorder.Event(
				r.workloadHardeningCheck,
				corev1.EventTypeWarning,
				conditionReason,
				fmt.Sprintf(
					"%sCheck: Timeout while waiting for workloads to be updated in namespace %s",
					r.checkType,
					targetNamespace,
				),
			)

			labelSelector, _ := r.workloadHardeningCheck.GetLabelSelector(ctx)

			// Record the logs of a failed workload update
			logs, err := r.recordLogs(ctx, targetNamespace, labelSelector, true)
			if err != nil {
				r.logger.Error(err, "failed to record logs")
				return false, err
			}

			err = r.valKeyClient.StoreRecording(
				ctx,
				// prefix with original namespace to avoid conflict if suffix is reused
				r.workloadHardeningCheck.GetNamespace()+":"+r.workloadHardeningCheck.Spec.Suffix,
				&recording.WorkloadRecording{
					Type:            r.checkType,
					PodStateRunning: false,
					StartTime:       startTime,
					EndTime:         metav1.Now(),
					Logs:            logs,
				},
			)
			if err != nil {
				r.logger.Error(err, "failed to store workload recording in Valkey")
			}

			err = namespace.Delete(ctx, r.Client, targetNamespace)
			if err != nil {
				r.logger.Error(err, "failed to delete target namespace with failed workload")
			}

			return false, err
		}
		if !updated {
			r.logger.V(2).Info("workload is not updated yet, waiting for it to be ready")
			time.Sleep(5 * time.Second) // Wait for 5 seconds before checking again
		}
	}

	return true, nil
}

// Modifies the target workload to apply the security context built for the check
// Scales down the workload to 0 replicas befor applying the securityContext to make sure no pods are running with the old configuration
// Scales the workload back to the original replica count after applying the security context, except for Daemonsets which can't be scaled
func (r *WorkloadCheckRunner) applySecurityContext(ctx context.Context, workloadUnderTest *client.Object, securityContext *checksv1alpha1.SecurityContextDefaults) error {

	originalReplicaCount := int32(0)
	var err error
	// Daemonsets can't be scaled down, so we just apply the security context to them
	if strings.ToLower(r.workloadHardeningCheck.Spec.TargetRef.Kind) != "daemonset" {

		originalReplicaCount, err = r.workloadHardeningCheck.GetReplicaCount(ctx, (*workloadUnderTest).GetNamespace())
		// If there's an error getting the replica count, we log it but continue without scaling down
		if err != nil {
			r.logger.Error(err, "failed to get replica count for workload under test")
		}
		if originalReplicaCount > 0 && err == nil {
			r.logger.V(1).Info("Scaling target workload to 0", "originalReplicaCount", originalReplicaCount)
			r.workloadHardeningCheck.ScaleWorkloadUnderTest(ctx, (*workloadUnderTest).GetNamespace(), 0)
		}

	}

	if securityContext != nil {
		r.logger.V(1).Info("applying security context to workload under test")

		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {

			// Re-fetch the workload under test to ensure we have the latest state
			if err := r.Get(ctx, types.NamespacedName{Namespace: (*workloadUnderTest).GetNamespace(), Name: (*workloadUnderTest).GetName()}, *workloadUnderTest); err != nil {
				if apierrors.IsNotFound(err) {
					// workloadHardeningCheck resource was deleted, while a check was running
					r.logger.Info("WorkloadHardeningCheck not found, skipping check run update")
					return err // If the resource is not found, we can skip the update
				}
				r.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
				return fmt.Errorf("failed to re-fetch WorkloadHardeningCheck: %w", err)

			}

			err := securitycontextUtil.ApplyCheckSecurityContext(ctx, workloadUnderTest, securityContext.Container, securityContext.Pod)
			if err != nil {
				r.logger.Error(err, "failed to apply security context to workload under test")

				return fmt.Errorf("failed to apply security context to workload under test: %w", err)
			}

			return r.Update(ctx, *workloadUnderTest)

		})

		if err != nil {
			r.logger.Error(err, "failed to update workload under test with security context")
			return err
		}

		r.logger.V(2).Info("applied security context to workload under test")
	}

	// Scale the workload under test to the original replica count
	if strings.ToLower(r.workloadHardeningCheck.Spec.TargetRef.Kind) != "daemonset" && originalReplicaCount > 0 {
		r.logger.V(1).Info("scaling workload to original replica count", "replicaCount", originalReplicaCount)
		err = r.workloadHardeningCheck.ScaleWorkloadUnderTest(ctx, (*workloadUnderTest).GetNamespace(), originalReplicaCount)
		if err != nil {
			r.logger.Error(err, "failed to scale workload under test to original replica count")
			return err
		}
	}

	return nil
}

// Records the metrics of the currently running pods matching the label selector in the target namespace
// If the workload is crashlooping, the metrics will still be recorded, but the results may be incomplete
func (r *WorkloadCheckRunner) recordMetrics(ctx context.Context, targetNamespace string, labelSelector labels.Selector) ([]*recording.ResourceUsageRecord, error) {

	time.Sleep(2 * time.Second) // Give the workload some time to be ready with the updated security context

	metricsRecorder := recording.NewMetricsRecorder(
		ctx,
		r.Client,
	)

	return metricsRecorder.RecordMetrics(
		ctx,
		targetNamespace,
		labelSelector,
		r.workloadHardeningCheck.GetCheckDuration(),
	)
}

// Record logs of the pods matching the label selector in the target namespace
// previous indicates whether to record the previous logs (before the last restart) or the current logs
func (r *WorkloadCheckRunner) recordLogs(ctx context.Context, targetNamespace string, labelSelector labels.Selector, previous bool) (map[string][]string, error) {

	podLogRecorder := recording.NewPodLogRecorder(ctx, r.Client)

	return podLogRecorder.RecordLogs(
		ctx,
		targetNamespace,
		labelSelector,
		previous,
	)

}
