package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
func (t *Telegram) Send(title, message string, silent bool) error {
	text := fmt.Sprintf("*%s*\n%s", escapeMarkdown(title), escapeMarkdown(message))

	payload := map[string]any{
		"chat_id":              t.chatID,
		"text":                 text,
		"parse_mode":           "MarkdownV2",
		"disable_notification": silent,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.botToken)
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sending telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("telegram API error %d: %v", resp.StatusCode, result["description"])
	}

	return nil
}

// markdownV2 escapes every character Telegram's MarkdownV2 parser treats as
// markup. Anything unescaped makes the API reject the whole message, so a
// stray bracket in a journal excerpt would lose the alert entirely.
var markdownV2 = strings.NewReplacer(
	"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]",
	"(", "\\(", ")", "\\)", "~", "\\~", "`", "\\`",
	">", "\\>", "#", "\\#", "+", "\\+", "-", "\\-",
	"=", "\\=", "|", "\\|", "{", "\\{", "}", "\\}",
	".", "\\.", "!", "\\!",
)

func escapeMarkdown(s string) string { return markdownV2.Replace(s) }
