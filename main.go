package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"salert/config"
	"salert/notifier"
	"salert/plugin"
	"salert/scheduler"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Error("failed to get home directory", "error", err)
		os.Exit(1)
	}
	defaultConfig := filepath.Join(home, ".config", "salert", "config.yaml")
	configPath := flag.String("config", defaultConfig, "path to config file")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	tg := notifier.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	sched := scheduler.New(tg)

	absConfig, _ := filepath.Abs(*configPath)
	dataDir := filepath.Dir(absConfig)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	plugins := buildPlugins(cfg, sched, dataDir)
	handler := buildCommandHandler(sched, plugins)
	go tg.PollCommands(ctx, handler)

	// Notify on Telegram if any plugin needs login.
	for _, p := range plugins {
		if ls, ok := p.(plugin.LoginStarter); ok && ls.NeedsLogin() {
			slog.Info("plugin needs login", "plugin", p.Name())
			tg.Send(p.Name(), "Not authenticated. Send /login to start.")
		}
	}

	slog.Info("salert starting")
	sched.Run(ctx)
	slog.Info("salert stopped")
}

func buildCommandHandler(sched *scheduler.Scheduler, plugins []plugin.Plugin) notifier.CommandHandler {
	// Find the first LoginStarter plugin (currently only TGTG).
	var loginPlugin plugin.LoginStarter
	for _, p := range plugins {
		if ls, ok := p.(plugin.LoginStarter); ok {
			loginPlugin = ls
			break
		}
	}

	return func(command string) string {
		switch {
		case command == "/alert":
			entries := sched.Entries()
			if len(entries) == 0 {
				return "No alerts configured."
			}
			var sb strings.Builder
			sb.WriteString("Configured alerts:\n\n")
			for _, e := range entries {
				sb.WriteString(fmt.Sprintf("%s (every %s)\n", e.Plugin.Name(), e.Interval))
				sb.WriteString(e.Plugin.Describe())
				sb.WriteString("\n")
			}
			return sb.String()

		case command == "/login":
			if loginPlugin == nil {
				return "No plugin requires login."
			}
			if err := loginPlugin.StartLogin(context.Background()); err != nil {
				return fmt.Sprintf("Login failed: %v", err)
			}
			return "Login email sent! Please check your inbox for a login code and reply here with: /pin <YOUR_CODE>\n\nNote: If you click the link in the email, make sure to do it on a device that DOES NOT have the TooGoodToGo app installed."

		case strings.HasPrefix(command, "/pin "):
			if loginPlugin == nil {
				return "No plugin requires login."
			}
			pin := strings.TrimSpace(strings.TrimPrefix(command, "/pin "))
			if pin == "" {
				return "Usage: /pin <code>"
			}
			if err := loginPlugin.SubmitPIN(context.Background(), pin); err != nil {
				return fmt.Sprintf("PIN failed: %v", err)
			}
			return "Authenticated successfully!"

		default:
			return ""
		}
	}
}

func buildPlugins(cfg *config.Config, sched *scheduler.Scheduler, dataDir string) []plugin.Plugin {
	var plugins []plugin.Plugin

	if pc, ok := cfg.Plugins["btcprice"]; ok {
		p := plugin.NewBTCPrice(
			pc.GetFloat("above_usd"),
			pc.GetFloat("below_usd"),
			pc.GetFloat("change_percent"),
			pc.GetDuration("cooldown"),
		)
		sched.Add(p, pc.ParseInterval())
		plugins = append(plugins, p)
	}

	if pc, ok := cfg.Plugins["toogoodtogo"]; ok {
		p := plugin.NewTooGoodToGo(
			pc.GetString("email"),
			dataDir,
		)
		sched.Add(p, pc.ParseInterval())
		plugins = append(plugins, p)
	}

	return plugins
}
