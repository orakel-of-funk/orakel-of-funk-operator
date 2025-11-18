package workload

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

// RemoveCheckConditions removes all check-related conditions from the workloadHardeningCheck.
func (m *WorkloadCheckManager) RemoveCheckConditions(ctx context.Context) error {
	// Remove all check conditions from the workloadHardeningCheck
	m.logger.V(2).Info("Removing all check conditions from WorkloadHardeningCheck", "name", m.WorkloadHardeningCheck.Name)

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Re-fetch the workload hardening check Custom Resource
		if err := m.Get(ctx, types.NamespacedName{Name: m.WorkloadHardeningCheck.Name, Namespace: m.WorkloadHardeningCheck.Namespace}, &m.WorkloadHardeningCheck.WorkloadHardeningCheck); err != nil {
			return err
		}

		remainingConditions := []metav1.Condition{}
		// Iterate over the existing conditions and keep only those that are not related to checks
		for _, condition := range m.WorkloadHardeningCheck.WorkloadHardeningCheck.Status.Conditions {
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
		m.WorkloadHardeningCheck.WorkloadHardeningCheck.Status.Conditions = remainingConditions

		return m.Status().Update(ctx, &m.WorkloadHardeningCheck.WorkloadHardeningCheck)

	})

	return err
}

// Returns true if all checks in Status.CheckRuns are marked as finished
func (m *WorkloadCheckManager) AllChecksFinished() bool {
	m.WorkloadHardeningCheck.RefreshWorkloadHardeningCheck()

	if !m.WorkloadHardeningCheck.BaselineRecorded() {
		return false // Baseline must be recorded before checks can be considered finished
	}

	requiredChecks := m.GetRequiredCheckRuns(context.Background())

	for _, checkType := range requiredChecks {
		// Check if the condition for the check is true
		if !m.WorkloadHardeningCheck.CheckRecorded(checkType) {
			m.logger.V(2).Info("Check not finished", "checkType", checkType)
			return false // If any required check is not finished, return false
		}
	}

	// If we reach here, it means no checks are running
	return true
}
