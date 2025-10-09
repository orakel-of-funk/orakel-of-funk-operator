package checks_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/checks"
)

func TestUserCheck(t *testing.T) {

	t.Run("GetType", func(t *testing.T) {
		check := &checks.UserCheck{}
		expectedType := "user"
		if check.GetType() != expectedType {
			t.Errorf("Expected %s, got %s", expectedType, check.GetType())
		}
	})

	t.Run("GetSecurityContextDefaults", func(t *testing.T) {
		check := &checks.UserCheck{}

		baseSecurityContext := &checksv1alpha1.SecurityContextDefaults{
			Pod:       &checksv1alpha1.PodSecurityContextDefaults{},
			Container: &checksv1alpha1.ContainerSecurityContextDefaults{},
		}

		defaults := check.GetSecurityContextDefaults(baseSecurityContext)

		assert.Equal(t, int64(1000), *defaults.Container.RunAsUser, "Expected `Container.RunAsUser` to be set to `1000` by default")
		assert.Equal(t, int64(1000), *defaults.Pod.RunAsUser, "Expected `Pod.RunAsUser` to be set to `1000` by default")

		assert.True(t, *defaults.Container.RunAsNonRoot, "Expected `Container.RunAsNonRoot` to be set to `true` by default")
		assert.True(t, *defaults.Pod.RunAsNonRoot, "Expected `Pod.RunAsNonRoot` to be set to `true` by default")
	})

	t.Run("ShouldRunSingleContainer", func(t *testing.T) {
		check := &checks.UserCheck{}
		podSpec := &corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:    nil,
				RunAsNonRoot: nil,
			},
		}

		assert.True(t, check.ShouldRun(podSpec), "Expected ShouldRun to return true if `runAsUser` or `runAsNonRoot` are not set")
	})

	t.Run("ShouldRunNotRunSingleContainer", func(t *testing.T) {
		check := &checks.UserCheck{}
		podSpec := &corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:    ptr.To(int64(2000)),
				RunAsNonRoot: ptr.To(true),
			},
		}

		assert.False(t, check.ShouldRun(podSpec), "Expected ShouldRun to return false if `runAsUser` and `runAsNonRoot` are set")
	})

}
