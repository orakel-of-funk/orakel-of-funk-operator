package workload

import (
	"context"
	"fmt"
	"strings"
	"time"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/executor"
	oflabels "github.com/orakel-of-funk/orakel-of-funk-operator/internal/labels"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/namespace"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/runner"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/valkey"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/workload"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// WorkloadHardeningCheckReconciler reconciles a WorkloadHardeningCheck object
type WorkloadHardeningCheckReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	Recorder            record.EventRecorder
	ValKeyClient        *valkey.ValkeyClient
	Executor            *executor.CheckExecutor
	NormalizeTimestamps bool
}

// Required to convert "user" to "User", strings.ToTitle converts each rune to title case not just the first one
var titleCase = cases.Title(language.English)

// +kubebuilder:rbac:groups=orakel.ofunk.org,resources=workloadhardeningchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=orakel.ofunk.org,resources=workloadhardeningchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=orakel.ofunk.org,resources=workloadhardeningchecks/finalizers,verbs=update
// +kubebuilder:rbac:groups=*,resources=*,verbs=*

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The flow is basically implemented in reverse, to finish execution early
// The order they are implemented is as follows:
// 1. Check if the WorkloadHardeningCheck instance exists, if not, we clean up the resources (as the reconcile loop is also called on deletion)
// 2. If the WorkloadHardeningCheck instance is already finished, we skip the reconciliation
// 3. If the final check run is already finished, we set the Finished condition to true
// 4. If all checks are finished and the analysis is not yet done, we analyze the results
// 5. Create a final check run using the recommended security context
// -- Only now we start to check if we need to record a baseline or run checks --
// 6. Ensure the original workload is running, otherwise there is nothing to analyze
// 7. If the baseline is not recorded yet, we start the baseline recording
// 8. If the baselnie is recorded, we start recording the different checks
func (r *WorkloadHardeningCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithName("Reconcile")

	// Get Resource
	// Fetch the WorkloadHardeningCheck instance
	workloadHardening := &checksv1alpha1.WorkloadHardeningCheck{}
	err := r.Get(ctx, req.NamespacedName, workloadHardening)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Resource is gone — nothing to do (finalizer should have handled cleanup)
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get WorkloadHardeningCheck, requeing")
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, err
	}

	// Handle deletion via finalizer
	if workloadHardening.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(workloadHardening, oflabels.FinalizerCleanup) {
			return r.handleDeletion(ctx, workloadHardening)
		}
		// Finalizer already removed, nothing to do
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(workloadHardening, oflabels.FinalizerCleanup) {
		controllerutil.AddFinalizer(workloadHardening, oflabels.FinalizerCleanup)
		if err := r.Update(ctx, workloadHardening); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// ConditionFinished is set to true when the reconciliation is finished
	if meta.IsStatusConditionTrue(workloadHardening.Status.Conditions, checksv1alpha1.ConditionTypeFinished) {
		logger.V(1).Info("WorkloadHardeningCheck is already finished, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	checkManager := workload.NewWorkloadCheckManager(ctx, r.Client, r.ValKeyClient, workloadHardening, r.NormalizeTimestamps)

	// If the final check run is already finished, we set the Finished condition to true
	if checkManager.WorkloadHardeningCheck.RecommendationExists() && checkManager.WorkloadHardeningCheck.FinalCheckRecorded() {
		logger.Info("Final check run finished, setting Finished condition")
		err = checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ConditionTypeFinished,
			Message: "Finished, recommendation ready",
		})

		// Remove no longer needed check conditions
		if err == nil {
			err = checkManager.RemoveCheckConditions(ctx)
		}

		return ctrl.Result{}, err
	}

	// If all checks are finished and the analysis is not yet done, we need to analyze the results
	if checkManager.AllChecksFinished() && !meta.IsStatusConditionTrue(workloadHardening.Status.Conditions, checksv1alpha1.ConditionTypeAnalysis) {
		// !meta.IsStatusConditionTrue also returns true if the condition is not set at all !!

		checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonAnalysisRunning,
			Message: "Check runs are being analyzed",
		})

		logger.Info("All checks are finished, analyzing results")
		checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeAnalysis,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonAnalysisRunning,
			Message: "Check runs are being analyzed",
		})

		err = checkManager.AnalyzeCheckRuns(ctx)
		if err != nil {
			logger.Error(err, "Failed to analyze check runs")
			checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeAnalysis,
				Status:  metav1.ConditionTrue,
				Reason:  checksv1alpha1.ReasonAnalysisFailed,
				Message: fmt.Sprintf("Analyzing check runs failed: %s", err.Error()),
			})
			checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeFinished,
				Status:  metav1.ConditionTrue,
				Reason:  checksv1alpha1.ReasonAnalysisFailed,
				Message: "Analyzing check runs failed, cannot proceed with checks. Check the CheckResultAnalysis condition for more details.",
			})
			return ctrl.Result{}, nil
		}

		checkManager.SetRecommendation(ctx)

		checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeAnalysis,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ReasonAnalysisFinished,
			Message: "Check runs are analyzed",
		})

		checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonAnalysisRunning,
			Message: "Check runs are analyzed, waiting for final check run",
		})

	}

	// The final check run has failed! We set the Finished condition to true and return
	// ToDo: Analyse the results for the final check run and report the errors
	if meta.IsStatusConditionPresentAndEqual(workloadHardening.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck, metav1.ConditionUnknown) {
		finalCheckID := executor.NewCheckRunID(workloadHardening.Namespace, workloadHardening.Name, "Final")

		// If the condition is Unknown due to being overdue (e.g. operator restart), retry the check
		if !r.Executor.IsRunning(finalCheckID) {
			logger.Info("Final check run condition is Unknown and executor is not running it, re-submitting")

			securityContext := checkManager.WorkloadHardeningCheck.GetRecommendedSecurityContext()
			r.Executor.Submit(finalCheckID, func(runCtx context.Context) {
				finalCheckRunner := runner.NewWorkloadCheckRunner(runCtx, r.Client, r.ValKeyClient, r.Recorder, workloadHardening, "Final")
				finalCheckRunner.RunCheck(runCtx, securityContext)
			})

			return ctrl.Result{RequeueAfter: checkManager.WorkloadHardeningCheck.GetCheckDuration() + 10*time.Second}, nil
		}

		// Executor is still running it — wait
		return ctrl.Result{RequeueAfter: checkManager.WorkloadHardeningCheck.GetCheckDuration() / 2}, nil
	}

	// If the final check run is already running, we need to wait for it to finish
	if checkManager.WorkloadHardeningCheck.FinalCheckInProgress() {

		if checkManager.WorkloadHardeningCheck.FinalCheckOverdue() {
			logger.Info("FinalCheck recording is overdue, requeuing reconciliation")
			checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeFinalCheck,
				Status:  metav1.ConditionUnknown,
				Reason:  checksv1alpha1.ReasonRequeue,
				Message: "Final check recording is still running, but last transition time is older than 2x duration, requeuing",
			})
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		logger.Info("Final check run is still running, waiting for it to finish")
		return ctrl.Result{RequeueAfter: checkManager.WorkloadHardeningCheck.GetCheckDuration() / 2}, nil
	}

	// If all checks are finished and the results are analyzed, create a final check run using the recommended security context
	if checkManager.WorkloadHardeningCheck.RecommendationExists() && !checkManager.WorkloadHardeningCheck.FinalCheckRecorded() {
		// !meta.IsStatusConditionTrue also returns true if the condition is not set at all !!
		logger.Info("Starting Final check run with recommended security context")

		securityContext := checkManager.WorkloadHardeningCheck.GetRecommendedSecurityContext()
		finalCheckID := executor.NewCheckRunID(workloadHardening.Namespace, workloadHardening.Name, "Final")

		if !r.Executor.IsRunning(finalCheckID) {
			r.Executor.Submit(finalCheckID, func(runCtx context.Context) {
				finalCheckRunner := runner.NewWorkloadCheckRunner(runCtx, r.Client, r.ValKeyClient, r.Recorder, workloadHardening, "Final")
				finalCheckRunner.RunCheck(runCtx, securityContext)
			})
		}

		checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  "Final" + checksv1alpha1.ReasonCheckRecording,
			Message: "Final check run with recommended security context started",
		})

		return ctrl.Result{RequeueAfter: checkManager.WorkloadHardeningCheck.GetCheckDuration() + 10*time.Second}, nil
	}

	// We use the baseline duration to determine how long we should wait before requeuing the reconciliation
	duration := checkManager.WorkloadHardeningCheck.GetCheckDuration()

	// Based on the Status, we need to decide what to do next
	// If there is no Baseline recorded yet, we need to start the baseline recording

	if checkManager.WorkloadHardeningCheck.BaselineInProgress() {
		// If the baseline is not recorded yet, we need to wait for the baseline recording to finish
		if checkManager.WorkloadHardeningCheck.BaselineOverdue() {
			logger.Info("Baseline recording is overdue, requeuing reconciliation")
			checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeBaseline,
				Status:  metav1.ConditionUnknown,
				Reason:  checksv1alpha1.ReasonRequeue,
				Message: "Baseline recording is still running, but last transition time is older than 2x duration, requeuing",
			})
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		logger.Info("Baseline not recorded yet, waiting for baseline recording to finish")
		return ctrl.Result{RequeueAfter: duration / 2}, nil
	}

	// The validation webhook already ensures that the BaselineRecordingReference exists during creation, so we simply update the status here
	if len(workloadHardening.Spec.BaselineRecordingReference) > 0 && !checkManager.WorkloadHardeningCheck.BaselineRecorded() {
		logger.Info("Using existing baseline recording from ValKey", "reference", strings.Join(workloadHardening.Spec.BaselineRecordingReference, ","))
		checkManager.SetBaselineRecorded(ctx)
	}

	if !checkManager.WorkloadHardeningCheck.BaselineRecorded() {
		logger.Info("Baseline not recorded yet. Starting baseline recording")
		return r.recordBaseline(ctx, workloadHardening, checkManager)
	}

	if checkManager.WorkloadHardeningCheck.BaselineRecorded() {
		for _, baselineRun := range workloadHardening.Status.BaselineRuns {
			if baselineRun.CheckSuccessfull == nil || !*baselineRun.CheckSuccessfull {
				logger.Info("Baseline recording failed, we will never get the workload running, aborting further checks")
				checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
					Type:    checksv1alpha1.ConditionTypeFinished,
					Status:  metav1.ConditionTrue,
					Reason:  checksv1alpha1.ReasonBaselineRecordingFailed,
					Message: "Baseline recording failed, aborting further checks",
				})
				return ctrl.Result{}, nil
			}
		}
	}

	// If we are here, it means that the baseline recording is done successfully
	// We can now start recording the workload under test with different security context configurations
	if checkManager.WorkloadHardeningCheck.BaselineRecorded() && !checkManager.AllChecksFinished() {
		return r.recordChecks(ctx, workloadHardening, checkManager)
	}

	return ctrl.Result{}, nil
}

// clones the target workloads into two baseline namespaces, one for each baseline recording
func (r *WorkloadHardeningCheckReconciler) recordBaseline(ctx context.Context, workloadHardening *checksv1alpha1.WorkloadHardeningCheck, checkManager *workload.WorkloadCheckManager) (ctrl.Result, error) {
	// Submit first baseline via executor
	baselineID1 := executor.NewCheckRunID(workloadHardening.Namespace, workloadHardening.Name, "baseline")
	if !r.Executor.IsRunning(baselineID1) {
		r.Executor.Submit(baselineID1, func(runCtx context.Context) {
			baselineRunner := runner.NewWorkloadCheckRunner(runCtx, r.Client, r.ValKeyClient, r.Recorder, workloadHardening, "baseline")
			baselineRunner.RunCheck(runCtx, workloadHardening.Spec.SecurityContext)
		})
	}

	// Submit second baseline with a slight delay (10-19s) to ensure log timestamps differ.
	// This is required for Drain3 pattern matching to work correctly.
	baselineID2 := executor.NewCheckRunID(workloadHardening.Namespace, workloadHardening.Name, "baseline-2")
	if !r.Executor.IsRunning(baselineID2) {
		r.Executor.Submit(baselineID2, func(runCtx context.Context) {
			// Delay to ensure different log timestamps between baselines
			offset := 10 + utilrand.Intn(9) // Random offset between 10 and 19 seconds
			select {
			case <-time.After(time.Duration(offset) * time.Second):
			case <-runCtx.Done():
				return
			}
			baselineRunner := runner.NewWorkloadCheckRunner(runCtx, r.Client, r.ValKeyClient, r.Recorder, workloadHardening, "baseline-2")
			baselineRunner.RunCheck(runCtx, workloadHardening.Spec.SecurityContext)
		})
	}

	checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
		Type:    checksv1alpha1.ConditionTypeFinished,
		Status:  metav1.ConditionFalse,
		Reason:  checksv1alpha1.ReasonBaselineRecording,
		Message: "Baseline recording started",
	})

	// Requeue the reconciliation after the baseline duration, to continue with the next steps
	return ctrl.Result{RequeueAfter: checkManager.WorkloadHardeningCheck.GetCheckDuration() + 30*time.Second}, nil

}

// determines which checks need to be run and starts them
// If the RunMode is set to Parallel, all checks are started in parallel
// If the RunMode is set to Sequential, the checks are started one after another
func (r *WorkloadHardeningCheckReconciler) recordChecks(ctx context.Context, workloadHardening *checksv1alpha1.WorkloadHardeningCheck, checkManager *workload.WorkloadCheckManager) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithName("recordChecks")

	logger.Info("Not all checks are finished, running checks")

	requiredChecks := checkManager.GetRequiredCheckRuns(ctx)

	logger.Info("Required checks to run", "checks", requiredChecks)

	checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
		Type:    checksv1alpha1.ConditionTypeFinished,
		Status:  metav1.ConditionFalse,
		Reason:  checksv1alpha1.ReasonCheckRecording,
		Message: "Check recording started",
	})

	if workloadHardening.Spec.RunMode == checksv1alpha1.RunModeParallel {
		logger.V(2).Info("Running checks in parallel mode")
		// Run all checks in parallel
		for _, checkType := range requiredChecks {

			if checkManager.WorkloadHardeningCheck.CheckRecorded(checkType) {
				logger.Info("Check already finished, skipping", "checkType", checkType)
				continue
			}

			checkRunID := executor.NewCheckRunID(workloadHardening.Namespace, workloadHardening.Name, checkType)

			if r.Executor.IsRunning(checkRunID) {
				if checkManager.WorkloadHardeningCheck.CheckOverdue(checkType) {
					checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
						Type:    titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck,
						Status:  metav1.ConditionUnknown,
						Reason:  checksv1alpha1.ReasonRequeue,
						Message: "Check is still running, but last transition time is older than 2x duration, requeuing",
					})
					logger.Info("Check is overdue, rescheduling", "checkType", checkType)
				} else {
					logger.Info("Check still running not yet overdue, skipping", "checkType", checkType)
					continue
				}
			}

			if checkManager.WorkloadHardeningCheck.CheckInProgress(checkType) && !r.Executor.IsRunning(checkRunID) {
				// Condition says in-progress but executor doesn't have it — it may have crashed. Allow re-submit.
				logger.Info("Check condition says in-progress but executor has no active run, re-submitting", "checkType", checkType)
			}

			logger.Info("Starting new check run", "checkType", checkType)
			securityContext := checkManager.GetSecurityContextForCheckType(checkType)

			// Capture checkType for the closure
			ct := checkType
			sc := securityContext
			r.Executor.Submit(checkRunID, func(runCtx context.Context) {
				checkRunner := runner.NewWorkloadCheckRunner(runCtx, r.Client, r.ValKeyClient, r.Recorder, workloadHardening, ct)
				checkRunner.RunCheck(runCtx, sc)
			})
		}

		// Requeue the reconciliation after the check duration, to continue with the next steps
		return ctrl.Result{RequeueAfter: checkManager.WorkloadHardeningCheck.GetCheckDuration() + 10*time.Second}, nil
	} else {
		logger.Info("Running checks in sequential mode")

		for _, checkType := range requiredChecks {
			if checkManager.WorkloadHardeningCheck.CheckRecorded(checkType) {
				logger.V(2).Info("Check already finished, skipping", "checkType", checkType)
				continue
			}

			checkRunID := executor.NewCheckRunID(workloadHardening.Namespace, workloadHardening.Name, checkType)

			if r.Executor.IsRunning(checkRunID) {
				if checkManager.WorkloadHardeningCheck.CheckOverdue(checkType) {
					checkManager.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
						Type:    titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck,
						Status:  metav1.ConditionUnknown,
						Reason:  checksv1alpha1.ReasonRequeue,
						Message: "Check is still running, but last transition time is older than duration + 1 minute, requeuing",
					})
				} else {
					logger.V(2).Info("Check still running, skipping", "checkType", checkType)
					// In sequential mode, wait for the current one to finish before starting next
					return ctrl.Result{RequeueAfter: checkManager.WorkloadHardeningCheck.GetCheckDuration() + 10*time.Second}, nil
				}
			}

			securityContext := checkManager.GetSecurityContextForCheckType(checkType)
			ct := checkType
			sc := securityContext
			logger.Info("Running check", "checkType", ct)
			r.Executor.Submit(checkRunID, func(runCtx context.Context) {
				checkRunner := runner.NewWorkloadCheckRunner(runCtx, r.Client, r.ValKeyClient, r.Recorder, workloadHardening, ct)
				checkRunner.RunCheck(runCtx, sc)
			})

			// In sequential mode, requeue after duration to check on the next one
			return ctrl.Result{RequeueAfter: checkManager.WorkloadHardeningCheck.GetCheckDuration() + 10*time.Second}, nil
		}
	}

	// All checks were either already finished or already running
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// handleDeletion performs cleanup when a WorkloadHardeningCheck is being deleted.
// It cancels active executor runs, deletes cloned namespaces and associated ClusterRoleBindings,
// then removes the finalizer to allow Kubernetes to complete the deletion.
func (r *WorkloadHardeningCheckReconciler) handleDeletion(ctx context.Context, workloadHardening *checksv1alpha1.WorkloadHardeningCheck) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("handleDeletion")
	log.Info("WorkloadHardeningCheck being deleted, cleaning up resources")

	// Cancel all active executor runs for this WHC
	prefix := workloadHardening.Namespace + "/" + workloadHardening.Name + ":"
	r.Executor.CancelByPrefix(prefix)

	// Delete all cloned namespaces created by this WHC
	checkNamespaces := corev1.NamespaceList{}
	err := r.List(
		ctx,
		&checkNamespaces,
		&client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				oflabels.LabelSourceNamespace: workloadHardening.Namespace,
			}),
		},
	)
	if err != nil {
		log.Error(err, "Failed to list namespaces for cleanup")
		return ctrl.Result{}, err
	}

	for _, ns := range checkNamespaces.Items {
		log.Info("Deleting namespace", "namespace", ns.Name)
		if err := namespace.Delete(ctx, r.Client, ns.Name); err != nil {
			log.Error(err, "Failed to delete namespace", "namespace", ns.Name)
		}
	}

	// Remove the finalizer to allow deletion to proceed
	controllerutil.RemoveFinalizer(workloadHardening, oflabels.FinalizerCleanup)
	if err := r.Update(ctx, workloadHardening); err != nil {
		log.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	log.Info("Cleanup complete, finalizer removed")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkloadHardeningCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&checksv1alpha1.WorkloadHardeningCheck{}).
		Owns(&corev1.Namespace{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		WithEventFilter(ignoreStatusChanges()).
		Named("workloadhardeningcheck").
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
