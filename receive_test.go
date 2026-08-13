package main

import (
	"encoding/json"
	"flag"
	"io"
	"strings"
	"testing"
	"time"

	"morse/notifier"
)

func TestSummary(t *testing.T) {
	tests := []struct {
		name string
		msg  notifier.Message
		want string
	}{{
		// A line is what a fuzzy finder matches against, and a message pasted
		// out of a terminal is mostly newlines.
		name: "newlines collapse to one line",
		msg:  notifier.Message{Text: "panic: nil map\n\ngoroutine 1 [running]:"},
		want: "panic: nil map goroutine 1 [running]:",
	}, {
		name: "a file is named",
		msg:  notifier.Message{File: &notifier.File{Name: "report.pdf"}},
		want: "[report.pdf]",
	}, {
		name: "a caption keeps its file",
		msg:  notifier.Message{Text: "the logs", File: &notifier.File{Name: "run.log"}},
		want: "the logs  [run.log]",
	}, {
		// A photo, a sticker and a voice note have no filename of their own, so
		// the kind stands in — "[sticker]" says more than an empty pair of
		// brackets, and is what a person scanning the list matches against.
		name: "an unnamed file shows its kind",
		msg:  notifier.Message{File: &notifier.File{ID: "large", Kind: "sticker"}},
		want: "[sticker]",
	}, {
		name: "a file that is nothing in particular still shows as one",
		msg:  notifier.Message{File: &notifier.File{ID: "x"}},
		want: "[file]",
	}}

	for _, tt := range tests {
		if got := summary(tt.msg); got != tt.want {
			t.Errorf("%s: summary = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSummaryTruncatesALongMessage(t *testing.T) {
	got := summary(notifier.Message{Text: strings.Repeat("a", 200)})
	if len([]rune(got)) != 80 {
		t.Errorf("summary is %d runes, want 80", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("summary = %q, want the cut marked", got)
	}
}

func TestWriteTable(t *testing.T) {
	var out strings.Builder
	messages := []notifier.Message{
		{MessageID: 11, Date: time.Now(), From: "Ada", Text: "ssh-ed25519 AAAAC3Nz"},
		{MessageID: 10, Date: time.Now().Add(-30 * time.Hour), From: "Ada", File: &notifier.File{Name: "report.pdf"}},
	}
	if err := writeTable(&out, messages); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), out.String())
	}
	// The id comes first on every line, unpadded, so `cut -d' ' -f1` on
	// whatever a picker hands back is enough to act on it.
	for i, want := range []string{"11", "10"} {
		if id, _, _ := strings.Cut(lines[i], " "); id != want {
			t.Errorf("line %d starts %q, want the id %s first", i, id, want)
		}
	}
	if !strings.Contains(lines[1], "report.pdf") {
		t.Errorf("line 1 = %q, want the filename in it", lines[1])
	}
}

// An empty window is the normal state of a chat nobody has written to today,
// so it is reported rather than left as silence a caller has to interpret.
func TestWriteTableEmpty(t *testing.T) {
	var out strings.Builder
	if err := writeTable(&out, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "no messages") {
		t.Errorf("output = %q, want it to say the window is empty", out.String())
	}
}

func TestBacklogNoteOnlySuggestsALargerLimitWhenOneExists(t *testing.T) {
	if got := backlogNote(10); !strings.Contains(got, "Ask for more with --limit") {
		t.Errorf("note for a small window = %q, want it to offer a larger one", got)
	}
	if got := backlogNote(notifier.MaxWindow); strings.Contains(got, "Ask for more with --limit") {
		t.Errorf("note for the maximum window = %q, must not offer an impossible larger limit", got)
	}
}

// One object per line, so a picker, a `while read` loop and jq all take it
// without being told anything.
func TestWriteJSONLineIsOneLine(t *testing.T) {
	var out strings.Builder
	msg := notifier.Message{MessageID: 10, ChatID: 42, Date: time.Unix(1700000000, 0),
		Text: "ssh-ed25519 AAAAC3Nz", File: &notifier.File{ID: "f", Name: "report.pdf"}}
	if err := writeJSONLine(&out, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Count(out.String(), "\n") != 1 || !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("output = %q, want exactly one trailing newline", out.String())
	}
	var back notifier.Message
	if err := json.Unmarshal([]byte(out.String()), &back); err != nil {
		t.Fatalf("the line does not parse as JSON: %v", err)
	}
	if back.MessageID != 10 || back.File == nil || back.File.Name != "report.pdf" {
		t.Errorf("round trip = %+v", back)
	}
}

// A flag may come before or after the id: `get 481 --save ~/Downloads` is what
// people type, and dropping the flag there would print the text where a file
// was asked for.
func TestSplitMessageID(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantID    string
		wantFlags []string
	}{
		{"id alone", []string{"481"}, "481", []string{}},
		{"flag first", []string{"--save", "/tmp", "481"}, "481", []string{"--save", "/tmp"}},
		{"flag last", []string{"481", "--save", "/tmp"}, "481", []string{"--save", "/tmp"}},
		{"flags either side", []string{"--config", "m.yaml", "481", "--save", "/tmp"}, "481",
			[]string{"--config", "m.yaml", "--save", "/tmp"}},
		// A flag's value looks exactly like a positional argument, and is not
		// the id however much it resembles one.
		{"a joined value", []string{"--save=/tmp", "481"}, "481", []string{"--save=/tmp"}},
		{"single dash form", []string{"-save", "/tmp", "481"}, "481", []string{"-save", "/tmp"}},
		{"after a terminator", []string{"--save", "/tmp", "--", "481"}, "481", []string{"--save", "/tmp"}},
	}

	for _, tt := range tests {
		id, flags, err := splitMessageID(getFlags(), tt.args)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
			continue
		}
		if id != tt.wantID {
			t.Errorf("%s: id = %q, want %q", tt.name, id, tt.wantID)
		}
		if strings.Join(flags, " ") != strings.Join(tt.wantFlags, " ") {
			t.Errorf("%s: flags = %v, want %v", tt.name, flags, tt.wantFlags)
		}
	}

	for _, args := range [][]string{nil, {"--save", "/tmp"}, {"--save=/tmp"}, {"--save", "/tmp", "--"}} {
		if id, _, err := splitMessageID(getFlags(), args); err == nil {
			t.Errorf("splitMessageID(%v) = %q, want an error about the missing id", args, id)
		}
	}
}

// Which flags swallow the argument after them is the flag set's answer, not a
// list kept alongside it: a boolean added later must not make the id vanish
// into it.
func TestSplitMessageIDAsksTheFlagSet(t *testing.T) {
	fs := getFlags()
	fs.Bool("verbose", false, "a boolean, of the kind get does not have today")

	id, flags, err := splitMessageID(fs, []string{"--verbose", "481"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "481" {
		t.Errorf("id = %q, want 481 — a boolean takes no value, so the id follows it", id)
	}
	if strings.Join(flags, " ") != "--verbose" {
		t.Errorf("flags = %v, want [--verbose]", flags)
	}
}

// getFlags builds the flag set `receive get` parses with, so a test asks the
// same question the command does.
func getFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("receive get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("config", "", "path to config file")
	fs.String("save", "", "download the attachment into this directory")
	return fs
}

// Telegram withholds updates it has just delivered, so the poll that follows a
// `list` — which is exactly the poll `list | fzf | xargs get` makes — can come
// back without the message that was just on screen. Asking once is not enough.
func TestSearchWindowAsksTwice(t *testing.T) {
	windows := [][]notifier.Message{
		{{MessageID: 246, Text: "the oldest, which telegram always re-sends"}},
		{{MessageID: 246}, {MessageID: 249, Text: "Test 2"}},
	}
	var polls int
	poll := func() ([]notifier.Message, error) {
		w := windows[polls]
		polls++
		return w, nil
	}

	found, err := searchWindow(t.Context(), 249, poll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.Text != "Test 2" {
		t.Errorf("found %+v, want the message from the second poll", found)
	}
	if polls != 2 {
		t.Errorf("polled %d times, want 2", polls)
	}
}

// Twice, though, is enough: a message that is genuinely gone must be reported
// rather than polled for indefinitely.
func TestSearchWindowGivesUpAfterTheSecondAsk(t *testing.T) {
	var polls int
	poll := func() ([]notifier.Message, error) {
		polls++
		return []notifier.Message{{MessageID: 246}}, nil
	}

	_, err := searchWindow(t.Context(), 999, poll)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "no message 999") {
		t.Errorf("error = %q, want it to name the message", err)
	}
	if polls != 2 {
		t.Errorf("polled %d times, want 2", polls)
	}
}

// A message in the first answer costs no second poll, and so no pause.
func TestSearchWindowStopsOnTheFirstAnswer(t *testing.T) {
	var polls int
	poll := func() ([]notifier.Message, error) {
		polls++
		return []notifier.Message{{MessageID: 249}}, nil
	}

	if _, err := searchWindow(t.Context(), 249, poll); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if polls != 1 {
		t.Errorf("polled %d times, want 1", polls)
	}
}

func TestReceiveRejectsBadInvocations(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrStr string
	}{
		{"no subcommand", nil, "needs a subcommand"},
		{"unknown subcommand", []string{"fetch"}, "unknown subcommand"},
		{"get without an id", []string{"get"}, "needs a message id"},
		{"get with a word", []string{"get", "latest"}, "is not a message id"},
		{"get with two ids", []string{"get", "10", "11"}, "has nothing to be"},
		{"list with an argument", []string{"list", "10"}, "takes no arguments"},
		{"limit out of range", []string{"list", "--limit", "500"}, "--limit must be between"},
		{"limit of zero", []string{"list", "--limit", "0"}, "--limit must be between"},
	}

	for _, tt := range tests {
		// The config path names nothing, so anything that got as far as talking
		// to Telegram would fail differently and say so.
		err := cmdReceive(t.Context(), "/nonexistent/config.yaml", tt.args, &strings.Builder{})
		if err == nil {
			t.Errorf("%s: expected an error", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantErrStr) {
			t.Errorf("%s: error = %q, want it to mention %q", tt.name, err, tt.wantErrStr)
		}
	}
}
