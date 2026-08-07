package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"morse/plugin"
)

// Notifier sends alert messages.
type Notifier interface {
	Send(title, message string, severity plugin.Severity) error
}

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

// Send delivers one message. Severity decides whether it buzzes: info arrives
// silently, so routine facts can be reported without training the reader to
// mute the chat, which would cost the alerts that matter.
func (t *Telegram) Send(title, message string, severity plugin.Severity) error {
	text := fmt.Sprintf("*%s*\n%s", escapeMarkdown(title), escapeMarkdown(message))

	payload := map[string]any{
		"chat_id":              t.chatID,
		"text":                 text,
		"parse_mode":           "MarkdownV2",
		"disable_notification": severity == plugin.SeverityInfo,
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

// Update represents a Telegram Bot API update.
type Update struct {
	UpdateID int64      `json:"update_id"`
	Message  *TGMessage `json:"message"`
}

// TGMessage represents a Telegram message.
type TGMessage struct {
	Text string `json:"text"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

// CommandHandler is called for each incoming command. Returns the response to send.
type CommandHandler func(command string) string

// PollCommands long-polls for incoming Telegram commands and dispatches them to the handler.
func (t *Telegram) PollCommands(ctx context.Context, handler CommandHandler) {
	offset := int64(0)
	pollClient := &http.Client{Timeout: 35 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		url := fmt.Sprintf("%s/bot%s/getUpdates?offset=%d&timeout=30", t.baseURL, t.botToken, offset)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			slog.Error("creating getUpdates request", "error", err)
			return
		}

		resp, err := pollClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("getUpdates failed", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		var result struct {
			OK          bool     `json:"ok"`
			ErrorCode   int      `json:"error_code"`
			Description string   `json:"description"`
			Result      []Update `json:"result"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if !result.OK {
			// Telegram says why in the body. Logging only "ok=false" hides the
			// difference between a bad token, a webhook holding the updates,
			// and a second process polling the same bot -- which read
			// identically and cost an hour to tell apart.
			slog.Error("getUpdates rejected",
				"code", result.ErrorCode, "description", result.Description)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, u := range result.Result {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message == nil || !strings.HasPrefix(u.Message.Text, "/") {
				continue
			}

			reply := handler(u.Message.Text)
			if reply == "" {
				continue
			}

			t.sendPlainText(u.Message.Chat.ID, reply)
		}
	}
}

func (t *Telegram) sendPlainText(chatID int64, text string) {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.botToken)
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Error("sending reply", "error", err)
		return
	}
	resp.Body.Close()
}

func escapeMarkdown(s string) string {
	replacer := []string{
		"_", "\\_",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	}
	result := s
	for i := 0; i < len(replacer); i += 2 {
		result = replaceAll(result, replacer[i], replacer[i+1])
	}
	return result
}

func replaceAll(s, old, new string) string {
	var buf bytes.Buffer
	for i := 0; i < len(s); i++ {
		if string(s[i]) == old {
			buf.WriteString(new)
		} else {
			buf.WriteByte(s[i])
		}
	}
	return buf.String()
}
