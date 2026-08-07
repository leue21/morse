package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromFile(t *testing.T) {
	cfg, err := Load(write(t, "telegram:\n  bot_token: \"tok123\"\n  chat_id: 999\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.BotToken != "tok123" || cfg.Telegram.ChatID != 999 {
		t.Errorf("got %+v", cfg.Telegram)
	}
}

// An agent or a container should be able to use morse with no config file at
// all: two variables is a much lower bar than installing and editing a file.
func TestLoadFromEnvironmentWithoutAFile(t *testing.T) {
	t.Setenv(BotTokenEnv, "envtok")
	t.Setenv(ChatIDEnv, "4242")

	cfg, err := Load("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("a missing file with the environment set must load: %v", err)
	}
	if cfg.Telegram.BotToken != "envtok" || cfg.Telegram.ChatID != 4242 {
		t.Errorf("got %+v", cfg.Telegram)
	}
}

func TestEnvironmentOverridesFile(t *testing.T) {
	path := write(t, "telegram:\n  bot_token: \"fromfile\"\n  chat_id: 1\n")
	t.Setenv(BotTokenEnv, "fromenv")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.BotToken != "fromenv" {
		t.Errorf("bot_token = %q, want the environment to win", cfg.Telegram.BotToken)
	}
	// Only what the environment sets is overridden.
	if cfg.Telegram.ChatID != 1 {
		t.Errorf("chat_id = %d, want the file's value kept", cfg.Telegram.ChatID)
	}
}

// The error has to say where to put the missing value, or the reader has to go
// looking for documentation that may not be to hand.
func TestLoadReportsWhereCredentialsGo(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no token", "telegram:\n  chat_id: 1\n", BotTokenEnv},
		{"no chat id", "telegram:\n  bot_token: \"t\"\n", ChatIDEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, tc.body))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %s", err, tc.want)
			}
		})
	}
}

func TestLoadRejectsANonNumericChatID(t *testing.T) {
	t.Setenv(ChatIDEnv, "not-a-number")
	if _, err := Load(write(t, "telegram:\n  bot_token: \"t\"\n  chat_id: 1\n")); err == nil {
		t.Error("want an error for an unparseable chat id")
	}
}
