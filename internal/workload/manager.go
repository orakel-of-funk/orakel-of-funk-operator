package workload

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"

	checksv1alpha1 "github.com/orakel-of-funk/orakel-of-funk-operator/api/v1alpha1"
	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/valkey"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/checks"
	"github.com/orakel-of-funk/orakel-of-funk-operator/pkg/orakel"
)

var (
	// Required to convert "user" to "User", strings.ToTitle converts each rune to title case not just the first one
	titleCase = cases.Title(language.English)
)

type WorkloadCheckManager struct {
	client.Client

	valKeyClient *valkey.ValkeyClient

	logger logr.Logger

	workloadHardeningCheck *checksv1alpha1.WorkloadHardeningCheck

	allChecks map[string]checks.CheckInterface
}

func NewWorkloadCheckManager(ctx context.Context, valKeyClient *valkey.ValkeyClient, workloadHardeningCheck *checksv1alpha1.WorkloadHardeningCheck) *WorkloadCheckManager {

	log := logf.FromContext(ctx).WithName("WorkloadManager")
	scheme := runtime.NewScheme()

	//nolint:errcheck
	clientgoscheme.AddToScheme(scheme)
	//nolint:errcheck
	checksv1alpha1.AddToScheme(scheme)

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

	checkManager := &WorkloadCheckManager{
		Client:                 cl,
		logger:                 log,
		workloadHardeningCheck: workloadHardeningCheck.DeepCopy(),
		valKeyClient:           valKeyClient,
		allChecks:              checks.GetAllChecks(),
	}

	return checkManager

}

func (m *WorkloadCheckManager) refreshWorkloadHardeningCheck() {
	if err := m.Get(context.Background(), types.NamespacedName{Name: m.workloadHardeningCheck.Name, Namespace: m.workloadHardeningCheck.Namespace}, m.workloadHardeningCheck); err != nil {
		m.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
	}
}

func (m *WorkloadCheckManager) GetReplicaCount(ctx context.Context, namespace string) (int32, error) {

	workloadUnderTestPtr, err := m.GetWorkloadUnderTest(ctx, namespace)
	if err != nil {
		m.logger.Error(err, "failed to get workload under test")
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

func (m *WorkloadCheckManager) ScaleWorkloadUnderTest(ctx context.Context, namespace string, replicas int32) error {

	workloadUnderTestPtr, err := m.GetWorkloadUnderTest(ctx, namespace)
	if err != nil {
		m.logger.Error(err, "failed to get workload under test")
		return fmt.Errorf("failed to get workload under test: %w", err)
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := m.Get(ctx, types.NamespacedName{Name: (*workloadUnderTestPtr).GetName(), Namespace: namespace}, *workloadUnderTestPtr); err != nil {
			if apierrors.IsNotFound(err) {
				// workloadHardeningCheck resource was deleted, while a check was running
				m.logger.Info("WorkloadHardeningCheck not found, skipping check run update")
				return nil // If the resource is not found, we can skip the update
			}
			m.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
			return fmt.Errorf("failed to re-fetch WorkloadHardeningCheck: %w", err)
		}

		switch v := (*workloadUnderTestPtr).(type) {
		case *appsv1.Deployment:
			v.Spec.Replicas = &replicas
		case *appsv1.StatefulSet:
			v.Spec.Replicas = &replicas
		case *appsv1.DaemonSet:
			return fmt.Errorf("cannot scale DaemonSet")
		default:
			return fmt.Errorf("unsupported workload kind: %T", v)
		}

		return m.Update(ctx, *workloadUnderTestPtr)
	})

	if err == nil {
		m.logger.V(2).Info("scaled workload under test", "replicas", replicas, "workload", (*workloadUnderTestPtr).GetName())
		return nil
	}

	return err
}

func (m *WorkloadCheckManager) GetPodSpecTemplate(ctx context.Context, namespace string) (*corev1.PodSpec, error) {

	workloadUnderTestPtr, err := m.GetWorkloadUnderTest(ctx, namespace)
	if err != nil {
		m.logger.Error(err, "failed to get workload under test")
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

func (m *WorkloadCheckManager) GetWorkloadUnderTest(ctx context.Context, namespace string) (*client.Object, error) {

	name := m.workloadHardeningCheck.Spec.TargetRef.Name
	kind := m.workloadHardeningCheck.Spec.TargetRef.Kind

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

	err := m.Get(
		ctx,
		types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
		workloadUnderTest,
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then it usually means that it was deleted or not created
			m.logger.Info("TargetRef not found. You must reference an existing workload to test it", "name", name, "namespace", namespace, "kind", kind)
			return nil, fmt.Errorf("TargetRef not found. You must reference an existing workload to test it")
		}
		// Error reading the object - requeue the request.
		m.logger.Error(err, "failed to get workloadHardeningCheck.Spec.TargetRef, requeing")
		return nil, fmt.Errorf("failed to get workloadHardeningCheck.Spec.TargetRef: %w", err)
	}

	return &workloadUnderTest, nil
}

func (m *WorkloadCheckManager) VerifyRunning(ctx context.Context, namespace string) (bool, error) {
	workloadUnderTestPtr, err := m.GetWorkloadUnderTest(ctx, namespace)
	if err != nil {
		m.logger.Error(err, "failed to get workload under test")
		return false, fmt.Errorf("failed to get workload under test: %w", err)
	}

	return VerifyReadiness(workloadUnderTestPtr, m.Client)
}

func (m *WorkloadCheckManager) GetLabelSelector(ctx context.Context) (labels.Selector, error) {
	workloadUnderTest, err := m.GetWorkloadUnderTest(ctx, m.workloadHardeningCheck.GetNamespace())
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

func (m *WorkloadCheckManager) AnalyzeCheckRuns(ctx context.Context) error {

	// Contains a drainMiner for each container in the baseline recording
	logOraclePerContainer := make(map[string]*orakel.LogOrakel)
	metricsOracle := orakel.NewMetricsOrakel()

	baselineRecordings := []string{
		fmt.Sprintf("%s:%s:%s", m.workloadHardeningCheck.Namespace, m.workloadHardeningCheck.Spec.Suffix, "baseline"),
		fmt.Sprintf("%s:%s:%s", m.workloadHardeningCheck.Namespace, m.workloadHardeningCheck.Spec.Suffix, "baseline-2"),
	}

	// Use custom baseline recording if specified, we assume that the existance of this was already validated
	if m.workloadHardeningCheck.Spec.BaselineRecordingReference != nil && *m.workloadHardeningCheck.Spec.BaselineRecordingReference != "" {
		baselineRecordings = []string{
			*m.workloadHardeningCheck.Spec.BaselineRecordingReference,
			*m.workloadHardeningCheck.Spec.BaselineRecordingReference + "-2",
		}
	}

	// Record baseline for both baseline recordings
	for _, baseline := range baselineRecordings {
		// Get results from the workload hardening check from ValKey
		baselineRecording, err := m.valKeyClient.GetRecording(ctx, baseline)
		if err != nil {
			m.logger.Error(err, "Failed to get baseline recording from ValKey")
			return fmt.Errorf("failed to get baseline recording from ValKey: %w", err)
		}
		if baselineRecording == nil {
			m.logger.Info("No baseline recording found, skipping analysis")
			return nil
		}

		for containerName, logs := range baselineRecording.Logs {

			drainMiner, exists := logOraclePerContainer[containerName]
			if !exists {
				// Initialize a new DrainMiner
				drainMiner = orakel.NewLogOrakel()
			}

			drainMiner.LoadBaseline(logs)
			logOraclePerContainer[containerName] = drainMiner
		}

		// Metrics oracle is per pod/workload
		metricsOracle.LoadBaseline(baselineRecording)
	}

	checkRuns := m.workloadHardeningCheck.Status.CheckRuns

	if len(checkRuns) == 0 {
		m.logger.V(2).Info("No check runs found in workload hardening check status, skipping analysis")
		return nil
	}

	updatedCheckRuns := make(map[string]*checksv1alpha1.CheckRun, len(checkRuns))
	// Iterate over all check runs and analyze the logs
	for _, checkRun := range checkRuns {
		checkRun := checkRun.DeepCopy() // Create a copy to avoid modifying the original

		m.logger.V(2).Info("Analyzing check run", "checkRun", checkRun.Name)

		// Get the recording for this check run
		checkRecording, err := m.valKeyClient.GetRecording(ctx, fmt.Sprintf("%s:%s:%s", m.workloadHardeningCheck.Namespace, m.workloadHardeningCheck.Spec.Suffix, checkRun.Name))
		if err != nil {
			return fmt.Errorf("failed to get recording for check run from ValKey: %w", err)
		}
		if checkRecording == nil {
			return fmt.Errorf("no recording found for check run %s", checkRun.Name)
		}

		checkSuccessful := checkRecording.Success // If the pod was crashLooping, the recording will be marked as unsuccessful
		if checkRun.CheckSuccessfull != nil {
			checkSuccessful = *checkRun.CheckSuccessfull // Use the existing value if it exists
		}
		for containerName, logs := range checkRecording.Logs {
			drainMiner, exists := logOraclePerContainer[containerName]
			if !exists {
				m.logger.Info("No baseline found for pod", "podName", containerName)
				continue
			}

			anomalies, _ := drainMiner.AnalyzeTarget(logs)
			if len(anomalies) > 0 {
				m.logger.Info("Anomalies found in check run", "checkRun", checkRun.Name, "containerName", containerName, "anomalyCount", len(anomalies))

				if checkRun.FailureReason == "" {
					// Set the failure reason only if it is not already set
					checkRun.FailureReason = fmt.Sprintf("Anomalies found in logs of container %s", containerName)
				} else {
					// Append to the existing failure reason
					checkRun.FailureReason += fmt.Sprintf(", Anomalies found in logs of container %s", containerName)
				}
				checkSuccessful = false

				if checkRun.LogAnomalies == nil {
					checkRun.LogAnomalies = make(map[string][]string)
				}

				if len(anomalies) <= 10 {

					checkRun.LogAnomalies[containerName] = anomalies

				} else if len(anomalies) > 10 {

					anomalyMiner := orakel.NewLogOrakel()
					anomalyMiner.LoadBaseline(logs)

					anomalyTemplates := anomalyMiner.GetTemplates()

					if len(anomalyTemplates) > 5 {
						m.logger.V(2).Info("Trimming anomaly templates to last 5", "checkRun", checkRun.Name, "containerName", containerName)
						// First 5 anomalies are the most significant ones
						checkRun.LogAnomalies[containerName] = anomalyTemplates[:5]
					} else {
						checkRun.LogAnomalies[containerName] = anomalyTemplates
					}

				}
			} else {
				m.logger.Info("No anomalies found in check run", "checkRun", checkRun.Name, "containerName", containerName)
			}
		}

		// Update the check run with the analysis results
		checkRun.CheckSuccessfull = ptr.To(checkSuccessful)
		updatedCheckRuns[checkRun.Name] = checkRun

		// Just add them to the check run, currently not further evaluated
		cpuDeviation, memoryDeviation := metricsOracle.AnalyzeTarget(checkRecording)
		checkRun.CpuDeviation = ptr.To(cpuDeviation)
		checkRun.MemoryDeviation = ptr.To(memoryDeviation)
	}

	// Update the check run status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := m.Get(ctx, types.NamespacedName{Name: m.workloadHardeningCheck.Name, Namespace: m.workloadHardeningCheck.Namespace}, m.workloadHardeningCheck); err != nil {
			if apierrors.IsNotFound(err) {
				// workloadHardeningCheck resource was deleted, while a check was running
				m.logger.Info("WorkloadHardeningCheck not found, skipping check run update")
				return nil // If the resource is not found, we can skip the update
			}
			m.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
			return fmt.Errorf("failed to re-fetch WorkloadHardeningCheck: %w", err)
		}

		// Set/Update condition
		m.workloadHardeningCheck.Status.CheckRuns = updatedCheckRuns

		return m.Status().Update(ctx, m.workloadHardeningCheck)
	})

	return err

}

func (m *WorkloadCheckManager) SetRecommendation(ctx context.Context) error {

	securityContexts := map[string]*checksv1alpha1.SecurityContextDefaults{}

	// Get the security context for each check type
	for _, checkRun := range m.workloadHardeningCheck.Status.CheckRuns {
		if checkRun.Name == "baseline" {
			continue // Skip baseline check
		}
		if checkRun.CheckSuccessfull != nil && !*checkRun.CheckSuccessfull {
			continue // Skip check runs that were not successful
		}

		securityContexts[checkRun.Name] = checkRun.SecurityContext
	}

	podSpecTemplate, err := m.GetPodSpecTemplate(ctx, m.workloadHardeningCheck.Namespace)
	if err != nil {
		m.logger.Error(err, "Failed to get workload under test")
		return fmt.Errorf("failed to get workload under test: %w", err)
	}

	podSecurityContext := &corev1.PodSecurityContext{}
	containerSecurityContext := &corev1.SecurityContext{}

	// Get security context already set in the original manifest
	if podSpecTemplate != nil && podSpecTemplate.SecurityContext != nil {
		podSecurityContext = podSpecTemplate.SecurityContext
	}
	if len(podSpecTemplate.Containers) > 0 && podSpecTemplate.Containers[0].SecurityContext != nil {
		// We assume that the first container in the pod spec template is the main container
		containerSecurityContext = podSpecTemplate.Containers[0].SecurityContext
	}

	for _, securityContext := range securityContexts {
		podSecurityContext = mergePodSecurityContexts(ctx, podSecurityContext, securityContext.Pod.ToK8sSecurityContext())
		containerSecurityContext = mergeContainerSecurityContexts(ctx, containerSecurityContext, securityContext.Container.ToK8sSecurityContext())
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Awlays re-fetch the workload hardening check Custom before updating the status
		if err := m.Get(ctx, types.NamespacedName{Name: m.workloadHardeningCheck.Name, Namespace: m.workloadHardeningCheck.Namespace}, m.workloadHardeningCheck); err != nil {
			m.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
			return fmt.Errorf("failed to re-fetch WorkloadHardeningCheck: %w", err)
		}
		// Set/Update the recommendation

		m.workloadHardeningCheck.Status.Recommendation = &checksv1alpha1.Recommendation{
			ContainerSecurityContexts: containerSecurityContext,
			PodSecurityContext:        podSecurityContext,
		}

		return m.Status().Update(ctx, m.workloadHardeningCheck)
	})

	if err != nil {
		m.logger.Error(err, "Failed to update recommendation in WorkloadHardeningCheck status")
		return fmt.Errorf("failed to update recommendation in WorkloadHardeningCheck status: %w", err)
	}

	return nil

}

func (m *WorkloadCheckManager) GetRecommendedSecurityContext() *checksv1alpha1.SecurityContextDefaults {
	// If the recommendation is not set, return nil
	if !m.RecommendationExists() {
		return nil
	}

	recommendation := checksv1alpha1.SecurityContextDefaults{
		Pod:       &checksv1alpha1.PodSecurityContextDefaults{},
		Container: &checksv1alpha1.ContainerSecurityContextDefaults{},
	}

	// If the pod security context is set, use it
	if m.workloadHardeningCheck.Status.Recommendation.PodSecurityContext != nil {
		recommendation.Pod = &checksv1alpha1.PodSecurityContextDefaults{
			RunAsGroup:   m.workloadHardeningCheck.Status.Recommendation.PodSecurityContext.RunAsGroup,
			RunAsUser:    m.workloadHardeningCheck.Status.Recommendation.PodSecurityContext.RunAsUser,
			RunAsNonRoot: m.workloadHardeningCheck.Status.Recommendation.PodSecurityContext.RunAsNonRoot,
			FSGroup:      m.workloadHardeningCheck.Status.Recommendation.PodSecurityContext.FSGroup,
		}
	}

	// If the container security context is set, use it
	if m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts != nil {
		recommendation.Container = &checksv1alpha1.ContainerSecurityContextDefaults{
			RunAsGroup:               m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.RunAsGroup,
			RunAsUser:                m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.RunAsUser,
			RunAsNonRoot:             m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.RunAsNonRoot,
			ReadOnlyRootFilesystem:   m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.ReadOnlyRootFilesystem,
			AllowPrivilegeEscalation: m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.AllowPrivilegeEscalation,
		}
		if m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.Capabilities != nil &&
			m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.Capabilities.Drop != nil {
			recommendation.Container.CapabilitiesDrop = m.workloadHardeningCheck.Status.Recommendation.ContainerSecurityContexts.Capabilities.Drop
		} else {
			recommendation.Container.CapabilitiesDrop = []corev1.Capability{} // Default to dropping all capabilities if not set
		}
	}

	// Return the recommendation from the status
	return &recommendation

}

func (m *WorkloadCheckManager) GetCheckDuration() time.Duration {
	// Default to 5 minutes if not specified
	if m.workloadHardeningCheck.Spec.RecordingDuration == "" {
		return 5 * time.Minute
	}

	// Parse the duration string
	duration, err := time.ParseDuration(m.workloadHardeningCheck.Spec.RecordingDuration)
	if err != nil {
		return 5 * time.Minute // Fallback to default if parsing fails
	}

	return duration
}

func (m *WorkloadCheckManager) SetBaselineRecorded(ctx context.Context) error {
	// Set the BaselineRecorded condition to True
	err := m.SetCondition(ctx, metav1.Condition{
		Type:    checksv1alpha1.ConditionTypeBaseline,
		Status:  metav1.ConditionTrue,
		Reason:  checksv1alpha1.ReasonBaselineRecordingFinished,
		Message: "Baseline recording has been finished",
	})

	if err != nil {
		m.logger.Error(err, "Failed to set BaselineRecorded condition")
		return fmt.Errorf("failed to set BaselineRecorded condition: %w", err)
	}

	baselineRuns := []*checksv1alpha1.CheckRun{
		{
			Name:                 "baseline",
			RecordingSuccessfull: ptr.To(true),
			CheckSuccessfull:     ptr.To(true),
		},
		{
			Name:                 "baseline-2",
			RecordingSuccessfull: ptr.To(true),
			CheckSuccessfull:     ptr.To(true),
		},
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Let's re-fetch the workload hardening check Custom Resource after updating the status so that we have the latest state
		if err := m.Get(ctx, types.NamespacedName{Name: m.workloadHardeningCheck.Name, Namespace: m.workloadHardeningCheck.Namespace}, m.workloadHardeningCheck); err != nil {
			if apierrors.IsNotFound(err) {
				// workloadHardeningCheck resource was deleted, while a check was running
				m.logger.Info("WorkloadHardeningCheck not found, skipping status update")
				return nil // If the resource is not found, we can skip the update
			}
			m.logger.Error(err, "Failed to re-fetch WorkloadHardeningCheck")
		}

		m.workloadHardeningCheck.Status.BaselineRuns = baselineRuns

		return m.Status().Update(ctx, m.workloadHardeningCheck)

	})
}
