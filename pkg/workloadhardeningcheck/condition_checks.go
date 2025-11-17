package workloadhardeningcheck

import (
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"k8s.io/apimachinery/pkg/api/meta"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

var (
	// Required to convert "user" to "User", strings.ToTitle converts each rune to title case not just the first one
	titleCase = cases.Title(language.English)
)

func (w *WorkloadHardeningCheck) BaselineRecorded() bool {
	w.RefreshWorkloadHardeningCheck()
	if meta.IsStatusConditionTrue(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline) {
		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline)
		return (condition.Reason == checksv1alpha1.ReasonBaselineRecordingFinished) || (condition.Reason == checksv1alpha1.ReasonBaselineRecordingFailed)
	}
	return false
}

func (w *WorkloadHardeningCheck) BaselineInProgress() bool {
	w.RefreshWorkloadHardeningCheck()
	if meta.IsStatusConditionFalse(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline) {
		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline)
		return condition.Reason == checksv1alpha1.ReasonBaselineRecording
	}
	return false
}

func (w *WorkloadHardeningCheck) BaselineOverdue() bool {
	// Check if the check is in progress, will also refresh the workloadHardeningCheck
	if w.BaselineInProgress() {
		// Get the duration for the check
		checkDuration := w.GetCheckDuration()

		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeBaseline)

		// Check if the check is overdue
		if time.Since(condition.LastTransitionTime.Time) > checkDuration+1*time.Minute { // Adding a buffer of 1 minute
			return true
		}
	}

	return false
}

func (w *WorkloadHardeningCheck) FinalCheckRecorded() bool {
	w.RefreshWorkloadHardeningCheck()

	if meta.IsStatusConditionTrue(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck) {
		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck)
		return (condition.Reason == checksv1alpha1.ReasonCheckRecordingFinished) || (condition.Reason == checksv1alpha1.ReasonCheckRecordingFailed)
	}
	return false
}

func (w *WorkloadHardeningCheck) FinalCheckInProgress() bool {
	w.RefreshWorkloadHardeningCheck()

	if meta.IsStatusConditionFalse(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck) {
		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck)
		return condition.Reason == checksv1alpha1.ReasonCheckRecording
	}
	return false
}

func (w *WorkloadHardeningCheck) FinalCheckOverdue() bool {
	// Check if the check is in progress, will also refresh the workloadHardeningCheck
	if w.FinalCheckInProgress() {
		// Get the duration for the check
		checkDuration := w.GetCheckDuration()

		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, checksv1alpha1.ConditionTypeFinalCheck)

		// Check if the check is overdue
		if time.Since(condition.LastTransitionTime.Time) > checkDuration+1*time.Minute { // Adding a buffer of 1 minute
			return true
		}
	}

	return false
}

func (w *WorkloadHardeningCheck) CheckRecorded(checkType string) bool {
	w.RefreshWorkloadHardeningCheck()
	conditionType := titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck
	if meta.IsStatusConditionTrue(w.WorkloadHardeningCheck.Status.Conditions, conditionType) {
		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, conditionType)
		return (condition.Reason == checksv1alpha1.ReasonCheckRecordingFinished) || (condition.Reason == checksv1alpha1.ReasonCheckRecordingFailed)
	}
	return false
}

func (w *WorkloadHardeningCheck) CheckInProgress(checkType string) bool {
	w.RefreshWorkloadHardeningCheck()
	conditionType := titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck
	if meta.IsStatusConditionFalse(w.WorkloadHardeningCheck.Status.Conditions, conditionType) {
		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, conditionType)
		return condition.Reason == checksv1alpha1.ReasonCheckRecording
	}
	return false
}

func (w *WorkloadHardeningCheck) CheckOverdue(checkType string) bool {
	// Check if the check is in progress, will also refresh the workloadHardeningCheck
	if w.CheckInProgress(checkType) {
		// Get the duration for the check
		checkDuration := w.GetCheckDuration()

		condition := meta.FindStatusCondition(w.WorkloadHardeningCheck.Status.Conditions, titleCase.String(checkType)+checksv1alpha1.ConditionTypeCheck)

		// Check if the check is overdue
		if time.Since(condition.LastTransitionTime.Time) > checkDuration+1*time.Minute { // Adding a buffer of 1 minute
			return true
		}
	}

	return false
}
