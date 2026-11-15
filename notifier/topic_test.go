package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leue21/morse/internal/testutil"
)

// A topic is how one chat keeps several senders apart, so it has to reach
// Telegram under the name Telegram reads.
func TestSendPostsIntoTheTopic(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	if _, err := tg.Send(context.Background(), "Backup", "done", SendOptions{Topic: 7}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(srv.Requests[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["message_thread_id"]; got != float64(7) {
		t.Errorf("message_thread_id = %v, want 7", got)
	}
}

// Without a topic the field is left out rather than sent as 0: 0 is not a
// topic, and a chat with no topics should see exactly the request it always
// has.
func TestSendWithoutATopicLeavesTheFieldOut(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	if _, err := tg.Send(context.Background(), "Backup", "done", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(srv.Requests[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	if got, ok := payload["message_thread_id"]; ok {
		t.Errorf("message_thread_id = %v, want it absent", got)
	}
}

func TestSendDocumentPostsIntoTheTopic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nightly.log")
	if err := os.WriteFile(path, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendDocument", 200, `{"ok":true}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	if _, err := tg.SendDocument(context.Background(), path, "Nightly", "", SendOptions{Topic: 7}); err != nil {
		t.Fatal(err)
	}
	form, cleanup := parseUpload(t, srv.Requests[0])
	defer cleanup()
	if got := form.Value["message_thread_id"]; len(got) != 1 || got[0] != "7" {
		t.Errorf("message_thread_id = %q, want 7", got)
	}
}

func TestSendToATopicThatIsNotThereSaysSo(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 400,
			`{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	_, err := tg.Send(context.Background(), "Backup", "done", SendOptions{Topic: 999})
	if !errors.Is(err, ErrNoSuchTopic) {
		t.Fatalf("err = %v, want ErrNoSuchTopic", err)
	}
}

// message_thread_id alone is not a topic: Telegram also sets it on a reply in
// a group without topics. Only is_topic_message makes it one.
func TestUpdatesReportTheTopicAMessageWasPostedIn(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/getUpdates", 200, `{"ok":true,"result":[
  {"update_id":1,"message":{"message_id":10,"date":1700000000,"chat":{"id":42},
    "message_thread_id":7,"is_topic_message":true,"text":"in a topic"}},
  {"update_id":2,"message":{"message_id":11,"date":1700000100,"chat":{"id":42},
    "message_thread_id":3,"text":"a reply, not a topic"}}
]}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	messages, _, err := tg.Updates(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	if messages[0].Topic != 7 {
		t.Errorf("topic = %d, want 7", messages[0].Topic)
	}
	if messages[1].Topic != 0 {
		t.Errorf("topic = %d on a plain reply, want 0", messages[1].Topic)
	}
}

func TestCreateTopicReportsTheNewID(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/createForumTopic", 200,
			`{"ok":true,"result":{"message_thread_id":12,"name":"ci","icon_color":7322096}}`).
		Start()
	tg := NewTelegram("tok", -1001)
	tg.baseURL = srv.URL()

	id, err := tg.CreateTopic(context.Background(), "ci")
	if err != nil || id != 12 {
		t.Fatalf("got (%d, %v), want (12, nil)", id, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(srv.Requests[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["name"] != "ci" || payload["chat_id"] != float64(-1001) {
		t.Errorf("payload = %v", payload)
	}
}

// The id is the whole point of the call; a success without one is not one.
func TestCreateTopicWithoutAnIDFails(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/createForumTopic", 200, `{"ok":true,"result":{"name":"ci"}}`).
		Start()
	tg := NewTelegram("tok", -1001)
	tg.baseURL = srv.URL()

	if _, err := tg.CreateTopic(context.Background(), "ci"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCreateTopicWithoutTheRightSaysSo(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/createForumTopic", 400,
			`{"ok":false,"error_code":400,"description":"Bad Request: not enough rights to create a topic"}`).
		Start()
	tg := NewTelegram("tok", -1001)
	tg.baseURL = srv.URL()

	if _, err := tg.CreateTopic(context.Background(), "ci"); !errors.Is(err, ErrNoRights) {
		t.Fatalf("err = %v, want ErrNoRights", err)
	}
}
