package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"time"
)

// Telegram sends messages via the Telegram Bot API.
type Telegram struct {
	botToken string
	chatID   int64
	client   *http.Client
	baseURL  string // overridable for testing; defaults to "https://api.telegram.org"
}

func NewTelegram(botToken string, chatID int64) *Telegram {
	return &Telegram{
		botToken: botToken,
		chatID:   chatID,
		client:   &http.Client{Timeout: 10 * time.Second},
		baseURL:  "https://api.telegram.org",
	}
}

// Send delivers one message. A silent message still arrives and stays in the
// chat; it just does not make the reader's phone buzz, so routine facts can be
// reported without training them to mute the chat — which would cost the
// messages that matter.
func (t *Telegram) Send(ctx context.Context, title, message string, silent bool) error {
	// HTML rather than MarkdownV2: Telegram's HTML mode reserves only <, > and
	// &, which stdlib escapes correctly, where MarkdownV2 reserves twenty-odd
	// characters that a hand-written escaper has to track. An excerpt full of
	// brackets, dots and backslashes is exactly what morse carries, and one
	// missed character makes the API reject the whole message — losing the
	// alert entirely.
	text := fmt.Sprintf("<b>%s</b>\n%s", html.EscapeString(title), html.EscapeString(message))

	payload := map[string]any{
		"chat_id":              t.chatID,
		"text":                 text,
		"parse_mode":           "HTML",
		"disable_notification": silent,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", withoutURL(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending telegram message: %w", withoutURL(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("telegram API error %d: %v", resp.StatusCode, result["description"])
	}

	return nil
}

// withoutURL strips the request URL out of a transport error. The bot token is
// part of that URL, and morse's errors go to stderr — where a caller is free to
// capture them into a log — so reporting one verbatim would publish the
// credential every time the network hiccuped.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}
