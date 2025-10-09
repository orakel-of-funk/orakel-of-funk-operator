package checks

import (
	v1 "k8s.io/api/core/v1"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
)

type CheckInterface interface {
	// GetType returns the type of the check, e.g., "group", "user
	GetType() string
	// GetSecurityContext returns the security context defaults for the check
	GetSecurityContextDefaults(*checksv1alpha1.SecurityContextDefaults) *checksv1alpha1.SecurityContextDefaults
	// ShouldRun checks if the check should run for the given pod spec
	ShouldRun(*v1.PodSpec) bool
}

var checks = map[string]CheckInterface{}

func RegisterCheck(check CheckInterface) {
	checks[check.GetType()] = check
}

func GetAllChecks() map[string]CheckInterface {
	return checks
}
