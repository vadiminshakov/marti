package collector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertIntervalToBybit(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  string
		shouldErr bool
	}{
		{
			name:      "1 minute",
			input:     "1m",
			expected:  "1",
			shouldErr: false,
		},
		{
			name:      "5 minutes",
			input:     "5m",
			expected:  "5",
			shouldErr: false,
		},
		{
			name:      "15 minutes",
			input:     "15m",
			expected:  "15",
			shouldErr: false,
		},
		{
			name:      "1 hour",
			input:     "1h",
			expected:  "60",
			shouldErr: false,
		},
		{
			name:      "4 hours",
			input:     "4h",
			expected:  "240",
			shouldErr: false,
		},
		{
			name:      "1 day",
			input:     "1d",
			expected:  "D",
			shouldErr: false,
		},
		{
			name:      "1 week",
			input:     "1w",
			expected:  "W",
			shouldErr: false,
		},
		{
			name:      "invalid interval - empty",
			input:     "",
			expected:  "",
			shouldErr: true,
		},
		{
			name:      "invalid interval - no unit",
			input:     "1",
			expected:  "",
			shouldErr: true,
		},
		{
			name:      "invalid interval - unsupported unit",
			input:     "1x",
			expected:  "",
			shouldErr: true,
		},
		{
			name:      "invalid interval - no number",
			input:     "m",
			expected:  "",
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertIntervalToBybit(tt.input)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		shouldErr bool
	}{
		{
			name:      "valid timestamp",
			input:     "1672531200000",
			shouldErr: false,
		},
		{
			name:      "empty timestamp",
			input:     "",
			shouldErr: true,
		},
		{
			name:      "invalid timestamp - not a number",
			input:     "abc",
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTimestamp(tt.input)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}
