package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"morse/config"
	"morse/notifier"
	"morse/plugin"
	"morse/scheduler"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Error("failed to get home directory", "error", err)
		os.Exit(1)
	}
	defaultConfig := filepath.Join(home, ".config", "morse", "config.yaml")

	// `morse send <title> [body]` posts a single message and exits. systemd
	// OnFailure handlers need to report a unit that died, which the scheduled
	// plugins cannot do: by then this process may be the thing that died.
	if len(os.Args) > 1 && os.Args[1] == "send" {
		if err := cmdSend(defaultConfig, os.Args[2:]); err != nil {
			slog.Error("send failed", "error", err)
			os.Exit(1)
		}
		return
	}

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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	plugins := buildPlugins(cfg, sched)
	handler := buildCommandHandler(sched, plugins)
	go tg.PollCommands(ctx, handler)

	slog.Info("morse starting")
	sched.Run(ctx)
	slog.Info("morse stopped")
}

// buildCommandHandler answers the Telegram commands morse understands. There
// is only one: what is being watched, and what it currently sees.
func buildCommandHandler(sched *scheduler.Scheduler, _ []plugin.Plugin) notifier.CommandHandler {
	return func(command string) string {
		if command != "/alert" {
			return ""
		}
		entries := sched.Entries()
		if len(entries) == 0 {
			return "Nothing is being watched."
		}
		var sb strings.Builder
		sb.WriteString("Watching:\n\n")
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("%s (every %s)\n", e.Plugin.Name(), e.Interval))
			sb.WriteString(e.Plugin.Describe())
			sb.WriteString("\n")
		}
		return sb.String()
	}
}

func buildPlugins(cfg *config.Config, sched *scheduler.Scheduler) []plugin.Plugin {
	var plugins []plugin.Plugin

	if pc, ok := cfg.Plugins["diskspace"]; ok {
		p := plugin.NewDiskSpace(
			pc.GetStringSlice("paths"),
			pc.GetFloat("min_free_percent"),
			pc.GetFloat("min_free_gb"),
			pc.GetDuration("cooldown"),
		)
		sched.Add(p, pc.ParseInterval())
		plugins = append(plugins, p)
	}

	return plugins
}

// resolveMessage works out what a `morse send` invocation should report.
// Text after the title becomes the body; with no body arguments it is read from
// stdin, so a caller can pipe in a journal excerpt.
func resolveMessage(args []string, stdin io.Reader) (title, body string, err error) {
	title = "morse"
	titled := false
	if len(args) > 0 {
		title, args, titled = args[0], args[1:], true
	}
	body = strings.Join(args, " ")
	if body == "" {
		piped, err := io.ReadAll(stdin)
		if err != nil {
			return "", "", fmt.Errorf("read message: %w", err)
		}
		body = strings.TrimSpace(string(piped))
	}
	if body == "" {
		if !titled {
			return "", "", errors.New("nothing to send")
		}
		// A unit that dies without logging anything is still worth reporting —
		// arguably more so. The title carries which unit failed, so an empty
		// journal must not swallow the alert.
		body = "(no journal output)"
	}
	return title, body, nil
}

// cmdSend parses the flags of the send subcommand and posts one message.
func cmdSend(defaultConfig string, args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	configPath := fs.String("config", defaultConfig, "path to config file")
	severity := fs.String("severity", string(plugin.SeverityWarning),
		"info (arrives silently), warning, or critical")
	if err := fs.Parse(args); err != nil {
		return err
	}
	level, err := plugin.ParseSeverity(*severity)
	if err != nil {
		return err
	}
	return sendOnce(*configPath, fs.Args(), level)
}

// sendOnce posts one message using the configured Telegram credentials.
func sendOnce(configPath string, args []string, severity plugin.Severity) error {
	title, body, err := resolveMessage(args, os.Stdin)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return notifier.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID).Send(title, body, severity)
}
