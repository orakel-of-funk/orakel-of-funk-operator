package orakel

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/faceair/drain"
)

var (
	drainTemplateRegex = regexp.MustCompile(`id=\{\d+\}\s+:\s+size=\{(?P<size>\d+)\}\s+:\s+(?P<template>.*)$`)
	sizeIndex          = drainTemplateRegex.SubexpIndex("size")
	templateIndex      = drainTemplateRegex.SubexpIndex("template")
)

type LogOrakel struct {
	*drain.Drain

	BaselineLogsCount int
	TargetLogCount    int

	Anomalies []string

	// normalizeTimestamps enables timestamp normalization before training/matching
	normalizeTimestamps bool
}

// Returns a new LogOrakel instance with a default drain configuration
// The drain is used to train the model with baseline logs and analyze target logs
func NewLogOrakel() *LogOrakel {
	drainConfig := drain.DefaultConfig()

	return &LogOrakel{
		Drain: drain.New(drainConfig),
	}
}

// NewLogOrakelWithNormalization creates a LogOrakel with optional timestamp normalization.
func NewLogOrakelWithNormalization(normalizeTimestamps bool) *LogOrakel {
	drainConfig := drain.DefaultConfig()

	return &LogOrakel{
		Drain:               drain.New(drainConfig),
		normalizeTimestamps: normalizeTimestamps,
	}
}

// GetTemplates returns a map of templates extracted from the trained model
// The keys are the template strings and the values are the sizes of the clusters
// The map is sorted by size in descending order
func (o *LogOrakel) GetTemplates() []string {
	// Returns the templates extracted from the trained model
	// These are the clusters that represent the learned patterns from the baseline logs
	clusters := o.Clusters()
	templates := make(map[string]int, len(clusters))

	for _, cluster := range o.Clusters() {
		template := strings.TrimSpace(cluster.String())

		matches := drainTemplateRegex.FindStringSubmatch(template)
		templates[matches[templateIndex]], _ = strconv.Atoi(matches[sizeIndex])
	}

	// Sort the anomalies by size (descending)
	keys := make([]string, 0, len(templates))
	for key := range templates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return templates[keys[i]] > templates[keys[j]] })

	return keys
}

// LoadBaseline takes a slice of log lines, trains the drain model with them,
// and returns the numebr of logs processed and the number of templates extracted.
func (o *LogOrakel) LoadBaseline(input []string) (int, int) {

	for _, line := range input {
		if strings.TrimSpace(line) == "" {
			continue
		}
		o.BaselineLogsCount++
		line = strings.TrimSpace(line)
		if o.normalizeTimestamps {
			line = NormalizeTimestamps(line)
		}
		o.Train(line)
	}

	return o.BaselineLogsCount, len(o.Clusters())
}

// AnalyzeTarget takes a slice of log lines, analyzes them against the trained model,
// and returns a slice of anomalies and the number of logs processed.
// Anomalies are lines that do not match any of the trained templates.
func (o *LogOrakel) AnalyzeTarget(input []string) ([]string, int) {
	o.TargetLogCount = 0
	o.Anomalies = []string{}

	for _, line := range input {
		if strings.TrimSpace(line) == "" {
			continue
		}
		o.TargetLogCount++
		line = strings.TrimSpace(line)
		if o.normalizeTimestamps {
			line = NormalizeTimestamps(line)
		}
		matchedCluster := o.Match(line)
		if matchedCluster == nil {
			o.Anomalies = append(o.Anomalies, line)
			continue
		}
	}

	return o.Anomalies, o.TargetLogCount
}
