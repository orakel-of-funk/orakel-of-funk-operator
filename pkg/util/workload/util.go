package workload

import (
	"context"
	"fmt"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var supportedWorkloadKinds = []string{
	"deployment",
	"statefulset",
	"daemonSet",
}

func IsSupportedUnstructured(resource *unstructured.Unstructured) bool {
	// Check if the resource has a kind and apiVersion
	if resource.GetKind() == "" || resource.GetAPIVersion() == "" {
		return false
	}

	return slices.Contains(supportedWorkloadKinds, strings.ToLower(resource.GetKind()))
}

func VerifyUpdated(workloadUnderTest client.Object) (bool, error) {
	switch v := workloadUnderTest.(type) {
	case *appsv1.Deployment:
		return *v.Spec.Replicas == v.Status.UpdatedReplicas, nil
	case *appsv1.StatefulSet:
		return *v.Spec.Replicas == v.Status.CurrentReplicas, nil
	case *appsv1.DaemonSet:
		return v.Status.DesiredNumberScheduled == v.Status.UpdatedNumberScheduled, nil
	}

	return false, fmt.Errorf("kind of workloadUnderTest not supported")
}

func GetPodsForWorkload(ctx context.Context, c client.Client, workloadUnderTest *client.Object) ([]corev1.Pod, error) {

	var labelSelector *metav1.LabelSelector
	switch v := (*workloadUnderTest).(type) {
	case *appsv1.Deployment:
		labelSelector = v.Spec.Selector
	case *appsv1.StatefulSet:
		labelSelector = v.Spec.Selector
	case *appsv1.DaemonSet:
		labelSelector = v.Spec.Selector
	}

	// Get the label selector for the workload
	podLabelSelector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to get label selector: %w", err)
	}

	podList := &corev1.PodList{}
	err = c.List(ctx, podList, &client.ListOptions{
		Namespace:     (*workloadUnderTest).GetNamespace(),
		LabelSelector: podLabelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	return podList.Items, nil
}

// VerifyReadiness checks if the workload under test is ready by verifying the pods are running and their containers are ready.
func VerifyReadiness(workloadUnderTest *client.Object, c client.Client) (bool, error) {
	pods, err := GetPodsForWorkload(context.Background(), c, workloadUnderTest)
	if err != nil {
		return false, fmt.Errorf("failed to get pods for workload: %w", err)
	}
	if len(pods) == 0 {
		return false, fmt.Errorf("no pods found for workload %s/%s", (*workloadUnderTest).GetNamespace(), (*workloadUnderTest).GetName())
	}

	for _, pod := range pods {
		if pod.Status.Phase != corev1.PodRunning {
			return false, fmt.Errorf("pod %s/%s is not running", pod.Namespace, pod.Name)
		}
		if len(pod.Status.ContainerStatuses) == 0 {
			return false, fmt.Errorf("pod %s/%s has no container statuses", pod.Namespace, pod.Name)
		}
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil {
				return false, fmt.Errorf("pod %s/%s container %s is waiting: %s", pod.Namespace, pod.Name, containerStatus.Name, containerStatus.State.Waiting.Reason)
			}
			if containerStatus.State.Terminated != nil {
				return false, fmt.Errorf("pod %s/%s container %s is terminated: %s", pod.Namespace, pod.Name, containerStatus.Name, containerStatus.State.Terminated.Reason)
			}
			if !containerStatus.Ready {
				return false, fmt.Errorf("pod %s/%s container %s is not ready", pod.Namespace, pod.Name, containerStatus.Name)
			}
		}
	}
	return true, nil
}
