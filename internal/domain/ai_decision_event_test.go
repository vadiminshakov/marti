package entity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewAIDecisionEvent_ModelNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Standard OpenAI model",
			input:    "gpt-4",
			expected: "gpt-4",
		},
		{
			name:     "YandexGPT with folder ID",
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
			expected: "deepseek/deepseek-v3", // Should remain as is or be handled if we want to strip provider? Current logic might strip it if it starts with gpt://. Let's see existing logic.
			// Existing logic only strips if it starts with gpt://
		},
		{
			name:     "Arbitrary model",
			input:    "claude-3-opus",
			expected: "claude-3-opus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewAIDecisionEvent(
				time.Now(),
				"BTC_USDT",
				tt.input,
				"hold",
				"reasoning",
				0, 0, 0, "", "0", "0", "0", "long", "0",
			)
			assert.Equal(t, tt.expected, event.Model)
		})
	}
}
