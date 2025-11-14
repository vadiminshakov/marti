package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/marti/internal/services/promptbuilder"
)

const (
	defaultTimeout    = 60 * time.Second
	defaultMaxRetries = 3
	defaultRetryDelay = 2 * time.Second
)

// LLMClient defines the interface for interacting with LLM services
type LLMClient interface {
	// GetDecision sends market data to the LLM and returns a trading decision
	GetDecision(ctx context.Context, marketContext promptbuilder.MarketContext) (string, error)
}

type OpenAICompatibleClient struct {
	apiURL        string
	apiKey        string
	model         string
	httpClient    *http.Client
	promptBuilder *promptbuilder.PromptBuilder
	maxRetries    int
	retryDelay    time.Duration
}

// NewOpenAICompatibleClient creates a new client for OpenAI-compatible APIs
func NewOpenAICompatibleClient(apiURL, apiKey, model string, promptBuilder *promptbuilder.PromptBuilder) *OpenAICompatibleClient {
	return &OpenAICompatibleClient{
		apiURL:        apiURL,
		apiKey:        apiKey,
		model:         model,
		promptBuilder: promptBuilder,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		maxRetries: defaultMaxRetries,
		retryDelay: defaultRetryDelay,
	}
}

// chatRequest represents the request structure for OpenAI-compatible APIs
type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse represents the response structure from OpenAI-compatible APIs
type chatResponse struct {
	ID      string    `json:"id"`
	Object  string    `json:"object"`
	Created int64     `json:"created"`
	Model   string    `json:"model"`
	Choices []choice  `json:"choices"`
	Usage   usage     `json:"usage"`
	Error   *apiError `json:"error,omitempty"`
}

type choice struct {
	Index        int     `json:"index"`
	Message      message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// GetDecision builds prompts, sends a chat request to the LLM API, and returns the response
func (c *OpenAICompatibleClient) GetDecision(ctx context.Context, marketContext promptbuilder.MarketContext) (string, error) {
	if c.apiKey == "" {
		return "", errors.New("LLM API key is empty")
	}

	userPrompt := c.promptBuilder.BuildUserPrompt(marketContext)

	reqBody := chatRequest{
		Model: c.model,
		Messages: []message{
			{
				Role:    "system",
				Content: promptbuilder.SystemPrompt,
			},
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
		Temperature: 0.0, // deterministic responses for trading decisions
		MaxTokens:   18000,
	}

	var lastErr error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(c.retryDelay):
			}
		}

		response, err := c.sendRequest(ctx, reqBody)
		if err != nil {
			lastErr = err
			continue
		}

		return response, nil
	}

	return "", errors.Wrapf(lastErr, "failed after %d retries", c.maxRetries)
}

func (c *OpenAICompatibleClient) sendRequest(ctx context.Context, reqBody chatRequest) (string, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", errors.Wrap(err, "failed to create HTTP request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "HTTP request failed")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read response body")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal response")
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("LLM API error: %s (type: %s, code: %s)",
			chatResp.Error.Message, chatResp.Error.Type, chatResp.Error.Code)
	}

	if len(chatResp.Choices) == 0 {
		return "", errors.New("LLM API returned no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}
