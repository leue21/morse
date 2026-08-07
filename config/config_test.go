package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeTestConfig(t, `
telegram:
  bot_token: "tok123"
  chat_id: 999

plugins:
  diskspace:
    interval: "2m"
    min_free_percent: 8.0
    min_free_gb: 120.0
    cooldown: "15m"
    paths: ["/", "/srv"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Telegram.BotToken != "tok123" {
		t.Errorf("bot_token = %q, want %q", cfg.Telegram.BotToken, "tok123")
	}
	if cfg.Telegram.ChatID != 999 {
		t.Errorf("chat_id = %d, want 999", cfg.Telegram.ChatID)
	}

	disk, ok := cfg.Plugins["diskspace"]
	if !ok {
		t.Fatal("diskspace plugin config missing")
	}
	if disk.ParseInterval() != 2*time.Minute {
		t.Errorf("interval = %v, want 2m", disk.ParseInterval())
	}
	if disk.GetFloat("min_free_percent") != 8.0 {
		t.Errorf("min_free_percent = %f, want 8", disk.GetFloat("min_free_percent"))
	}
	if disk.GetFloat("min_free_gb") != 120.0 {
		t.Errorf("min_free_gb = %f, want 120", disk.GetFloat("min_free_gb"))
	}
	if disk.GetDuration("cooldown") != 15*time.Minute {
		t.Errorf("cooldown = %v, want 15m", disk.GetDuration("cooldown"))
	}
	paths := disk.GetStringSlice("paths")
	if len(paths) != 2 || paths[0] != "/" || paths[1] != "/srv" {
		t.Errorf("paths = %v, want [/ /srv]", paths)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadMissingBotToken(t *testing.T) {
	path := writeTestConfig(t, `
telegram:
  chat_id: 123
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing bot_token")
	}
}

func TestLoadMissingChatID(t *testing.T) {
	path := writeTestConfig(t, `
telegram:
  bot_token: "tok"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing chat_id")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTestConfig(t, `:::not valid yaml`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestPluginConfDefaults(t *testing.T) {
	path := writeTestConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 1

plugins:
  test:
    foo: "bar"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pc := cfg.Plugins["test"]

	// Missing interval defaults to 5m.
	if pc.ParseInterval() != 5*time.Minute {
		t.Errorf("default interval = %v, want 5m", pc.ParseInterval())
	}

	// Missing keys return zero values.
	if pc.GetString("missing") != "" {
		t.Error("expected empty string for missing key")
	}
	if pc.GetFloat("missing") != 0 {
		t.Error("expected 0 for missing float key")
	}
	if pc.GetStringSlice("missing") != nil {
		t.Error("expected nil for missing slice key")
	}
	if pc.GetDuration("missing") != 0 {
		t.Error("expected 0 for missing duration key")
	}
}

func TestParseIntervalInvalid(t *testing.T) {
	path := writeTestConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 1

plugins:
  test:
    interval: "notaduration"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pc := cfg.Plugins["test"]
	if pc.ParseInterval() != 5*time.Minute {
		t.Errorf("invalid interval should default to 5m, got %v", pc.ParseInterval())
	}
}
