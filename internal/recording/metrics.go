package recording

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	statsapi "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	// setup during init() to avoid passing the client around
	k8sClient  client.Client // Kubernetes client to fetch pods and nodes
	restClient *http.Client  // HTTP client to fetch metrics from kubelet
	apiHost    string        // Host of the kubelet API

	metricsRecordingInterval = 15 // seconds
	logger                   = logf.Log.WithName("MetricsRecorder")

	// the metrics are stored in a map on the package level, to avoid fetching them multiple if multiple hardening checks are run in parallel
	// this is a global cache of pod metrics, refreshed every 10 seconds
	refreshMutex         = sync.Mutex{}
	podMetrics           = make(map[types.UID]statsapi.PodStats)
	podMetricsLastUpdate = time.Time{}
)

type PodError struct {
	message string
	Pod     corev1.Pod
}

func (e *PodError) Error() string {
	return fmt.Sprintf("pod %s: %v", e.Pod.Name, e.message)
}

type MetricsRecorder struct {
	client.Client
	logger logr.Logger
}

func NewMetricsRecorder(ctx context.Context, ksClient client.Client) *MetricsRecorder {
	if apiHost == "" || restClient == nil {
		clientConfig := config.GetConfigOrDie()
		restClient, _ = rest.HTTPClientFor(clientConfig)
		apiHost = clientConfig.Host
	}

	if k8sClient == nil {
		k8sClient = ksClient
	}

	return &MetricsRecorder{
		Client: ksClient,
		logger: logf.FromContext(ctx).WithName("MetricsRecorder"),
	}
}

func (r *MetricsRecorder) RecordMetrics(ctx context.Context, targetNamespace string, labelSelector labels.Selector, checkDuration time.Duration) ([]*ResourceUsageRecord, error) {
	// get pods under observation, we use the label selector from the workload under test
	pods := &corev1.PodList{}
PodsAssigned:
	for len(pods.Items) == 0 {
		err := r.List(
			ctx,
			pods,
			&client.ListOptions{
				Namespace:     targetNamespace,
				LabelSelector: labelSelector,
			},
		)
		if err != nil {
			r.logger.Error(err, "error fetching pods")
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
			r.logger.Info("Pods are not assigned to a node yet, retrying")
			time.Sleep(1 * time.Second) // Wait for 1 second before retrying
		}
	}

	// Filter pods not belonging to the latest generation
	podsToRecord := []corev1.Pod{}
	podNames := make(map[string]bool)
	for _, pod := range pods.Items {
		r.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}, &pod)

		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}
		if pod.DeletionTimestamp != nil {
			// Pod is being deleted, skip it
			continue
		}

		podsToRecord = append(podsToRecord, pod)
		podNames[pod.Name] = true
	}

	r.logger.Info(
		"fetched pods matching workload",
		"numberOfPods", len(podsToRecord),
		"podsToRecord", slices.Collect(maps.Keys(podNames)),
	)

	var wg sync.WaitGroup
	wg.Add(len(podsToRecord))

	// Initialize the channels with the expected capacity to avoid blocking
	metricsChannel := make(chan *RecordedMetrics, len(podsToRecord))

	for _, pod := range podsToRecord {
		go func() {
			// the metrics are collected per pod
			recordedMetrics, err := r.recordPodMetrics(ctx, &pod, checkDuration)
			if err != nil {
				r.logger.Error(err, "failed recording metrics")
			} else {
				r.logger.Info(
					"recorded metrics",
					"podName", pod.Name,
				)
			}

			metricsChannel <- recordedMetrics

			wg.Done()

		}()
	}

	wg.Wait()

	// close channels so that the range loops will stop
	close(metricsChannel)

	resourceUsageRecords := []*ResourceUsageRecord{}
	for result := range metricsChannel {
		for _, usage := range result.Usage {
			resourceUsageRecords = append(resourceUsageRecords, &usage)
		}
	}
	r.logger.V(1).Info("collected metrics")
	if len(resourceUsageRecords) == 0 {
		r.logger.Info("no resource usage metrics recoded, this could be due to the workload not being ready or not having any resource usage metrics available")
	}

	return resourceUsageRecords, nil
}

func (r *MetricsRecorder) recordPodMetrics(ctx context.Context, pod *corev1.Pod, duration time.Duration) (*RecordedMetrics, error) {
	log := r.logger.WithValues(
		"podName", pod.Name,
		"podUID", pod.UID,
		"targetNamespace", pod.Namespace,
	)

	recordedMetrics := map[metav1.Time]ResourceUsageRecord{}

	start := time.Now()
	log.Info("recording resource usage for pod", "expectedEndTime", start.Add(duration))

	lastLoop := false
	for {
		// break loop if the duration is reached
		if time.Since(start) >= duration {
			lastLoop = true
		}

		if pod.Spec.NodeName == "" {
			return nil, fmt.Errorf("pod %s has no node assigned", pod.Name)
		}
		err := refreshPodMetrics(ctx)
		if err != nil {
			return nil, err
		}
		podResources, err := getPodResourceUsage(*pod)

		if err != nil {
			if strings.Contains(err.Error(), "pod not found in metrics") {
				log.V(2).Info("pod not yet found in metrics, retry in 5 seconds")
			} else {
				log.Error(err, "error getting pod resource usage")
			}
			if lastLoop {
				break
			}
			// wait for 5 seconds and try again
			time.Sleep(time.Duration(5) * time.Second)
			continue
		}

		metric := ResourceUsageRecord{
			Time:         metav1.Now(),
			CpuNanoCores: int64(*podResources.CPU.UsageNanoCores),
			MemoryBytes:  int64(*podResources.Memory.WorkingSetBytes),
		}
		recordedMetrics[metric.Time] = metric

		if lastLoop {
			break
		}

		time.Sleep(time.Duration(metricsRecordingInterval) * time.Second)
	}

	log.Info("recorded resource usage", "dataPoints", len(recordedMetrics))

	recordedMetricsData := RecordedMetrics{
		Pod:      pod.Name,
		Start:    start,
		End:      time.Now(),
		Interval: metricsRecordingInterval,
		Duration: int(duration),
		Usage:    recordedMetrics,
	}

	return &recordedMetricsData, nil
}

func getNodes(ctx context.Context) (*corev1.NodeList, error) {

	nodeList := &corev1.NodeList{}
	err := k8sClient.List(
		ctx,
		nodeList,
		&client.ListOptions{},
	)
	if err != nil {
		logger.Error(err, "error fetching nodes")
		return nil, err
	}

	return nodeList, nil
}

func getPodResourceUsage(pod corev1.Pod) (*statsapi.PodStats, error) {

	if podMetricsLastUpdate.IsZero() {
		return nil, &PodError{Pod: pod, message: "pod metrics not initialized"}
	}

	if podStat, ok := podMetrics[pod.UID]; ok {
		return &podStat, nil
	} else {
		return nil, &PodError{Pod: pod, message: "pod not found in metrics"}
	}
}

// refreshPodMetrics refreshes the pod metrics for all nodes
func refreshPodMetrics(ctx context.Context) error {
	refreshMutex.Lock()
	defer refreshMutex.Unlock()

	if time.Since(podMetricsLastUpdate) < 10*time.Second {
		return nil
	}

	nodes, err := getNodes(ctx)
	if err != nil {
		logger.Error(err, "error fetching nodes")
		return err
	}

	var wg sync.WaitGroup
	wg.Add(len(nodes.Items))

	resultsChannel := make(chan map[types.UID]statsapi.PodStats, len(nodes.Items))

	for _, node := range nodes.Items {
		// update nodes in parallel
		go func() {
			refreshPodMetricsPerNode(node.Name, resultsChannel)
			wg.Done()
		}()
	}

	wg.Wait()

	close(resultsChannel)

	newPodMetrics := map[types.UID]statsapi.PodStats{}

	for result := range resultsChannel {
		maps.Copy(newPodMetrics, result)
	}

	podMetricsLastUpdate = time.Now()
	podMetrics = newPodMetrics

	return nil

}

// refreshes the pod metrics for a specific node, this relies on the stats/summary endpoint of the kubelet
func refreshPodMetricsPerNode(nodeName string, resultsChannel chan map[types.UID]statsapi.PodStats) error {

	// Reset the pod metrics map and known pods
	newMetrics := map[types.UID]statsapi.PodStats{}

	req, err := restClient.Get(apiHost + "/api/v1/nodes/" + nodeName + "/proxy/stats/summary")
	if err != nil {
		logger.Error(
			err,
			"couldn't fetch metrics",
			"nodeName", nodeName,
		)
		return err
	}
	defer req.Body.Close()

	if req.StatusCode != http.StatusOK {
		logger.Error(
			fmt.Errorf("failed to get node metrics: %s", req.Status),
			"couldn't fetch metrics",
			"nodeName", nodeName,
		)
		return fmt.Errorf("failed to get node metrics: %s", req.Status)
	}

	var nodeMetrics statsapi.Summary
	err = json.NewDecoder(req.Body).Decode(&nodeMetrics)
	if err != nil {
		logger.Error(
			err,
			"couldn't decode node metrics",
			"nodeName", nodeName,
		)
		return err
	}

	for _, pod := range nodeMetrics.Pods {
		newMetrics[types.UID(pod.PodRef.UID)] = pod
	}

	resultsChannel <- newMetrics

	return nil

}
