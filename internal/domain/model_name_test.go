package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeModelName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Standard model name",
			input:    "gpt-4",
			expected: "gpt-4",
		},
		{
			name:     "YandexGPT with folder ID and version",
			input:    "gpt://b1g8t5pmnjifaov0paff/yandexgpt/rc",
			expected: "yandexgpt",
		},
		{
			name:     "YandexGPT simple",
			input:    "gpt://folder/yandexgpt",
			expected: "yandexgpt",
		},
		{
			name:     "DeepSeek with provider",
			input:    "deepseek/deepseek-v3",
			expected: "deepseek/deepseek-v3",
		},
		{
			name:     "OpenRouter model",
			input:    "tngtech/deepseek-r1t2-chimera:free",
			expected: "tngtech/deepseek-r1t2-chimera:free",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeModelName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
