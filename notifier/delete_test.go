package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/leue21/morse/internal/testutil"
)

// deleteMessages takes at most a hundred ids, so more go out in batches.
func TestDeleteBatchesByAHundred(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/deleteMessages", 200, `{"ok":true,"result":true}`).
		Start()
	tg := NewTelegram("tok", 42).UseServer(srv.URL())

	ids := make([]int64, 250)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if err := tg.Delete(context.Background(), ids...); err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, r := range srv.Requests {
		var p struct {
			IDs []int64 `json:"message_ids"`
		}
		if err := json.Unmarshal(r.Body, &p); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(p.IDs))
	}
	if len(sizes) != 3 || sizes[0] != 100 || sizes[1] != 100 || sizes[2] != 50 {
		t.Errorf("batches = %v, want [100 100 50]", sizes)
	}
}

func TestDeleteWithoutTheRightSaysSo(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/deleteMessages", 400,
			`{"ok":false,"error_code":400,"description":"Bad Request: not enough rights to delete a message"}`).
		Start()
	tg := NewTelegram("tok", 42).UseServer(srv.URL())
	if err := tg.Delete(context.Background(), 1); !errors.Is(err, ErrNoRights) {
		t.Fatalf("err = %v, want ErrNoRights", err)
	}
}

func TestDeleteTopicNamesTheTopic(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/deleteForumTopic", 200, `{"ok":true,"result":true}`).
		Start()
	tg := NewTelegram("tok", -1001).UseServer(srv.URL())
	if err := tg.DeleteTopic(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(srv.Requests[0].Body, &p); err != nil {
		t.Fatal(err)
	}
	if p["message_thread_id"] != float64(25) || p["chat_id"] != float64(-1001) {
		t.Errorf("payload = %v", p)
	}
}
