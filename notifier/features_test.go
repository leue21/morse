package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leue21/morse/internal/testutil"
)

// limited answers 429 with retry_after for the first `times` calls, then
// succeeds.
func limited(t *testing.T, retryAfter, times int) (*Telegram, *testutil.FakeAPI, *[]time.Duration) {
	t.Helper()
	calls := 0
	srv := testutil.NewFakeAPI(t).
		HandleFunc("POST", "/bottok/sendMessage", func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls <= times {
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprintf(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after %d","parameters":{"retry_after":%d}}`, retryAfter, retryAfter)
				return
			}
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":9}}`)
		}).
		Start()
	tg := NewTelegram("tok", 42).UseServer(srv.URL())
	var waits []time.Duration
	tg.wait = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return tg, srv, &waits
}

// A burst of alerts is when Telegram says "slow down", and dropping one then is
// the worst time to: morse waits as asked and sends it.
func TestRateLimitIsWaitedOutOnce(t *testing.T) {
	tg, srv, waits := limited(t, 3, 1)
	id, err := tg.Send(context.Background(), "Unit failed", "x", SendOptions{})
	if err != nil || id != 9 {
		t.Fatalf("got (%d, %v), want (9, nil)", id, err)
	}
	if len(*waits) != 1 || (*waits)[0] != 3*time.Second {
		t.Errorf("waits = %v, want [3s]", *waits)
	}
	// The same request both times: the body had to survive the first send.
	if len(srv.Requests) != 2 || string(srv.Requests[0].Body) != string(srv.Requests[1].Body) {
		t.Errorf("requests = %d, bodies differ or missing", len(srv.Requests))
	}
}

func TestRateLimitTooLongIsNotWaited(t *testing.T) {
	tg, srv, waits := limited(t, 600, 1)
	_, err := tg.Send(context.Background(), "T", "x", SendOptions{})
	if !errors.Is(err, ErrRateLimited) || !strings.Contains(err.Error(), "10m0s") {
		t.Fatalf("err = %v", err)
	}
	if len(*waits) != 0 || len(srv.Requests) != 1 {
		t.Errorf("waited %v over %d requests, want no wait", *waits, len(srv.Requests))
	}
}

// Once, not in a loop: a limit that outlasts its own wait is reported, not
// chased.
func TestRateLimitTwiceIsReported(t *testing.T) {
	tg, srv, _ := limited(t, 2, 5)
	if _, err := tg.Send(context.Background(), "T", "x", SendOptions{}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if len(srv.Requests) != 2 {
		t.Errorf("requests = %d, want 2", len(srv.Requests))
	}
}

func TestRateLimitWaitEndsWithTheContext(t *testing.T) {
	tg, _, _ := limited(t, 3, 1)
	tg.wait = sleep
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tg.Send(ctx, "T", "x", SendOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func jsonBody(t *testing.T, srv *testutil.FakeAPI, i int) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(srv.Requests[i].Body, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReplyAndNoPreviewReachTheAPI(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
		Handle("POST", "/bottok/editMessageText", 200, `{"ok":true}`).
		Start()
	tg := NewTelegram("tok", 42).UseServer(srv.URL())

	if _, err := tg.Send(context.Background(), "Backup", "done https://ci.example/1",
		SendOptions{ReplyTo: 77, Format: Format{NoPreview: true}}); err != nil {
		t.Fatal(err)
	}
	p := jsonBody(t, srv, 0)
	reply, _ := p["reply_parameters"].(map[string]any)
	if reply["message_id"] != float64(77) || reply["allow_sending_without_reply"] != true {
		t.Errorf("reply_parameters = %v", p["reply_parameters"])
	}
	if preview, _ := p["link_preview_options"].(map[string]any); preview["is_disabled"] != true {
		t.Errorf("link_preview_options = %v", p["link_preview_options"])
	}

	// An edit rebuilds the preview from the new text, so it has to be told
	// again; without the option, nothing about previews is sent at all.
	if err := tg.Edit(context.Background(), 5, "T", "https://x", Format{NoPreview: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := jsonBody(t, srv, 1)["link_preview_options"]; !ok {
		t.Error("the edit dropped no-preview")
	}
	if _, err := tg.Send(context.Background(), "T", "x", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if p := jsonBody(t, srv, 2); p["link_preview_options"] != nil || p["reply_parameters"] != nil {
		t.Errorf("unasked-for options sent: %v", p)
	}
}

// A form carries only strings, so reply_parameters has to go as JSON, not as
// Go's own map syntax.
func TestPhotoUploadIsAPhotoWithItsReply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.png")
	if err := os.WriteFile(path, []byte("\x89PNG..."), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendPhoto", 200, `{"ok":true,"result":{"message_id":3}}`).
		Start()
	tg := NewTelegram("tok", 42).UseServer(srv.URL())

	if _, err := tg.SendPhoto(context.Background(), path, "Disk", "92%", SendOptions{ReplyTo: 77}); err != nil {
		t.Fatal(err)
	}
	form, cleanup := parseUpload(t, srv.Requests[0])
	defer cleanup()
	if len(form.File["photo"]) != 1 {
		t.Errorf("no photo part: %v", form.File)
	}
	var reply map[string]any
	if raw := form.Value["reply_parameters"]; len(raw) != 1 || json.Unmarshal([]byte(raw[0]), &reply) != nil || reply["message_id"] != float64(77) {
		t.Errorf("reply_parameters = %q", form.Value["reply_parameters"])
	}
}

func TestPhotoOverItsLimitIsRefusedBeforeSending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxPhotoBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	tg := NewTelegram("tok", 42).UseServer("http://127.0.0.1:1") // never reached
	_, err = tg.SendPhoto(context.Background(), path, "T", "", SendOptions{})
	if err == nil || !strings.Contains(err.Error(), "at most 10 MB through sendPhoto") {
		t.Fatalf("err = %v", err)
	}
}

func TestPinIsSilent(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/pinChatMessage", 200, `{"ok":true,"result":true}`).
		Start()
	tg := NewTelegram("tok", 42).UseServer(srv.URL())
	if err := tg.Pin(context.Background(), 12); err != nil {
		t.Fatal(err)
	}
	if p := jsonBody(t, srv, 0); p["message_id"] != float64(12) || p["disable_notification"] != true {
		t.Errorf("payload = %v", p)
	}
}

// An edit replaces the message in place in the listing, and a later edit wins
// over an earlier one.
func TestUpdatesShowAMessageAsLastEdited(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getUpdates", 200, `{"ok":true,"result":[
  {"update_id":1,"message":{"message_id":10,"date":1700000000,"chat":{"id":42},"text":"ssh-ed25519 AAAA typo"}},
  {"update_id":2,"message":{"message_id":11,"date":1700000100,"chat":{"id":42},"text":"second"}},
  {"update_id":3,"edited_message":{"message_id":10,"date":1700000000,"edit_date":1700000200,"chat":{"id":42},"text":"ssh-ed25519 AAAA fixed"}},
  {"update_id":4,"edited_message":{"message_id":10,"date":1700000000,"edit_date":1700000300,"chat":{"id":42},"text":"ssh-ed25519 AAAA final"}},
  {"update_id":5,"edited_message":{"message_id":3,"date":1690000000,"chat":{"id":42},"text":"original aged out"}}
]}`).
		Start()
	tg := NewTelegram("tok", 42).UseServer(srv.URL())

	messages, _, err := tg.Updates(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range messages {
		got = append(got, fmt.Sprintf("%d:%s", m.MessageID, m.Text))
	}
	want := "10:ssh-ed25519 AAAA final 11:second 3:original aged out"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q\nwant %q", strings.Join(got, " "), want)
	}
}

func TestUpdatesNameWhatAPinNoticePinned(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getUpdates", 200, `{"ok":true,"result":[
  {"update_id":1,"message":{"message_id":37,"date":1700000000,"chat":{"id":42},
    "pinned_message":{"message_id":36,"date":1700000000,"chat":{"id":42},"text":"status"}}}
]}`).
		Start()
	messages, _, err := NewTelegram("tok", 42).UseServer(srv.URL()).Updates(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Pinned != 36 {
		t.Errorf("messages = %+v, want a notice pinning 36", messages)
	}
}
