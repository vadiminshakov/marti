package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/pkg/retrier"
)

const (
	defaultTimeout    = 60 * time.Second
	defaultMaxRetries = 7
	defaultRetryDelay = 2 * time.Second
	defaultMaxDelay   = 1 * time.Minute
)

// LLMClient interface for interacting with LLM services.
type LLMClient interface {
	// GetDecision sends market data to the LLM and returns a trading decision.
	GetDecision(ctx context.Context, prompt *domain.Prompt) (string, error)
}

type OpenAICompatibleClient struct {
	apiURL        string
	apiKey        string
	model         string
	httpClient    *http.Client
	retrier       *retrier.Retrier
	customHeaders map[string]string
}

// NewOpenAICompatibleClient creates a new client for OpenAI-compatible APIs.
func NewOpenAICompatibleClient(apiURL, apiKey, model, proxyURL string) (*OpenAICompatibleClient, error) {
	transport := &http.Transport{}
	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse proxy URL")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}

	client := &OpenAICompatibleClient{
		apiURL: apiURL,
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout:   defaultTimeout,
			Transport: transport,
		},
		retrier: retrier.New(
			retrier.WithMaxRetries(defaultMaxRetries),
			retrier.WithInitialInterval(defaultRetryDelay),
			retrier.WithMaxInterval(defaultMaxDelay),
			retrier.WithJitter(0.1),
		),
		customHeaders: make(map[string]string),
	}

	client.setProviderSpecificHeaders()

	return client, nil
}

// setProviderSpecificHeaders configures provider-specific headers.
func (c *OpenAICompatibleClient) setProviderSpecificHeaders() {
	if strings.Contains(c.apiURL, "yandex") || strings.Contains(c.apiURL, "yandex.net") {
		// For Yandex GPT, extract folder ID from model name like "gpt://folder/model"
		if strings.HasPrefix(c.model, "gpt://") {
			parts := strings.SplitN(strings.TrimPrefix(c.model, "gpt://"), "/", 2)
			if len(parts) >= 1 {
				c.customHeaders["OpenAI-Project"] = parts[0]
			}
		}
	}
}

// chatRequest request structure for OpenAI-compatible APIs.
type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// yandexRequest request structure for Yandex GPT API.
type yandexRequest struct {
	Model           string  `json:"model"`
	Instructions    string  `json:"instructions"`
	Input           string  `json:"input"`
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse response structure from OpenAI-compatible APIs.
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

// isYandexAPI checks if the API endpoint is Yandex GPT.
func (c *OpenAICompatibleClient) isYandexAPI() bool {
	return strings.Contains(c.apiURL, "yandex") || strings.Contains(c.apiURL, "yandex.net")
}

// GetDecision builds prompts, sends a chat request to the LLM API, and returns the response.
func (c *OpenAICompatibleClient) GetDecision(ctx context.Context, prompt *domain.Prompt) (string, error) {
	if c.apiKey == "" {
		return "", errors.New("LLM API key is empty")
	}

	userPrompt := prompt.String()

	return retrier.DoWithData(c.retrier, ctx, func(ctx context.Context) (string, error) {
		if c.isYandexAPI() {
			return c.getYandexDecision(ctx, userPrompt)
		}
		return c.getOpenAIDecision(ctx, userPrompt)
	})
}

// getOpenAIDecision handles standard OpenAI-compatible API requests.
func (c *OpenAICompatibleClient) getOpenAIDecision(ctx context.Context, userPrompt string) (string, error) {
	reqBody := chatRequest{
		Model: c.model,
		Messages: []message{
			{
				Role:    "system",
				Content: domain.SystemPrompt,
			},
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
		Temperature: 0.0, // deterministic responses for trading decisions
		MaxTokens:   8000,
	}
	return c.sendRequest(ctx, reqBody)
}

// getYandexDecision handles Yandex GPT API requests.
func (c *OpenAICompatibleClient) getYandexDecision(ctx context.Context, userPrompt string) (string, error) {
	reqBody := yandexRequest{
		Model:           c.model,
		Instructions:    domain.SystemPrompt,
		Input:           userPrompt,
		Temperature:     0.0, // deterministic responses for trading decisions
		MaxOutputTokens: 8000,
	}
	return c.sendYandexRequest(ctx, reqBody)
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

	// Add custom headers for specific providers
	for key, value := range c.customHeaders {
		req.Header.Set(key, value)
	}

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

func (c *OpenAICompatibleClient) sendYandexRequest(ctx context.Context, reqBody yandexRequest) (string, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal Yandex request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL+"/responses", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", errors.Wrap(err, "failed to create Yandex HTTP request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	// Add custom headers for Yandex
	for key, value := range c.customHeaders {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "Yandex API request failed")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read Yandex response body")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Yandex API returned status %d: %s", resp.StatusCode, string(body))
	}

	var yandexResp yandexResponse
	if err := json.Unmarshal(body, &yandexResp); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal Yandex response")
	}

	if yandexResp.Error != nil {
		return "", fmt.Errorf("Yandex API error: %s (type: %s, code: %s)",
			yandexResp.Error.Message, yandexResp.Error.Type, yandexResp.Error.Code)
	}

	// Extract the response text from the output array
	if len(yandexResp.Output) == 0 || len(yandexResp.Output[0].Content) == 0 {
		return "", errors.New("Yandex API returned empty output")
	}

	// Clean up the response - remove markdown code blocks if present
	responseText := yandexResp.Output[0].Content[0].Text
	responseText = strings.TrimSpace(responseText)

	// Remove markdown code blocks
	if strings.HasPrefix(responseText, "```json") {
		responseText = strings.TrimPrefix(responseText, "```json")
	}
	if strings.HasPrefix(responseText, "```") && !strings.HasPrefix(responseText, "```json") {
		responseText = strings.TrimPrefix(responseText, "```")
	}
	if strings.HasSuffix(responseText, "```") {
		responseText = strings.TrimSuffix(responseText, "```")
	}

	return strings.TrimSpace(responseText), nil
}

// yandexResponse response structure from Yandex GPT API.
type yandexResponse struct {
	Output []struct {
		Content []struct {
			Text string `json:"text"`
			Type string `json:"type"`
		} `json:"content"`
		Role   string `json:"role"`
		Status string `json:"status"`
		Type   string `json:"type"`
	} `json:"output,omitempty"`
	Error *apiError `json:"error,omitempty"`
}
