package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Refused before the config is even read: these could never be a topic.
func TestTopicCreateRefusesANameTelegramWouldNot(t *testing.T) {
	for name, args := range map[string][]string{
		"no name":   {"create"},
		"blank":     {"create", "  "},
		"too long":  {"create", strings.Repeat("x", 129)},
		"no create": {},
		"other":     {"delete", "ci"},
	} {
		if err := cmdTopic(context.Background(), "/nonexistent", args, io.Discard); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The printed lines are meant to be pasted into the config, so they have to
// read back as the same name and id — including a name YAML would otherwise
// take apart.
func TestTopicHintIsYAMLThatReadsBack(t *testing.T) {
	for _, name := range []string{"ci", "Nightly backup", "ops: prod", `say "hi"`, "📦 releases"} {
		var out strings.Builder
		if err := writeTopicHint(&out, "config.yaml", name, 12); err != nil {
			t.Fatal(err)
		}
		_, snippet, ok := strings.Cut(out.String(), ":\n\n")
		if !ok {
			t.Fatalf("no snippet in %q", out.String())
		}
		var got struct {
			Telegram struct {
				Topics map[string]int64 `yaml:"topics"`
			} `yaml:"telegram"`
		}
		if err := yaml.Unmarshal([]byte(snippet), &got); err != nil {
			t.Fatalf("%q: %v\n%s", name, err, snippet)
		}
		if got.Telegram.Topics[name] != 12 {
			t.Errorf("%q read back as %v\n%s", name, got.Telegram.Topics, snippet)
		}
		// Pasted under an existing telegram: block indented two spaces, as
		// the example config is, the lines have to line up with its keys.
		_, topicLines, _ := strings.Cut(snippet, "telegram:\n")
		pasted := "telegram:\n  bot_token: tok\n  chat_id: -1001\n" + dedent(topicLines, "  ")
		if err := yaml.Unmarshal([]byte(pasted), &got); err != nil || got.Telegram.Topics[name] != 12 {
			t.Errorf("%q pasted under a config: (%v, %v)\n%s", name, got.Telegram.Topics, err, pasted)
		}
	}
}

// dedent strips the indentation writeTopicHint adds to set the snippet off in
// a terminal, giving back the lines as they would be pasted.
func dedent(text, prefix string) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, prefix)
	}
	return strings.Join(lines, "")
}
