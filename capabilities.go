package main

import (
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/leue21/morse/config"
)

// Capabilities is what morse accepts, in a form a caller can read without
// importing anything or reading this source. A script that has to learn the
// interface by reading the code is coupled to it; one that can ask is not.
type Capabilities struct {
	Name      string          `json:"name"`
	Version   string          `json:"version"`
	Config    string          `json:"config"`
	ConfigErr string          `json:"config_error,omitempty"`
	Delivery  Delivery        `json:"delivery"`
	Send      SendContract    `json:"send"`
	Edit      EditContract    `json:"edit"`
	Topic     TopicContract   `json:"topic"`
	Delete    DeleteContract  `json:"delete"`
	Receive   ReceiveContract `json:"receive"`
}

type Delivery struct {
	Channel    string `json:"channel"`
	Configured bool   `json:"configured"`
	// Env names the variables that supply credentials without a config file.
	Env []string `json:"env"`
}

// SendContract describes `morse send` precisely enough to call it blind.
type SendContract struct {
	Usage     string `json:"usage"`
	Named     string `json:"named"`
	Silent    string `json:"silent"`
	BodyStdin bool   `json:"body_from_stdin"`
	File      string `json:"file"`
	Track     string `json:"track"`
	Topic     string `json:"topic"`
	Format    string `json:"format"`
	Preview   string `json:"no_preview"`
	Photo     string `json:"photo"`
	Reply     string `json:"reply_to"`
	Pin       string `json:"pin"`
	RateLimit string `json:"rate_limit"`
	JSON      string `json:"json"`
}

// DeleteContract describes `morse delete`.
type DeleteContract struct {
	Usage string `json:"usage"`
	Track string `json:"track"`
}

// TopicContract describes `morse topic`, the one thing about a chat's topics
// a bot can do that a person cannot do as easily: create one and be sure of
// its id.
type TopicContract struct {
	Usage  string `json:"usage"`
	Rights string `json:"rights"`
	Config string `json:"config"`
}

// EditContract describes `morse edit`, whose whole point is what it does not
// do: an edit never notifies anyone, so a caller reporting the state of
// something long-running can say it as often as it likes.
type EditContract struct {
	Usage  string `json:"usage"`
	Track  string `json:"track"`
	JSON   string `json:"json"`
	Silent string `json:"silent"`
	State  string `json:"state"`
}

// ReceiveContract describes `morse receive`, whose limits are the point: a bot
// cannot read a chat's history, so what a caller can reach is a window, and one
// that finds that out by getting less than it expected has learned it the
// expensive way.
type ReceiveContract struct {
	Usage  string `json:"usage"`
	Window string `json:"window"`
	JSON   string `json:"json"`
	Text   string `json:"text"`
	Save   string `json:"save"`
	Pick   string `json:"pick"`
}

// cmdCapabilities reports the interface. It answers even when the config is
// missing or invalid: "what is this and how would I call it" is a fair question
// to ask of an installation that is not set up yet, and refusing would make the
// command useless exactly when it is most wanted.
func cmdCapabilities(defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	configPath := fs.String("config", defaultConfig, "path to config file")
	asJSON := fs.Bool("json", false, "emit the capabilities as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	caps := Capabilities{
		Name:    "morse",
		Version: buildVersion(),
		Config:  *configPath,
		Delivery: Delivery{
			Channel: "telegram",
			Env:     []string{config.BotTokenEnv, config.ChatIDEnv},
		},
		Send: SendContract{
			Usage:     "morse send [--silent] [--code] [--collapse] [--no-preview] [--topic id|name] [--reply-to id|label] [--pin] [--file path | --photo path] <title> [body]",
			Named:     "--title and --body take the same two values by name, and win over the arguments; safest for a caller building a command from variables",
			Silent:    "--silent delivers without a notification sound; the message still arrives",
			BodyStdin: true,
			File:      "--file uploads that file, with the title and body as its caption; at most 50 MB",
			Track:     "--track <label> keeps one message current under that label: sent the first time, rewritten in place every time after, so a caller makes the same call whether or not it has reported before. Rewrites never notify",
			Topic:     "--topic <id|name> posts into that forum topic of the chat (telegram's message_thread_id), so each sender keeps its own thread; a name is looked up, in any case, under telegram.topics in the config, and an unknown one is refused rather than sent to General, which takes no --topic. A --track label remembers its topic, and a different --topic starts a new message there",
			Format:    "--code shows the body as a monospace code block, for logs and stack traces; --collapse folds the body behind \"show more\". Either, both, or neither; edit takes them too, and a rewrite without them drops the formatting",
			Preview:   "--no-preview stops the first URL becoming a preview card; a rewrite rebuilds the preview, so edit takes it too",
			Photo:     "--photo uploads an image shown in the chat rather than as a file; telegram re-encodes it and takes at most 10 MB. Not with --file",
			Reply:     "--reply-to <id|label> sends the message as a reply to an earlier one, named by id or by --track label; a number is always an id. A reply to a message that is gone is sent anyway, as a plain message",
			Pin:       "--pin pins the new message without a notification; a tracked send pins only when its message is new, not on every rewrite. A pin that fails leaves the message delivered and recorded, and exits 1",
			RateLimit: "when telegram answers 429, morse waits the retry_after it names, up to 60 seconds, and sends once more",
			JSON:      "--json prints the message id as JSON, for a caller that would rather hold the id itself than use a label",
		},
		Delete: DeleteContract{
			Usage: "morse delete <message_id>...  |  morse delete --track <label>",
			Track: "--track deletes the message the label stands for and forgets the label, so the next send --track starts a new message; a label from another chat is refused. A bot may delete its own messages for 48 hours",
		},
		Topic: TopicContract{
			Usage:  "morse topic create [--json] <name>",
			Rights: "the chat must be a group with topics turned on, and the bot an admin with the right to manage topics",
			Config: "prints the new topic's id and the YAML to add under telegram.topics so --topic can take its name; morse never edits the config itself",
		},
		Edit: EditContract{
			Usage:  "morse edit [--code] [--collapse] [--no-preview] <message_id> <title> [body]  |  morse edit --track <label> <title> [body]",
			Track:  "--track <label> names the message morse remembered under that label, and fails if there is none; `morse send --track <label>` sends or rewrites without caring which, and is what a caller reporting repeatedly should use",
			JSON:   "--json prints the id of the message that now carries the text, which differs from the one edited when a gone message was replaced",
			Silent: "an edit never notifies anyone — that is the API's behaviour, not a flag — so there is no --silent to pass and nothing arrives on a lock screen",
			State:  "labels live in $XDG_STATE_HOME/morse (else ~/.local/state/morse); they record what morse last sent, not anything Telegram reports back",
		},
		Receive: ReceiveContract{
			Usage:  "morse receive list [--json] [--limit n]  |  morse receive get <message_id> [--save dir]",
			Window: "what the bot can see is up to 100 of the updates telegram still holds, roughly a day's worth; there is no Bot API method that reads a chat's history, and morse keeps no archive of its own. Reading never consumes: morse sends no offset at all, since a positive one deletes every update below it and a negative one forgets everything before the window — both for every reader of the bot, not just morse. The cost is that a backlog longer than the window comes back oldest first, hiding newer messages behind it; list says so on stderr when it happens. morse also never sends allowed_updates, which telegram would remember and apply to every other client polling this bot",
			JSON:   "--json prints one JSON object per line — message_id, chat_id, topic, date, from, text, file, pinned — for jq or a picker. An edited message appears once, with its current text",
			Text:   "get writes the message's text to stdout exactly, with no trailing newline of its own, so where it ends up is the caller's business; a clipboard is one pipe away and is not something morse reaches for you",
			Save:   "--save <dir> downloads what the message carried into that directory and prints the path; a bot may download at most 20 MB, and a filename already taken in that directory is refused rather than overwritten",
			Pick:   "choosing a message is a fuzzy finder's job: `list` prints the id first on every line, so `morse receive list | fzf | cut -d' ' -f1 | xargs morse receive get` is the whole integration",
		},
	}
	if _, err := config.Load(*configPath); err != nil {
		caps.ConfigErr = err.Error()
	} else {
		caps.Delivery.Configured = true
	}

	if *asJSON {
		return writeJSON(out, caps)
	}

	state := "not configured"
	if caps.Delivery.Configured {
		state = "configured"
	}
	fmt.Fprintf(out, "%s %s\nconfig    %s\ndelivery  %s (%s)\n",
		caps.Name, caps.Version, caps.Config, caps.Delivery.Channel, state)
	if caps.ConfigErr != "" {
		fmt.Fprintf(out, "          %s\n", caps.ConfigErr)
	}
	fmt.Fprintf(out, "env       %s\n", strings.Join(caps.Delivery.Env, " "))

	// A command is a heading and its lines, so it is written that way: adding
	// one line to a contract is adding one line here, rather than a format verb
	// that has to stay in step with an argument three lines further along.
	for _, command := range []struct {
		name  string
		lines []string
	}{
		{"send", []string{caps.Send.Usage, caps.Send.Named, caps.Send.Silent, caps.Send.File,
			caps.Send.Track, caps.Send.Topic, caps.Send.Format, caps.Send.Preview, caps.Send.Photo,
			caps.Send.Reply, caps.Send.Pin, caps.Send.RateLimit, caps.Send.JSON,
			"the body is read from stdin when no body argument is given"}},
		{"delete", []string{caps.Delete.Usage, caps.Delete.Track}},
		{"topic", []string{caps.Topic.Usage, caps.Topic.Rights, caps.Topic.Config}},
		{"edit", []string{caps.Edit.Usage, caps.Edit.Track, caps.Edit.JSON, caps.Edit.Silent, caps.Edit.State}},
		{"receive", []string{caps.Receive.Usage, caps.Receive.Window, caps.Receive.JSON,
			caps.Receive.Text, caps.Receive.Save, caps.Receive.Pick}},
	} {
		fmt.Fprintf(out, "\n%s\n", command.name)
		for _, line := range command.lines {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
	return nil
}

// version is the release this binary claims to be, set at link time with
//
//	-ldflags "-X main.version=v0.1.0"
//
// A packaged build has no git metadata to read — a package manager compiles an
// unpacked tarball — so without this it could only report "devel", which is no
// answer to "which morse is installed?". A build from a checkout leaves it
// empty and falls back to the revision below.
var version string

// buildVersion reports what this binary was built from: the stamped release if
// there is one, otherwise the revision Go records automatically for a build from
// a git checkout, so a development build needs nothing kept in step by hand,
// and failing that the module version `go install` fetched.
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			if setting.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	if revision == "" {
		// `go install github.com/leue21/morse@v0.5.0` builds from the module
		// cache, which has no git metadata, but Go records which version of the
		// module it fetched. A build from a checkout reports "(devel)" there.
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		return "devel"
	}
	return revision[:min(len(revision), 12)] + modified
}
