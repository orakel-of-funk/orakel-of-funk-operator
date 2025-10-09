package checks_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/checks"
)

func TestDropAllCapabilitiesCheck(t *testing.T) {

	t.Run("GetType", func(t *testing.T) {
		check := &checks.DropAllCapabilitiesCheck{}
		expectedType := "dropAllCapabilities"
		if check.GetType() != expectedType {
			t.Errorf("Expected %s, got %s", expectedType, check.GetType())
		}
	})

	t.Run("GetSecurityContextDefaults", func(t *testing.T) {
		check := &checks.DropAllCapabilitiesCheck{}

		baseSecurityContext := &checksv1alpha1.SecurityContextDefaults{
			Pod:       &checksv1alpha1.PodSecurityContextDefaults{},
			Container: &checksv1alpha1.ContainerSecurityContextDefaults{},
		}

		defaults := check.GetSecurityContextDefaults(baseSecurityContext)

		assert.Equal(t, []corev1.Capability{"ALL"}, defaults.Container.CapabilitiesDrop, "Expected `CapabilitiesDrop` to be set to `ALL` by default")
	})

	t.Run("ShouldRunSingleContainer", func(t *testing.T) {
		check := &checks.DropAllCapabilitiesCheck{}
		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					SecurityContext: &corev1.SecurityContext{},
				},
			},
		}

		assert.True(t, check.ShouldRun(podSpec), "Expected ShouldRun to return true if `Capabilities` are empty")
	})

	t.Run("ShouldRunMultipleContainers", func(t *testing.T) {
		check := &checks.DropAllCapabilitiesCheck{}
		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
				{
					SecurityContext: &corev1.SecurityContext{},
				},
			},
		}

		assert.True(t, check.ShouldRun(podSpec), "Expected ShouldRun to return true when at least one container has `Capabilities.Drop` not set to `ALL`")
	})

	t.Run("ShouldRunNotRunSingleContainer", func(t *testing.T) {
		check := &checks.DropAllCapabilitiesCheck{}
		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
			},
		}

		assert.False(t, check.ShouldRun(podSpec), "Expected ShouldRun to return false if `Capabilities.Drop` is set to `ALL`")
	})

	t.Run("ShouldRunNotRunMultipleContainers", func(t *testing.T) {
		check := &checks.DropAllCapabilitiesCheck{}
		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
				{
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
			},
		}

		assert.False(t, check.ShouldRun(podSpec), "Expected ShouldRun to return false if all containers have `Capabilities.Drop` set to `ALL`")
	})
}
