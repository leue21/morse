package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"morse/config"
	"morse/notifier"
)

// cmdReceive reads the chat back: what the bot can currently see, and what one
// of those messages carried.
//
// This is the other half of a conversation morse could until now only talk into.
// It is still one call that exits — it does not poll, hold a cursor, or keep an
// archive — so what it can see is what Telegram is holding: up to a hundred
// updates from the oldest unconfirmed one, for about a day. There is no Bot API method that reads a chat's
// history, so no amount of state on this side would widen that window; storing
// what it sees would only mean morse owning a second copy of the chat.
//
// Picking one of the messages is a fuzzy finder's job, not morse's, so `list`
// prints the id first on every line and `get` takes an id. Anything from fzf to
// awk goes in between.
func cmdReceive(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("receive needs a subcommand: list, or get <message_id>")
	}
	switch args[0] {
	case "list":
		return cmdReceiveList(ctx, defaultConfig, args[1:], out)
	case "get":
		return cmdReceiveGet(ctx, defaultConfig, args[1:], out)
	default:
		return fmt.Errorf("receive: unknown subcommand %q; try list or get", args[0])
	}
}

// cmdReceiveList prints the window, newest first — the order a person scrolling
// for something they just sent themselves is looking in.
func cmdReceiveList(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("receive list", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	configPath := fs.String("config", defaultConfig, "path to config file")
	asJSON := fs.Bool("json", false, "print one JSON object per line")
	limit := fs.Int("limit", notifier.MaxWindow, "how many updates to ask for, starting with the oldest unconfirmed one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return fmt.Errorf("list takes no arguments, so %q has nothing to be", extra[0])
	}
	if *limit < 1 || *limit > notifier.MaxWindow {
		return fmt.Errorf("--limit must be between 1 and %d", notifier.MaxWindow)
	}

	tg, err := dial(*configPath)
	if err != nil {
		return err
	}
	messages, full, err := window(ctx, tg, *limit)
	if err != nil {
		return err
	}
	if full {
		// To stderr: a note about the window is not part of the window, and a
		// picker reading this on stdout would offer it as a message to choose.
		fmt.Fprintln(os.Stderr, backlogNote(*limit))
	}

	if *asJSON {
		// One object per line, not one array: a line is what a picker, a while
		// read loop and jq all take without being told anything.
		for _, m := range messages {
			if err := writeJSONLine(out, m); err != nil {
				return err
			}
		}
		return nil
	}
	return writeTable(out, messages)
}

// cmdReceiveGet hands over one message: its text on stdout, or what it carried
// into a directory.
//
// The text goes to stdout rather than anywhere cleverer, so that where it ends
// up is the caller's business — a clipboard, a file, another program. A
// clipboard in particular is not morse's to reach: which program holds one
// depends on the display server, and over ssh the clipboard worth writing to is
// on the other end of the connection entirely. A shell function composes that
// in one line; morse guessing at it would be several hundred lines of guessing
// wrong.
func cmdReceiveGet(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("receive get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfig, "path to config file")
	save := fs.String("save", "", "download the attachment into this directory")
	id, flags, err := splitMessageID(fs, args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return fmt.Errorf("get takes one message id, so %q has nothing to be", extra[0])
	}
	messageID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("%q is not a message id; pass one from `morse receive list`", id)
	}

	// The window is re-read rather than the caller's line being parsed back, so
	// that any picker at all can sit between list and get: whatever it prints,
	// only the id has to survive.
	tg, found, err := findMessage(ctx, *configPath, messageID)
	if err != nil {
		return err
	}

	if *save != "" {
		if found.File == nil {
			return fmt.Errorf("message %d carries no file", messageID)
		}
		path, err := tg.Download(ctx, found.File.ID, *save, found.File.Name)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, path)
		return nil
	}

	if found.Text == "" {
		if found.File != nil {
			return fmt.Errorf("message %d is a file with no text; fetch it with --save <dir>", messageID)
		}
		return fmt.Errorf("message %d has no text", messageID)
	}
	// The text and nothing else — no trailing newline of morse's own. What
	// comes out here is meant to be pasted, and a newline on the end of a
	// pasted command line is a keystroke that runs it. It also means text that
	// already ends in a newline does not grow a blank line each time it passes
	// through. The cost is a prompt that lands on the same line when this is
	// read by a person rather than a pipe, which is the cheaper of the two.
	_, err = io.WriteString(out, found.Text)
	return err
}

// settleDelay is how long to wait before asking a second time. Telegram briefly
// withholds updates it has just handed over: a second getUpdates within about
// half a second is answered with a truncated window — the queue's oldest update
// and nothing else — and everything is back the moment that passes. Nothing is
// consumed, and no offset was confirmed; the updates are simply not offered
// twice in the same breath.
const settleDelay = 700 * time.Millisecond

// findMessage locates one message in the window, and asks twice before giving
// up on it.
//
// Once is not enough because of the pause above: `list | fzf | xargs get` is
// two polls, and a caller who is not choosing by hand makes them back to back —
// so the message that list printed a moment ago is exactly the one the second
// poll leaves out. Waiting and asking again is the whole fix; the alternative,
// reading the record back from the caller's line, would tie the pipeline to one
// particular picker's output.
func findMessage(ctx context.Context, configPath string, messageID int64) (*notifier.Telegram, *notifier.Message, error) {
	tg, err := dial(configPath)
	if err != nil {
		return nil, nil, err
	}
	found, err := searchWindow(ctx, messageID, func() ([]notifier.Message, error) {
		messages, _, err := window(ctx, tg, notifier.MaxWindow)
		return messages, err
	})
	return tg, found, err
}

// searchWindow polls for the message, and polls again after a pause before
// concluding it is not there.
func searchWindow(ctx context.Context, messageID int64, poll func() ([]notifier.Message, error)) (*notifier.Message, error) {
	for attempt := 0; ; attempt++ {
		messages, err := poll()
		if err != nil {
			return nil, err
		}
		for i, m := range messages {
			if m.MessageID == messageID {
				return &messages[i], nil
			}
		}
		if attempt > 0 {
			// Which end it fell off cannot be known from here: the window is up
			// to MaxWindow updates from the oldest unconfirmed one, so a
			// message is missing either because Telegram no longer holds it or
			// because a backlog is standing in front of it. Saying "older than"
			// would name the likelier half and be wrong exactly when a caller
			// is confused enough to read the message carefully.
			return nil, fmt.Errorf("no message %d in the window of up to %d updates; telegram no longer holds it, or a backlog is hiding it — `morse receive list` shows what is reachable",
				messageID, notifier.MaxWindow)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(settleDelay):
		}
	}
}

// dial reads the credentials and builds the client to use them. Separate from
// the reading below because a client outlives one call: `get` may poll twice
// and then download, and the config does not change in between.
func dial(configPath string) (*notifier.Telegram, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	return notifier.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID), nil
}

// window reads what the bot can see, newest first, and says whether Telegram's
// answer filled the requested limit.
func window(ctx context.Context, tg *notifier.Telegram, limit int) (messages []notifier.Message, full bool, err error) {
	messages, full, err = tg.Updates(ctx, limit)
	if err != nil {
		// A webhook is the one refusal with a specific way out, and the 409 it
		// arrives as says nothing about which conflict it was. notifier reports
		// which refusal it was; deciding what to say about it is main's job.
		if errors.Is(err, notifier.ErrWebhookSet) {
			return nil, false, fmt.Errorf("%w — a bot delivers its updates to one place or the other, so polling is refused while a webhook is set. Delete it with deleteWebhook, or read the updates where they are already going", notifier.ErrWebhookSet)
		}
		return nil, false, err
	}
	// Newest first is a reading order, not a fact about the API, so notifier
	// hands them over as Telegram gave them and the reversal happens here.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, full, nil
}

// backlogNote is what to say when the answer filled the limit exactly.
//
// That is not proof of a backlog — a queue of exactly the size asked for looks
// the same from here, and with --limit 1 it usually is one — so the note says
// what was observed and what it may mean, rather than asserting there is more.
// It is worth saying either way: read without confirming an offset, the queue
// comes back from its oldest end, so anything queued behind what is on screen
// is newer than it, which is the opposite of what somebody looking for what
// they just sent expects.
func backlogNote(limit int) string {
	note := "note: the window came back full, and telegram hands updates over oldest first — so newer messages may be queued behind these."
	if limit < notifier.MaxWindow {
		return note + " Ask for more with --limit, or wait; a backlog clears itself within a day."
	}
	return note + " The API will not return more than 100 at once; a backlog clears itself within a day."
}

// writeTable prints the window for a person to read and for a picker to filter,
// with the id first on every line so `cut -d' ' -f1` is enough to act on a
// chosen one.
func writeTable(out io.Writer, messages []notifier.Message) error {
	if len(messages) == 0 {
		// An empty window is the normal state of a chat nobody has written to
		// today, not a failure — but it is also what a bot with privacy mode on
		// sees in a group, so it is worth saying which window turned up empty.
		_, err := fmt.Fprintln(out, "no messages in the window telegram is holding")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, m := range messages {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", m.MessageID, m.Date.Local().Format(stamp(m.Date)), m.From, summary(m))
	}
	return w.Flush()
}

// stamp shows the time for something from today and the date for anything
// older, which is how long the window is in practice.
func stamp(t time.Time) string {
	if time.Since(t) < 12*time.Hour {
		return "15:04"
	}
	return "Jan _2 15:04"
}

// summary is one line standing in for a message: what it says, and what it
// carried. Newlines become spaces because a line is the unit a fuzzy finder
// matches against, and a message pasted from a terminal is mostly newlines.
func summary(m notifier.Message) string {
	text := strings.Join(strings.Fields(m.Text), " ")
	if len([]rune(text)) > 80 {
		text = string([]rune(text)[:79]) + "…"
	}
	if m.File == nil {
		return text
	}
	// A document has a filename; a photo, a sticker or a voice note has only a
	// kind, which is still the useful thing to show — and the thing a person
	// scanning the list is fuzzy-matching against.
	name := m.File.Name
	if name == "" {
		name = m.File.Kind
	}
	if name == "" {
		name = "file"
	}
	if text == "" {
		return "[" + name + "]"
	}
	return text + "  [" + name + "]"
}

// writeJSONLine prints one message as a single line of JSON. Unlike writeJSON,
// which indents for a person reading one object in a terminal, this is meant to
// be read a line at a time.
func writeJSONLine(out io.Writer, m notifier.Message) error {
	return json.NewEncoder(out).Encode(m)
}

// splitMessageID lifts the message id out of get's arguments and hands back
// what is left for the flag package.
//
// Go's flag package stops at the first positional argument, so `get 481 --save
// ~/Downloads` would otherwise leave --save unparsed, and the text would print
// where a file was asked for. Rather than refuse the order people actually
// type, the id is taken out first and the flags parsed without it.
//
// This is safe here in a way it is not for send: get's one positional is a
// message id, so there is nothing ambiguous about which argument it is. A `--`
// ends the flags, in case a future flag makes the question interesting again.
//
// fs is the flag set the arguments are headed for, and is asked which flags
// take a value — `--save dir` puts the id one argument further along, and
// a boolean does not. Asking the flag set rather than keeping a list here means
// a flag added later is described once, where it is declared.
func splitMessageID(fs *flag.FlagSet, args []string) (id string, flags []string, err error) {
	missing := errors.New("get needs a message id, from `morse receive list`")
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			flags = append(flags, args[:i]...)
			rest := args[i+1:]
			if len(rest) == 0 {
				return "", nil, missing
			}
			return rest[0], append(flags, rest[1:]...), nil
		case strings.HasPrefix(arg, "-"):
			// --save=dir carries its value already; --save dir does not, and
			// then the next argument is that value rather than the id.
			if !strings.Contains(arg, "=") && takesValue(fs, strings.TrimLeft(arg, "-")) {
				i++
			}
		default:
			return arg, append(append(flags, args[:i]...), args[i+1:]...), nil
		}
	}
	return "", nil, missing
}

// takesValue reports whether a flag consumes the argument after it. The flag
// package marks a boolean by the method on its value, which is how it decides
// the same question when parsing; an unknown flag is left to fs.Parse to
// complain about, and is assumed to take a value so that its argument is not
// mistaken for the id.
func takesValue(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return true
	}
	boolFlag, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !boolFlag.IsBoolFlag()
}
