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

// What Telegram refuses, as something a caller can branch on.
//
// The Bot API says why it refused only in prose, in the description of a 400,
// so these are the one place morse reads that prose — see refusal. A caller
// matching on the text of an error instead would be reading a sentence morse
// wrote, and would break the moment that sentence was reworded.
var (
	// ErrMessageGone reports that the message an edit named is no longer there
	// to edit: deleted from the chat, or belonging to a different chat or bot.
	// Telling it apart from "that request was wrong" is the difference between
	// recovering by sending a fresh message and hiding a real mistake behind
	// one.
	ErrMessageGone = errors.New("message not found")

	// ErrNotModified reports that an edit would have changed nothing, which
	// Telegram treats as a failure. Whether that matters is the caller's to
	// decide: something reporting an unchanged state has done its job.
	ErrNotModified = errors.New("message is unchanged")

	// ErrWebhookSet reports that the bot delivers its updates to a webhook, so
	// there is nothing for morse to poll: the two ways of receiving are
	// exclusive, and Telegram answers getUpdates with a 409 while one is set.
	// The fix is specific enough to be worth naming — delete the webhook — and
	// a bare conflict does not say which conflict it was.
	ErrWebhookSet = errors.New("the bot has a webhook set")

	// ErrNoSuchTopic reports a topic id the chat does not have: a typo, a
	// deleted topic, or a chat that is not a forum at all. It is named because
	// Telegram's own wording does not say which number it could not find.
	ErrNoSuchTopic = errors.New("topic not found")

	// ErrNoRights reports that the bot is in the chat but lacks the admin
	// right a call needs. Telegram names the action but not the right, so the
	// caller, which knows what it was trying to do, says which switch to flip.
	ErrNoRights = errors.New("the bot lacks the rights for this")
)

// SendOptions is how a new message is delivered, as opposed to what it says.
type SendOptions struct {
	// Silent maps to disable_notification: the message arrives without a
	// sound, and stays in the chat all the same.
	Silent bool
	// Topic is the forum topic to post into — Telegram's message_thread_id —
	// or 0 for the chat itself. A chat with topics turned on is how one chat
	// keeps several senders apart: each gets its own thread, with its own
	// unread count and its own mute, in every Telegram client.
	Topic int64
	// Format is how the body is shown.
	Format Format
}

// Format is how a message's body is shown, as opposed to what it says. It is
// separate from SendOptions because an edit has to be told it too: a rewrite
// replaces the markup along with the text, so a body sent as code and edited
// without it would lose its formatting.
type Format struct {
	// Code shows the body in monospace, as a preformatted block. Most of what
	// morse carries is a log excerpt or a stack trace, whose columns and
	// indentation mean something and are lost in a proportional font.
	Code bool
	// Collapse folds the body into an expandable quote: a few lines and a
	// "show more". A journal excerpt is worth having in the chat and not worth
	// scrolling past every time.
	Collapse bool
}

// wrap puts the body's markup around text that is already escaped. The tags
// are not counted against Telegram's limit — it counts the text once entities
// are parsed out — so wrapping after truncation costs nothing. Telegram accepts
// a preformatted block inside an expandable quote, and that is the nesting
// used when both are asked for: the quote is what folds, the block is what it
// holds.
func (f Format) wrap(escaped string) string {
	if f.Code {
		escaped = "<pre>" + escaped + "</pre>"
	}
	if f.Collapse {
		escaped = "<blockquote expandable>" + escaped + "</blockquote>"
	}
	return escaped
}

// apply adds the options to a request, in the form the Bot API takes them.
// The topic is left out entirely when there is none, rather than sent as 0,
// because 0 is not a topic and Telegram is under no obligation to read it as
// "none".
func (o SendOptions) apply(set func(name string, value any)) {
	set("disable_notification", o.Silent)
	if o.Topic != 0 {
		set("message_thread_id", o.Topic)
	}
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

// UseServer points the client at a Bot API server other than Telegram's: a
// self-hosted one, or a fake one in a test. url is the server's root, with no
// trailing slash and no /bot<token> — the client adds those.
func (t *Telegram) UseServer(url string) *Telegram {
	if url != "" {
		t.baseURL = url
	}
	return t
}

// Send delivers one message. A silent message still arrives and stays in the
// chat; it just does not make the reader's phone buzz, so routine facts can be
// reported without training them to mute the chat — which would cost the
// messages that matter.
// It reports the id of the message it sent, so a caller that wants to come back
// and edit it later has something to refer to; nothing else can name a message
// after the fact, since the Bot API has no way to look one up.
func (t *Telegram) Send(ctx context.Context, title, message string, opts SendOptions) (int64, error) {
	// HTML rather than MarkdownV2: Telegram's HTML mode reserves only <, > and
	// &, which stdlib escapes correctly, where MarkdownV2 reserves twenty-odd
	// characters that a hand-written escaper has to track. An excerpt full of
	// brackets, dots and backslashes is exactly what morse carries, and one
	// missed character makes the API reject the whole message — losing the
	// alert entirely.
	payload := map[string]any{
		"chat_id":    t.chatID,
		"text":       caption(title, message, messageLimit, opts.Format),
		"parse_mode": "HTML",
	}
	opts.apply(func(name string, value any) { payload[name] = value })

	return t.postJSON(ctx, "sendMessage", payload)
}

// Edit rewrites a message already in the chat, in place.
//
// An edit never makes a phone buzz — that is the API's behaviour, not a flag —
// so a caller reporting the state of something long-running can keep one line
// in the chat up to date instead of adding to it. There is no silent parameter
// because there is no louder alternative to choose.
//
// A refusal is reported as it came: ErrNotModified and ErrMessageGone say what
// Telegram objected to, and what should happen about it is the caller's to
// decide.
func (t *Telegram) Edit(ctx context.Context, messageID int64, title, message string, format Format) error {
	payload := map[string]any{
		"chat_id":    t.chatID,
		"message_id": messageID,
		"text":       caption(title, message, messageLimit, format),
		"parse_mode": "HTML",
	}

	_, err := t.postJSON(ctx, "editMessageText", payload)
	return err
}

// CreateTopic opens a new forum topic in the chat and reports its id — the
// number every later message to it has to carry.
//
// A bot cannot list a chat's topics, and only learns one exists if it is in
// the chat, with rights to see it, when the topic is made. Making the topic
// itself is the one way a bot is guaranteed to know the id.
func (t *Telegram) CreateTopic(ctx context.Context, name string) (int64, error) {
	result, err := t.callJSON(ctx, "createForumTopic", map[string]any{
		"chat_id": t.chatID,
		"name":    name,
	})
	if err != nil {
		return 0, fmt.Errorf("creating topic: %w", err)
	}
	var topic struct {
		ThreadID int64 `json:"message_thread_id"`
	}
	if err := json.Unmarshal(result, &topic); err != nil || topic.ThreadID == 0 {
		// Unlike a message id, this one is the whole point of the call: a
		// topic created without its id being reported is a topic nobody can
		// post to without going and finding it by hand.
		return 0, errors.New("creating topic: telegram did not report the topic's id")
	}
	return topic.ThreadID, nil
}

// maxDelete is the most messages deleteMessages takes in one call.
const maxDelete = 100

// Delete removes messages from the chat.
//
// Telegram reports success for a batch even when some of it was already gone,
// so a nil error means the messages are not there any more — not that each one
// was there to delete. That is the question a caller cleaning up is asking.
// A bot may delete its own messages for 48 hours, and anyone's in a group
// where it is an admin with the right to delete; beyond that the call fails.
func (t *Telegram) Delete(ctx context.Context, messageIDs ...int64) error {
	for len(messageIDs) > 0 {
		batch := messageIDs[:min(len(messageIDs), maxDelete)]
		messageIDs = messageIDs[len(batch):]
		if _, err := t.callJSON(ctx, "deleteMessages", map[string]any{
			"chat_id":     t.chatID,
			"message_ids": batch,
		}); err != nil {
			return fmt.Errorf("deleting messages: %w", err)
		}
	}
	return nil
}

// DeleteTopic removes a forum topic and every message in it.
func (t *Telegram) DeleteTopic(ctx context.Context, topic int64) error {
	if _, err := t.callJSON(ctx, "deleteForumTopic", map[string]any{
		"chat_id":           t.chatID,
		"message_thread_id": topic,
	}); err != nil {
		return fmt.Errorf("deleting topic: %w", err)
	}
	return nil
}

// postJSON sends one JSON request to a Bot API method, and reads the message id
// out of what came back.
func (t *Telegram) postJSON(ctx context.Context, method string, payload map[string]any) (int64, error) {
	result, err := t.callJSON(ctx, method, payload)
	if err != nil {
		return 0, err
	}
	return messageID(result), nil
}

// callJSON sends one JSON request to a Bot API method and hands back its
// result, still encoded.
func (t *Telegram) callJSON(ctx context.Context, method string, payload map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling payload: %w", err)
	}
	return t.call(ctx, method, "application/json", bytes.NewReader(body))
}

// SendDocument uploads a file and reports it with the same title and body a
// plain message would carry. A log excerpt survives a pipe, but an archive or a
// binary does not, and asking the reader to go and fetch it defeats the point of
// telling them at all.
func (t *Telegram) SendDocument(ctx context.Context, path, title, message string, opts SendOptions) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("reading file: %w", err)
	}
	if info.IsDir() {
		return 0, fmt.Errorf("%s is a directory", path)
	}
	// Telegram refuses an upload over 50 MB. Checking here turns a rejection
	// that arrives after the whole file has gone up the wire — on the kind of
	// connection that made the size a problem — into an error before the first
	// byte is sent.
	if info.Size() > maxUploadBytes {
		return 0, fmt.Errorf("%s is %d MB; telegram accepts at most %d MB",
			path, info.Size()>>20, maxUploadBytes>>20)
	}

	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	fields := map[string]string{
		"chat_id":    strconv.FormatInt(t.chatID, 10),
		"parse_mode": "HTML",
	}
	opts.apply(func(name string, value any) { fields[name] = fmt.Sprint(value) })
	// An empty caption field is not the same as no caption: Telegram would
	// render the bold title of a message that has none.
	if text := caption(title, message, captionLimit, opts.Format); text != "" {
		fields["caption"] = text
	}
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			return 0, fmt.Errorf("building upload: %w", err)
		}
	}
	part, err := form.CreateFormFile("document", filepath.Base(path))
	if err != nil {
		return 0, fmt.Errorf("building upload: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return 0, fmt.Errorf("reading file: %w", err)
	}
	if err := form.Close(); err != nil {
		return 0, fmt.Errorf("building upload: %w", err)
	}

	return t.post(ctx, "sendDocument", form.FormDataContentType(), &buf)
}

// post sends one request to a Bot API method and reports what came back: the id
// of the message the call produced, when it produced one.
//
// A method that acts on a message answers with that Message, so morse reads the
// id out rather than discarding the body. It is the only chance to learn it —
// the Bot API has no method that looks a message up afterwards — and without it
// nothing could ever refer back to what was just sent.
func (t *Telegram) post(ctx context.Context, method, contentType string, body io.Reader) (int64, error) {
	result, err := t.call(ctx, method, contentType, body)
	if err != nil {
		return 0, err
	}
	return messageID(result), nil
}

// messageID reads the id out of the Message a method answered with.
//
// It is not something to insist on: the id is useful, not required, and a
// caller that only wanted the message delivered should not see a failure
// because the response was shaped unexpectedly.
func messageID(result json.RawMessage) int64 {
	var message struct {
		MessageID int64 `json:"message_id"`
	}
	json.Unmarshal(result, &message)
	return message.MessageID
}

// call sends one request to a Bot API method and hands back the `result` it
// answered with, still encoded. Every method wraps its answer in the same
// envelope but puts something different inside it — a Message, a File, an array
// of Updates — so the envelope, the refusals and the token-stripping live here
// and each caller decodes only the part it asked for.
func (t *Telegram) call(ctx context.Context, method, contentType string, body io.Reader) (json.RawMessage, error) {
	endpoint := fmt.Sprintf("%s/bot%s/%s", t.baseURL, t.botToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", withoutURL(err))
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling telegram %s: %w", method, withoutURL(err))
	}
	defer resp.Body.Close()

	var envelope struct {
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&envelope)

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("telegram API error %d: %v", resp.StatusCode, envelope.Description)
		if sentinel := refusal(envelope.Description); sentinel != nil {
			return nil, fmt.Errorf("%w: %w", sentinel, err)
		}
		return nil, err
	}
	return envelope.Result, nil
}

// refusal reads what Telegram objected to out of its description, and returns
// the sentinel that says so — or nil for a rejection with no particular meaning
// to a caller.
//
// This is the only place in morse that matches on Telegram's prose. The Bot API
// offers nothing else to go on: the error codes do not distinguish these cases,
// so the sentence is the signal. Keeping it to one function means a reworded
// phrase is one thing to fix, and the phrases are kept to the ones that mean
// exactly what the sentinel claims — "message can't be edited", for instance,
// is a message that exists but is too old or not the bot's, which is a real
// problem to report rather than one to paper over with a new message.
func refusal(description string) error {
	for _, known := range []struct {
		phrase   string
		sentinel error
	}{
		{"message to edit not found", ErrMessageGone},
		{"MESSAGE_ID_INVALID", ErrMessageGone},
		{"message is not modified", ErrNotModified},
		{"file not found", ErrNoSuchFile},
		{"wrong file_id", ErrNoSuchFile},
		{"can't use getUpdates method while webhook is active", ErrWebhookSet},
		{"message thread not found", ErrNoSuchTopic},
		{"not enough rights", ErrNoRights},
	} {
		if strings.Contains(description, known.phrase) {
			return known.sentinel
		}
	}
	return nil
}

// caption renders a title and body as Telegram HTML, within a length limit.
//
// The limit counts the escaped text, and truncation happens before escaping so
// that a cut can never land inside an &amp; — a half-written entity is markup
// Telegram cannot parse, and it would reject the whole message.
//
// The format applies to the body alone. The title is what a lock screen shows,
// and a notification preview renders no markup, so it stays bold and nothing
// else.
func caption(title, message string, limit int, format Format) string {
	title, message = strings.TrimSpace(title), strings.TrimSpace(message)
	switch {
	case title == "" && message == "":
		return ""
	case message == "":
		return "<b>" + html.EscapeString(truncate(title, limit)) + "</b>"
	case title == "":
		return format.wrap(html.EscapeString(truncate(message, limit)))
	}
	// The title is what gets read on a lock screen, so it keeps its length and
	// the body gives way. The markup and the newline are not part of the text
	// Telegram counts, but the escaping is.
	title = truncate(title, limit)
	head := "<b>" + html.EscapeString(title) + "</b>\n"
	return head + format.wrap(html.EscapeString(truncate(message, limit-escapedLen(title)-1)))
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
