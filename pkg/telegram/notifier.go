// Package telegram provides a minimal Telegram Bot API client for sending notifications.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Notifier sends messages to a Telegram chat via the Bot API.
// If token or chatID is empty, Send is a no-op.
type Notifier struct {
	token  string
	chatID string
	client *http.Client
}

// New creates a Notifier. When token or chatID is empty, Send is a no-op.
func New(token, chatID string) *Notifier {
	return &Notifier{
		token:  token,
		chatID: chatID,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// IsConfigured reports whether the notifier has valid credentials.
func (n *Notifier) IsConfigured() bool {
	return n != nil && n.token != "" && n.chatID != ""
}

// Send posts a plain-text message to the configured Telegram chat.
// Errors are returned for observability but should not block trading operations.
func (n *Notifier) Send(ctx context.Context, text string) error {
	if !n.IsConfigured() {
		return nil
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.token)

	form := url.Values{}
	form.Set("chat_id", n.chatID)
	form.Set("text", text)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("telegram: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body struct {
			Description string `json:"description"`
		}

		_ = json.NewDecoder(resp.Body).Decode(&body)
		return fmt.Errorf("telegram: api error %d: %s", resp.StatusCode, body.Description)
	}

	return nil
}
