package namespace

import (
	"context"
	"fmt"
	"strings"
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
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/workload"
)

// NamespaceHardeningCheckReconciler reconciles a NamespaceHardeningCheck object
type NamespaceHardeningCheckReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
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
	namespaceHardening := &checksv1alpha1.NamespaceHardeningCheck{}
	err := r.Get(ctx, req.NamespacedName, namespaceHardening)
	if err != nil {
		// If the resource is not found it's usually because it was deleted, we need to cleanup remaining resources
		if apierrors.IsNotFound(err) {
			return r.cleanupReconcileLoop(ctx, req.Name)
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get NamespaceHardeningCheck, requeing")
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, err
	}

	// ConditionFinished is set to true when the reconciliation is finished
	if meta.IsStatusConditionTrue(namespaceHardening.Status.Conditions, checksv1alpha1.ConditionTypeFinished) {
		logger.Info("NamespaceHardeningCheck is already finished, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Validate the namespace referenced exists => Move to validation webhook
	if namespaceHardening.Spec.TargetNamespace == "" {
		logger.Error(nil, "NamespaceHardeningCheck has no namespace specified, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	targetNamespace := corev1.Namespace{}
	err = r.Get(ctx, client.ObjectKey{Name: namespaceHardening.Spec.TargetNamespace}, &targetNamespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, "Target namespace for NamespaceHardeningCheck not found, aborting reconciliation")

			r.SetCondition(ctx, namespaceHardening, metav1.Condition{
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
	if meta.FindStatusCondition(namespaceHardening.Status.Conditions, checksv1alpha1.ConditionTypeFinished) == nil {
		// Initial run...
		logger.Info("Starting NamespaceHardeningCheck reconciliation", "namespace", namespaceHardening.Spec.TargetNamespace)
		r.SetCondition(ctx, namespaceHardening, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ConditionTypePreparation,
			Message: "Starting NamespaceHardeningCheck reconciliation, preparing to create WorkloadHardeningChecks",
		})
	}

	topLevelResources := r.getTopLevelResourcesToCheck(ctx, namespaceHardening)

	// Filter topLevelResoruces for those compatible with WorkloadHardeningCheck
	if len(topLevelResources) == 0 {
		logger.Info("No top-level resources found in target namespace, skipping WorkloadHardeningCheck creation",
			"namespace", namespaceHardening.Spec.TargetNamespace)
		r.SetCondition(ctx, namespaceHardening, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ReasonAnalysisFinished,
			Message: "No top-level resources found in target namespace, skipping WorkloadHardeningCheck creation",
		})
		return ctrl.Result{}, nil
	}

	workloadChecks := []*checksv1alpha1.WorkloadHardeningCheck{}
	for _, resource := range topLevelResources {
		logger.Info("Found top-level resource to check", "kind", resource.GetKind(), "name", resource.GetName(), "namespace", namespaceHardening.Spec.TargetNamespace)
		// Create a WorkloadHardeningCheck for each top-level resource
		workloadCheck, err := r.createWorkloadHardeningCheck(ctx, namespaceHardening, resource)
		if err != nil {
			logger.Error(err, "Failed to create WorkloadHardeningCheck for top-level resource",
				"kind", resource.GetKind(), "name", resource.GetName(), "namespace", namespaceHardening.Spec.TargetNamespace)
			r.Recorder.Eventf(namespaceHardening, corev1.EventTypeWarning, "Failed",
				"Failed to create WorkloadHardeningCheck for %s/%s in namespace %s: %v",
				resource.GetKind(), resource.GetName(), namespaceHardening.Spec.TargetNamespace, err)

			continue // Skip this resource and continue with the next one
		}
		workloadChecks = append(workloadChecks, workloadCheck)

	}

	if len(workloadChecks) == 0 {
		logger.Info("No WorkloadHardeningChecks created as they already exist",
			"namespace", namespaceHardening.Spec.TargetNamespace)
	} else {
		r.Recorder.Eventf(namespaceHardening, corev1.EventTypeNormal, "WorkloadChecksCreated",
			"Created %d WorkloadHardeningChecks for top-level resources in namespace %s",
			len(workloadChecks), namespaceHardening.Spec.TargetNamespace)

		r.SetCondition(ctx, namespaceHardening, metav1.Condition{
			Type:   checksv1alpha1.ConditionTypeFinished,
			Status: metav1.ConditionFalse,
			Reason: checksv1alpha1.ReasonWorkloadChecksCreated,
			Message: fmt.Sprintf("Created %d WorkloadHardeningChecks for top-level resources in namespace %s",
				len(workloadChecks), namespaceHardening.Spec.TargetNamespace),
		})
	}

	// check if all WorkloadHardeningChecks in the namespace are finished
	// If not, we can return and wait for the next reconciliation loop
	workloadCheckList := &checksv1alpha1.WorkloadHardeningCheckList{}
	err = r.List(ctx, workloadCheckList, client.InNamespace(namespaceHardening.Spec.TargetNamespace))
	if err != nil {
		logger.Error(err, "Failed to list WorkloadHardeningChecks in target namespace", "namespace", namespaceHardening.Spec.TargetNamespace)
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
			"namespace", namespaceHardening.Spec.TargetNamespace)

		r.SetCondition(ctx, namespaceHardening, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonNamespaceInProgress,
			Message: fmt.Sprintf("NamespaceHardeningCheck is in progress, %d/%d finished", finishedCount, len(topLevelResources)),
		})
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// All WorkloadHardeningChecks are finished, we can run a final check with all workloads hardened at once
	logger.Info("All WorkloadHardeningChecks are finished, creating final check for namespace hardening",
		"namespace", namespaceHardening.Spec.TargetNamespace)

	recommendations := make(map[string]*checksv1alpha1.Recommendation)
	for _, check := range workloadCheckList.Items {
		if check.Status.Recommendation != nil {
			recommendations[check.Spec.TargetRef.Kind+"/"+check.Spec.TargetRef.Name] = check.Status.Recommendation
		}
	}

	retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-fetch the NamespaceHardeningCheck instance to ensure we have the latest state
		if err := r.Get(ctx, req.NamespacedName, namespaceHardening); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("NamespaceHardeningCheck not found, skipping finalization")
				return nil // If the resource is not found, we can skip the update
			}
			logger.Error(err, "Failed to re-fetch NamespaceHardeningCheck for finalization")
			return err
		}

		namespaceHardening.Status.Recommendations = recommendations

		meta.SetStatusCondition(&namespaceHardening.Status.Conditions, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ConditionTypeFinalCheck,
			Message: "Creating final check for namespace hardening",
		})
		return r.Status().Update(ctx, namespaceHardening)
	})

	success, err := r.createFinalCheckRun(ctx, namespaceHardening)
	if err != nil {
		logger.Error(err, "Failed to create final check run for namespace hardening", "namespace", namespaceHardening.Spec.TargetNamespace)
		r.SetCondition(ctx, namespaceHardening, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ConditionTypeFinished,
			Message: fmt.Sprintf("Failed to create final check run for namespace hardening: %v", err),
		})
		return ctrl.Result{}, err
	}

	if success {
		logger.Info("Final check run for namespace hardening completed successfully", "namespace", namespaceHardening.Spec.TargetNamespace)
		r.SetCondition(ctx, namespaceHardening, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ReasonSuccess,
			Message: "Namespace hardening checks completed successfully",
		})
	} else {
		logger.Info("Final check run for namespace hardening failed", "namespace", namespaceHardening.Spec.TargetNamespace)
		r.SetCondition(ctx, namespaceHardening, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ReasonFailed,
			Message: "Final check run failed, not all workloads are running successfully",
		})
	}

	return ctrl.Result{}, nil
}

func (r *NamespaceHardeningCheckReconciler) createFinalCheckRun(ctx context.Context, namespaceHardening *checksv1alpha1.NamespaceHardeningCheck) (bool, error) {
	logger := logf.FromContext(ctx).WithName("createFinalCheckRun")

	finalCheckNamespace := namespaceHardening.Spec.TargetNamespace + "-" + namespaceHardening.Spec.Suffix + "-final"
	if len(finalCheckNamespace) > 63 {
		finalCheckNamespace = finalCheckNamespace[:63] // Ensure the namespace name is within the 63 character limit
	}

	err := namespace.Clone(ctx, r.Client, namespaceHardening.Spec.TargetNamespace, finalCheckNamespace, namespaceHardening.Spec.Suffix)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("Final check namespace already exists, using it", "namespace", finalCheckNamespace)
			// If the namespace already exists, we can continue with the final check
		} else {
			logger.Error(err, "Failed to clone namespace for final check", "namespace", namespaceHardening.Spec.TargetNamespace, "finalCheckNamespace", finalCheckNamespace)
			return false, err
		}
	}

	finalCheckNamespaceObj := &corev1.Namespace{}
	r.Get(ctx, types.NamespacedName{Name: finalCheckNamespace}, finalCheckNamespaceObj)

	// Set controller reference to the NamespaceHardeningCheck, to ensure it gets cleaned up automatically
	ctrl.SetControllerReference(namespaceHardening, finalCheckNamespaceObj, r.Scheme)

	logger.Info("Cloned namespace for final check", "sourceNamespace", namespaceHardening.Spec.TargetNamespace, "finalCheckNamespace", finalCheckNamespace)
	topLevelResources := namespace.GetTopLevelResources(ctx, finalCheckNamespace)

	// Should be caught way earlier, but just in case
	if len(topLevelResources) == 0 {
		logger.Info("No top-level resources found in final check namespace, skipping final check creation",
			"namespace", finalCheckNamespace)
		return true, nil
	}

	// Apply securityContext from recommendations to all relevant resources
	for _, resource := range topLevelResources {
		if workload.IsSupportedUnstructured(resource) {
			workloadUnderTest, err := r.GetWorkloadUnderTest(ctx, resource)
			if err != nil {
				logger.Error(err, "Failed to get workload under test for final check", "kind", resource.GetKind(), "name", resource.GetName())
				continue // Skip this resource if we can't get the workload
			}
			// Apply security context from recommendations if available
			if recommendation, ok := namespaceHardening.Status.Recommendations[resource.GetKind()+"/"+resource.GetName()]; ok {
				workload.ApplySecurityContext(ctx, workloadUnderTest, recommendation.ContainerSecurityContexts, recommendation.PodSecurityContext)

				r.Update(ctx, *workloadUnderTest)
				for updated := false; !updated; updated, _ = workload.VerifyUpdated(*workloadUnderTest) {
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
		if workload.IsSupportedUnstructured(resource) {
			workloadUnderTest, err := r.GetWorkloadUnderTest(ctx, resource)
			if err != nil {
				logger.Error(err, "Failed to get workload under test for final check", "kind", resource.GetKind(), "name", resource.GetName())
				continue // Skip this resource if we can't get the workload
			}

			for running := false; !running; running, _ = workload.VerifyReadiness(workloadUnderTest, r.Client) {
				logger.Info("Waiting for workload to be running after security context update", "kind", resource.GetKind(), "name", resource.GetName())
				time.Sleep(5 * time.Second)
				r.Get(ctx, types.NamespacedName{Namespace: (*workloadUnderTest).GetNamespace(), Name: (*workloadUnderTest).GetName()}, *workloadUnderTest)
				if time.Since(startTime.Time) > 2*time.Minute {
					logger.Error(nil, "Workload did not become running in time after security context update", "kind", resource.GetKind(), "name", resource.GetName())
					success = false
					r.Recorder.Eventf(namespaceHardening, corev1.EventTypeWarning, "WorkloadNotRunning",
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

	if !workload.IsSupportedUnstructured(resource) {
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

func (r *NamespaceHardeningCheckReconciler) createWorkloadHardeningCheck(ctx context.Context, namespaceHardeningCheck *checksv1alpha1.NamespaceHardeningCheck, resource *unstructured.Unstructured) (*checksv1alpha1.WorkloadHardeningCheck, error) {
	logger := logf.FromContext(ctx)

	workloadCheck := &checksv1alpha1.WorkloadHardeningCheck{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      strings.ToLower(resource.GetKind() + "-" + resource.GetName() + "-" + namespaceHardeningCheck.Spec.Suffix),
		Namespace: namespaceHardeningCheck.Spec.TargetNamespace,
	}, workloadCheck); err == nil {
		// WorkloadHardeningCheck already exists, we can skip creating it
		logger.Info("WorkloadHardeningCheck already exists, skipping creation", "workload", resource.GetName(), "namespace", namespaceHardeningCheck.Spec.TargetNamespace)
		return workloadCheck, nil
	} else {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to get existing WorkloadHardeningCheck", "workload", resource.GetName(), "namespace", namespaceHardeningCheck.Spec.TargetNamespace)
			return nil, fmt.Errorf("failed to get existing WorkloadHardeningCheck for %s/%s in namespace %s: %w",
				resource.GetKind(), resource.GetName(), namespaceHardeningCheck.Spec.TargetNamespace, err)
		}

		// If the WorkloadHardeningCheck does not exist, we will create it
		logger.Info("WorkloadHardeningCheck not found, creating new one", "workload", resource.GetName(), "namespace", namespaceHardeningCheck.Spec.TargetNamespace)
	}

	// Create a new WorkloadHardeningCheck for each top-level resource
	workloadCheck = &checksv1alpha1.WorkloadHardeningCheck{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.ToLower(resource.GetKind() + "-" + resource.GetName() + "-" + namespaceHardeningCheck.Spec.Suffix),
			Namespace: namespaceHardeningCheck.Spec.TargetNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       strings.ToLower(resource.GetKind() + "-" + resource.GetName() + "-" + namespaceHardeningCheck.Spec.Suffix),
				"app.kubernetes.io/managed-by": "oracle-of-funk",
				"appkubernetes.io/part-of":     namespaceHardeningCheck.Name,
			},
		},
		Spec: checksv1alpha1.WorkloadHardeningCheckSpec{
			Suffix: namespaceHardeningCheck.Spec.Suffix + "-" + utilrand.String(8), // Generate a random suffix of 8 characters
			TargetRef: checksv1alpha1.TargetReference{
				Kind: resource.GetKind(),
				Name: resource.GetName(),
			},
			RecordingDuration: namespaceHardeningCheck.Spec.RecordingDuration,
			RunMode:           namespaceHardeningCheck.Spec.RunMode,
			SecurityContext:   namespaceHardeningCheck.Spec.SecurityContext.DeepCopy(),
		},
	}

	// set owner reference to the NamespaceHardeningCheck
	if err := ctrl.SetControllerReference(namespaceHardeningCheck, workloadCheck, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference for WorkloadHardeningCheck", "workload", workloadCheck.Spec.TargetRef.Name, "namespace", namespaceHardeningCheck.Spec.TargetNamespace)
	}

	// Create the WorkloadHardeningCheck
	if err := r.Create(ctx, workloadCheck); err != nil {
		logger.Error(err, "Failed to create WorkloadHardeningCheck", "workload", workloadCheck.Spec.TargetRef.Name, "namespace", namespaceHardeningCheck.Spec.TargetNamespace)
		return nil, fmt.Errorf("failed to create WorkloadHardeningCheck for %s/%s in namespace %s: %w",
			workloadCheck.Spec.TargetRef.Kind, workloadCheck.Spec.TargetRef.Name, namespaceHardeningCheck.Spec.TargetNamespace, err)
	}

	logger.Info("Created WorkloadHardeningCheck", "workload", workloadCheck.Spec.TargetRef.Name, "namespace", namespaceHardeningCheck.Spec.TargetNamespace)

	return workloadCheck, nil
}

func (r *NamespaceHardeningCheckReconciler) getTopLevelResourcesToCheck(ctx context.Context, namespaceHardeningCheck *checksv1alpha1.NamespaceHardeningCheck) []*unstructured.Unstructured {
	logger := logf.FromContext(ctx).WithName("getTopLevelResourcesToCheck")

	// Fetch all top-level resources in the target namespace
	allTopLevelResources := namespace.GetTopLevelResources(ctx, namespaceHardeningCheck.Spec.TargetNamespace)

	if len(allTopLevelResources) == 0 {
		logger.Info("No top-level resources found in target namespace", "namespace", namespaceHardeningCheck.Spec.TargetNamespace)
		return []*unstructured.Unstructured{}
	}

	// Filter for resources that are compatible with WorkloadHardeningCheck,
	// Currently supported are:
	// - Deployments
	// - StatefulSets
	// - DaemonSets
	usableResources := []*unstructured.Unstructured{}
	for _, resource := range allTopLevelResources {
		if workload.IsSupportedUnstructured(resource) {
			usableResources = append(usableResources, resource)
		}
	}

	return usableResources
}

func (r *NamespaceHardeningCheckReconciler) SetCondition(ctx context.Context, namespaceHardeningCheck *checksv1alpha1.NamespaceHardeningCheck, condition metav1.Condition) error {
	logger := logf.FromContext(ctx).WithName("NamespaceHardeningCheckReconciler")

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := r.Get(ctx, client.ObjectKey{Name: namespaceHardeningCheck.Name}, namespaceHardeningCheck); err != nil {
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
			&namespaceHardeningCheck.Status.Conditions,
			condition,
		)

		return r.Status().Update(ctx, namespaceHardeningCheck)

	})

	return err
}

// Called if the NamespaceHardeningCheck instance is removed or deleted and we need to clean up the resources
func (r *NamespaceHardeningCheckReconciler) cleanupReconcileLoop(ctx context.Context, name string) (ctrl.Result, error) {
	_ = logf.FromContext(ctx).WithName("cleanupReconcileLoop")

	// Check if a namespace belonging to the NamespaceHardeningCheck exists

	return ctrl.Result{}, nil
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
