// Package orakel provides oracle implementations for analyzing workload behavior.
// An Oracle compares baseline recordings against check run recordings to detect anomalies.
// Currently, the LogOrakel (log-based analysis using Drain) is the primary implementation.
// The interface is designed to be extensible for future oracle implementations (e.g., eBPF-based).
package orakel

import "github.com/orakel-of-funk/orakel-of-funk-operator/internal/recording"

// Orakel defines the interface for workload behavior analysis oracles.
// An oracle is trained with baseline recordings, then used to analyze target recordings
// for anomalies.
type Orakel interface {
	// LoadBaseline ingests a baseline recording for training.
	// May be called multiple times (e.g., two baseline recordings for better coverage).
	LoadBaseline(recording *recording.WorkloadRecording) error

	// AnalyzeTarget compares a check run recording against the trained baseline.
	// Returns an AnalysisResult indicating whether the check was successful and any anomalies found.
	AnalyzeTarget(recording *recording.WorkloadRecording) *AnalysisResult
}

// AnalysisResult contains the outcome of analyzing a check run recording against baselines.
type AnalysisResult struct {
	// Success indicates whether the check run behavior was within expected bounds.
	Success bool

	// FailureReason provides a human-readable explanation if Success is false.
	FailureReason string

	// Anomalies maps container names to lists of anomalous log lines or signals.
	Anomalies map[string][]string
}
