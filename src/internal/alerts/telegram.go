// Package alerts implements threshold-based alerting with Telegram delivery.
// The AlertEngine evaluates each MetricsSnapshot against configured thresholds
// and fires a notification on state transitions (OK→WARN, OK→CRIT, CRIT→OK,
// etc.) with a per-metric cooldown to avoid spam.
package alerts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Notifier delivers an alert message to an external channel.
type Notifier interface {
	Send(msg string) error
	SendHTML(msg string) error
}

// TelegramNotifier posts messages to a Telegram chat via the Bot API.
type TelegramNotifier struct {
	token   string
	chatID  string
	client  *http.Client
	baseURL string // overridden in tests; production leaves this empty
}

// NewTelegramNotifier constructs a notifier for the given bot token and chat ID.
func NewTelegramNotifier(token, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		token:   token,
		chatID:  chatID,
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://api.telegram.org",
	}
}

// Send posts msg to the configured Telegram chat using Markdown parse mode.
func (t *TelegramNotifier) Send(msg string) error {
	return t.post(msg, "Markdown")
}

// SendHTML posts msg to the configured Telegram chat using HTML parse mode.
// Use this when the message contains <a href> links with non-standard hostnames.
func (t *TelegramNotifier) SendHTML(msg string) error {
	return t.post(msg, "HTML")
}

func (t *TelegramNotifier) post(msg, parseMode string) error {
	payload := map[string]string{
		"chat_id":    t.chatID,
		"text":       msg,
		"parse_mode": parseMode,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	url := fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.token)
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned %d", resp.StatusCode)
	}
	return nil
}

