package orakel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeTimestamps(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "ISO 8601 with nanoseconds and Z",
			input:    `2025-07-07T13:59:39.37824263Z some log message`,
			expected: `<TIME> some log message`,
		},
		{
			name:     "ISO 8601 with timezone offset",
			input:    `2025-07-07T13:59:39.378+02:00 starting server`,
			expected: `<TIME> starting server`,
		},
		{
			name:     "ISO 8601 without fractional seconds",
			input:    `2025-07-07T13:59:39Z INFO ready`,
			expected: `<TIME> INFO ready`,
		},
		{
			name:     "Common datetime with space separator",
			input:    `2026-08-02 11:39:31 [INFO] connection established`,
			expected: `<TIME> [INFO] connection established`,
		},
		{
			name:     "Common datetime with milliseconds",
			input:    `2026-08-02 11:39:31.456 [WARN] timeout`,
			expected: `<TIME> [WARN] timeout`,
		},
		{
			name:     "Syslog style timestamp",
			input:    `Aug  2 11:39:31 myhost kernel: something happened`,
			expected: `<TIME> myhost kernel: something happened`,
		},
		{
			name:     "Syslog style with single digit day",
			input:    `Jul 7 13:59:39 app[1234]: started`,
			expected: `<TIME> app[1234]: started`,
		},
		{
			name:     "Time only with milliseconds (dot separator)",
			input:    `[13:59:39.378] DEBUG processing request`,
			expected: `[<TIME>] DEBUG processing request`,
		},
		{
			name:     "Time only with milliseconds (comma separator)",
			input:    `13:59:39,378 ERROR failed to connect`,
			expected: `<TIME> ERROR failed to connect`,
		},
		{
			name:     "No timestamp - should remain unchanged",
			input:    `INFO: no timestamp in this line`,
			expected: `INFO: no timestamp in this line`,
		},
		{
			name:     "Multiple timestamps in one line",
			input:    `2025-07-07T13:59:39.378Z request started, finished at 2025-07-07T14:00:01.123Z`,
			expected: `<TIME> request started, finished at <TIME>`,
		},
		{
			name:     "MariaDB style log",
			input:    `2026-08-02 11:39:31 0 [Note] InnoDB: Buffer pool(s) load completed at 260802 11:39:31`,
			expected: `<TIME> 0 [Note] InnoDB: Buffer pool(s) load completed at 260802 <TIME>`,
		},
		{
			name:     "Kubernetes container log with timestamp prefix",
			input:    `2025-07-07T13:59:39.37824263Z {"level":"info","msg":"server started","port":8080}`,
			expected: `<TIME> {"level":"info","msg":"server started","port":8080}`,
		},
		{
			name:     "Empty string",
			input:    ``,
			expected: ``,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeTimestamps(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
