package namespace

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/namespace"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/recording"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/valkey"
	securitycontextUtil "github.com/orakel-of-funk/orakel-of-funk-operator/pkg/util/securitycontext"
	workloadUtil "github.com/orakel-of-funk/orakel-of-funk-operator/pkg/util/workload"
)

// NamespaceHardeningCheckReconciler reconciles a NamespaceHardeningCheck object
type NamespaceHardeningCheckReconciler struct {
	client.Client

	ValkeyClient *valkey.ValkeyClient
	Scheme       *runtime.Scheme
	Recorder     record.EventRecorder
}

// +kubebuilder:rbac:groups=orakel.ofunk.org,resources=namespacehardeningchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=orakel.ofunk.org,resources=namespacehardeningchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=orakel.ofunk.org,resources=namespacehardeningchecks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NamespaceHardeningCheck object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *NamespaceHardeningCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithName("NamespaceHardeningCheckReconciler")

	// Get Resource
	// Fetch the NamespaceHardeningCheck instance
	nsHardenCheck := &checksv1alpha1.NamespaceHardeningCheck{}
	err := r.Get(ctx, req.NamespacedName, nsHardenCheck)
	if err != nil {
		// If the resource is not found it's usually because it was deleted, we rely on owner references to clean up child resources
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get NamespaceHardeningCheck, requeing")
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, err
	}

	// ConditionFinished is set to true when the reconciliation is finished
	if meta.IsStatusConditionTrue(nsHardenCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinished) {
		logger.Info("NamespaceHardeningCheck is already finished, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Validate the namespace referenced exists => Move to validation webhook
	if nsHardenCheck.Spec.TargetNamespace == "" {
		logger.Error(nil, "NamespaceHardeningCheck has no namespace specified, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Already validated in webhook, but double check here anyway
	targetNamespace := corev1.Namespace{}
	err = r.Get(ctx, client.ObjectKey{Name: nsHardenCheck.Spec.TargetNamespace}, &targetNamespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, "Target namespace for NamespaceHardeningCheck not found, aborting reconciliation")

			r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeFinished,
				Status:  metav1.ConditionFalse,
				Reason:  checksv1alpha1.ReasonTargetNamespaceNotFound,
				Message: "The target namespace for the NamespaceHardeningCheck does not exist",
			})

			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get target namespace for NamespaceHardeningCheck, requeuing")

		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Check if the NamespaceHardeningCheck is already in progress
	if meta.FindStatusCondition(nsHardenCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinished) == nil {
		// Initial run...
		logger.Info("Starting NamespaceHardeningCheck reconciliation", "namespace", nsHardenCheck.Spec.TargetNamespace)
		r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ConditionTypePreparation,
			Message: "Starting NamespaceHardeningCheck reconciliation, preparing to create WorkloadHardeningChecks",
		})
	}

	supportedWorkloadResources := namespace.GetSupportedWorkloadResources(ctx, nsHardenCheck.Spec.TargetNamespace)

	// Filter topLevelResoruces for those compatible with WorkloadHardeningCheck
	if len(supportedWorkloadResources) == 0 {
		logger.Info("No top-level resources found in target namespace, skipping WorkloadHardeningCheck creation",
			"namespace", nsHardenCheck.Spec.TargetNamespace)
		r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ReasonAnalysisFinished,
			Message: "No top-level resources found in target namespace, skipping WorkloadHardeningCheck creation",
		})
		return ctrl.Result{}, nil
	}

	// No baseline recorded yet, let's record a basline for each workload in the target namespace, but in a single namespace
	if meta.FindStatusCondition(nsHardenCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline) == nil {
		logger.Info("Recording baseline for workloads in target namespace", "namespace", nsHardenCheck.Spec.TargetNamespace)
		r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeBaseline,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonBaselineRecording,
			Message: "No baseline recordings yet, creating new ones.",
		})

		go func() {
			wg := sync.WaitGroup{}
			wg.Add(2)
			go func() {
				defer wg.Done()
				err := r.recordAllWorkloads(ctx, nsHardenCheck, "baseline")
				if err != nil {
					logger.Error(err, "Failed to record baseline for workloads in target namespace", "namespace", nsHardenCheck.Spec.TargetNamespace)
				}
			}()
			go func() {
				defer wg.Done()
				err := r.recordAllWorkloads(ctx, nsHardenCheck, "baseline-2")
				if err != nil {
					logger.Error(err, "Failed to record second baseline for workloads in target namespace", "namespace", nsHardenCheck.Spec.TargetNamespace)
				}
			}()
			wg.Wait()
			r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeBaseline,
				Status:  metav1.ConditionTrue,
				Reason:  checksv1alpha1.ReasonBaselineRecordingFinished,
				Message: "Baseline recordings completed for all workloads.",
			})
		}()

		return ctrl.Result{RequeueAfter: GetCheckDuration(nsHardenCheck) + 1*time.Minute}, nil

	}

	if meta.FindStatusCondition(nsHardenCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline) != nil &&
		meta.FindStatusCondition(nsHardenCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline).Status != metav1.ConditionTrue {
		condition := meta.FindStatusCondition(nsHardenCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline)
		if condition != nil && condition.LastTransitionTime.Add(GetCheckDuration(nsHardenCheck)+5*time.Minute).Before(time.Now()) {
			// Baseline recording took too long, we assume it failed
			logger.Error(fmt.Errorf("baseline recording timeout"), "Baseline recordings took too long, marking NamespaceHardeningCheck as failed",
				"namespace", nsHardenCheck.Spec.TargetNamespace)
			r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeFinished,
				Status:  metav1.ConditionTrue,
				Reason:  checksv1alpha1.ReasonFailed,
				Message: "Baseline recordings took too long, marking NamespaceHardeningCheck as failed",
			})
			return ctrl.Result{}, nil
		} else {
			logger.Info("Baseline recordings are still in progress, waiting before creating WorkloadHardeningChecks")

			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}
	}

	workloadChecks := []*checksv1alpha1.WorkloadHardeningCheck{}
	for _, resource := range supportedWorkloadResources {
		logger.Info("Found top-level resource to check", "kind", resource.GetKind(), "name", resource.GetName(), "namespace", nsHardenCheck.Spec.TargetNamespace)
		// Create a WorkloadHardeningCheck for each top-level resource
		workloadCheck, err := r.createWorkloadHardeningCheck(ctx, nsHardenCheck, resource)
		if err != nil {
			logger.Error(err, "Failed to create WorkloadHardeningCheck for top-level resource",
				"kind", resource.GetKind(), "name", resource.GetName(), "namespace", nsHardenCheck.Spec.TargetNamespace)
			r.Recorder.Eventf(nsHardenCheck, corev1.EventTypeWarning, "Failed",
				"Failed to create WorkloadHardeningCheck for %s/%s in namespace %s: %v",
				resource.GetKind(), resource.GetName(), nsHardenCheck.Spec.TargetNamespace, err)

			continue // Skip this resource and continue with the next one
		}
		workloadChecks = append(workloadChecks, workloadCheck)

	}

	if len(workloadChecks) == 0 {
		logger.Info("No WorkloadHardeningChecks created as they already exist",
			"namespace", nsHardenCheck.Spec.TargetNamespace)
	} else {
		r.Recorder.Eventf(nsHardenCheck, corev1.EventTypeNormal, "WorkloadChecksCreated",
			"Created %d WorkloadHardeningChecks for top-level resources in namespace %s",
			len(workloadChecks), nsHardenCheck.Spec.TargetNamespace)

		r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
			Type:   checksv1alpha1.ConditionTypeFinished,
			Status: metav1.ConditionFalse,
			Reason: checksv1alpha1.ReasonWorkloadChecksCreated,
			Message: fmt.Sprintf("Created %d WorkloadHardeningChecks for top-level resources in namespace %s",
				len(workloadChecks), nsHardenCheck.Spec.TargetNamespace),
		})
	}

	// check if all WorkloadHardeningChecks in the namespace are finished
	// If not, we can return and wait for the next reconciliation loop
	workloadCheckList := &checksv1alpha1.WorkloadHardeningCheckList{}
	err = r.List(ctx, workloadCheckList, client.InNamespace(nsHardenCheck.Spec.TargetNamespace))
	if err != nil {
		logger.Error(err, "Failed to list WorkloadHardeningChecks in target namespace", "namespace", nsHardenCheck.Spec.TargetNamespace)
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, err
	}

	// Check if all WorkloadHardeningChecks are finished
	finishedCount := 0
	for _, check := range workloadCheckList.Items {
		if !meta.IsStatusConditionTrue(check.Status.Conditions, checksv1alpha1.ConditionTypeFinished) {
			logger.Info("WorkloadHardeningCheck is not finished", "check", check.Name, "namespace", check.Namespace)
		} else {
			finishedCount++
		}
	}
	// Not all checks are finished, requeue
	if finishedCount != len(workloadCheckList.Items) {
		logger.Info("Not all WorkloadHardeningChecks are finished, waiting for next reconciliation loop",
			"namespace", nsHardenCheck.Spec.TargetNamespace)

		r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonNamespaceInProgress,
			Message: fmt.Sprintf("NamespaceHardeningCheck is in progress, %d/%d finished", finishedCount, len(supportedWorkloadResources)),
		})
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// All WorkloadHardeningChecks are finished, we can run a final check with all workloads hardened at once
	logger.Info("All WorkloadHardeningChecks are finished, creating final check for namespace hardening",
		"namespace", nsHardenCheck.Spec.TargetNamespace)

	recommendations := make(map[string]*checksv1alpha1.Recommendation)
	for _, check := range workloadCheckList.Items {
		if check.Status.Recommendation != nil {
			recommendations[check.Spec.TargetRef.Kind+"/"+check.Spec.TargetRef.Name] = check.Status.Recommendation
		}
	}

	retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-fetch the NamespaceHardeningCheck instance to ensure we have the latest state
		if err := r.Get(ctx, req.NamespacedName, nsHardenCheck); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("NamespaceHardeningCheck not found, skipping finalization")
				return nil // If the resource is not found, we can skip the update
			}
			logger.Error(err, "Failed to re-fetch NamespaceHardeningCheck for finalization")
			return err
		}

		nsHardenCheck.Status.Recommendations = recommendations

		meta.SetStatusCondition(&nsHardenCheck.Status.Conditions, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ConditionTypeFinalCheck,
			Message: "Creating final check for namespace hardening",
		})
		return r.Status().Update(ctx, nsHardenCheck)
	})

	success, err := r.createFinalCheckRun(ctx, nsHardenCheck)
	if err != nil {
		logger.Error(err, "Failed to create final check run for namespace hardening", "namespace", nsHardenCheck.Spec.TargetNamespace)
		r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ConditionTypeFinished,
			Message: fmt.Sprintf("Failed to create final check run for namespace hardening: %v", err),
		})
		return ctrl.Result{}, err
	}

	if success {
		logger.Info("Final check run for namespace hardening completed successfully", "namespace", nsHardenCheck.Spec.TargetNamespace)
		r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ReasonSuccess,
			Message: "Namespace hardening checks completed successfully",
		})
	} else {
		logger.Info("Final check run for namespace hardening failed", "namespace", nsHardenCheck.Spec.TargetNamespace)
		r.SetCondition(ctx, nsHardenCheck, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ReasonFailed,
			Message: "Final check run failed, not all workloads are running successfully",
		})
	}

	return ctrl.Result{}, nil
}

func (r *NamespaceHardeningCheckReconciler) recordAllWorkloads(ctx context.Context, nsHardenCheck *checksv1alpha1.NamespaceHardeningCheck, recordingName string) error {
	logger := logf.FromContext(ctx).WithName("recordAllWorkloads")

	topLevelResources := namespace.GetSupportedWorkloadResources(ctx, nsHardenCheck.Spec.TargetNamespace)

	targetNamespace := nsHardenCheck.Spec.TargetNamespace + "-" + nsHardenCheck.Spec.Suffix + "-" + recordingName
	err := namespace.Clone(ctx, r.Client, nsHardenCheck.Spec.TargetNamespace, targetNamespace, nsHardenCheck.Spec.Suffix)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("Baseline namespace already exists, using it", "namespace", targetNamespace)
			// If the namespace already exists, we can continue with the baseline recording
		} else {
			logger.Error(err, "Failed to clone namespace for baseline recording", "namespace", nsHardenCheck.Spec.TargetNamespace, "baselineNamespace", targetNamespace)
			return err
		}
	}
	logger.Info("Cloned namespace for baseline recording", "sourceNamespace", nsHardenCheck.Spec.TargetNamespace, "baselineNamespace", targetNamespace)

	// Set controller reference to the NamespaceHardeningCheck, to ensure it gets cleaned up automatically
	baselineNamespaceObj := &corev1.Namespace{}
	r.Get(ctx, types.NamespacedName{Name: targetNamespace}, baselineNamespaceObj)
	ctrl.SetControllerReference(nsHardenCheck, baselineNamespaceObj, r.Scheme)
	// Update the namespace with the controller reference
	_ = r.Update(ctx, baselineNamespaceObj)

	time.Sleep(5 * time.Second)

	successChannels := make(map[string]chan *recording.WorkloadRecording)
	errorChannels := make(map[string]chan *error)
	wg := sync.WaitGroup{}

	// Record baseline for each workload in the baseline namespace
	for _, resource := range topLevelResources {
		successChannel := make(chan *recording.WorkloadRecording, 1)
		errorChannel := make(chan *error, 1)

		successChannels[resource.GetKind()+"/"+resource.GetName()] = successChannel
		errorChannels[resource.GetKind()+"/"+resource.GetName()] = errorChannel

		logger.Info("Recording baseline for workload", "kind", resource.GetKind(), "name", resource.GetName(), "namespace", targetNamespace)
		checkRecorder := recording.NewCheckRecorder(
			ctx,
			recordingName,
			targetNamespace,
			checksv1alpha1.TargetReference{Kind: resource.GetKind(), Name: resource.GetName()},
			GetCheckDuration(nsHardenCheck),
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			checkRecorder.Record(ctx, successChannel, errorChannel)
		}()
	}

	wg.Wait()

	// Wait for all baseline recordings to finish
	for _, resource := range topLevelResources {
		select {
		case recording := <-successChannels[resource.GetKind()+"/"+resource.GetName()]:
			logger.Info("Baseline recording completed for workload", "kind", resource.GetKind(), "name", resource.GetName())
			valkeyKey := strings.ToLower(nsHardenCheck.Spec.TargetNamespace + ":" + nsHardenCheck.Spec.Suffix + ":" + resource.GetKind() + "-" + resource.GetName())
			err := r.ValkeyClient.StoreRecording(ctx, valkeyKey, recording)
			if err != nil {
				logger.Error(err, "Failed to store baseline recording in ValKey for workload", "kind", resource.GetKind(), "name", resource.GetName())
			}
		case err := <-errorChannels[resource.GetKind()+"/"+resource.GetName()]:
			logger.Error(*err, "Baseline recording failed for workload", "kind", resource.GetKind(), "name", resource.GetName())
		}

	}

	// Cleanup baseline namespace in the background
	go func() { namespace.Delete(ctx, r.Client, targetNamespace) }()
	return nil
}

func GetCheckDuration(check *checksv1alpha1.NamespaceHardeningCheck) time.Duration {
	// Default to 5 minutes if not specified
	if check.Spec.RecordingDuration == "" {
		return 5 * time.Minute
	}

	// Parse the duration string
	duration, err := time.ParseDuration(check.Spec.RecordingDuration)
	if err != nil {
		return 5 * time.Minute // Fallback to default if parsing fails
	}

	return duration
}

// ToDo: With baseline recordings done from this controller, we can now also compare the final check run with the initial baseline recordings!
func (r *NamespaceHardeningCheckReconciler) createFinalCheckRun(ctx context.Context, nsHardenCheck *checksv1alpha1.NamespaceHardeningCheck) (bool, error) {
	logger := logf.FromContext(ctx).WithName("createFinalCheckRun")

	finalCheckNamespace := nsHardenCheck.Spec.TargetNamespace + "-" + nsHardenCheck.Spec.Suffix + "-final"
	if len(finalCheckNamespace) > 63 {
		finalCheckNamespace = finalCheckNamespace[:63] // Ensure the namespace name is within the 63 character limit
	}

	err := namespace.Clone(ctx, r.Client, nsHardenCheck.Spec.TargetNamespace, finalCheckNamespace, nsHardenCheck.Spec.Suffix)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("Final check namespace already exists, using it", "namespace", finalCheckNamespace)
			// If the namespace already exists, we can continue with the final check
		} else {
			logger.Error(err, "Failed to clone namespace for final check", "namespace", nsHardenCheck.Spec.TargetNamespace, "finalCheckNamespace", finalCheckNamespace)
			return false, err
		}
	}

	finalCheckNamespaceObj := &corev1.Namespace{}
	r.Get(ctx, types.NamespacedName{Name: finalCheckNamespace}, finalCheckNamespaceObj)

	// Set controller reference to the NamespaceHardeningCheck, to ensure it gets cleaned up automatically
	ctrl.SetControllerReference(nsHardenCheck, finalCheckNamespaceObj, r.Scheme)

	logger.Info("Cloned namespace for final check", "sourceNamespace", nsHardenCheck.Spec.TargetNamespace, "finalCheckNamespace", finalCheckNamespace)
	topLevelResources := namespace.GetTopLevelResources(ctx, finalCheckNamespace)

	// Should be caught way earlier, but just in case
	if len(topLevelResources) == 0 {
		logger.Info("No top-level resources found in final check namespace, skipping final check creation",
			"namespace", finalCheckNamespace)
		return true, nil
	}

	// Apply securityContext from recommendations to all relevant resources
	for _, resource := range topLevelResources {
		if workloadUtil.IsSupportedUnstructured(resource) {
			workloadUnderTest, err := r.GetWorkloadUnderTest(ctx, resource)
			if err != nil {
				logger.Error(err, "Failed to get workload under test for final check", "kind", resource.GetKind(), "name", resource.GetName())
				continue // Skip this resource if we can't get the workload
			}
			// Apply security context from recommendations if available
			if recommendation, ok := nsHardenCheck.Status.Recommendations[resource.GetKind()+"/"+resource.GetName()]; ok {
				securitycontextUtil.ApplySecurityContext(ctx, workloadUnderTest, recommendation.ContainerSecurityContexts, recommendation.PodSecurityContext)

				r.Update(ctx, *workloadUnderTest)
				for updated := false; !updated; updated, _ = workloadUtil.VerifyUpdated(*workloadUnderTest) {
					logger.Info("Waiting for workload to be updated with security context", "kind", resource.GetKind(), "name", resource.GetName())
					time.Sleep(5 * time.Second)
					r.Get(ctx, types.NamespacedName{Namespace: (*workloadUnderTest).GetNamespace(), Name: (*workloadUnderTest).GetName()}, *workloadUnderTest)
				}
			} else {
				logger.Info("No recommendation found for resource, skipping security context update", "kind", resource.GetKind(), "name", resource.GetName())
				continue // Skip this resource if no recommendation is found
			}
		}
	}

	// Make sure all resources are successfully running after applying security context
	startTime := metav1.Now()
	success := true
Resources:
	for _, resource := range topLevelResources {
		if workloadUtil.IsSupportedUnstructured(resource) {
			workloadUnderTest, err := r.GetWorkloadUnderTest(ctx, resource)
			if err != nil {
				logger.Error(err, "Failed to get workload under test for final check", "kind", resource.GetKind(), "name", resource.GetName())
				continue // Skip this resource if we can't get the workload
			}

			for running := false; !running; running, _ = workloadUtil.VerifyReadiness(workloadUnderTest, r.Client) {
				logger.Info("Waiting for workload to be running after security context update", "kind", resource.GetKind(), "name", resource.GetName())
				time.Sleep(5 * time.Second)
				r.Get(ctx, types.NamespacedName{Namespace: (*workloadUnderTest).GetNamespace(), Name: (*workloadUnderTest).GetName()}, *workloadUnderTest)
				if time.Since(startTime.Time) > 2*time.Minute {
					logger.Error(nil, "Workload did not become running in time after security context update", "kind", resource.GetKind(), "name", resource.GetName())
					success = false
					r.Recorder.Eventf(nsHardenCheck, corev1.EventTypeWarning, "WorkloadNotRunning",
						"Workload %s/%s did not become running in time after security context update",
						resource.GetKind(), resource.GetName())

					break Resources // Break out of the loop if any workload does not become running in time
				}
			}
		}
	}

	err = namespace.Delete(ctx, r.Client, finalCheckNamespace)
	if err != nil {
		logger.Error(err, "Failed to delete cloned namespace after final check", "namespace", finalCheckNamespace)
		return success, fmt.Errorf("failed to delete cloned namespace %s after final check: %w", finalCheckNamespace, err)
	}
	logger.Info("Deleted cloned namespace after final check", "namespace", finalCheckNamespace)

	return success, nil

}

func (r *NamespaceHardeningCheckReconciler) GetWorkloadUnderTest(ctx context.Context, resource *unstructured.Unstructured) (*client.Object, error) {
	logger := logf.FromContext(ctx).WithName("GetWorkloadUnderTest")

	if !workloadUtil.IsSupportedUnstructured(resource) {
		logger.Error(nil, "Unsupported resource kind for workload under test", "kind", resource.GetKind(), "name", resource.GetName())
		return nil, fmt.Errorf("unsupported resource kind: %s", resource.GetKind())
	}

	var workloadUnderTest client.Object
	switch resource.GetKind() {
	case "Deployment":
		workloadUnderTest = &appsv1.Deployment{}
	case "StatefulSet":
		workloadUnderTest = &appsv1.StatefulSet{}
	case "DaemonSet":
		workloadUnderTest = &appsv1.DaemonSet{}
	}

	if err := r.Get(ctx, client.ObjectKey{Namespace: resource.GetNamespace(), Name: resource.GetName()}, workloadUnderTest); err != nil {
		logger.Error(err, "Failed to get workload under test", "kind", resource.GetKind(), "name", resource.GetName())
		return nil, err
	}

	return &workloadUnderTest, nil
}

func (r *NamespaceHardeningCheckReconciler) createWorkloadHardeningCheck(ctx context.Context, nsHardenCheck *checksv1alpha1.NamespaceHardeningCheck, resource *unstructured.Unstructured) (*checksv1alpha1.WorkloadHardeningCheck, error) {
	logger := logf.FromContext(ctx)

	workloadCheck := &checksv1alpha1.WorkloadHardeningCheck{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      strings.ToLower(resource.GetKind() + "-" + resource.GetName() + "-" + nsHardenCheck.Spec.Suffix),
		Namespace: nsHardenCheck.Spec.TargetNamespace,
	}, workloadCheck); err == nil {
		// WorkloadHardeningCheck already exists, we can skip creating it
		logger.Info("WorkloadHardeningCheck already exists, skipping creation", "workload", resource.GetName(), "namespace", nsHardenCheck.Spec.TargetNamespace)
		return workloadCheck, nil
	} else {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to get existing WorkloadHardeningCheck", "workload", resource.GetName(), "namespace", nsHardenCheck.Spec.TargetNamespace)
			return nil, fmt.Errorf("failed to get existing WorkloadHardeningCheck for %s/%s in namespace %s: %w",
				resource.GetKind(), resource.GetName(), nsHardenCheck.Spec.TargetNamespace, err)
		}

		// If the WorkloadHardeningCheck does not exist, we will create it
		logger.Info("WorkloadHardeningCheck not found, creating new one", "workload", resource.GetName(), "namespace", nsHardenCheck.Spec.TargetNamespace)
	}

	baselineRecordingReference := strings.ToLower(nsHardenCheck.Spec.TargetNamespace + ":" + nsHardenCheck.Spec.Suffix + ":" + resource.GetKind() + "-" + resource.GetName())

	// Create a new WorkloadHardeningCheck for each top-level resource
	workloadCheck = &checksv1alpha1.WorkloadHardeningCheck{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.ToLower(resource.GetKind() + "-" + resource.GetName() + "-" + nsHardenCheck.Spec.Suffix),
			Namespace: nsHardenCheck.Spec.TargetNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       strings.ToLower(resource.GetKind() + "-" + resource.GetName() + "-" + nsHardenCheck.Spec.Suffix),
				"app.kubernetes.io/managed-by": "oracle-of-funk",
				"appkubernetes.io/part-of":     nsHardenCheck.Name,
			},
		},
		Spec: checksv1alpha1.WorkloadHardeningCheckSpec{
			Suffix: nsHardenCheck.Spec.Suffix + "-" + utilrand.String(8), // Generate a random suffix of 8 characters
			TargetRef: checksv1alpha1.TargetReference{
				Kind: resource.GetKind(),
				Name: resource.GetName(),
			},
			RecordingDuration: nsHardenCheck.Spec.RecordingDuration,
			RunMode:           nsHardenCheck.Spec.RunMode,
			SecurityContext:   nsHardenCheck.Spec.SecurityContext.DeepCopy(),
			BaselineRecordingReference: []string{
				baselineRecordingReference + ":baseline",
				baselineRecordingReference + ":baseline-2",
			},
		},
	}

	// set owner reference to the NamespaceHardeningCheck
	if err := ctrl.SetControllerReference(nsHardenCheck, workloadCheck, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference for WorkloadHardeningCheck", "workload", workloadCheck.Spec.TargetRef.Name, "namespace", nsHardenCheck.Spec.TargetNamespace)
	}

	// Create the WorkloadHardeningCheck
	if err := r.Create(ctx, workloadCheck); err != nil {
		logger.Error(err, "Failed to create WorkloadHardeningCheck", "workload", workloadCheck.Spec.TargetRef.Name, "namespace", nsHardenCheck.Spec.TargetNamespace)
		return nil, fmt.Errorf("failed to create WorkloadHardeningCheck for %s/%s in namespace %s: %w",
			workloadCheck.Spec.TargetRef.Kind, workloadCheck.Spec.TargetRef.Name, nsHardenCheck.Spec.TargetNamespace, err)
	}

	logger.Info("Created WorkloadHardeningCheck", "workload", workloadCheck.Spec.TargetRef.Name, "namespace", nsHardenCheck.Spec.TargetNamespace)

	return workloadCheck, nil
}

func (r *NamespaceHardeningCheckReconciler) SetCondition(ctx context.Context, nsHardenCheck *checksv1alpha1.NamespaceHardeningCheck, condition metav1.Condition) error {
	logger := logf.FromContext(ctx).WithName("NamespaceHardeningCheckReconciler")

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := r.Get(ctx, client.ObjectKey{Name: nsHardenCheck.Name}, nsHardenCheck); err != nil {
			if apierrors.IsNotFound(err) {
				// workloadHardeningCheck resource was deleted, while a check was running
				logger.Info("WorkloadHardeningCheck not found, skipping condition update")
				return nil // If the resource is not found, we can skip the update
			}
			logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
			return err
		}

		// Set/Update condition
		meta.SetStatusCondition(
			&nsHardenCheck.Status.Conditions,
			condition,
		)

		return r.Status().Update(ctx, nsHardenCheck)

	})

	return err
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceHardeningCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&checksv1alpha1.NamespaceHardeningCheck{}).
		Owns(&checksv1alpha1.WorkloadHardeningCheck{}).
		// ToDo: Decide if configurable
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		WithEventFilter(ignoreStatusChanges()).
		Named("namespacehardeningcheck").
		Complete(r)
}

func ignoreStatusChanges() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Ignore updates to CR status in which case metadata.Generation does not change
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
		},
	}
}
