package workload

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

func (m *WorkloadCheckManager) SetCondition(ctx context.Context, condition metav1.Condition) error {

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := m.Get(ctx, types.NamespacedName{Name: m.workloadHardeningCheck.Name, Namespace: m.workloadHardeningCheck.Namespace}, m.workloadHardeningCheck); err != nil {
			if apierrors.IsNotFound(err) {
				// workloadHardeningCheck resource was deleted, while a check was running
				m.logger.Info("WorkloadHardeningCheck not found, skipping condition update")
				return nil // If the resource is not found, we can skip the update
			}
			m.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
			return err
		}

		// Set/Update condition
		meta.SetStatusCondition(
			&m.workloadHardeningCheck.Status.Conditions,
			condition,
		)

		return m.Status().Update(ctx, m.workloadHardeningCheck)

	})

	return err
}

// RemoveCheckConditions removes all check-related conditions from the workloadHardeningCheck.
func (m *WorkloadCheckManager) RemoveCheckConditions(ctx context.Context) error {
	// Remove all check conditions from the workloadHardeningCheck
	m.logger.V(2).Info("Removing all check conditions from WorkloadHardeningCheck", "name", m.workloadHardeningCheck.Name)

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Re-fetch the workload hardening check Custom Resource
		if err := m.Get(ctx, types.NamespacedName{Name: m.workloadHardeningCheck.Name, Namespace: m.workloadHardeningCheck.Namespace}, m.workloadHardeningCheck); err != nil {
			return err
		}

		remainingConditions := []metav1.Condition{}
		// Iterate over the existing conditions and keep only those that are not related to checks
		for _, condition := range m.workloadHardeningCheck.Status.Conditions {
			if condition.Type == checksv1alpha1.ConditionTypeFinished {
				// Keep the Finished condition, as it is not a check condition
				remainingConditions = append(remainingConditions, condition)
			}
			if condition.Type == checksv1alpha1.ConditionTypeAnalysis {
				// Keep the Analysis condition, as it is not a check condition
				remainingConditions = append(remainingConditions, condition)
			}
		}

		// Overwrite the conditions with only the finished condition
		m.workloadHardeningCheck.Status.Conditions = remainingConditions

		return m.Status().Update(ctx, m.workloadHardeningCheck)

	})

	return err
}

func (m *WorkloadCheckManager) BaselineRecorded() bool {
	m.refreshWorkloadHardeningCheck()
	if meta.IsStatusConditionTrue(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline) {
		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline)
		return (condition.Reason == checksv1alpha1.ReasonBaselineRecordingFinished) || (condition.Reason == checksv1alpha1.ReasonBaselineRecordingFailed)
	}
	return false
}

func (m *WorkloadCheckManager) BaselineInProgress() bool {
	m.refreshWorkloadHardeningCheck()
	if meta.IsStatusConditionFalse(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline) {
		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline)
		return condition.Reason == checksv1alpha1.ReasonBaselineRecording
	}
	return false
}

func (m *WorkloadCheckManager) BaselineOverdue() bool {
	// Check if the check is in progress, will also refresh the workloadHardeningCheck
	if m.BaselineInProgress() {
		// Get the duration for the check
		checkDuration := m.GetCheckDuration()

		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline)

		// Check if the check is overdue
		if time.Since(condition.LastTransitionTime.Time) > checkDuration+1*time.Minute { // Adding a buffer of 1 minute
			m.logger.V(2).Info("Baseline is overdue", "duration", checkDuration)
			return true
		}
	}

	return false
}

func (m *WorkloadCheckManager) FinalCheckRecorded() bool {
	m.refreshWorkloadHardeningCheck()

	if meta.IsStatusConditionTrue(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck) {
		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck)
		return (condition.Reason == checksv1alpha1.ReasonCheckRecordingFinished) || (condition.Reason == checksv1alpha1.ReasonCheckRecordingFailed)
	}
	return false
}

func (m *WorkloadCheckManager) FinalCheckInProgress() bool {
	m.refreshWorkloadHardeningCheck()

	if meta.IsStatusConditionFalse(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck) {
		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck)
		return condition.Reason == checksv1alpha1.ReasonCheckRecording
	}
	return false
}

func (m *WorkloadCheckManager) FinalCheckOverdue() bool {
	// Check if the check is in progress, will also refresh the workloadHardeningCheck
	if m.FinalCheckInProgress() {
		// Get the duration for the check
		checkDuration := m.GetCheckDuration()

		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck)

		// Check if the check is overdue
		if time.Since(condition.LastTransitionTime.Time) > checkDuration+1*time.Minute { // Adding a buffer of 1 minute
			m.logger.V(2).Info("FinalCheck is overdue", "duration", checkDuration)
			return true
		}
	}

	return false
}

func (m *WorkloadCheckManager) CheckRecorded(checkType string) bool {
	m.refreshWorkloadHardeningCheck()
	conditionType := titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck
	if meta.IsStatusConditionTrue(m.workloadHardeningCheck.Status.Conditions, conditionType) {
		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, conditionType)
		return (condition.Reason == checksv1alpha1.ReasonCheckRecordingFinished) || (condition.Reason == checksv1alpha1.ReasonCheckRecordingFailed)
	}
	return false
}

func (m *WorkloadCheckManager) CheckInProgress(checkType string) bool {
	m.refreshWorkloadHardeningCheck()
	conditionType := titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck
	if meta.IsStatusConditionFalse(m.workloadHardeningCheck.Status.Conditions, conditionType) {
		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, conditionType)
		return condition.Reason == checksv1alpha1.ReasonCheckRecording
	}
	return false
}

func (m *WorkloadCheckManager) CheckOverdue(checkType string) bool {
	// Check if the check is in progress, will also refresh the workloadHardeningCheck
	if m.CheckInProgress(checkType) {
		// Get the duration for the check
		checkDuration := m.GetCheckDuration()

		condition := meta.FindStatusCondition(m.workloadHardeningCheck.Status.Conditions, titleCase.String(checkType)+checksv1alpha1.ConditionTypeCheck)

		// Check if the check is overdue
		if time.Since(condition.LastTransitionTime.Time) > checkDuration+1*time.Minute { // Adding a buffer of 1 minute
			m.logger.Info("Check is overdue", "checkType", checkType, "duration", checkDuration)
			return true
		}
	}

	return false
}

func (m *WorkloadCheckManager) AllChecksFinished() bool {
	m.refreshWorkloadHardeningCheck()

	if !m.BaselineRecorded() {
		return false // Baseline must be recorded before checks can be considered finished
	}

	requiredChecks := m.GetRequiredCheckRuns(context.Background())

	for _, checkType := range requiredChecks {
		// Check if the condition for the check is true
		if !m.CheckRecorded(checkType) {
			m.logger.V(2).Info("Check not finished", "checkType", checkType)
			return false // If any required check is not finished, return false
		}
	}

	// If we reach here, it means no checks are running
	return true
}

func (m *WorkloadCheckManager) RecommendationExists() bool {
	// Check if the recommendation is set in the status
	if m.workloadHardeningCheck.Status.Recommendation == nil {
		return false
	}

	// Check if the recommendation has a pod security context or container security context
	if m.workloadHardeningCheck.Status.Recommendation.PodSecurityContext == nil &&
		m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts == nil {
		return false
	}

	return true
}
