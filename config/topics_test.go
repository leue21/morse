package config

import (
	"strings"
	"testing"
)

const withTopics = "telegram:\n  bot_token: tok\n  chat_id: -1001\n  topics:\n    stream: 7\n    CI: 12\n"

func TestTopicResolvesANameOrAnID(t *testing.T) {
	t.Setenv(BotTokenEnv, "")
	t.Setenv(ChatIDEnv, "")
	cfg, err := Load(write(t, withTopics))
	if err != nil {
		t.Fatal(err)
	}
	for given, want := range map[string]int64{
		"stream": 7,
		"ci":     12, // a person's name for it, in whatever case they typed
		"Stream": 7,
		"42":     42, // a number is an id, named in the config or not
	} {
		got, err := cfg.Telegram.Topic(given)
		if err != nil || got != want {
			t.Errorf("Topic(%q) = (%d, %v), want %d", given, got, err, want)
		}
	}
}

// An unknown name has to fail rather than post to the chat itself: an alert
// meant for one topic landing in General is the mistake this is here to
// prevent. Listing the names that do exist is the fix for a typo.
func TestTopicRefusesAnUnknownName(t *testing.T) {
	cfg := TelegramConfig{Topics: map[string]int64{"stream": 7, "ci": 12}}
	_, err := cfg.Topic("backup")
	if err == nil || !strings.Contains(err.Error(), `"backup"`) || !strings.Contains(err.Error(), "ci, stream") {
		t.Fatalf("err = %v", err)
	}

	_, err = TelegramConfig{}.Topic("backup")
	if err == nil || !strings.Contains(err.Error(), "telegram.topics") {
		t.Fatalf("with no topics: err = %v", err)
	}
}

func TestTopicRefusesAnIDThatIsNotOne(t *testing.T) {
	for _, given := range []string{"0", "-3"} {
		if _, err := (TelegramConfig{}).Topic(given); err == nil {
			t.Errorf("Topic(%q) succeeded", given)
		}
	}
}

// A mistake in the file is reported by the first call, not by the one that
// happens to use the broken topic — which may be the alert that matters.
func TestLoadRefusesTopicsThatCannotWork(t *testing.T) {
	t.Setenv(BotTokenEnv, "")
	t.Setenv(ChatIDEnv, "")
	for name, body := range map[string]string{
		"a name that is a number": "telegram:\n  bot_token: tok\n  chat_id: 1\n  topics:\n    \"7\": 9\n",
		"an id that is not one":   "telegram:\n  bot_token: tok\n  chat_id: 1\n  topics:\n    ci: 0\n",
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: loaded without complaint", name)
		}
	}
}
