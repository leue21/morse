package notifier

import (
	"encoding/json"
	"net/http"
	"testing"

	"morse/internal/testutil"
)

func TestSendSuccess(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok123/sendMessage", 200, `{"ok":true}`).
		Start()

	tg := NewTelegram("tok123", 42)
	tg.baseURL = srv.URL()

	err := tg.Send("Test Title", "Hello world", false)
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

	err := tg.Send("Title", "Msg", false)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestSendNetworkError(t *testing.T) {
	tg := NewTelegram("tok", 1)
	tg.baseURL = "http://127.0.0.1:1" // nothing listening

	err := tg.Send("Title", "Msg", false)
	if err == nil {
		t.Fatal("expected error for unreachable server")
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

func TestSendMapsSilenceToTheTelegramFlag(t *testing.T) {
	for _, silent := range []bool{true, false} {
		srv := testutil.NewFakeAPI(t).
			Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
			Start()
		tg := NewTelegram("tok", 42)
		tg.baseURL = srv.URL()

		if err := tg.Send("Title", "Body", silent); err != nil {
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
