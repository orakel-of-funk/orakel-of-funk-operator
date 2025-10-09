package checks

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

type GroupCheck struct{}

func (c *GroupCheck) GetType() string {
	return "group"
}

func (c *GroupCheck) GetSecurityContextDefaults(baseSecurityContext *checksv1alpha1.SecurityContextDefaults) *checksv1alpha1.SecurityContextDefaults {
	if baseSecurityContext.Pod.RunAsUser == nil {
		baseSecurityContext.Pod.RunAsUser = ptr.To(int64(1000)) // Default user ID
	}

	if baseSecurityContext.Pod.RunAsGroup == nil {
		baseSecurityContext.Pod.RunAsGroup = baseSecurityContext.Pod.RunAsUser
	}

	if baseSecurityContext.Pod.FSGroup == nil {
		baseSecurityContext.Pod.FSGroup = baseSecurityContext.Pod.RunAsGroup
	}

	if baseSecurityContext.Container.RunAsUser == nil {
		baseSecurityContext.Container.RunAsUser = baseSecurityContext.Pod.RunAsUser
	}
	if baseSecurityContext.Container.RunAsGroup == nil {
		baseSecurityContext.Container.RunAsGroup = baseSecurityContext.Pod.RunAsGroup
	}

	return baseSecurityContext
}

// This check should run if the pod spec does not have a RunAsGroup set
func (c *GroupCheck) ShouldRun(podSpec *corev1.PodSpec) bool {
	if podSpec.SecurityContext == nil {
		return true
	}

	return podSpec.SecurityContext.RunAsGroup == nil || podSpec.SecurityContext.FSGroup == nil
}

func init() {
	RegisterCheck(&GroupCheck{})
}
