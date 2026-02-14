package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram TelegramConfig        `yaml:"telegram"`
	Plugins  map[string]PluginConf `yaml:"plugins"`
}

type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   int64  `yaml:"chat_id"`
}

// PluginConf holds arbitrary plugin config as raw YAML nodes.
// Each plugin extracts what it needs.
type PluginConf struct {
	Interval string `yaml:"interval"`
	raw      map[string]any
}

func (p *PluginConf) UnmarshalYAML(node *yaml.Node) error {
	// Decode into raw map for plugin-specific access
	if err := node.Decode(&p.raw); err != nil {
		return err
	}
	// Also extract interval directly
	if v, ok := p.raw["interval"]; ok {
		if s, ok := v.(string); ok {
			p.Interval = s
		}
	}
	return nil
}

func (p *PluginConf) GetString(key string) string {
	if v, ok := p.raw[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (p *PluginConf) GetFloat(key string) float64 {
	if v, ok := p.raw[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return 0
}

func (p *PluginConf) GetStringSlice(key string) []string {
	if v, ok := p.raw[key]; ok {
		if slice, ok := v.([]any); ok {
			result := make([]string, 0, len(slice))
			for _, item := range slice {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}

func (p *PluginConf) GetDuration(key string) time.Duration {
	s := p.GetString(key)
	if s == "" {
		return 0
	}
	d, _ := time.ParseDuration(s)
	return d
}

func (p *PluginConf) ParseInterval() time.Duration {
	if p.Interval == "" {
		return 5 * time.Minute // default
	}
	d, err := time.ParseDuration(p.Interval)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Telegram.BotToken == "" {
		return nil, fmt.Errorf("telegram.bot_token is required")
	}
	if cfg.Telegram.ChatID == 0 {
		return nil, fmt.Errorf("telegram.chat_id is required")
	}
	return &cfg, nil
}
