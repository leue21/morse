// Package config reads morse's only settings: where to send.
package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Environment variables override the file. An agent or a container can then use
// morse with no config file at all, which is the difference between "install
// and configure this" and "set two variables".
const (
	BotTokenEnv = "MORSE_BOT_TOKEN"
	ChatIDEnv   = "MORSE_CHAT_ID"
)

type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
}

type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   int64  `yaml:"chat_id"`
	// Topics names the chat's forum topics, so a caller can say --topic ci
	// rather than carry a number. The Bot API has no way for a bot to list a
	// chat's topics, so the names cannot be looked up and have to be written
	// down — here, once, rather than in every caller.
	Topics map[string]int64 `yaml:"topics"`
}

// Topic resolves what --topic was given: a topic id as it is, or a name from
// the config. A number is always an id, which is why a name may not be one.
func (t TelegramConfig) Topic(given string) (int64, error) {
	if id, err := strconv.ParseInt(given, 10, 64); err == nil {
		if id <= 0 {
			return 0, fmt.Errorf("--topic %d is not a topic id; it is the number at the end of a link to the topic", id)
		}
		return id, nil
	}
	// Case does not matter: it is a name a person typed into a config file
	// and again on a command line, and "CI" and "ci" are not two topics.
	for name, id := range t.Topics {
		if strings.EqualFold(name, given) {
			return id, nil
		}
	}
	if len(t.Topics) == 0 {
		return 0, fmt.Errorf("no topic named %q: the config names no topics; add them under telegram.topics, or pass the topic's id", given)
	}
	names := make([]string, 0, len(t.Topics))
	for name := range t.Topics {
		names = append(names, name)
	}
	slices.Sort(names)
	return 0, fmt.Errorf("no topic named %q; the config names %s", given, strings.Join(names, ", "))
}

// Load reads path if it exists, then applies any environment override. A
// missing file is not an error when the environment supplies both values.
func Load(path string) (*Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if token := os.Getenv(BotTokenEnv); token != "" {
		cfg.Telegram.BotToken = token
	}
	if raw := os.Getenv(ChatIDEnv); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ChatIDEnv, err)
		}
		cfg.Telegram.ChatID = id
	}

	if cfg.Telegram.BotToken == "" {
		return nil, fmt.Errorf("no bot token: set it in %s or %s", path, BotTokenEnv)
	}
	if cfg.Telegram.ChatID == 0 {
		return nil, fmt.Errorf("no chat id: set it in %s or %s", path, ChatIDEnv)
	}
	// Checked here rather than when a topic is used, so a mistake in the file
	// is reported by the first call rather than by the one that happens to
	// name it — which may be an alert that then never arrives.
	for name, id := range cfg.Telegram.Topics {
		if _, err := strconv.ParseInt(name, 10, 64); err == nil {
			return nil, fmt.Errorf("topic name %q is a number, which --topic would read as an id; give it a name", name)
		}
		if id <= 0 {
			return nil, fmt.Errorf("topic %q: %d is not a topic id; it is the number at the end of a link to the topic", name, id)
		}
	}
	return &cfg, nil
}
