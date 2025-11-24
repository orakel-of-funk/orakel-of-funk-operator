package checks

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

var capabilityChecks = map[string]CheckInterface{}

// RegisterCapabilityCheck registers a capability check
func registerCapabilityCheck(capabilityCheck CheckInterface) error {
	if _, exists := capabilityChecks[capabilityCheck.GetType()]; exists {
		return fmt.Errorf("check of type %s already registered", capabilityCheck.GetType())
	}

	capabilityChecks[capabilityCheck.GetType()] = capabilityCheck
	return nil
}

// DropAllCapabilitiesCheck checks if containers remain functional with all capabilities dropped
// Ideally, if this check fails, individual capability checks will be run to determine which capabilities are necessary
type DropAllCapabilitiesCheck struct{}

func (c *DropAllCapabilitiesCheck) GetType() string {
	return "dropAllCapabilities"
}

func (c *DropAllCapabilitiesCheck) GetSecurityContextDefaults(baseSecurityContext *checksv1alpha1.SecurityContextDefaults) *checksv1alpha1.SecurityContextDefaults {
	if baseSecurityContext.Container.CapabilitiesDrop == nil {
		baseSecurityContext.Container.CapabilitiesDrop = []corev1.Capability{"ALL"}
	}

	return baseSecurityContext
}

// This check should run if the pod spec does not already drop all capabilities in all containers
func (c *DropAllCapabilitiesCheck) ShouldRun(podSpec *corev1.PodSpec) bool {
	// if any container does not any capabilities dropped, we should run this check
	for _, container := range podSpec.Containers {
		if container.SecurityContext != nil {
			if container.SecurityContext.Capabilities == nil || len(container.SecurityContext.Capabilities.Drop) == 0 {
				return true
			}
		} else {
			return true
		}
	}

	// At least one container drops a capability, so we check the capabilityChecks, and if necessary run them
	for _, capabilityCheck := range capabilityChecks {
		if capabilityCheck.ShouldRun(podSpec) {
			RegisterCheck(capabilityCheck)
		}
	}

	return false
}

func init() {
	RegisterCheck(&DropAllCapabilitiesCheck{})
}
