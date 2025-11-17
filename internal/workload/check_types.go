package workload

import (
	"context"
	"fmt"

	"golang.org/x/exp/maps"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

func (m *WorkloadCheckManager) GetRequiredCheckRuns(ctx context.Context) []string {
	checks := map[string]bool{}

	workloadUnderTest, err := m.WorkloadHardeningCheck.GetWorkloadUnderTest(ctx, m.WorkloadHardeningCheck.Namespace)
	if err != nil {
		m.logger.Error(err, "Failed to get workload under test")
		return []string{}
	}

	// Determine the type of workload under test and extract the pod spec template
	var podSpecTemplate *v1.PodSpec
	switch v := (*workloadUnderTest).(type) {
	case *appsv1.Deployment:
		podSpecTemplate = &v.Spec.Template.Spec
	case *appsv1.StatefulSet:
		podSpecTemplate = &v.Spec.Template.Spec
	case *appsv1.DaemonSet:
		podSpecTemplate = &v.Spec.Template.Spec
	}

	for _, check := range m.allChecks {
		if check.ShouldRun(podSpecTemplate) {
			checks[check.GetType()] = true
			checkType := check.GetType()

			if meta.FindStatusCondition(m.WorkloadHardeningCheck.WorkloadHardeningCheck.Status.Conditions, titleCase.String(checkType)+checksv1alpha1.ConditionTypeCheck) == nil {
				// if the conditions is not found, we add it in unknown state
				m.WorkloadHardeningCheck.SetCondition(ctx, metav1.Condition{
					Type:    titleCase.String(checkType) + checksv1alpha1.ConditionTypeCheck,
					Status:  metav1.ConditionUnknown,
					Reason:  checksv1alpha1.ReasonCheckNotStarted,
					Message: fmt.Sprintf("Check %s has not been started yet", checkType),
				})

			}
		}
	}

	return maps.Keys(checks)

}

func (m *WorkloadCheckManager) GetSecurityContextForCheckType(checkType string) *checksv1alpha1.SecurityContextDefaults {

	baseSecurityContext := m.WorkloadHardeningCheck.Spec.SecurityContext
	if baseSecurityContext == nil {
		baseSecurityContext = &checksv1alpha1.SecurityContextDefaults{
			Pod:       &checksv1alpha1.PodSecurityContextDefaults{},
			Container: &checksv1alpha1.ContainerSecurityContextDefaults{},
		}
	}

	return m.allChecks[checkType].GetSecurityContextDefaults(baseSecurityContext)

}
