package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"morse/internal/testutil"
)

// The id is the only thing that lets anything refer back to a message, and the
// Bot API never offers it again.
func TestSendReportsTheMessageID(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 200, `{"ok":true,"result":{"message_id":4242}}`).
		Start()

	tg := NewTelegram("tok", 1)
	tg.baseURL = srv.URL()

	id, err := tg.Send(context.Background(), "Title", "Body", false)
	if err != nil {
		t.Fatal(err)
	}
	if id != 4242 {
		t.Errorf("message id = %d, want 4242", id)
	}
}

// The message was delivered; a response morse could not read the id out of is
// no reason to report a failure to the caller.
func TestSendWithoutAMessageIDStillSucceeds(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
		Start()

	tg := NewTelegram("tok", 1)
	tg.baseURL = srv.URL()

	id, err := tg.Send(context.Background(), "Title", "Body", false)
	if err != nil || id != 0 {
		t.Fatalf("got (%d, %v), want (0, nil)", id, err)
	}
}

func TestEditRewritesTheNamedMessage(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/editMessageText", 200, `{"ok":true,"result":{"message_id":7}}`).
		Start()

	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	if err := tg.Edit(context.Background(), 7, "Nightly backup", "saved show.ts"); err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(srv.Requests[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	if got, ok := payload["message_id"].(float64); !ok || int64(got) != 7 {
		t.Errorf("message_id = %v, want 7", payload["message_id"])
	}
	if got, ok := payload["chat_id"].(float64); !ok || int64(got) != 42 {
		t.Errorf("chat_id = %v, want 42", payload["chat_id"])
	}
	if payload["text"] != "<b>Nightly backup</b>\nsaved show.ts" {
		t.Errorf("text = %v", payload["text"])
	}
	// An edit has no louder form, so there is nothing to suppress and nothing
	// to send: a disable_notification here would be a claim morse cannot make.
	if _, ok := payload["disable_notification"]; ok {
		t.Error("edit sent disable_notification, which the API does not take")
	}
}

// Whether an unchanged edit matters is the caller's to decide, so the notifier
// reports what Telegram objected to rather than ruling on it.
func TestEditOfUnchangedTextReportsWhy(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/editMessageText", http.StatusBadRequest,
			`{"ok":false,"description":"Bad Request: message is not modified"}`).
		Start()

	tg := NewTelegram("tok", 1)
	tg.baseURL = srv.URL()

	err := tg.Edit(context.Background(), 7, "Title", "same")
	if !errors.Is(err, ErrNotModified) {
		t.Fatalf("err = %v, want ErrNotModified", err)
	}
}

// "can't be edited" is a message that exists but is too old, or not this bot's
// to change — a real problem to report, not one to paper over by sending a
// replacement.
func TestEditOfAnUneditableMessageIsNotMistakenForAMissingOne(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/editMessageText", http.StatusBadRequest,
			`{"ok":false,"description":"Bad Request: message can't be edited"}`).
		Start()

	tg := NewTelegram("tok", 1)
	tg.baseURL = srv.URL()

	err := tg.Edit(context.Background(), 7, "Title", "Body")
	if err == nil || errors.Is(err, ErrMessageGone) || errors.Is(err, ErrNotModified) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
}

// Deleting the message must be recoverable, so "it is gone" has to be
// distinguishable from "that request was wrong".
func TestEditOfADeletedMessageReportsItIsGone(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/editMessageText", http.StatusBadRequest,
			`{"ok":false,"description":"Bad Request: message to edit not found"}`).
		Start()

	tg := NewTelegram("tok", 1)
	tg.baseURL = srv.URL()

	err := tg.Edit(context.Background(), 7, "Title", "Body")
	if !errors.Is(err, ErrMessageGone) {
		t.Fatalf("err = %v, want ErrMessageGone", err)
	}
}

func TestEditOtherFailuresAreNotMistakenForAMissingMessage(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/editMessageText", http.StatusBadRequest,
			`{"ok":false,"description":"Bad Request: can't parse entities"}`).
		Start()

	tg := NewTelegram("tok", 1)
	tg.baseURL = srv.URL()

	err := tg.Edit(context.Background(), 7, "Title", "Body")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrMessageGone) {
		t.Errorf("err = %v, want a plain failure, not ErrMessageGone", err)
	}
}

func TestEditNetworkErrorDoesNotLeakTheToken(t *testing.T) {
	tg := NewTelegram("s3cr3t-token", 1)
	tg.baseURL = "http://127.0.0.1:1" // nothing listening

	err := tg.Edit(context.Background(), 7, "Title", "Body")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "s3cr3t-token") {
		t.Errorf("error leaks the bot token: %v", err)
	}
}
