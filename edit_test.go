package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/leue21/morse/notifier"
	"github.com/leue21/morse/track"
)

func TestEditTargetFromAnExplicitID(t *testing.T) {
	id, _, rest, err := editTarget([]string{"4242", "Backup", "42", "files copied"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if id != 4242 {
		t.Errorf("message id = %d, want 4242", id)
	}
	// The number after the title is text, not another id to notice.
	if strings.Join(rest, " ") != "Backup 42 files copied" {
		t.Errorf("rest = %q", rest)
	}
}

// Silently treating it as a title would edit nothing and report success.
func TestEditTargetRejectsAnIDThatIsNotOne(t *testing.T) {
	_, _, _, err := editTarget([]string{"Backup", "done"}, "")
	if err == nil || !strings.Contains(err.Error(), "is not a message id") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditTargetNeedsSomethingToEdit(t *testing.T) {
	_, _, _, err := editTarget(nil, "")
	if err == nil || !strings.Contains(err.Error(), "message id") {
		t.Fatalf("err = %v", err)
	}
}

// With a label morse holds the id, so every argument is text — including a
// first one that happens to be a number.
func TestEditTargetFromALabel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := remember("nightly-backup", 77, 42, 0, "Nightly backup", "running"); err != nil {
		t.Fatal(err)
	}

	id, _, rest, err := editTarget([]string{"3", "files", "copied"}, "nightly-backup")
	if err != nil {
		t.Fatal(err)
	}
	if id != 77 {
		t.Errorf("message id = %d, want 77", id)
	}
	if strings.Join(rest, " ") != "3 files copied" {
		t.Errorf("rest = %q", rest)
	}
}

// The first report of a run has nothing to edit, and the error should say what
// to do instead of leaving the caller to guess.
func TestEditTargetFromALabelThatWasNeverSent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, _, _, err := editTarget([]string{"Title"}, "nightly-backup")
	if !errors.Is(err, track.ErrNoSuchLabel) {
		t.Fatalf("err = %v, want ErrNoSuchLabel", err)
	}
	if !strings.Contains(err.Error(), "morse send --track nightly-backup") {
		t.Errorf("err = %v, want it to name the way out", err)
	}
}

// `send --track` is the call a caller makes every time, so a label it has never
// seen is not a failure: it is the first send, which is what a fresh install and
// a lost state file both look like.
func TestTrackedIDOfAnUnknownLabelIsNotAnError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	id, err := trackedID("never-sent", 0)
	if err != nil || id != 0 {
		t.Fatalf("got (%d, %v), want (0, nil)", id, err)
	}
}

func TestTrackedIDFindsAKnownLabel(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := remember("nightly-backup", 77, 42, 0, "Nightly backup", "running"); err != nil {
		t.Fatal(err)
	}
	id, err := trackedID("nightly-backup", 0)
	if err != nil || id != 77 {
		t.Fatalf("got (%d, %v), want (77, nil)", id, err)
	}
}

// A label that is not a name has to stay an error rather than being read as
// "unknown, so send a new one" — that would write the message to a path the
// caller did not ask for.
func TestTrackedIDStillRefusesALabelThatIsNotAName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, err := trackedID("../../.ssh/config", 0); err == nil {
		t.Fatal("expected a refusal")
	}
}

// What morse writes down has to be enough to edit the message again, and enough
// for a person reading the file to see what the label stands for.
func TestRememberRecordsTheMessage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := remember("nightly", 5, 42, 0, "Backup", "done"); err != nil {
		t.Fatal(err)
	}

	rec, err := track.Load(dir+"/morse", "nightly")
	if err != nil {
		t.Fatal(err)
	}
	if rec.MessageID != 5 || rec.ChatID != 42 {
		t.Errorf("got %+v, want message 5 in chat 42", *rec)
	}
	if rec.Text != "Backup\ndone" {
		t.Errorf("text = %q", rec.Text)
	}
	if rec.UpdatedAt.IsZero() {
		t.Error("updated_at is zero")
	}
}

// A message cannot move between topics, so a label asked to report somewhere
// new starts a new message there instead of rewriting the old one where
// nobody is looking any more.
func TestTrackedIDInAnotherTopicStartsANewMessage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := remember("nightly-backup", 77, 42, 7, "Nightly backup", "running"); err != nil {
		t.Fatal(err)
	}
	if id, err := trackedID("nightly-backup", 7); err != nil || id != 77 {
		t.Fatalf("same topic: got (%d, %v), want (77, nil)", id, err)
	}
	if id, err := trackedID("nightly-backup", 9); err != nil || id != 0 {
		t.Fatalf("other topic: got (%d, %v), want (0, nil)", id, err)
	}
	if id, err := trackedID("nightly-backup", 0); err != nil || id != 0 {
		t.Fatalf("no topic: got (%d, %v), want (0, nil)", id, err)
	}
}

// `edit --track` takes no --topic, so the record is the only thing that can say
// where a replacement for a deleted message belongs.
func TestEditTargetCarriesTheLabelsTopic(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := remember("nightly-backup", 77, 42, 7, "Nightly backup", "running"); err != nil {
		t.Fatal(err)
	}
	id, topic, _, err := editTarget([]string{"done"}, "nightly-backup")
	if err != nil {
		t.Fatal(err)
	}
	if id != 77 || topic != 7 {
		t.Errorf("got message %d in topic %d, want 77 in 7", id, topic)
	}
}

func TestExplainTopicNamesTheTopic(t *testing.T) {
	err := explainTopic(fmt.Errorf("sending: %w", notifier.ErrNoSuchTopic), 999)
	if !errors.Is(err, notifier.ErrNoSuchTopic) || !strings.Contains(err.Error(), "topic 999") {
		t.Errorf("err = %v", err)
	}
	other := errors.New("network down")
	if explainTopic(other, 999) != other {
		t.Error("an unrelated error was rewritten")
	}
}
