package orakel

import "regexp"

// timestampPatterns contains compiled regexes for common log timestamp formats.
// Order matters: more specific patterns are checked first to avoid partial matches.
var timestampPatterns = []*regexp.Regexp{
	// ISO 8601 with nanoseconds and timezone: 2025-07-07T13:59:39.378242630Z or 2025-07-07T13:59:39.378242630+02:00
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`),

	// Common log format date with timezone offset: 2026-08-02 11:39:31.123+02:00
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}(\.\d+)?([+-]\d{2}:\d{2})?`),

	// Syslog-style: Aug  2 11:39:31 or Jul 07 13:59:39 (must be before time-only patterns)
	regexp.MustCompile(`(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}`),

	// Unix timestamp with milliseconds: 1719319179.378
	regexp.MustCompile(`\b\d{10}\.\d+\b`),

	// Unix timestamp (10 digits): 1719319179
	regexp.MustCompile(`\b\d{10}\b`),

	// Time only with milliseconds: 13:59:39.378 or 13:59:39,378
	regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}[.,]\d+\b`),

	// Time only: 13:59:39
	regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}\b`),
}

const timeToken = "<TIME>"

// NormalizeTimestamps replaces all recognized timestamp patterns in a log line
// with the <TIME> token. This ensures the drain algorithm doesn't treat timestamps
// as distinguishing features between log lines.
func NormalizeTimestamps(line string) string {
	for _, pattern := range timestampPatterns {
		line = pattern.ReplaceAllString(line, timeToken)
	}
	return line
}
