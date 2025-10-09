package checks

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

type AllowPrivilegeEscalationCheck struct{}

func (c *AllowPrivilegeEscalationCheck) GetType() string {
	return "allowPrivilegeEscalation"
}

func (c *AllowPrivilegeEscalationCheck) GetSecurityContextDefaults(baseSecurityContext *checksv1alpha1.SecurityContextDefaults) *checksv1alpha1.SecurityContextDefaults {
	if baseSecurityContext.Container.AllowPrivilegeEscalation == nil {
		baseSecurityContext.Container.AllowPrivilegeEscalation = ptr.To(false) // Default to no privilege escalation
	}

	return baseSecurityContext
}

// This check should run if the pod spec does not have ReadOnlyRootFilesystem set
func (c *AllowPrivilegeEscalationCheck) ShouldRun(podSpec *corev1.PodSpec) bool {
	// if any container does not have ReadOnlyRootFilesystem set to true, we should run this check
	for _, container := range podSpec.Containers {
		if container.SecurityContext != nil {
			if container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
				return true
			}
		} else {
			return true
		}
	}

	return false
}

func init() {
	RegisterCheck(&AllowPrivilegeEscalationCheck{})
}
