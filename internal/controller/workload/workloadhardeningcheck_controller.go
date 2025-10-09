package workload

import (
	"context"
	"fmt"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/namespace"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/runner"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/valkey"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/workload"
)

// WorkloadHardeningCheckReconciler reconciles a WorkloadHardeningCheck object
type WorkloadHardeningCheckReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Recorder     record.EventRecorder
	ValKeyClient *valkey.ValkeyClient
}

// Required to convert "user" to "User", strings.ToTitle converts each rune to title case not just the first one
var titleCase = cases.Title(language.English)

// +kubebuilder:rbac:groups=checks.funk.fhnw.ch,resources=workloadhardeningchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=checks.funk.fhnw.ch,resources=workloadhardeningchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=checks.funk.fhnw.ch,resources=workloadhardeningchecks/finalizers,verbs=update
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
		// If the resource is not found it's usually because it was deleted, we need to cleanup remaining resources
		if apierrors.IsNotFound(err) {
			return r.cleanupReconcileLoop(ctx, req.Namespace)
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get WorkloadHardeningCheck, requeing")
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, err
	}

	// ConditionFinished is set to true when the reconciliation is finished
	if meta.IsStatusConditionTrue(workloadHardening.Status.Conditions, checksv1alpha1.ConditionTypeFinished) {
		logger.V(1).Info("WorkloadHardeningCheck is already finished, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	checkManager := workload.NewWorkloadCheckManager(ctx, r.ValKeyClient, workloadHardening)

	// If the final check run is already finished, we set the Finished condition to true
	if checkManager.RecommendationExists() && checkManager.FinalCheckRecorded() {
		logger.Info("Final check run finished, setting Finished condition")
		err = checkManager.SetCondition(ctx, metav1.Condition{
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

		checkManager.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonAnalysisRunning,
			Message: "Check runs are being analyzed",
		})

		logger.Info("All checks are finished, analyzing results")
		checkManager.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeAnalysis,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonAnalysisRunning,
			Message: "Check runs are being analyzed",
		})

		err = checkManager.AnalyzeCheckRuns(ctx)
		if err != nil {
			logger.Error(err, "Failed to analyze check runs")
			checkManager.SetCondition(ctx, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeAnalysis,
				Status:  metav1.ConditionTrue,
				Reason:  checksv1alpha1.ReasonAnalysisFailed,
				Message: fmt.Sprintf("Analyzing check runs failed: %s", err.Error()),
			})
			checkManager.SetCondition(ctx, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeFinished,
				Status:  metav1.ConditionTrue,
				Reason:  checksv1alpha1.ReasonAnalysisFailed,
				Message: "Analyzing check runs failed, cannot proceed with checks. Check the CheckResultAnalysis condition for more details.",
			})
			return ctrl.Result{}, nil
		}

		checkManager.SetRecommendation(ctx)

		checkManager.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeAnalysis,
			Status:  metav1.ConditionTrue,
			Reason:  checksv1alpha1.ReasonAnalysisFinished,
			Message: "Check runs are analyzed",
		})

		checkManager.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  checksv1alpha1.ReasonAnalysisRunning,
			Message: "Check runs are analyzed, waiting for final check run",
		})

	}

	// The final check run has failed! We set the Finished condition to true and return
	// ToDo: Analyse the results for the final check run and report the errors
	if meta.IsStatusConditionPresentAndEqual(workloadHardening.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck, metav1.ConditionUnknown) {
		logger.Info("Final check run failed, setting Finished condition to true")
		err = checkManager.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionTrue,
			Reason:  "FinalCheckFailed",
			Message: "Final check run failed, cannot proceed with checks",
		})

		return ctrl.Result{}, err
	}

	// If the final check run is already running, we need to wait for it to finish
	if checkManager.FinalCheckInProgress() {

		if checkManager.FinalCheckOverdue() {
			logger.Info("FinalCheck recording is overdue, requeuing reconciliation")
			checkManager.SetCondition(ctx, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeFinalCheck,
				Status:  metav1.ConditionUnknown,
				Reason:  checksv1alpha1.ReasonRequeue,
				Message: "Final check recording is still running, but last transition time is older than 2x duration, requeuing",
			})
		} else {
			logger.Info("Final check run is still running, waiting for it to finish")
		}

		return ctrl.Result{RequeueAfter: checkManager.GetCheckDuration() / 2}, nil
	}

	// If all checks are finished and the results are analyzed, create a final check run using the recommended security context
	if checkManager.RecommendationExists() && !checkManager.FinalCheckRecorded() {
		// !meta.IsStatusConditionTrue also returns true if the condition is not set at all !!
		logger.Info("Starting Final check run with recommended security context")

		securityContext := checkManager.GetRecommendedSecurityContext()
		finalCheckRunner := runner.NewWorkloadCheckRunner(ctx, r.ValKeyClient, r.Recorder, workloadHardening, "Final")

		go finalCheckRunner.RunCheck(ctx, securityContext)

		checkManager.SetCondition(ctx, metav1.Condition{
			Type:    checksv1alpha1.ConditionTypeFinished,
			Status:  metav1.ConditionFalse,
			Reason:  "Final" + checksv1alpha1.ReasonCheckRecording,
			Message: "Final check run with recommended security context started",
		})

		return ctrl.Result{RequeueAfter: checkManager.GetCheckDuration() + 10*time.Second}, nil
	}

	// We use the baseline duration to determine how long we should wait before requeuing the reconciliation
	duration := checkManager.GetCheckDuration()

	// Based on the Status, we need to decide what to do next
	// If there is no Baseline recorded yet, we need to start the baseline recording

	if checkManager.BaselineInProgress() {
		// If the baseline is not recorded yet, we need to wait for the baseline recording to finish
		if checkManager.BaselineOverdue() {
			logger.Info("Baseline recording is overdue, requeuing reconciliation")
			checkManager.SetCondition(ctx, metav1.Condition{
				Type:    checksv1alpha1.ConditionTypeBaseline,
				Status:  metav1.ConditionUnknown,
				Reason:  checksv1alpha1.ReasonRequeue,
				Message: "Baseline recording is still running, but last transition time is older than 2x duration, requeuing",
			})
		} else {
			logger.Info("Baseline not recorded yet, waiting for baseline recording to finish")
		}

		return ctrl.Result{RequeueAfter: duration / 2}, nil
	}

	if !checkManager.BaselineRecorded() {
		logger.Info("Baseline not recorded yet. Starting baseline recording")
		return r.recordBaseline(ctx, workloadHardening, checkManager)
	}

	if checkManager.BaselineRecorded() {
		for _, baselineRun := range workloadHardening.Status.BaselineRuns {
			if baselineRun.CheckSuccessfull == nil || !*baselineRun.CheckSuccessfull {
				logger.Info("Baseline recording failed, we will never get the workload running, aborting further checks")
				checkManager.SetCondition(ctx, metav1.Condition{
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
	if checkManager.BaselineRecorded() && !checkManager.AllChecksFinished() {
		return r.recordChecks(ctx, workloadHardening, checkManager)
	}

	return ctrl.Result{}, nil
}

// clones the target workloads into two baseline namespaces, one for each baseline recording
func (r *WorkloadHardeningCheckReconciler) recordBaseline(ctx context.Context, workloadHardening *checksv1alpha1.WorkloadHardeningCheck, checkManager *workload.WorkloadCheckManager) (ctrl.Result, error) {
	// Set the condition to indicate that we are starting the baseline recording

	baselineRunner := runner.NewWorkloadCheckRunner(ctx, r.ValKeyClient, r.Recorder, workloadHardening, "baseline")
	go baselineRunner.RunCheck(ctx, workloadHardening.Spec.SecurityContext)

	checkManager.SetCondition(ctx, metav1.Condition{
		Type:    checksv1alpha1.ConditionTypeFinished,
		Status:  metav1.ConditionFalse,
		Reason:  checksv1alpha1.ReasonBaselineRecording,
		Message: "Baseline recording started",
	})

	// The baseline is recorded twice, to make the log matching better, as the logs are ingested using different timestamps

	offset := 10 + utilrand.Intn(9)                 // Random offset between 10 and 19 seconds to avoid all checks running at the same time
	time.Sleep(time.Duration(offset) * time.Second) // Sleep for a short duration to allow the first baseline recording to start
	baselineRunner = runner.NewWorkloadCheckRunner(ctx, r.ValKeyClient, r.Recorder, workloadHardening, "baseline-2")
	go baselineRunner.RunCheck(ctx, workloadHardening.Spec.SecurityContext)

	// Requeue the reconciliation after the baseline duration, to continue with the next steps
	return ctrl.Result{RequeueAfter: checkManager.GetCheckDuration() + 10*time.Second}, nil

}

// determines which checks need to be run and starts them
// If the RunMode is set to Parallel, all checks are started in parallel
// If the RunMode is set to Sequential, the checks are started one after another
func (r *WorkloadHardeningCheckReconciler) recordChecks(ctx context.Context, workloadHardening *checksv1alpha1.WorkloadHardeningCheck, checkManager *workload.WorkloadCheckManager) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithName("recordChecks")

	logger.Info("Not all checks are finished, running checks")

	requiredChecks := checkManager.GetRequiredCheckRuns(ctx)

	logger.Info("Required checks to run", "checks", requiredChecks)

	checkManager.SetCondition(ctx, metav1.Condition{
		Type:    checksv1alpha1.ConditionTypeFinished,
		Status:  metav1.ConditionFalse,
		Reason:  checksv1alpha1.ReasonCheckRecording,
		Message: "Check recording started",
	})

	if workloadHardening.Spec.RunMode == checksv1alpha1.RunModeParallel {
		logger.V(2).Info("Running checks in parallel mode")
		// Run all checks in parallel
		for _, checkType := range requiredChecks {

			if checkManager.CheckRecorded(checkType) {
				logger.Info("Check already finished, skipping", "checkType", checkType)
				continue // Skip if the check is already recorded
			}

			if checkManager.CheckInProgress(checkType) {

				if checkManager.CheckOverdue(checkType) {
					checkManager.SetCondition(ctx, metav1.Condition{
						Type:    titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck,
						Status:  metav1.ConditionUnknown,
						Reason:  checksv1alpha1.ReasonRequeue,
						Message: "Check is still running, but last transition time is older than 2x duration, requeuing",
					})

					logger.Info("Check is overdue, rescheduling", "checkType", checkType)

				} else {
					logger.Info("Check still running not yet overdue, skipping", "checkType", checkType)
					continue // Skip if the check might still be running
				}

			}

			logger.Info("Starting new check run", "checkType", checkType)

			securityContext := checkManager.GetSecurityContextForCheckType(checkType)

			checkRunner := runner.NewWorkloadCheckRunner(ctx, r.ValKeyClient, r.Recorder, workloadHardening, checkType)

			go checkRunner.RunCheck(ctx, securityContext)
		}

		// Requeue the reconciliation after the baseline duration, to continue with the next steps
		return ctrl.Result{RequeueAfter: checkManager.GetCheckDuration() + 10*time.Second}, nil
	} else {
		logger.Info("Running checks in sequential mode")

		for _, checkType := range requiredChecks {
			if checkManager.CheckRecorded(checkType) {
				logger.V(2).Info("Check already finished, skipping", "checkType", checkType)
				continue // Skip if the check is already recorded
			}
			if checkManager.CheckInProgress(checkType) {

				if checkManager.CheckOverdue(checkType) {
					checkManager.SetCondition(ctx, metav1.Condition{
						Type:    titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck,
						Status:  metav1.ConditionUnknown,
						Reason:  checksv1alpha1.ReasonRequeue,
						Message: "Check is still running, but last transition time is older than duration + 1 minute, requeuing",
					})

				} else {
					logger.V(2).Info("Check still running, skipping", "checkType", checkType)
					continue // Skip if the check is already recorded
				}
			}

			securityContext := checkManager.GetSecurityContextForCheckType(checkType)
			checkRunner := runner.NewWorkloadCheckRunner(ctx, r.ValKeyClient, r.Recorder, workloadHardening, checkType)
			logger.Info("Running check", "checkType", checkType)
			go checkRunner.RunCheck(ctx, securityContext)

			// Requeue the reconciliation after the  duration, to continue with the next check
			return ctrl.Result{RequeueAfter: checkManager.GetCheckDuration() + 10*time.Second}, nil
		}
	}

	// We don't get here... either of the if/else branches should return
	return ctrl.Result{}, nil
}

// Called if the WorkloadHardeningCheck instance is removed or deleted and we need to clean up the resources
func (r *WorkloadHardeningCheckReconciler) cleanupReconcileLoop(ctx context.Context, sourceNamespace string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("cleanupReconcileLoop")

	// If the custom resource is not found then it usually means that it was deleted or not created
	log.Info("WorkloadHardeningCheck deleted, cleaning up resources")

	checkNamespaces := corev1.NamespaceList{}
	err := r.List(
		ctx,
		&checkNamespaces,
		&client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"orakel.fhnw.ch/source-namespace": sourceNamespace,
			}),
		},
	)

	if err != nil {
		log.Error(err, "Failed to list namespaces for cleanup")
		return ctrl.Result{}, err
	}
	if len(checkNamespaces.Items) == 0 {
		log.Info("No namespaces found for cleanup")
		return ctrl.Result{}, nil
	}

	log.Info("Found namespaces for cleanup", "count", len(checkNamespaces.Items))

	for _, ns := range checkNamespaces.Items {
		log.Info("Deleting namespace", "namespace", ns.Name)
		err = namespace.Delete(ctx, r.Client, ns.Name)
		if err != nil {
			log.Error(err, "Failed to delete namespace", "namespace", ns.Name)
		} else {
			log.Info("Deleted namespace", "namespace", ns.Name)
		}
	}

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
