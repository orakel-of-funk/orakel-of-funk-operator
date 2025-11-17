package workloadhardeningcheck

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/util/workload"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WorkloadHardeningCheck struct {
	client.Client
	v1alpha1.WorkloadHardeningCheck
}

func (w *WorkloadHardeningCheck) RefreshWorkloadHardeningCheck() {
	w.Get(context.Background(), types.NamespacedName{Name: w.Name, Namespace: w.Namespace}, w)
}

func (w *WorkloadHardeningCheck) GetWorkloadUnderTest(ctx context.Context, targetNamespace string) (*client.Object, error) {

	name := w.Spec.TargetRef.Name
	kind := w.Spec.TargetRef.Kind

	// Verify namespace contains target workload
	var workloadUnderTest client.Object
	switch strings.ToLower(kind) {
	case "deployment":
		workloadUnderTest = &appsv1.Deployment{}
	case "statefulset":
		workloadUnderTest = &appsv1.StatefulSet{}
	case "daemonset":
		workloadUnderTest = &appsv1.DaemonSet{}
	}

	err := w.Get(
		ctx,
		types.NamespacedName{
			Namespace: targetNamespace,
			Name:      name,
		},
		workloadUnderTest,
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then it usually means that it was deleted or not created
			return nil, fmt.Errorf("TargetRef not found. You must reference an existing workload to test it")
		}
		// Error reading the object - requeue the request.
		return nil, fmt.Errorf("failed to get workloadHardeningCheck.Spec.TargetRef: %w", err)
	}

	return &workloadUnderTest, nil
}

func (w *WorkloadHardeningCheck) GetReplicaCount(ctx context.Context, targetNamespace string) (int32, error) {

	workloadUnderTestPtr, err := w.GetWorkloadUnderTest(ctx, targetNamespace)
	if err != nil {
		return 0, fmt.Errorf("failed to get workload under test: %w", err)
	}

	switch v := (*workloadUnderTestPtr).(type) {
	case *appsv1.Deployment:
		if v.Spec.Replicas != nil {
			return *v.Spec.Replicas, nil
		}
		return 1, nil // Default to 1 if not set
	case *appsv1.StatefulSet:
		if v.Spec.Replicas != nil {
			return *v.Spec.Replicas, nil
		}
		return 1, nil // Default to 1 if not set
	case *appsv1.DaemonSet:
		return 0, fmt.Errorf("cannot scale DaemonSet")
	default:
		return 0, fmt.Errorf("unsupported workload kind: %T", v)
	}
}

func (w *WorkloadHardeningCheck) ScaleWorkloadUnderTest(ctx context.Context, targetNamespace string, replicas int32) error {

	workloadUnderTestPtr, err := w.GetWorkloadUnderTest(ctx, targetNamespace)
	if err != nil {
		return fmt.Errorf("failed to get workload under test: %w", err)
	}

	switch v := (*workloadUnderTestPtr).(type) {
	case *appsv1.Deployment:
	case *appsv1.StatefulSet:
	case *appsv1.DaemonSet:
		return fmt.Errorf("cannot scale DaemonSet")
	default:
		return fmt.Errorf("unsupported workload kind: %T", v)
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := w.Get(ctx, types.NamespacedName{Name: (*workloadUnderTestPtr).GetName(), Namespace: targetNamespace}, *workloadUnderTestPtr); err != nil {
			if apierrors.IsNotFound(err) {
				// workloadHardeningCheck resource was deleted, while a check was running
				return nil // If the resource is not found, we can skip the update
			}
			return fmt.Errorf("failed to re-fetch WorkloadHardeningCheck: %w", err)
		}

		switch v := (*workloadUnderTestPtr).(type) {
		case *appsv1.Deployment:
			v.Spec.Replicas = &replicas
		case *appsv1.StatefulSet:
			v.Spec.Replicas = &replicas
		}

		return w.Update(ctx, *workloadUnderTestPtr)
	})

	if err == nil {
		return nil
	}

	return err
}

func (w *WorkloadHardeningCheck) GetPodSpecTemplate(ctx context.Context, namespace string) (*corev1.PodSpec, error) {

	workloadUnderTestPtr, err := w.GetWorkloadUnderTest(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get workload under test: %w", err)
	}

	var podSpecTemplate *corev1.PodSpec
	switch v := (*workloadUnderTestPtr).(type) {
	case *appsv1.Deployment:
		podSpecTemplate = &v.Spec.Template.Spec
	case *appsv1.StatefulSet:
		podSpecTemplate = &v.Spec.Template.Spec
	case *appsv1.DaemonSet:
		podSpecTemplate = &v.Spec.Template.Spec
	default:
		return nil, fmt.Errorf("unsupported workload kind: %T", v)
	}

	return podSpecTemplate, nil
}

func (w *WorkloadHardeningCheck) VerifyRunning(ctx context.Context, namespace string) (bool, error) {
	workloadUnderTestPtr, err := w.GetWorkloadUnderTest(ctx, namespace)
	if err != nil {
		return false, fmt.Errorf("failed to get workload under test: %w", err)
	}

	return workload.VerifyReadiness(workloadUnderTestPtr, w.Client)
}

func (w *WorkloadHardeningCheck) GetLabelSelector(ctx context.Context) (labels.Selector, error) {
	workloadUnderTest, err := w.GetWorkloadUnderTest(ctx, w.GetNamespace())
	if err != nil {
		return nil, err
	}

	var labelSelector *metav1.LabelSelector

	switch v := (*workloadUnderTest).(type) {
	case *appsv1.Deployment:
		labelSelector = v.Spec.Selector
	case *appsv1.StatefulSet:
		labelSelector = v.Spec.Selector
	case *appsv1.DaemonSet:
		labelSelector = v.Spec.Selector
	}

	return metav1.LabelSelectorAsSelector(labelSelector)
}

func (w *WorkloadHardeningCheck) GetRecommendedSecurityContext() *v1alpha1.SecurityContextDefaults {
	// If the recommendation is not set, return nil
	if !w.RecommendationExists() {
		return nil
	}

	recommendation := v1alpha1.SecurityContextDefaults{
		Pod:       &v1alpha1.PodSecurityContextDefaults{},
		Container: &v1alpha1.ContainerSecurityContextDefaults{},
	}

	// If the pod security context is set, use it
	if w.WorkloadHardeningCheck.Status.Recommendation.PodSecurityContext != nil {
		recommendation.Pod = &v1alpha1.PodSecurityContextDefaults{
			RunAsGroup:   w.WorkloadHardeningCheck.Status.Recommendation.PodSecurityContext.RunAsGroup,
			RunAsUser:    w.WorkloadHardeningCheck.Status.Recommendation.PodSecurityContext.RunAsUser,
			RunAsNonRoot: w.WorkloadHardeningCheck.Status.Recommendation.PodSecurityContext.RunAsNonRoot,
			FSGroup:      w.WorkloadHardeningCheck.Status.Recommendation.PodSecurityContext.FSGroup,
		}
	}

	// If the container security context is set, use it
	if w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts != nil {
		recommendation.Container = &v1alpha1.ContainerSecurityContextDefaults{
			RunAsGroup:               w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.RunAsGroup,
			RunAsUser:                w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.RunAsUser,
			RunAsNonRoot:             w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.RunAsNonRoot,
			ReadOnlyRootFilesystem:   w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.ReadOnlyRootFilesystem,
			AllowPrivilegeEscalation: w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.AllowPrivilegeEscalation,
		}
		if w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.Capabilities != nil &&
			w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.Capabilities.Drop != nil {
			recommendation.Container.CapabilitiesDrop = w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.Capabilities.Drop
		} else {
			recommendation.Container.CapabilitiesDrop = []corev1.Capability{} // Default to dropping all capabilities if not set
		}
	}

	// Return the recommendation from the status
	return &recommendation

}

func (w *WorkloadHardeningCheck) GetCheckDuration() time.Duration {
	// Default to 5 minutes if not specified
	if w.Spec.RecordingDuration == "" {
		return 5 * time.Minute
	}

	// Parse the duration string
	duration, err := time.ParseDuration(w.Spec.RecordingDuration)
	if err != nil {
		return 5 * time.Minute // Fallback to default if parsing fails
	}

	return duration
}

func (w *WorkloadHardeningCheck) RecommendationExists() bool {
	// Check if the recommendation is set in the status
	if w.WorkloadHardeningCheck.Status.Recommendation == nil {
		return false
	}

	// Check if the recommendation has a pod security context or container security context
	if w.WorkloadHardeningCheck.Status.Recommendation.PodSecurityContext == nil &&
		w.WorkloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts == nil {
		return false
	}

	return true
}

func (w *WorkloadHardeningCheck) SetCondition(ctx context.Context, condition metav1.Condition) error {

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := w.Get(ctx, types.NamespacedName{Name: w.Name, Namespace: w.Namespace}, &w.WorkloadHardeningCheck); err != nil {
			if apierrors.IsNotFound(err) {
				// workloadHardeningCheck resource was deleted, while a check was running
				return nil // If the resource is not found, we can skip the update
			}
			return err
		}

		// Set/Update condition
		meta.SetStatusCondition(
			&w.WorkloadHardeningCheck.Status.Conditions,
			condition,
		)

		return w.Client.Status().Update(ctx, &w.WorkloadHardeningCheck)

	})

	return err
}
