package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"morse/internal/testutil"
)

func TestSendSuccess(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok123/sendMessage", 200, `{"ok":true}`).
		Start()

	tg := NewTelegram("tok123", 42)
	tg.baseURL = srv.URL()

	err := tg.Send(context.Background(), "Test Title", "Hello world", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(srv.Requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(srv.Requests))
	}
	req := srv.Requests[0]
	if req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.Method)
	}
	if req.Path != "/bottok123/sendMessage" {
		t.Errorf("path = %s, want /bottok123/sendMessage", req.Path)
	}

	var received map[string]any
	json.Unmarshal(req.Body, &received)
	if received["parse_mode"] != "HTML" {
		t.Errorf("parse_mode = %v, want HTML", received["parse_mode"])
	}
	chatID, ok := received["chat_id"].(float64)
	if !ok || int64(chatID) != 42 {
		t.Errorf("chat_id = %v, want 42", received["chat_id"])
	}
}

func TestSendAPIError(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", http.StatusBadRequest, `{"ok":false,"description":"Bad Request: chat not found"}`).
		Start()

	tg := NewTelegram("tok", 1)
	tg.baseURL = srv.URL()

	err := tg.Send(context.Background(), "Title", "Msg", false)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestSendNetworkError(t *testing.T) {
	tg := NewTelegram("s3cr3t-token", 1)
	tg.baseURL = "http://127.0.0.1:1" // nothing listening

	err := tg.Send(context.Background(), "Title", "Msg", false)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	// morse's errors reach stderr, and a caller is free to log them.
	if strings.Contains(err.Error(), "s3cr3t-token") {
		t.Errorf("error leaks the bot token: %v", err)
	}
}

// Telegram rejects a message whose text contains unescaped markup, which would
// lose the alert entirely — and a piped log excerpt is exactly where stray
// markup comes from.
func TestSendEscapesMarkupInTheMessage(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
		Start()

	tg := NewTelegram("tok", 1)
	tg.baseURL = srv.URL()

	if err := tg.Send(context.Background(), "a<b & c", `open("x") failed: [Errno 2] C:\logs`, false); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(srv.Requests[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	text, _ := payload["text"].(string)
	want := "<b>a&lt;b &amp; c</b>\nopen(&#34;x&#34;) failed: [Errno 2] C:\\logs"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestSendMapsSilenceToTheTelegramFlag(t *testing.T) {
	for _, silent := range []bool{true, false} {
		srv := testutil.NewFakeAPI(t).
			Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
			Start()
		tg := NewTelegram("tok", 42)
		tg.baseURL = srv.URL()

		if err := tg.Send(context.Background(), "Title", "Body", silent); err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(srv.Requests[0].Body), &payload); err != nil {
			t.Fatal(err)
		}
		if got := payload["disable_notification"]; got != silent {
			t.Errorf("silent=%v: disable_notification = %v", silent, got)
		}
	}
}

// parseUpload reads back a recorded multipart request the way Telegram would.
func parseUpload(t *testing.T, req testutil.Request) (*multipart.Form, func()) {
	t.Helper()
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("content type %q: %v", req.Header.Get("Content-Type"), err)
	}
	form, err := multipart.NewReader(bytes.NewReader(req.Body), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	return form, func() { form.RemoveAll() }
}

func TestSendDocumentUploadsTheFileWithACaption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nightly.log")
	if err := os.WriteFile(path, []byte("412 files\nexit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendDocument", 200, `{"ok":true}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	if err := tg.SendDocument(context.Background(), path, "Nightly", "done", true); err != nil {
		t.Fatal(err)
	}

	form, cleanup := parseUpload(t, srv.Requests[0])
	defer cleanup()

	if got := form.Value["caption"]; len(got) != 1 || got[0] != "<b>Nightly</b>\ndone" {
		t.Errorf("caption = %q", got)
	}
	for field, want := range map[string]string{
		"chat_id": "42", "parse_mode": "HTML", "disable_notification": "true",
	} {
		if got := form.Value[field]; len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}

	files := form.File["document"]
	if len(files) != 1 {
		t.Fatalf("got %d document parts, want 1", len(files))
	}
	// The name Telegram shows is the base name, not the caller's whole path:
	// a temp directory is noise to the reader and leaks the sender's layout.
	if files[0].Filename != "nightly.log" {
		t.Errorf("filename = %q, want nightly.log", files[0].Filename)
	}
	f, err := files[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	content, _ := io.ReadAll(f)
	if string(content) != "412 files\nexit 0\n" {
		t.Errorf("uploaded content = %q", content)
	}
}

// A file is worth sending on its own. An empty caption field is not the same as
// no caption: Telegram would render the bold title of a message that has none.
func TestSendDocumentOmitsAnEmptyCaption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendDocument", 200, `{"ok":true}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	if err := tg.SendDocument(context.Background(), path, "", "", false); err != nil {
		t.Fatal(err)
	}
	form, cleanup := parseUpload(t, srv.Requests[0])
	defer cleanup()
	if _, ok := form.Value["caption"]; ok {
		t.Errorf("caption present for an uncaptioned file: %q", form.Value["caption"])
	}
}

// The size check happens before the upload, so an oversized file costs nothing
// to reject — and the error names the limit rather than echoing Telegram's.
func TestSendDocumentRefusesWhatTelegramWouldReject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxUploadBytes + 1); err != nil { // sparse: no disk used
		t.Fatal(err)
	}
	f.Close()

	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendDocument", 200, `{"ok":true}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	err = tg.SendDocument(context.Background(), path, "Image", "", false)
	if err == nil || !strings.Contains(err.Error(), "at most 50 MB") {
		t.Fatalf("err = %v, want a size refusal", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("sent %d requests for a file it refused", len(srv.Requests))
	}
}

func TestSendDocumentReportsAMissingFile(t *testing.T) {
	tg := NewTelegram("tok", 42)
	tg.baseURL = "http://127.0.0.1:1" // never reached
	err := tg.SendDocument(context.Background(), filepath.Join(t.TempDir(), "gone"), "T", "", false)
	if err == nil || !strings.Contains(err.Error(), "opening file") {
		t.Fatalf("err = %v, want an open failure", err)
	}
}

func TestCaptionFitsTheLimit(t *testing.T) {
	tests := []struct {
		name         string
		title, body  string
		limit        int
		want         string
		wantMaxRunes int
	}{{
		name: "short text is left alone", title: "T", body: "b", limit: 100,
		want: "<b>T</b>\nb",
	}, {
		name: "no body means no trailing newline", title: "T", limit: 100,
		want: "<b>T</b>",
	}, {
		name: "no title means no markup", body: "b", limit: 100,
		want: "b",
	}, {
		name: "nothing at all is nothing", limit: 100, want: "",
	}, {
		// The title is what gets read on a lock screen, so the body gives way.
		name: "a long body is cut, the title survives", title: "Nightly",
		body: strings.Repeat("x", 4000), limit: 100, wantMaxRunes: 100,
	}, {
		// An escaped & spends five characters of the budget, not one, and a cut
		// landing inside the entity would be markup Telegram cannot parse.
		name: "escaping counts against the budget", body: strings.Repeat("&", 50),
		limit: 20, wantMaxRunes: 20,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := caption(tc.title, tc.body, tc.limit)
			if tc.want != "" || tc.wantMaxRunes == 0 {
				if got != tc.want {
					t.Fatalf("caption = %q, want %q", got, tc.want)
				}
				return
			}
			// Markup and the newline are not text Telegram counts against the
			// limit; the escaped characters are.
			text := strings.TrimPrefix(got, "<b>")
			text = strings.Replace(text, "</b>\n", "", 1)
			if n := len([]rune(text)); n > tc.wantMaxRunes {
				t.Errorf("caption body is %d runes, want at most %d", n, tc.wantMaxRunes)
			}
			if strings.Contains(text, "&am") && !strings.Contains(text, "&amp;") {
				t.Errorf("caption ends inside an entity: %q", got)
			}
		})
	}
}
