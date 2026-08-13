package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// What Telegram will hand back. An upload may be 50 MB, but a bot may only
// download 20 of them: the ceilings are different numbers for different
// directions, and this is the one that governs `receive get --save`.
const maxDownloadBytes = 20 << 20

// MaxWindow is the most updates the Bot API will return in one call, and so —
// since morse asks once and never confirms an offset — the whole of what it can
// see. It is exported because a caller has to be able to say what the limit is
// without hardcoding a number that belongs to Telegram.
const MaxWindow = 100

// ErrNoSuchFile reports a file id Telegram will not resolve — most often one
// that has aged out of the window along with the message that carried it.
var ErrNoSuchFile = errors.New("file not found")

// Message is an inbound message, cut down to what morse can do something with:
// enough to show it in a list, and enough to fetch what it carried.
type Message struct {
	MessageID int64     `json:"message_id"`
	ChatID    int64     `json:"chat_id"`
	Date      time.Time `json:"date"`
	From      string    `json:"from,omitempty"`
	Text      string    `json:"text,omitempty"`
	File      *File     `json:"file,omitempty"`
}

// File is an attachment, named well enough to be listed and identified well
// enough to be downloaded. Size is what Telegram reported and may be 0: the API
// documents it as optional, so it is worth showing but not worth trusting.
type File struct {
	ID string `json:"id"`
	// Kind is what Telegram called it — "document", "photo", "voice". Only a
	// document and the media that came from a file have a Name; a photo, a
	// sticker or a voice note is named by nothing at all, and "[sticker]" in a
	// listing says more than an empty pair of brackets.
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`
	MIME string `json:"mime_type,omitempty"`
}

// Updates reports the messages the bot can currently see, oldest first, and
// whether the answer filled the request — full means as many updates came back
// as were asked for, so there may be more queued behind them. It is not proof
// that there are: a queue of exactly the size asked for fills the request too,
// and there is no way to tell the two apart without asking for more.
//
// It passes no offset at all, which is what makes the read non-destructive.
// There are three ways to ask, and two of them take something away:
//
//   - A positive offset confirms every update below it, deleting them
//     server-side, for good, for every reader of the bot.
//   - A negative offset means "the last n from the end of the queue", and the
//     API says of it: "All previous updates will be forgotten." With more
//     updates pending than the limit, asking this way destroys the oldest —
//     which is exactly the case where a caller most wants them.
//   - No offset returns "updates starting with the earliest unconfirmed
//     update", and forgets nothing.
//
// So morse asks the third way. The price is that a backlog longer than the
// limit is read from the wrong end: the oldest are returned and the newest are
// behind them, unreachable without a confirmation morse will not make on a
// caller's behalf. full says so, and the command layer reports it. Losing
// somebody else's updates is not recoverable; showing the wrong hundred is.
//
// allowed_updates is deliberately not sent either. Telegram keeps the last
// value given to it and applies it to every later call, from any client — so
// naming the two types morse cares about here would quietly stop callback
// queries and edits reaching anything else polling this bot. What morse does
// not understand it ignores below, which costs nothing and changes nothing.
//
// The window is also not identical every time. Telegram will not hand the same
// updates over twice in immediate succession: a second call within about half a
// second is answered with the queue's oldest update and nothing else, and
// everything is back once that passes. Nothing has been consumed — the updates
// are simply not offered again that fast. See findMessage in the command layer,
// which asks again rather than believing the first answer.
func (t *Telegram) Updates(ctx context.Context, limit int) (messages []Message, full bool, err error) {
	// The ceiling is Telegram's, so it is kept here rather than trusted to
	// whoever is calling: asking for more than the API will give is a request
	// it rejects outright, which would cost the window rather than trim it.
	if limit < 1 || limit > MaxWindow {
		limit = MaxWindow
	}
	result, err := t.callJSON(ctx, "getUpdates", map[string]any{
		"limit":   limit,
		"timeout": 0, // one call and out; morse does not long-poll
	})
	if err != nil {
		return nil, false, fmt.Errorf("reading updates: %w", err)
	}

	var updates []struct {
		Message     *rawMessage `json:"message"`
		ChannelPost *rawMessage `json:"channel_post"`
	}
	if err := json.Unmarshal(result, &updates); err != nil {
		return nil, false, fmt.Errorf("reading updates: %w", err)
	}

	messages = make([]Message, 0, len(updates))
	for _, update := range updates {
		raw := update.Message
		if raw == nil {
			raw = update.ChannelPost
		}
		// A bot can sit in several chats, and the config names the one morse
		// belongs to. Anything else is another conversation's business. An
		// update of a kind morse does not read — a callback query, a reaction —
		// falls out here too, which is why it need not be filtered for at the
		// API and nothing about the bot's settings has to change.
		if raw == nil || raw.Chat.ID != t.chatID {
			continue
		}
		messages = append(messages, raw.message())
	}
	// Counted in updates, not in messages: the question is whether Telegram had
	// more to give, and the ones filtered out above still took up room.
	return messages, len(updates) >= limit, nil
}

// Download fetches a file into dir and reports the path it wrote.
//
// Telegram hands out a temporary path rather than the file itself, so this is
// two calls: getFile to learn the path, and a plain GET to fetch it. The link
// carries the bot token exactly as an API URL does, so a transport failure gets
// the same treatment.
func (t *Telegram) Download(ctx context.Context, fileID, dir, name string) (string, error) {
	result, err := t.callJSON(ctx, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		if errors.Is(err, ErrNoSuchFile) {
			return "", fmt.Errorf("%w: %s", err, fileID)
		}
		return "", fmt.Errorf("locating file: %w", err)
	}
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := json.Unmarshal(result, &file); err != nil {
		return "", fmt.Errorf("locating file: %w", err)
	}
	if file.Path == "" {
		return "", fmt.Errorf("%w: %s", ErrNoSuchFile, fileID)
	}
	// Refusing here rather than mid-write turns a partial download into an
	// error before the first byte, and says the actual limit.
	if file.Size > maxDownloadBytes {
		return "", fmt.Errorf("the file is %d MB; a bot may download at most %d MB",
			file.Size>>20, maxDownloadBytes>>20)
	}

	if name == "" {
		name = filepath.Base(file.Path)
	}
	path, err := destination(dir, name)
	if err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("%s/file/bot%s/%s", t.baseURL, t.botToken, file.Path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", withoutURL(err))
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading file: %w", withoutURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading file: telegram API error %d", resp.StatusCode)
	}

	// Down to a temporary file first, and only then given its name. The name is
	// what anything else looking in this directory goes by, so it should appear
	// when the file behind it is whole: a partial download under the real name
	// looks like a finished one, to a person and to whatever they point at it,
	// and a kill -9 in the middle would leave it that way for good.
	tmp, err := os.CreateTemp(dir, ".morse-*")
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // whether it was published or abandoned
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, maxDownloadBytes))
	if err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	if file.Size > 0 && written != file.Size {
		return "", fmt.Errorf("writing %s: got %d bytes, telegram said %d", path, written, file.Size)
	}
	if err := publish(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// publish gives a finished download its name, and refuses to take a name that
// is already in use.
//
// A hard link does both at once, which nothing else here does: it is atomic, so
// the name never points at a half-written file, and it fails rather than
// replacing what is already there. Rename would clobber it; checking first and
// then renaming would leave a gap between the check and the write.
//
// Not every filesystem has links — a FAT-formatted stick does not — so there is
// a second way, which keeps the refusal and gives up only the atomicity that
// the filesystem itself cannot offer.
func publish(tmp, path string) error {
	switch err := os.Link(tmp, path); {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrExist):
		return fmt.Errorf("%s already exists; move it, or save into another directory", path)
	case errors.Is(err, errors.ErrUnsupported), errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EXDEV):
		return copyInto(tmp, path)
	default:
		return fmt.Errorf("writing %s: %w", path, err)
	}
}

// copyInto is publish for a filesystem that cannot link: the name is still
// claimed with O_EXCL and never taken from anything already holding it, but the
// bytes arrive after the name exists rather than before.
func copyInto(tmp, path string) error {
	source, err := os.Open(tmp)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer source.Close()

	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; move it, or save into another directory", path)
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if _, err := io.Copy(out, source); err != nil {
		out.Close()
		os.Remove(path)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// destination works out where a downloaded file goes, from a name morse did not
// choose. The sender did: a filename arrives from the chat the same way a
// --track label arrives from a command line (track.pathFor), and
// "../../.ssh/authorized_keys" is a path rather than a name.
//
// Anything that is not purely a name is refused rather than flattened to its
// last component. Flattening is safe — the write would still land in dir — but
// it is silent, and a file that arrives under a name nobody typed is worse than
// one that does not arrive: the caller asked to save what the sender sent, and
// a refusal says exactly which part of that was not possible.
func destination(dir, name string) (string, error) {
	notAName := fmt.Errorf("%q is not a filename; save it under a name of your own", name)
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return "", notAName
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return "", notAName
	}
	path := filepath.Join(dir, name)
	// Belt and braces: whatever the platform makes of the name, it has to end
	// up as a direct child of the directory the caller named.
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", notAName
	}
	return path, nil
}

// rawMessage is the part of Telegram's Message that morse reads.
type rawMessage struct {
	MessageID int64 `json:"message_id"`
	Date      int64 `json:"date"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	From *struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	} `json:"from"`
	Text      string    `json:"text"`
	Caption   string    `json:"caption"`
	Animation *rawFile  `json:"animation"`
	Audio     *rawFile  `json:"audio"`
	Document  *rawFile  `json:"document"`
	LivePhoto *rawFile  `json:"live_photo"`
	Sticker   *rawFile  `json:"sticker"`
	Video     *rawFile  `json:"video"`
	VideoNote *rawFile  `json:"video_note"`
	Voice     *rawFile  `json:"voice"`
	Photo     []rawFile `json:"photo"`
}

type rawFile struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	MIMEType string `json:"mime_type"`
}

func (r *rawMessage) message() Message {
	m := Message{
		MessageID: r.MessageID,
		ChatID:    r.Chat.ID,
		Date:      time.Unix(r.Date, 0),
		Text:      r.Text,
	}
	if m.Text == "" {
		// A caption is the text of a message that came with something attached,
		// and is what the sender typed. There is nothing else to call it.
		m.Text = r.Caption
	}
	if r.From != nil {
		m.From = strings.TrimSpace(strings.Join([]string{r.From.FirstName, r.From.LastName}, " "))
		if m.From == "" {
			m.From = r.From.Username
		}
	}
	m.File = r.attachment()
	return m
}

// attachment picks the one file a message carries.
//
// Every kind of file Telegram can put on a message is listed, because one that
// is not listed does not merely lose its download — the message shows up in the
// listing as a blank line, since a sticker or a video note usually carries no
// text either.
//
// Animation comes first on purpose: Telegram sends a GIF as an animation *and*
// as a document, and the animation is the one the sender meant. A photo, at the
// other end, arrives as a set of the same image at several sizes, and the last
// is the largest — the one a person asking for the photo means.
func (r *rawMessage) attachment() *File {
	for _, candidate := range []struct {
		kind string
		file *rawFile
	}{
		{"animation", r.Animation},
		{"audio", r.Audio},
		{"document", r.Document},
		{"live photo", r.LivePhoto},
		{"sticker", r.Sticker},
		{"video", r.Video},
		{"video note", r.VideoNote},
		{"voice", r.Voice},
	} {
		if candidate.file != nil && candidate.file.FileID != "" {
			return candidate.file.file(candidate.kind)
		}
	}
	if len(r.Photo) > 0 {
		return r.Photo[len(r.Photo)-1].file("photo")
	}
	return nil
}

func (r rawFile) file(kind string) *File {
	return &File{ID: r.FileID, Kind: kind, Name: r.FileName, Size: r.FileSize, MIME: r.MIMEType}
}
