package orakel

import (
	"fmt"

	"github.com/orakel-of-funk/orakel-of-funk-operator/internal/recording"
)

// LogAnalyzer implements the Orakel interface using Drain-based log pattern analysis.
// It maintains a per-container LogOrakel for template matching.
type LogAnalyzer struct {
	// Per-container log orakels, keyed by container name
	orakels map[string]*LogOrakel

	// Maximum number of anomaly entries to report per container
	maxAnomaliesPerContainer int

	// normalizeTimestamps enables timestamp normalization before training/matching
	normalizeTimestamps bool
}

// NewLogAnalyzer creates a new LogAnalyzer oracle.
func NewLogAnalyzer() *LogAnalyzer {
	return &LogAnalyzer{
		orakels:                  make(map[string]*LogOrakel),
		maxAnomaliesPerContainer: 10,
	}
}

// NewLogAnalyzerWithNormalization creates a new LogAnalyzer with optional timestamp normalization.
// When normalizeTimestamps is true, timestamps in log lines are replaced with a <TIME> token
// before feeding them to the drain algorithm, preventing false anomalies caused by
// differing timestamps between baseline and check recordings.
func NewLogAnalyzerWithNormalization(normalizeTimestamps bool) *LogAnalyzer {
	return &LogAnalyzer{
		orakels:                  make(map[string]*LogOrakel),
		maxAnomaliesPerContainer: 10,
		normalizeTimestamps:      normalizeTimestamps,
	}
}

// LoadBaseline trains the log oracle with baseline recording logs.
// Can be called multiple times with different baseline recordings.
func (la *LogAnalyzer) LoadBaseline(rec *recording.WorkloadRecording) error {
	if rec == nil {
		return fmt.Errorf("nil recording provided")
	}

	for containerName, logs := range rec.Logs {
		o, exists := la.orakels[containerName]
		if !exists {
			o = NewLogOrakelWithNormalization(la.normalizeTimestamps)
			la.orakels[containerName] = o
		}
		o.LoadBaseline(logs)
	}

	return nil
}

// AnalyzeTarget compares a check run's logs against the trained baseline patterns.
// Returns an AnalysisResult with anomalies per container.
func (la *LogAnalyzer) AnalyzeTarget(rec *recording.WorkloadRecording) *AnalysisResult {
	result := &AnalysisResult{
		Success:   true,
		Anomalies: make(map[string][]string),
	}

	if rec == nil {
		result.Success = false
		result.FailureReason = "nil recording"
		return result
	}

	for containerName, logs := range rec.Logs {
		o, exists := la.orakels[containerName]
		if !exists {
			// No baseline for this container — skip (it might be a new sidecar)
			continue
		}

		anomalies, _ := o.AnalyzeTarget(logs)
		if len(anomalies) > 0 {
			result.Success = false

			if result.FailureReason == "" {
				result.FailureReason = fmt.Sprintf("Anomalies found in logs of container %s", containerName)
			} else {
				result.FailureReason += fmt.Sprintf(", Anomalies found in logs of container %s", containerName)
			}

			// Limit anomalies: if too many, summarize via templates
			if len(anomalies) <= la.maxAnomaliesPerContainer {
				result.Anomalies[containerName] = anomalies
			} else {
				// Use drain to summarize the anomalies into templates
				anomalyMiner := NewLogOrakelWithNormalization(la.normalizeTimestamps)
				anomalyMiner.LoadBaseline(logs)
				templates := anomalyMiner.GetTemplates()

				if len(templates) > 5 {
					result.Anomalies[containerName] = templates[:5]
				} else {
					result.Anomalies[containerName] = templates
				}
			}
		}
	}

	return result
}

// Ensure LogAnalyzer implements Orakel at compile time.
var _ Orakel = (*LogAnalyzer)(nil)
