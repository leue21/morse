package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"salert/config"
	"salert/notifier"
	"salert/plugin"
	"salert/scheduler"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
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

	buildPlugins(cfg, sched)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler := buildCommandHandler(sched)
	go tg.PollCommands(ctx, handler)

	slog.Info("salert starting")
	sched.Run(ctx)
	slog.Info("salert stopped")
}

func buildCommandHandler(sched *scheduler.Scheduler) notifier.CommandHandler {
	return func(command string) string {
		if command != "/alert" {
			return ""
		}
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
	}
}

func buildPlugins(cfg *config.Config, sched *scheduler.Scheduler) {
	if pc, ok := cfg.Plugins["btcprice"]; ok {
		p := plugin.NewBTCPrice(
			pc.GetFloat("above_usd"),
			pc.GetFloat("below_usd"),
			pc.GetFloat("change_percent"),
			pc.GetDuration("cooldown"),
		)
		sched.Add(p, pc.ParseInterval())
	}

	if pc, ok := cfg.Plugins["toogoodtogo"]; ok {
		p := plugin.NewTooGoodToGo(
			pc.GetString("email"),
			pc.GetStringSlice("store_ids"),
		)
		sched.Add(p, pc.ParseInterval())
	}
}
