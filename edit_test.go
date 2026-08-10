package main

import (
	"errors"
	"strings"
	"testing"

	"morse/track"
)

func TestEditTargetFromAnExplicitID(t *testing.T) {
	id, rest, err := editTarget([]string{"4242", "Backup", "42", "files copied"}, "")
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
	_, _, err := editTarget([]string{"Backup", "done"}, "")
	if err == nil || !strings.Contains(err.Error(), "is not a message id") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditTargetNeedsSomethingToEdit(t *testing.T) {
	_, _, err := editTarget(nil, "")
	if err == nil || !strings.Contains(err.Error(), "message id") {
		t.Fatalf("err = %v", err)
	}
}

// With a label morse holds the id, so every argument is text — including a
// first one that happens to be a number.
func TestEditTargetFromALabel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := remember("nightly-backup", 77, 42, "Nightly backup", "running"); err != nil {
		t.Fatal(err)
	}

	id, rest, err := editTarget([]string{"3", "files", "copied"}, "nightly-backup")
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
	_, _, err := editTarget([]string{"Title"}, "nightly-backup")
	if !errors.Is(err, track.ErrNoSuchLabel) {
		t.Fatalf("err = %v, want ErrNoSuchLabel", err)
	}
	if !strings.Contains(err.Error(), "morse send --track nightly-backup") {
		t.Errorf("err = %v, want it to name the way out", err)
	}
}

// What morse writes down has to be enough to edit the message again, and enough
// for a person reading the file to see what the label stands for.
func TestRememberRecordsTheMessage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := remember("nightly", 5, 42, "Backup", "done"); err != nil {
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
