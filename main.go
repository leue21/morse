// Command morse sends a notification to Telegram.
//
// It sends one message and exits. Anything that wants to be told something — a
// script, a cron job, an agent, a person at a prompt — decides for itself when
// to speak and calls morse to do the speaking.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "send":
		if err := cmdSend(ctx, defaultConfig, args[1:]); err != nil {
			fail(err)
		}
	case "capabilities":
		if err := cmdCapabilities(defaultConfig, args[1:], os.Stdout); err != nil {
			fail(err)
		}
	case "version", "-v", "--version":
		fmt.Println("morse", buildVersion())
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
  morse send [--silent] [--file path] <title> [body]
                              send a message; body may come from stdin
  morse send --title <t> --body <b>
                              the same, named rather than positional
  morse capabilities [--json] what morse accepts, for a caller
  morse version
  morse help

Credentials come from ~/.config/morse/config.yaml, or from the environment:
  MORSE_BOT_TOKEN, MORSE_CHAT_ID
`)
}

// cmdSend parses the send flags and posts one message.
func cmdSend(ctx context.Context, defaultConfig string, args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	configPath := fs.String("config", defaultConfig, "path to config file")
	silent := fs.Bool("silent", false, "deliver without a notification sound")
	file := fs.String("file", "", "upload this file, with the title and body as its caption")
	titleFlag := fs.String("title", "", "the title, instead of the first argument")
	bodyFlag := fs.String("body", "", "the body, instead of the remaining arguments")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// A file is something to send in its own right, so a bare `morse send
	// --file report.pdf` is a complete instruction and needs no title.
	title, body, err := resolveMessage(fs.Args(), pipedStdin(os.Stdin), *file != "",
		given(fs, "title", titleFlag), given(fs, "body", bodyFlag))
	if err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	tg := notifier.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	if *file != "" {
		return tg.SendDocument(ctx, *file, title, body, *silent)
	}
	return tg.Send(ctx, title, body, *silent)
}

// pipedStdin returns f only when something is actually piped into it. Reading a
// terminal would block until the user thought to press Ctrl-D, so `morse send
// "Build failed"` at a prompt would hang instead of sending the titled message
// it promises.
func pipedStdin(f *os.File) io.Reader {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return f
}

// given returns the flag's value only when it was actually passed, so an
// explicit --body "" can be told apart from a --body that was never mentioned.
// A caller assembling a command from variables passes empty strings routinely,
// and the two cases mean different things: one says "no body", the other leaves
// the arguments and stdin to answer.
func given(fs *flag.FlagSet, name string, value *string) *string {
	var set bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	if !set {
		return nil
	}
	return value
}

// resolveMessage works out what a send invocation should report. Text after the
// title becomes the body; with no body arguments it is read from stdin (when
// anything is piped in), so a caller can pass a log excerpt that way.
//
// titleFlag and bodyFlag carry --title and --body when they were passed, and
// take precedence: a script building a command from variables should not have
// to care whether a value starts with a dash or came out empty and shifted
// everything along. What a flag supplies, the positional arguments no longer
// have to mean — with --title given, everything left is body — and a positional
// with nothing left to be is an error rather than a silently dropped message.
//
// haveFile says the invocation already carries something worth delivering, so
// empty text is not an empty send.
func resolveMessage(args []string, stdin io.Reader, haveFile bool, titleFlag, bodyFlag *string) (title, body string, err error) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			// Go's flag package stops at the first positional argument, so a
			// trailing --silent would silently become part of the body: the
			// message would go out loud with the flag printed in it.
			return "", "", fmt.Errorf("flag %q must come before the title", arg)
		}
	}

	title = "morse"
	titled := false
	switch {
	case titleFlag != nil:
		title, titled = *titleFlag, *titleFlag != ""
	case len(args) > 0:
		title, args, titled = args[0], args[1:], true
	}
	if bodyFlag != nil && len(args) > 0 {
		return "", "", fmt.Errorf("--body given, so %q has nothing to be", args[0])
	}

	if bodyFlag != nil {
		body = *bodyFlag
	} else {
		body = strings.Join(args, " ")
	}
	if body == "" && bodyFlag == nil && stdin != nil {
		piped, err := io.ReadAll(stdin)
		if err != nil {
			return "", "", fmt.Errorf("read message: %w", err)
		}
		body = strings.TrimSpace(string(piped))
	}
	if body == "" {
		if haveFile {
			// The file speaks for itself; captioning it "(no details)" would
			// say less than saying nothing.
			if !titled {
				title = ""
			}
			return title, "", nil
		}
		if !titled {
			return "", "", errors.New("nothing to send")
		}
		// A caller with nothing to add still has something to report: a job
		// that died without logging is exactly the case worth hearing about.
		body = "(no details)"
	}
	return title, body, nil
}
