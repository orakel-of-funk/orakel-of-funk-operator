package checks_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/checks"
)

func TestReadOnlyRootFileSystemCheck(t *testing.T) {

	t.Run("GetType", func(t *testing.T) {
		check := &checks.ReadOnlyRootFilesystemCheck{}
		expectedType := "readOnlyRootFilesystem"
		if check.GetType() != expectedType {
			t.Errorf("Expected %s, got %s", expectedType, check.GetType())
		}
	})

	t.Run("GetSecurityContextDefaults", func(t *testing.T) {
		check := &checks.ReadOnlyRootFilesystemCheck{}

		baseSecurityContext := &checksv1alpha1.SecurityContextDefaults{
			Pod:       &checksv1alpha1.PodSecurityContextDefaults{},
			Container: &checksv1alpha1.ContainerSecurityContextDefaults{},
		}

		defaults := check.GetSecurityContextDefaults(baseSecurityContext)

		assert.True(t, *defaults.Container.ReadOnlyRootFilesystem, "Expected `ReadOnlyRootFilesystem` to be set to true by `default`")
	})

	t.Run("ShouldRunSingleContainer", func(t *testing.T) {
		check := &checks.ReadOnlyRootFilesystemCheck{}
		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: ptr.To(false),
					},
				},
			},
		}

		assert.True(t, check.ShouldRun(podSpec), "Expected ShouldRun to return true if `ReadOnlyRootFilesystemCheck` is set to `false`")
	})

	t.Run("ShouldRunMultipleContainers", func(t *testing.T) {
		check := &checks.ReadOnlyRootFilesystemCheck{}
		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: ptr.To(true),
					},
				},
				{
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: ptr.To(false),
					},
				},
			},
		}

		assert.True(t, check.ShouldRun(podSpec), "Expected ShouldRun to return true when at least one container has `ReadOnlyRootFilesystemCheck` set to `false`")
	})

	t.Run("ShouldRunNotRunSingleContainer", func(t *testing.T) {
		check := &checks.ReadOnlyRootFilesystemCheck{}
		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: ptr.To(true),
					},
				},
			},
		}

		assert.False(t, check.ShouldRun(podSpec), "Expected ShouldRun to return false if `ReadOnlyRootFilesystemCheck` is set to `true`")
	})

	t.Run("ShouldRunNotRunMultipleContainers", func(t *testing.T) {
		check := &checks.ReadOnlyRootFilesystemCheck{}
		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: ptr.To(true),
					},
				},
				{
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: ptr.To(true),
					},
				},
			},
		}

		assert.False(t, check.ShouldRun(podSpec), "Expected ShouldRun to return false if `ReadOnlyRootFilesystemCheck` is set to `true` for all containers")
	})
}
