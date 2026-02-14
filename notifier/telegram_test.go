package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"salert/internal/testutil"
)

func TestSendSuccess(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok123/sendMessage", 200, `{"ok":true}`).
		Start()

	tg := NewTelegram("tok123", 42)
	tg.baseURL = srv.URL()

	err := tg.Send("Test Title", "Hello world")
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
	if received["parse_mode"] != "MarkdownV2" {
		t.Errorf("parse_mode = %v, want MarkdownV2", received["parse_mode"])
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

	err := tg.Send("Title", "Msg")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestSendNetworkError(t *testing.T) {
	tg := NewTelegram("tok", 1)
	tg.baseURL = "http://127.0.0.1:1" // nothing listening

	err := tg.Send("Title", "Msg")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestPollCommandsHandlesAlert(t *testing.T) {
	// getUpdates returns one update with /alert, then the test cancels.
	updatesBody := `{"ok":true,"result":[{"update_id":1,"message":{"text":"/alert","chat":{"id":42}}}]}`
	emptyUpdates := `{"ok":true,"result":[]}`

	var callCount atomic.Int32
	srv := testutil.NewFakeAPI(t).
		HandleFunc("GET", "/bottok/getUpdates", func(w http.ResponseWriter, r *http.Request) {
			if callCount.Add(1) == 1 {
				w.WriteHeader(200)
				w.Write([]byte(updatesBody))
			} else {
				w.WriteHeader(200)
				w.Write([]byte(emptyUpdates))
			}
		}).
		Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	ctx, cancel := context.WithCancel(context.Background())

	handlerCalled := make(chan string, 1)
	handler := func(cmd string) string {
		handlerCalled <- cmd
		cancel() // stop polling after first command
		return "test reply"
	}

	done := make(chan struct{})
	go func() {
		tg.PollCommands(ctx, handler)
		close(done)
	}()

	select {
	case cmd := <-handlerCalled:
		if cmd != "/alert" {
			t.Errorf("handler got command %q, want /alert", cmd)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timeout waiting for handler")
	}

	<-done

	// Verify sendMessage was called with the reply.
	for _, req := range srv.Requests {
		if req.Method == "POST" && req.Path == "/bottok/sendMessage" {
			var payload map[string]any
			json.Unmarshal(req.Body, &payload)
			if payload["text"] != "test reply" {
				t.Errorf("reply text = %v, want 'test reply'", payload["text"])
			}
			if _, hasParseMode := payload["parse_mode"]; hasParseMode {
				t.Error("reply should not have parse_mode (plain text)")
			}
			return
		}
	}
	t.Error("sendMessage was not called")
}

func TestPollCommandsIgnoresNonCommand(t *testing.T) {
	updatesBody := `{"ok":true,"result":[{"update_id":1,"message":{"text":"hello","chat":{"id":42}}}]}`

	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/bottok/getUpdates", 200, updatesBody).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	handlerCalled := false
	handler := func(cmd string) string {
		handlerCalled = true
		return ""
	}

	tg.PollCommands(ctx, handler)

	if handlerCalled {
		t.Error("handler should not be called for non-command messages")
	}
}

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"price: $100.50", "price: $100\\.50"},
		{"foo_bar", "foo\\_bar"},
		{"(test)", "\\(test\\)"},
		{"a > b", "a \\> b"},
		{"1+1=2", "1\\+1\\=2"},
	}
	for _, tc := range tests {
		got := escapeMarkdown(tc.input)
		if got != tc.want {
			t.Errorf("escapeMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
