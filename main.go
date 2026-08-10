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
                              keep one message current: sent the first time,
                              rewritten every time after — call it either way
  morse edit <message_id> <title> [body]
                              rewrite a message already in the chat; never notifies
  morse edit --track <label> [--json] <title> [body]
                              the same, by label rather than by id
  morse capabilities [--json] what morse accepts, for a caller
  morse version
  morse help

Credentials come from ~/.config/morse/config.yaml, or from the environment:
  MORSE_BOT_TOKEN, MORSE_CHAT_ID
`)
}

// messageFlags are the flags every command that carries a message takes: where
// to read credentials, what to say, and what to do with the message afterwards.
// Declaring them once keeps send and edit from drifting apart, which they can
// only do in the direction of a caller finding a flag on one and not the other.
type messageFlags struct {
	fs         *flag.FlagSet
	configPath *string
	titleFlag  *string
	bodyFlag   *string
	label      *string
	asJSON     *bool
}

// newMessageFlags starts a command's flag set with the shared flags on it. The
// caller adds whatever else it takes before parsing.
func newMessageFlags(name, defaultConfig, trackUsage string) *messageFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	return &messageFlags{
		fs:         fs,
		configPath: fs.String("config", defaultConfig, "path to config file"),
		titleFlag:  fs.String("title", "", "the title, instead of the first argument"),
		bodyFlag:   fs.String("body", "", "the body, instead of the remaining arguments"),
		label:      fs.String("track", "", trackUsage),
		asJSON:     fs.Bool("json", false, "print the message id, and what it was sent as, as JSON"),
	}
}

// message works out what to say, from the arguments left after parsing. haveFile
// says the invocation already carries something worth delivering, so empty text
// is not an empty send.
func (f *messageFlags) message(args []string, haveFile bool) (title, body string, err error) {
	return resolveMessage(args, pipedStdin(os.Stdin), haveFile,
		given(f.fs, "title", f.titleFlag), given(f.fs, "body", f.bodyFlag))
}

// done finishes a command once the message is delivered: it records the label,
// if one was given, and prints the result, if that was asked for.
//
// The message has gone out by the time any of this runs, so a failure here is
// reported as what it is — the bookkeeping failed — rather than as a message
// that was never sent.
func (f *messageFlags) done(out io.Writer, messageID, chatID int64, title, body string) error {
	if *f.label != "" {
		if messageID == 0 {
			// Writing the label down anyway would point it at nothing, and the
			// failure would surface later as an edit that cannot be explained.
			return fmt.Errorf("message delivered, but telegram did not report its id; --track %s not recorded", *f.label)
		}
		if err := remember(*f.label, messageID, chatID, title, body); err != nil {
			return err
		}
	}
	if *f.asJSON {
		return writeJSON(out, struct {
			MessageID int64  `json:"message_id"`
			ChatID    int64  `json:"chat_id"`
			Track     string `json:"track,omitempty"`
		}{messageID, chatID, *f.label})
	}
	return nil
}

// cmdSend parses the send flags and posts one message.
//
// With --track it reports a thing rather than an event: the label stands for one
// line in the chat, and send puts it there — creating it the first time and
// rewriting it every time after. A caller then makes the same call whether it is
// starting up or carrying on, and does not have to know which, because the
// answer changes underneath it. A job that lost its state file, or restarts
// straight into its next update, would otherwise have to tell a first report
// from a later one to avoid failing.
func cmdSend(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	f := newMessageFlags("send", defaultConfig, "keep the message under this label current: sent the first time, rewritten after")
	silent := f.fs.Bool("silent", false, "deliver without a notification sound")
	file := f.fs.String("file", "", "upload this file, with the title and body as its caption")
	if err := f.fs.Parse(args); err != nil {
		return err
	}
	// A file is something to send in its own right, so a bare `morse send
	// --file report.pdf` is a complete instruction and needs no title.
	title, body, err := f.message(f.fs.Args(), *file != "")
	if err != nil {
		return err
	}
	cfg, err := config.Load(*f.configPath)
	if err != nil {
		return err
	}
	tg := notifier.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID)

	// An upload has no in-place form — replacing a document is a different API
	// call, and morse does not make it — so a tracked file send is a new message
	// each time, and the label follows it.
	var tracked int64
	if *f.label != "" && *file == "" {
		if tracked, err = trackedID(*f.label); err != nil {
			return err
		}
	}

	var messageID int64
	switch {
	case tracked != 0:
		messageID, err = editMessage(ctx, tg, tracked, title, body, func() (int64, error) {
			return tg.Send(ctx, title, body, *silent)
		})
	case *file != "":
		messageID, err = tg.SendDocument(ctx, *file, title, body, *silent)
	default:
		messageID, err = tg.Send(ctx, title, body, *silent)
	}
	if err != nil {
		return err
	}
	return f.done(out, messageID, cfg.Telegram.ChatID, title, body)
}

// editMessage rewrites a message and reports which message ended up carrying the
// text, resolving the two ways Telegram can refuse an edit.
//
// resend says what to do about a message that is gone — deleted from the chat,
// or belonging to a chat the config no longer points at. A caller that named the
// message by label means "the message that reports this thing", so it passes a
// way to start a new one; one that named an id means that message, and passes
// nil so the failure stands.
func editMessage(ctx context.Context, tg *notifier.Telegram, messageID int64, title, body string, resend func() (int64, error)) (int64, error) {
	switch err := tg.Edit(ctx, messageID, title, body); {
	case errors.Is(err, notifier.ErrNotModified):
		// Telegram refuses an edit that would change nothing. Something
		// reporting an unchanged state is doing exactly what it should, and the
		// chat already says what it was asked to say.
		return messageID, nil
	case errors.Is(err, notifier.ErrMessageGone) && resend != nil:
		return resend()
	case err != nil:
		return 0, err
	}
	return messageID, nil
}

// cmdEdit rewrites a message morse already sent.
//
// Telegram never notifies anyone about an edit — no sound, no banner, nothing
// on a lock screen — so this is how something long-running keeps one line in
// the chat current instead of adding a line every time it has news. There is
// deliberately no --silent to pass: an edit has no louder form to suppress.
func cmdEdit(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	f := newMessageFlags("edit", defaultConfig, "the label the message was sent under")
	if err := f.fs.Parse(args); err != nil {
		return err
	}

	messageID, rest, err := editTarget(f.fs.Args(), *f.label)
	if err != nil {
		return err
	}
	title, body, err := f.message(rest, false)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*f.configPath)
	if err != nil {
		return err
	}
	tg := notifier.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID)

	// A label names the message that reports a thing, not one particular
	// message, so a gone one is replaced and the label repointed: otherwise
	// deleting a single message would silence the job behind it for good, and
	// the message a reader is meant to interact with is exactly the one that
	// gets deleted. The replacement goes out silently, since the caller asked
	// to edit and an edit never notifies.
	var resend func() (int64, error)
	if *f.label != "" {
		resend = func() (int64, error) { return tg.Send(ctx, title, body, true) }
	}
	messageID, err = editMessage(ctx, tg, messageID, title, body, resend)
	if err != nil {
		return err
	}
	return f.done(out, messageID, cfg.Telegram.ChatID, title, body)
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

// recall looks a label up for a command that needs it to mean something
// already, and says what to do when it does not.
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

// trackedID looks a label up for a command that is happy either way, reporting
// 0 for a label morse has not seen. A label with no message yet is not a
// problem to report but a message to send: it is what every label looks like
// the first time, and what one looks like again after its state file is lost.
func trackedID(label string) (int64, error) {
	dir, err := track.Dir()
	if err != nil {
		return 0, err
	}
	rec, err := track.Load(dir, label)
	switch {
	case errors.Is(err, track.ErrNoSuchLabel):
		return 0, nil
	case err != nil:
		return 0, err
	}
	return rec.MessageID, nil
}

// writeJSON prints what a --json flag asked for, indented so a person reading
// it in a terminal is as well served as the jq that usually consumes it.
func writeJSON(out io.Writer, value any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
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
