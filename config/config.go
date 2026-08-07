// Package config reads morse's only settings: where to send.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

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
	return &cfg, nil
}
