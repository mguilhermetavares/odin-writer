package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mguilhermetavares/odin-writer/internal/httpclient"
)

// Notifier sends messages via the Telegram Bot API.
type Notifier struct {
	token   string
	chatID  string
	client  *http.Client
	baseURL string // overridable in tests; defaults to https://api.telegram.org
}

// New returns a Notifier that sends messages to chatID using the given bot token.
func New(token, chatID string) *Notifier {
	return &Notifier{
		token:   token,
		chatID:  chatID,
		client:  httpclient.New(),
		baseURL: "https://api.telegram.org",
	}
}

// Notify sends msg to the configured Telegram chat.
func (n *Notifier) Notify(ctx context.Context, msg string) error {
	payload, err := json.Marshal(map[string]string{
		"chat_id": n.chatID,
		"text":    msg,
	})
	if err != nil {
		return fmt.Errorf("marshaling telegram payload: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", n.baseURL, n.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}
	return nil
}
