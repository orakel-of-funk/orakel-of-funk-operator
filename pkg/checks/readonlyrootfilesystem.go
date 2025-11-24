package checks

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

// Checks if the containers remain functional with ReadOnlyRootFilesystem set to true
type ReadOnlyRootFilesystemCheck struct{}

func (c *ReadOnlyRootFilesystemCheck) GetType() string {
	return "readOnlyRootFilesystem"
}

func (c *ReadOnlyRootFilesystemCheck) GetSecurityContextDefaults(baseSecurityContext *checksv1alpha1.SecurityContextDefaults) *checksv1alpha1.SecurityContextDefaults {
	if baseSecurityContext.Container.ReadOnlyRootFilesystem == nil {
		baseSecurityContext.Container.ReadOnlyRootFilesystem = ptr.To(true) // Default to read-only root filesystem
	}

	return baseSecurityContext
}

// This check should run if the pod spec does not have ReadOnlyRootFilesystem set
func (c *ReadOnlyRootFilesystemCheck) ShouldRun(podSpec *corev1.PodSpec) bool {
	// if any container does not have ReadOnlyRootFilesystem set to true, we should run this check
	for _, container := range podSpec.Containers {
		if container.SecurityContext != nil {
			if container.SecurityContext.ReadOnlyRootFilesystem == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
				return true
			}
		} else {
			return true
		}
	}

	return false
}

func init() {
	RegisterCheck(&ReadOnlyRootFilesystemCheck{})
}
