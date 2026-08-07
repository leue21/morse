package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// What Telegram accepts. A message and a file caption have different budgets,
// and an upload has a hard ceiling; morse trims to fit rather than letting the
// API reject an alert for being too long to deliver.
const (
	messageLimit   = 4096
	captionLimit   = 1024
	maxUploadBytes = 50 << 20
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
	payload := map[string]any{
		"chat_id":              t.chatID,
		"text":                 caption(title, message, messageLimit),
		"parse_mode":           "HTML",
		"disable_notification": silent,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	return t.post(ctx, "sendMessage", "application/json", bytes.NewReader(body))
}

// SendDocument uploads a file and reports it with the same title and body a
// plain message would carry. A log excerpt survives a pipe, but an archive or a
// binary does not, and asking the reader to go and fetch it defeats the point of
// telling them at all.
func (t *Telegram) SendDocument(ctx context.Context, path, title, message string, silent bool) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	// Telegram refuses an upload over 50 MB. Checking here turns a rejection
	// that arrives after the whole file has gone up the wire — on the kind of
	// connection that made the size a problem — into an error before the first
	// byte is sent.
	if info.Size() > maxUploadBytes {
		return fmt.Errorf("%s is %d MB; telegram accepts at most %d MB",
			path, info.Size()>>20, maxUploadBytes>>20)
	}

	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	fields := map[string]string{
		"chat_id":              strconv.FormatInt(t.chatID, 10),
		"parse_mode":           "HTML",
		"disable_notification": strconv.FormatBool(silent),
	}
	// An empty caption field is not the same as no caption: Telegram would
	// render the bold title of a message that has none.
	if text := caption(title, message, captionLimit); text != "" {
		fields["caption"] = text
	}
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			return fmt.Errorf("building upload: %w", err)
		}
	}
	part, err := form.CreateFormFile("document", filepath.Base(path))
	if err != nil {
		return fmt.Errorf("building upload: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("reading file: %w", err)
	}
	if err := form.Close(); err != nil {
		return fmt.Errorf("building upload: %w", err)
	}

	return t.post(ctx, "sendDocument", form.FormDataContentType(), &buf)
}

// post sends one request to a Bot API method and reports what came back.
func (t *Telegram) post(ctx context.Context, method, contentType string, body io.Reader) error {
	endpoint := fmt.Sprintf("%s/bot%s/%s", t.baseURL, t.botToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("building request: %w", withoutURL(err))
	}
	req.Header.Set("Content-Type", contentType)

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

// caption renders a title and body as Telegram HTML, within a length limit.
//
// The limit counts the escaped text, and truncation happens before escaping so
// that a cut can never land inside an &amp; — a half-written entity is markup
// Telegram cannot parse, and it would reject the whole message.
func caption(title, message string, limit int) string {
	title, message = strings.TrimSpace(title), strings.TrimSpace(message)
	switch {
	case title == "" && message == "":
		return ""
	case message == "":
		return "<b>" + html.EscapeString(truncate(title, limit)) + "</b>"
	case title == "":
		return html.EscapeString(truncate(message, limit))
	}
	// The title is what gets read on a lock screen, so it keeps its length and
	// the body gives way. The markup and the newline are not part of the text
	// Telegram counts, but the escaping is.
	title = truncate(title, limit)
	head := "<b>" + html.EscapeString(title) + "</b>\n"
	return head + html.EscapeString(truncate(message, limit-escapedLen(title)-1))
}

// truncate cuts text down until it fits the limit once escaped, marking the cut
// so a reader knows the rest exists.
func truncate(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if escapedLen(text) <= limit {
		return text
	}
	for runes := []rune(text); len(runes) > 0; runes = runes[:len(runes)-1] {
		if escapedLen(string(runes))+1 <= limit {
			return string(runes) + "…"
		}
	}
	return ""
}

// escapedLen counts the characters Telegram will count: the text as it arrives,
// escaped, since an escaped "&" spends five of the budget rather than one.
func escapedLen(text string) int {
	return len([]rune(html.EscapeString(text)))
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
