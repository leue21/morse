// Command morse sends a notification to Telegram.
//
// It sends one message and exits. Anything that wants to be told something — a
// script, a cron job, an agent, a person at a prompt — decides for itself when
// to speak and calls morse to do the speaking.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"morse/config"
	"morse/notifier"
	"morse/track"
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
		if err := cmdSend(ctx, defaultConfig, args[1:], os.Stdout); err != nil {
			fail(err)
		}
	case "edit":
		if err := cmdEdit(ctx, defaultConfig, args[1:], os.Stdout); err != nil {
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
  morse send --track <label> [--json] <title> [body]
                              remember the message under a label, or print its id
  morse edit <message_id> <title> [body]
                              rewrite a message already in the chat; never notifies
  morse edit --track <label> <title> [body]
                              the same, by label rather than by id
  morse capabilities [--json] what morse accepts, for a caller
  morse version
  morse help

Credentials come from ~/.config/morse/config.yaml, or from the environment:
  MORSE_BOT_TOKEN, MORSE_CHAT_ID
`)
}

// cmdSend parses the send flags and posts one message.
func cmdSend(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	configPath := fs.String("config", defaultConfig, "path to config file")
	silent := fs.Bool("silent", false, "deliver without a notification sound")
	file := fs.String("file", "", "upload this file, with the title and body as its caption")
	titleFlag := fs.String("title", "", "the title, instead of the first argument")
	bodyFlag := fs.String("body", "", "the body, instead of the remaining arguments")
	label := fs.String("track", "", "remember this message under a label, for a later edit")
	asJSON := fs.Bool("json", false, "print what was sent, including the message id, as JSON")
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

	var messageID int64
	if *file != "" {
		messageID, err = tg.SendDocument(ctx, *file, title, body, *silent)
	} else {
		messageID, err = tg.Send(ctx, title, body, *silent)
	}
	if err != nil {
		return err
	}
	// The message is delivered either way; what follows is bookkeeping for a
	// caller that asked for it, and its failure is reported without pretending
	// the message never went out.
	if *label != "" {
		if messageID == 0 {
			// Writing the label down anyway would point it at nothing, and the
			// failure would surface later as an edit that cannot be explained.
			return fmt.Errorf("message sent, but telegram did not report its id; --track %s not recorded", *label)
		}
		if err := remember(*label, messageID, cfg.Telegram.ChatID, title, body); err != nil {
			return err
		}
	}
	if *asJSON {
		return printJSON(out, messageID, cfg.Telegram.ChatID, *label)
	}
	return nil
}

// cmdEdit rewrites a message morse already sent.
//
// Telegram never notifies anyone about an edit — no sound, no banner, nothing
// on a lock screen — so this is how something long-running keeps one line in
// the chat current instead of adding a line every time it has news. There is
// deliberately no --silent to pass: an edit has no louder form to suppress.
func cmdEdit(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	configPath := fs.String("config", defaultConfig, "path to config file")
	titleFlag := fs.String("title", "", "the title, instead of the first argument")
	bodyFlag := fs.String("body", "", "the body, instead of the remaining arguments")
	label := fs.String("track", "", "the label the message was sent under")
	asJSON := fs.Bool("json", false, "print what was edited, including the message id, as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	messageID, rest, err := editTarget(fs.Args(), *label)
	if err != nil {
		return err
	}

	title, body, err := resolveMessage(rest, pipedStdin(os.Stdin), false,
		given(fs, "title", titleFlag), given(fs, "body", bodyFlag))
	if err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	tg := notifier.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID)

	err = tg.Edit(ctx, messageID, title, body)

	// A label names "the message that reports this thing", not one particular
	// message, so when that message is gone — deleted from the chat, or sent to
	// a chat the config no longer points at — the honest reading is to start a
	// new one and point the label at it. Otherwise deleting a single message
	// would silence the job behind it for good, and the message a reader is
	// meant to interact with is exactly the one that gets deleted. An explicit
	// id means *that* message, so there the failure stands.
	if errors.Is(err, notifier.ErrMessageGone) && *label != "" {
		messageID, err = tg.Send(ctx, title, body, true)
	}
	if err != nil {
		return err
	}
	if *label != "" && messageID != 0 {
		if err := remember(*label, messageID, cfg.Telegram.ChatID, title, body); err != nil {
			return err
		}
	}
	if *asJSON {
		return printJSON(out, messageID, cfg.Telegram.ChatID, *label)
	}
	return nil
}

// editTarget works out which message an edit is about, and what is left over to
// be the text.
//
// A label answers the question on its own, and then every argument is text. An
// explicit id can only be the first argument: the text that follows may be a
// number too — "42 files copied" is a perfectly good body — so there is no
// second place to look, and a first argument that is not a number is a mistake
// worth naming rather than a title to send.
func editTarget(args []string, label string) (messageID int64, rest []string, err error) {
	if label != "" {
		rec, err := recall(label)
		if err != nil {
			return 0, nil, err
		}
		return rec.MessageID, args, nil
	}
	if len(args) == 0 {
		return 0, nil, errors.New("edit needs a message id, or --track <label>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return 0, nil, fmt.Errorf("%q is not a message id; pass one from `morse send --json`, or name the message with --track", args[0])
	}
	return id, args[1:], nil
}

// remember writes down which message a label now stands for.
func remember(label string, messageID, chatID int64, title, body string) error {
	dir, err := track.Dir()
	if err != nil {
		return err
	}
	return track.Save(dir, track.Record{
		Label:     label,
		MessageID: messageID,
		ChatID:    chatID,
		Text:      strings.TrimSpace(title + "\n" + body),
		UpdatedAt: time.Now(),
	})
}

// recall looks a label up, and says what to do about a label that means nothing
// yet: the first report of a run is a send, and only the ones after it are
// edits.
func recall(label string) (*track.Record, error) {
	dir, err := track.Dir()
	if err != nil {
		return nil, err
	}
	rec, err := track.Load(dir, label)
	if errors.Is(err, track.ErrNoSuchLabel) {
		return nil, fmt.Errorf("%w — send it first: morse send --track %s ...", err, label)
	}
	return rec, err
}

// printJSON reports what was sent or edited, for a caller that would rather
// hold the id itself than have morse remember it.
func printJSON(out io.Writer, messageID, chatID int64, label string) error {
	result := struct {
		MessageID int64  `json:"message_id"`
		ChatID    int64  `json:"chat_id"`
		Track     string `json:"track,omitempty"`
	}{messageID, chatID, label}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
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
