//go:build smoke

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/leue21/morse/config"
	"github.com/leue21/morse/notifier"
)

// TestSmoke runs morse against the real Bot API, in a chat set aside for it,
// and deletes what it made on the way out.
//
// It exists for what a fake server cannot know: whether Telegram still accepts
// the markup morse writes, still words its refusals the way notifier.refusal
// reads them, and still answers with the fields morse reads back. Run it with
//
//	MORSE_SMOKE_CHAT_ID=-100… make smoke
//
// The chat has to be a group with topics turned on, with the bot an admin that
// may manage topics and delete messages. Credentials come from the usual
// config file or MORSE_BOT_TOKEN; MORSE_SMOKE_CHAT_ID replaces the chat, so a
// config pointed at a chat people read is never written to. Every message is
// sent silently.
func TestSmoke(t *testing.T) {
	chat := os.Getenv("MORSE_SMOKE_CHAT_ID")
	if chat == "" {
		t.Skip("set MORSE_SMOKE_CHAT_ID to a test group to run against the real API")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config", "morse", "config.yaml")
	t.Setenv("MORSE_CHAT_ID", chat)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	tg := client(cfg)
	ctx := context.Background()

	// Everything made is recorded as it is made, so a failure halfway still
	// cleans up what came before it.
	var messages, topics []int64
	t.Cleanup(func() {
		if err := tg.Delete(ctx, messages...); err != nil {
			t.Errorf("cleanup: %v — messages %v are still in the chat", err, messages)
		}
		for _, topic := range topics {
			if err := tg.DeleteTopic(ctx, topic); err != nil {
				t.Errorf("cleanup: %v — topic %d is still in the chat", err, topic)
			}
		}
	})

	run := func(cmd func(context.Context, string, []string, buf) error, args ...string) map[string]any {
		t.Helper()
		var out strings.Builder
		if err := cmd(ctx, configPath, append([]string{"--json"}, args...), &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
			t.Fatalf("%v printed %q: %v", args, out.String(), err)
		}
		return got
	}
	id := func(got map[string]any, field string) int64 {
		t.Helper()
		n, ok := got[field].(float64)
		if !ok || n == 0 {
			t.Fatalf("no %s in %v", field, got)
		}
		return int64(n)
	}
	sendCmd := func(ctx context.Context, cfg string, args []string, out buf) error {
		return cmdSend(ctx, cfg, args, out)
	}
	editCmd := func(ctx context.Context, cfg string, args []string, out buf) error {
		return cmdEdit(ctx, cfg, args, out)
	}
	topicCmd := func(ctx context.Context, cfg string, args []string, out buf) error {
		return cmdTopic(ctx, cfg, append([]string{"create"}, args...), out)
	}

	t.Run("send", func(t *testing.T) {
		messages = append(messages, id(run(sendCmd, "--silent", "morse smoke", "plain send"), "message_id"))
	})

	// The markup is the thing most likely to break: one tag Telegram stops
	// accepting and every formatted alert is rejected.
	t.Run("code and collapse, escaped", func(t *testing.T) {
		body := "if a < b && c > d {\n\tpanic(\"x\")\n}\n" + strings.Repeat("line\n", 30)
		messages = append(messages, id(run(sendCmd, "--silent", "--code", "--collapse", "morse smoke", body), "message_id"))
	})

	t.Run("tracked rewrite in place, unchanged, then edit", func(t *testing.T) {
		first := id(run(sendCmd, "--silent", "--track", "smoke", "--code", "morse smoke", "step 1"), "message_id")
		messages = append(messages, first)
		if again := id(run(sendCmd, "--silent", "--track", "smoke", "--code", "morse smoke", "step 2"), "message_id"); again != first {
			t.Errorf("rewrite went to %d, want %d", again, first)
		}
		// Telegram refuses an edit that changes nothing; morse has to read
		// that refusal as success, which depends on Telegram's wording.
		if again := id(run(sendCmd, "--silent", "--track", "smoke", "--code", "morse smoke", "step 2"), "message_id"); again != first {
			t.Errorf("unchanged rewrite went to %d, want %d", again, first)
		}
		if again := id(run(editCmd, "--track", "smoke", "--code", "morse smoke", "done"), "message_id"); again != first {
			t.Errorf("edit went to %d, want %d", again, first)
		}
	})

	t.Run("a deleted tracked message is replaced", func(t *testing.T) {
		first := id(run(sendCmd, "--silent", "--track", "gone", "morse smoke", "about to be deleted"), "message_id")
		if err := tg.Delete(ctx, first); err != nil {
			t.Fatal(err)
		}
		// Telegram's refusal for an edit of a deleted message is read as
		// ErrMessageGone; if its wording changed, this edit fails instead.
		second := id(run(sendCmd, "--silent", "--track", "gone", "morse smoke", "replacement"), "message_id")
		messages = append(messages, second)
		if second == first {
			t.Errorf("still message %d after deleting it", first)
		}
	})

	t.Run("topics", func(t *testing.T) {
		topic := id(run(topicCmd, "morse smoke"), "topic")
		topics = append(topics, topic)
		got := run(sendCmd, "--silent", "--topic", strconv.FormatInt(topic, 10), "morse smoke", "into a topic")
		messages = append(messages, id(got, "message_id"))
		if id(got, "topic") != topic {
			t.Errorf("sent to topic %v, want %d", got["topic"], topic)
		}

		err := cmdSend(ctx, configPath, []string{"--silent", "--topic", "999999", "morse smoke", "nowhere"}, &strings.Builder{})
		if !errors.Is(err, notifier.ErrNoSuchTopic) {
			t.Errorf("send to a missing topic: %v, want ErrNoSuchTopic", err)
		}
	})

	t.Run("receive list", func(t *testing.T) {
		if err := cmdReceive(ctx, configPath, []string{"list", "--json"}, &strings.Builder{}); err != nil {
			t.Error(err)
		}
	})
}

// buf is the output every command writes to, named short so the table of
// commands above stays readable.
type buf = *strings.Builder
