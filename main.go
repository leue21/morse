// Command morse sends a notification to Telegram.
//
// It is a CLI and nothing else: no daemon, no schedule, no watching. Anything
// that wants to be told something — a systemd OnFailure handler, a cron job, an
// agent, a person at a prompt — decides for itself when to speak and calls
// morse to do the speaking.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"morse/config"
	"morse/notifier"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fail(err)
	}
	defaultConfig := filepath.Join(home, ".config", "morse", "config.yaml")

	args := os.Args[1:]
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "send":
		if err := cmdSend(defaultConfig, args[1:]); err != nil {
			fail(err)
		}
	case "capabilities":
		if err := cmdCapabilities(defaultConfig, args[1:], os.Stdout); err != nil {
			fail(err)
		}
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "morse: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "morse:", err)
	os.Exit(1)
}

func usage(out io.Writer) {
	fmt.Fprint(out, `morse — send a notification to Telegram

usage:
  morse send [--silent] <title> [body]   send a message; body may come from stdin
  morse capabilities [--json]            what morse accepts, for a caller
  morse help

Credentials come from ~/.config/morse/config.yaml, or from the environment:
  MORSE_BOT_TOKEN, MORSE_CHAT_ID
`)
}

// cmdSend parses the send flags and posts one message.
func cmdSend(defaultConfig string, args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	configPath := fs.String("config", defaultConfig, "path to config file")
	silent := fs.Bool("silent", false, "deliver without a notification sound")
	if err := fs.Parse(args); err != nil {
		return err
	}
	title, body, err := resolveMessage(fs.Args(), os.Stdin)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	return notifier.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID).Send(title, body, *silent)
}

// resolveMessage works out what a send invocation should report. Text after the
// title becomes the body; with no body arguments it is read from stdin, so a
// caller can pipe in a log excerpt.
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
		// A caller with nothing to add still has something to report: a unit
		// that died without logging is exactly the case worth hearing about.
		body = "(no details)"
	}
	return title, body, nil
}
