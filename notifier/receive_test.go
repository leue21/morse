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
	"testing"

	"morse/internal/testutil"
)

// window is a getUpdates response with one text message, one photo, and one
// message from a chat the config does not name.
const window = `{"ok":true,"result":[
  {"update_id":1,"message":{"message_id":10,"date":1700000000,
    "chat":{"id":42},"from":{"first_name":"Ada","last_name":"Lovelace"},
    "text":"ssh-ed25519 AAAAC3Nz"}},
  {"update_id":2,"message":{"message_id":11,"date":1700000100,
    "chat":{"id":42},"from":{"username":"ada"},"caption":"the logs",
    "photo":[{"file_id":"small","file_size":100},{"file_id":"large","file_size":900}]}},
  {"update_id":3,"message":{"message_id":12,"date":1700000200,
    "chat":{"id":99},"text":"another conversation"}}
]}`

func TestUpdatesReadsTheWindowWithoutConsumingIt(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getUpdates", 200, window).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	messages, full, err := tg.Updates(context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if full {
		t.Error("full = true on a window of three updates out of a hundred asked for")
	}

	// The message from chat 99 is another conversation's business.
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(messages), messages)
	}
	if messages[0].Text != "ssh-ed25519 AAAAC3Nz" || messages[0].From != "Ada Lovelace" {
		t.Errorf("first message = %+v", messages[0])
	}
	if messages[0].File != nil {
		t.Errorf("first message has a file: %+v", messages[0].File)
	}
	// A caption is the text of a message that came with something attached.
	if messages[1].Text != "the logs" || messages[1].From != "ada" {
		t.Errorf("second message = %+v", messages[1])
	}
	// A photo arrives at several sizes and the largest is the one meant.
	if messages[1].File == nil || messages[1].File.ID != "large" {
		t.Errorf("second message file = %+v", messages[1].File)
	}

	// No offset of any sign may be sent. A positive one confirms — deleting
	// every update below it server-side, for every reader of the bot — and a
	// negative one is documented as "all previous updates will be forgotten",
	// which throws away the oldest whenever more are pending than were asked
	// for. Only the absence of the parameter reads without taking anything.
	var sent map[string]any
	json.Unmarshal(srv.Requests[0].Body, &sent)
	if _, ok := sent["offset"]; ok {
		t.Errorf("offset = %v was sent; any offset costs somebody their updates", sent["offset"])
	}
	// allowed_updates is remembered by Telegram and applied to every later
	// call, from any client. Naming morse's two types here would stop callback
	// queries and edits reaching anything else that polls this bot.
	if _, ok := sent["allowed_updates"]; ok {
		t.Errorf("allowed_updates = %v was sent; it would change the bot's setting for every other reader", sent["allowed_updates"])
	}
	if timeout, _ := sent["timeout"].(float64); timeout != 0 {
		t.Errorf("timeout = %v, want 0 — morse does not long-poll", sent["timeout"])
	}
}

// A window that comes back as long as the request says nothing about what is
// behind it, and reading from the oldest end means what is behind it is the
// newest. The caller has to be told, or it is looking at a list that is
// complete-looking and wrong.
func TestUpdatesReportsAFullWindow(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		HandleFunc("POST", "/bottok/getUpdates", func(w http.ResponseWriter, _ *http.Request) {
			var updates []string
			for i := range 3 {
				updates = append(updates, fmt.Sprintf(
					`{"update_id":%d,"message":{"message_id":%d,"date":1700000000,"chat":{"id":42},"text":"m"}}`,
					i, i))
			}
			fmt.Fprintf(w, `{"ok":true,"result":[%s]}`, strings.Join(updates, ","))
		}).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	messages, full, err := tg.Updates(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(messages))
	}
	if !full {
		t.Error("full = false, want true — telegram gave everything that was asked for, so there may be more")
	}
}

// Counted in updates rather than in messages: an update for another chat is
// dropped here but still took up a slot in the answer.
func TestUpdatesCountsFilteredUpdatesTowardsFull(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getUpdates", 200,
			`{"ok":true,"result":[
			  {"update_id":1,"message":{"message_id":1,"date":1700000000,"chat":{"id":99},"text":"elsewhere"}},
			  {"update_id":2,"message":{"message_id":2,"date":1700000000,"chat":{"id":42},"text":"here"}}
			]}`).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	messages, full, err := tg.Updates(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}
	if !full {
		t.Error("full = false, want true — two updates were asked for and two came back")
	}
}

// Every kind of file Telegram can attach has to be recognised. One that is not
// costs more than its download: the message carries no text either, so it shows
// up in the listing as a blank line.
func TestUpdatesFindsEveryKindOfAttachment(t *testing.T) {
	tests := []struct {
		kind   string
		body   string
		wantID string
	}{
		{"document", `"document":{"file_id":"d","file_name":"report.pdf"}`, "d"},
		{"audio", `"audio":{"file_id":"a"}`, "a"},
		{"video", `"video":{"file_id":"v"}`, "v"},
		{"voice", `"voice":{"file_id":"vo"}`, "vo"},
		{"sticker", `"sticker":{"file_id":"s"}`, "s"},
		{"video note", `"video_note":{"file_id":"vn"}`, "vn"},
		// A live photo also carries the static photo for backward compatibility;
		// the video file is the live part the sender meant.
		{"live photo over its static photo", `"live_photo":{"file_id":"lp"},"photo":[{"file_id":"still"}]`, "lp"},
		{"photo", `"photo":[{"file_id":"small"},{"file_id":"large"}]`, "large"},
		// Telegram sends a GIF as both, and the animation is the one meant.
		{"animation over its document", `"animation":{"file_id":"an"},"document":{"file_id":"doc"}`, "an"},
	}

	for _, tt := range tests {
		srv := testutil.NewFakeAPI(t).
			Handle("POST", "/bottok/getUpdates", 200, fmt.Sprintf(
				`{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"date":1700000000,"chat":{"id":42},%s}}]}`,
				tt.body)).
			Start()

		tg := NewTelegram("tok", 42)
		tg.baseURL = srv.URL()

		messages, _, err := tg.Updates(context.Background(), 100)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.kind, err)
			continue
		}
		if len(messages) != 1 {
			t.Errorf("%s: got %d messages, want 1", tt.kind, len(messages))
			continue
		}
		if messages[0].File == nil {
			t.Errorf("%s: no file found; it would list as a blank line", tt.kind)
			continue
		}
		if messages[0].File.ID != tt.wantID {
			t.Errorf("%s: file id = %q, want %q", tt.kind, messages[0].File.ID, tt.wantID)
		}
	}
}

// The ceiling is Telegram's, so it is kept where the call is made: asking for
// more than the API will give is a request it refuses outright, which would
// cost the whole window rather than trim it.
func TestUpdatesClampsTheLimit(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getUpdates", 200, `{"ok":true,"result":[]}`).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	for _, limit := range []int{0, -5, 500} {
		srv.Requests = nil
		if _, _, err := tg.Updates(context.Background(), limit); err != nil {
			t.Fatalf("limit %d: unexpected error: %v", limit, err)
		}
		var sent map[string]any
		json.Unmarshal(srv.Requests[0].Body, &sent)
		if asked, _ := sent["limit"].(float64); int(asked) != MaxWindow {
			t.Errorf("limit %d was sent as %v, want %d", limit, sent["limit"], MaxWindow)
		}
	}
}

func TestUpdatesEmptyWindow(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getUpdates", 200, `{"ok":true,"result":[]}`).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	messages, _, err := tg.Updates(context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("got %d messages, want none", len(messages))
	}
}

func TestUpdatesReportsAWebhook(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getUpdates", http.StatusConflict,
			`{"ok":false,"description":"Conflict: can't use getUpdates method while webhook is active"}`).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	_, _, err := tg.Updates(context.Background(), 100)
	if !errors.Is(err, ErrWebhookSet) {
		t.Fatalf("error = %v, want ErrWebhookSet", err)
	}
}

func TestDownload(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getFile", 200,
			`{"ok":true,"result":{"file_id":"f","file_path":"documents/file_1.pdf","file_size":9}}`).
		HandleFunc("GET", "/file/bottok/documents/file_1.pdf", func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, "%PDF-1.7\n")
		}).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	dir := t.TempDir()
	path, err := tg.Download(context.Background(), "f", dir, "report.pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != filepath.Join(dir, "report.pdf") {
		t.Errorf("path = %s, want %s", path, filepath.Join(dir, "report.pdf"))
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(content) != "%PDF-1.7\n" {
		t.Errorf("content = %q", content)
	}
	// Nothing half-written may survive alongside it.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want 1", len(entries))
	}
}

// A file that has no name of its own falls back to the one Telegram gave its
// path, which is the only name anybody involved knows.
func TestDownloadWithoutAName(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getFile", 200,
			`{"ok":true,"result":{"file_path":"photos/file_7.jpg","file_size":4}}`).
		Handle("GET", "/file/bottok/photos/file_7.jpg", 200, "jpeg").
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	dir := t.TempDir()
	path, err := tg.Download(context.Background(), "f", dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(path) != "file_7.jpg" {
		t.Errorf("path = %s, want it named file_7.jpg", path)
	}
}

// Telegram explains an invalid file id only in the refusal's prose. The
// package-level refusal mapper turns that prose into a sentinel before Download
// decides how to present it, just as it does for edit refusals.
func TestDownloadReportsNoSuchFile(t *testing.T) {
	for _, description := range []string{
		"Bad Request: file not found",
		"Bad Request: wrong file_id or the file is temporarily unavailable",
	} {
		srv := testutil.NewFakeAPI(t).
			Handle("POST", "/bottok/getFile", http.StatusBadRequest,
				fmt.Sprintf(`{"ok":false,"description":%q}`, description)).
			Start()

		tg := NewTelegram("tok", 42)
		tg.baseURL = srv.URL()

		_, err := tg.Download(context.Background(), "gone", t.TempDir(), "gone.pdf")
		if !errors.Is(err, ErrNoSuchFile) {
			t.Errorf("description %q: error = %v, want ErrNoSuchFile", description, err)
		}
	}
}

// The filename comes from whoever sent the file and the caller chose only a
// directory, so a name that is already taken has to be refused: two people pick
// "report.pdf" independently all the time, and the one already on disk was
// never part of this conversation.
func TestDownloadRefusesToOverwrite(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getFile", 200,
			`{"ok":true,"result":{"file_path":"documents/file_1.pdf","file_size":9}}`).
		Handle("GET", "/file/bottok/documents/file_1.pdf", 200, "%PDF-1.7\n").
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	dir := t.TempDir()
	existing := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(existing, []byte("something else entirely"), 0o600); err != nil {
		t.Fatalf("writing the file already there: %v", err)
	}

	_, err := tg.Download(context.Background(), "f", dir, "report.pdf")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want a refusal naming the file", err)
	}
	content, err := os.ReadFile(existing)
	if err != nil || string(content) != "something else entirely" {
		t.Errorf("the file on disk = %q, %v — it must be untouched", content, err)
	}
}

// A download that fails part way leaves nothing behind under the real name: a
// half-written file is worse than none, because it looks like it worked.
func TestDownloadLeavesNothingBehindOnFailure(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getFile", 200,
			`{"ok":true,"result":{"file_path":"documents/file_1.pdf","file_size":9}}`).
		HandleFunc("GET", "/file/bottok/documents/file_1.pdf", func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, "%PDF") // four bytes of the nine promised
		}).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	dir := t.TempDir()
	if _, err := tg.Download(context.Background(), "f", dir, "report.pdf"); err == nil {
		t.Fatal("expected an error on a truncated download")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("directory holds %v, want nothing", entries)
	}
}

func TestDownloadRefusesAFileTooLargeToFetch(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getFile", 200,
			`{"ok":true,"result":{"file_path":"videos/big.mp4","file_size":41943040}}`).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	_, err := tg.Download(context.Background(), "f", t.TempDir(), "big.mp4")
	if err == nil || !strings.Contains(err.Error(), "at most 20 MB") {
		t.Fatalf("error = %v, want the download ceiling named", err)
	}
	// The refusal comes before the transfer, not part-way through it.
	if len(srv.Requests) != 1 {
		t.Errorf("made %d requests, want 1 — the file must not be fetched", len(srv.Requests))
	}
}

// A filename arrives from the chat, so it is a sender's string and not morse's.
func TestDestinationRefusesAPath(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		want string // the base name expected, or "" for a refusal
	}{
		{"report.pdf", "report.pdf"},
		{"a file with spaces.txt", "a file with spaces.txt"},
		{".hidden", ".hidden"},
		// Everything below is a path, or nothing, rather than a filename.
		{"../../.ssh/authorized_keys", ""},
		{"/etc/passwd", ""},
		{"logs/today.txt", ""},
		{"..", ""},
		{".", ""},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		path, err := destination(dir, tt.name)
		if tt.want == "" {
			if err == nil {
				t.Errorf("destination(%q) = %s, want a refusal", tt.name, path)
			}
			continue
		}
		if err != nil {
			t.Errorf("destination(%q): unexpected error: %v", tt.name, err)
			continue
		}
		if path != filepath.Join(dir, tt.want) {
			t.Errorf("destination(%q) = %s, want %s", tt.name, path, filepath.Join(dir, tt.want))
		}
	}
}
