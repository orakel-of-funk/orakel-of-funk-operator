package recording

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/util/workload"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type CheckRecorder struct {
	client.Client

	log logr.Logger

	checkType       string
	targetNamespace string
	targetRef       checksv1alpha1.TargetReference
	checkDuration   time.Duration
}

func NewCheckRecorder(ctx context.Context, checkType, targetNamespace string, targetRef checksv1alpha1.TargetReference, checkDuration time.Duration) *CheckRecorder {
	log := logf.FromContext(ctx).WithName("CheckRecorder").WithValues("targetNamespace", targetNamespace, "targetKind", targetRef.Kind, "targetName", targetRef.Name)

	scheme := runtime.NewScheme()

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(checksv1alpha1.AddToScheme(scheme))

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Error(err, "failed to get Kubernetes config")
		return nil
	}

	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "failed to create Kubernetes client")
		return nil
	}

	return &CheckRecorder{
		Client:          cl,
		log:             log,
		checkType:       checkType,
		targetNamespace: targetNamespace,
		targetRef:       targetRef,
		checkDuration:   checkDuration,
	}
}

func (r *CheckRecorder) Record(ctx context.Context, successChannel chan *WorkloadRecording, errorChannel chan *error) (*WorkloadRecording, error) {
	r.log.Info("Starting workload hardening check recording", "duration", r.checkDuration.String())

	// Get the workload under test
	var workloadUnderTest client.Object
	switch strings.ToLower(r.targetRef.Kind) {
	case "deployment":
		workloadUnderTest = &appsv1.Deployment{}
	case "statefulset":
		workloadUnderTest = &appsv1.StatefulSet{}
	case "daemonset":
		workloadUnderTest = &appsv1.DaemonSet{}
	default:
		return nil, fmt.Errorf("unsupported workload kind: %s", r.targetRef.Kind)
	}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.targetNamespace, Name: r.targetRef.Name}, workloadUnderTest)
	if err != nil {
		r.log.Error(err, "failed to get workload under test", "namespace", r.targetNamespace, "name", r.targetRef.Name)
		errorChannel <- &err
		return nil, err
	}

	labelSelector, err := workload.GetLabelSelectorForWorkload(&workloadUnderTest)
	if err != nil {
		r.log.Error(err, "failed to get label selector")
		errorChannel <- &err
		return nil, fmt.Errorf("failed to get label selector for workload: %w", err)
	}

	// get pods under observation, we use the label selector from the workload under test
	pods := &corev1.PodList{}
PodsAssigned:
	for len(pods.Items) == 0 {
		err := r.List(
			ctx,
			pods,
			&client.ListOptions{
				Namespace:     r.targetNamespace,
				LabelSelector: labelSelector,
			},
		)
		if err != nil {
			r.log.Error(err, "error fetching pods")
			return nil, err
		}

		// If the pods aren't assigned to a node, we cannot record metrics
		if len(pods.Items) > 0 {
			allAssigned := true
			for _, pod := range pods.Items {
				if pod.Spec.NodeName == "" {
					allAssigned = false
					break
				}
			}
			if allAssigned {
				break PodsAssigned // All pods are assigned to a node, we can proceed
			}

			pods = &corev1.PodList{} // reset podList to retry fetching
			r.log.Info("Pods are not assigned to a node yet, retrying")
			time.Sleep(1 * time.Second) // Wait for 1 second before retrying
		}
	}

	// We don´t need to verify readiness here, as we didn´t change anything, we just need to wait for the check duration

	startTime := time.Now()
	// Wait for the check duration to elapse since all pods started
DurationDelayLoop:
	for {
		for _, pod := range pods.Items {
			if pod.Status.StartTime.Add(r.checkDuration).Before(time.Now()) {
				r.log.Info("Workload hardening check duration elapsed")
				break DurationDelayLoop
			}
		}
		time.Sleep(r.checkDuration / 2)
	}

	podLogRecorder := NewPodLogRecorder(ctx, r.Client)
	podLogs, err := podLogRecorder.RecordLogs(ctx, r.targetNamespace, labelSelector, false)
	if err != nil {
		r.log.Error(err, "failed to record pod logs")
		errorChannel <- &err
		return nil, fmt.Errorf("failed to record pod logs: %w", err)
	}

	workloadRecording := &WorkloadRecording{
		Type:            r.checkType,
		PodStateRunning: true,
		StartTime:       metav1.NewTime(startTime),
		EndTime:         metav1.NewTime(time.Now()),
		Logs:            podLogs,
	}

	r.log.Info("Completed workload hardening check recording")

	successChannel <- workloadRecording

	return workloadRecording, nil

}
