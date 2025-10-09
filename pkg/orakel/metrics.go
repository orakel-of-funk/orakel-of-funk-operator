package orakel

import (
	"slices"

	"gonum.org/v1/gonum/stat"

	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/recording"
)

type CheckMetricsSummary struct {
	CpuSummary    MetricsSummary
	MemorySummary MetricsSummary
}

func (cms *CheckMetricsSummary) updateValues(cpu, memory int64) {
	cms.CpuSummary.originalValues = append(cms.CpuSummary.originalValues, cpu)
	cms.MemorySummary.originalValues = append(cms.MemorySummary.originalValues, memory)
}

func NewCheckMetricsSummary(metrics []recording.ResourceUsageRecord) *CheckMetricsSummary {
	checkSummary := &CheckMetricsSummary{
		CpuSummary: MetricsSummary{
			originalValues: []int64{},
		},
		MemorySummary: MetricsSummary{
			originalValues: []int64{},
		},
	}

	for _, containerMetrics := range metrics {
		checkSummary.updateValues(containerMetrics.CpuNanoCores, containerMetrics.MemoryBytes)
	}

	// Calculate the summary statistics for CPU and Memory
	checkSummary.CpuSummary.updateSummary()
	checkSummary.MemorySummary.updateSummary()

	return checkSummary
}

type MetricsSummary struct {
	originalValues   []int64
	normalizedValues []float64 // Normalized values for statistical calculations

	// Absolute values
	Min int64
	Max int64
	Avg float64

	// Normalized values
	Median   float64
	StdDev   float64
	Variance float64
}

// updateSummary calculates the summary statistics based on the original values
// and updates the Min, Max, Avg, Median, and StdDev fields.
func (ms *MetricsSummary) updateSummary() {
	if len(ms.originalValues) == 0 {
		return
	}

	slices.Sort(ms.originalValues)

	ms.Min = ms.originalValues[0]
	ms.Max = ms.originalValues[len(ms.originalValues)-1]

	total := int64(0)
	for _, value := range ms.originalValues {
		total += value
	}
	ms.Avg = float64(total) / float64(len(ms.originalValues))

	// Reset normalized values
	ms.normalizedValues = make([]float64, len(ms.originalValues))
	// Normalize the original values to the range [0, 1]
	for i, value := range ms.originalValues {
		if ms.Max == ms.Min {
			ms.normalizedValues[i] = 0 // Avoid division by zero
		} else {
			ms.normalizedValues[i] = (float64(value) - float64(ms.Min)) / (float64(ms.Max) - float64(ms.Min))
		}
	}

	// Calculate median
	if len(ms.normalizedValues)%2 == 0 {
		ms.Median = (ms.normalizedValues[len(ms.normalizedValues)/2-1] + ms.normalizedValues[len(ms.normalizedValues)/2]) / 2
	} else {
		// Odd number of elements
		ms.Median = ms.normalizedValues[len(ms.normalizedValues)/2]
	}

	ms.StdDev = stat.StdDev(ms.normalizedValues, nil)
	ms.Variance = stat.Variance(ms.normalizedValues, nil)
}

type MetricsOrakel struct {
	// Baseline metrics count
	BaselineMetricsSummary *CheckMetricsSummary
}

func NewMetricsOrakel() *MetricsOrakel {
	return &MetricsOrakel{
		BaselineMetricsSummary: &CheckMetricsSummary{
			CpuSummary:    MetricsSummary{},
			MemorySummary: MetricsSummary{},
		},
	}
}

func (mo *MetricsOrakel) LoadBaseline(workloadRecording *recording.WorkloadRecording) {
	// Load baseline metrics from the recording
	if mo.BaselineMetricsSummary == nil {
		mo.BaselineMetricsSummary = NewCheckMetricsSummary(workloadRecording.RecordedMetrics)
		return
	}

	for _, containerMetrics := range workloadRecording.RecordedMetrics {
		mo.BaselineMetricsSummary.updateValues(containerMetrics.CpuNanoCores, containerMetrics.MemoryBytes)
	}

	// Calculate the summary statistics for CPU and Memory
	mo.BaselineMetricsSummary.CpuSummary.updateSummary()
	mo.BaselineMetricsSummary.MemorySummary.updateSummary()

}

func (mo *MetricsOrakel) AnalyzeTarget(workloadRecording *recording.WorkloadRecording) (cpuDeviation bool, memoryDeviation bool) {
	// This is called after all baseline metrics have been loaded, so we calculate the summary statistics
	targetMetricsSummary := NewCheckMetricsSummary(workloadRecording.RecordedMetrics)

	cpuUpperThreshold := mo.BaselineMetricsSummary.CpuSummary.Avg * (1 + mo.BaselineMetricsSummary.CpuSummary.StdDev)
	cpuLowerThreshold := mo.BaselineMetricsSummary.CpuSummary.Avg * (1 - mo.BaselineMetricsSummary.CpuSummary.StdDev)

	memoryUpperThreshold := mo.BaselineMetricsSummary.MemorySummary.Avg * (1 + mo.BaselineMetricsSummary.MemorySummary.StdDev)
	memoryLowerThreshold := mo.BaselineMetricsSummary.MemorySummary.Avg * (1 - mo.BaselineMetricsSummary.MemorySummary.StdDev)

	// Check if deviation from baseline is significant
	// This can be done by comparing the target metrics with the baseline metrics
	cpuDeviation = targetMetricsSummary.CpuSummary.Avg > cpuUpperThreshold || targetMetricsSummary.CpuSummary.Avg < cpuLowerThreshold
	memoryDeviation = targetMetricsSummary.MemorySummary.Avg > memoryUpperThreshold || targetMetricsSummary.MemorySummary.Avg < memoryLowerThreshold

	// Here you can implement further analysis logic, e.g., comparing with baseline
	// For now, we just return the target metrics summary
	return
}
