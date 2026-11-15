package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// topicNameLimit is Telegram's: a topic name is 1 to 128 characters.
const topicNameLimit = 128

// cmdTopic manages the chat's forum topics. There is one subcommand, because
// creating is the one thing about topics a bot can do that a person cannot do
// as easily by hand: a bot cannot list them, so what morse adds is a topic
// whose id it is sure of.
func cmdTopic(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("topic needs a subcommand: create <name>")
	}
	return cmdTopicCreate(ctx, defaultConfig, args[1:], out)
}

// cmdTopicCreate opens a topic and says how to use it by name.
//
// The name goes into the config by hand rather than by morse: the file holds
// the bot token and whatever comments its owner wrote, and rewriting it to add
// one line would put both at risk for a convenience the person can have by
// pasting the line printed here.
func cmdTopicCreate(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("topic create", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	configPath := fs.String("config", defaultConfig, "path to config file")
	asJSON := fs.Bool("json", false, "print the topic's id and name as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The name is everything left, so `morse topic create Nightly backup`
	// needs no quotes — a name is the one thing this command takes.
	name := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if name == "" {
		return fmt.Errorf("topic create needs a name")
	}
	if n := len([]rune(name)); n > topicNameLimit {
		return fmt.Errorf("a topic name is at most %d characters; this one is %d", topicNameLimit, n)
	}

	tg, err := dial(*configPath)
	if err != nil {
		return err
	}
	id, err := tg.CreateTopic(ctx, name)
	if err != nil {
		return explainRights(err, "manage topics")
	}

	if *asJSON {
		return writeJSON(out, struct {
			Topic int64  `json:"topic"`
			Name  string `json:"name"`
		}{id, name})
	}
	return writeTopicHint(out, *configPath, name, id)
}

// writeTopicHint reports the new topic, and the lines that let --topic take
// its name. The YAML is produced rather than formatted by hand, so a name with
// a colon, a quote or an emoji in it comes out as a key YAML reads back as
// the same name. It is indented two spaces a level, as the example config is:
// YAML refuses a block whose indentation does not line up with the keys around
// it, so lines indented any other way would break the file they are pasted
// into.
func writeTopicHint(out io.Writer, configPath, name string, id int64) error {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	err := enc.Encode(map[string]any{
		"telegram": map[string]any{"topics": map[string]int64{name: id}},
	})
	if err == nil {
		err = enc.Close()
	}
	if err != nil {
		return err
	}
	snippet := buf.String()
	_, err = fmt.Fprintf(out, "created topic %q: %d\n\nsend to it with --topic %d, or by name after adding it to %s:\n\n%s",
		name, id, id, configPath, indent(snippet, "  "))
	return err
}

func indent(text, prefix string) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "")
}
