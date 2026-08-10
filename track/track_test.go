package track

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveThenLoad(t *testing.T) {
	dir := t.TempDir()
	rec := Record{Label: "nightly-backup", MessageID: 99, ChatID: 42,
		Text: "Nightly backup\nrunning", UpdatedAt: time.Now().Truncate(time.Second)}

	if err := Save(dir, rec); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, "nightly-backup")
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID != rec.MessageID || got.ChatID != rec.ChatID || got.Text != rec.Text {
		t.Errorf("got %+v, want %+v", *got, rec)
	}
	if !got.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Errorf("updated_at = %v, want %v", got.UpdatedAt, rec.UpdatedAt)
	}
}

// A label stands for whatever it last stood for; the point is that the next run
// finds the message this run sent.
func TestSaveReplacesTheEarlierRecord(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []int64{1, 2, 3} {
		if err := Save(dir, Record{Label: "job", MessageID: id}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Load(dir, "job")
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID != 3 {
		t.Errorf("message id = %d, want 3", got.MessageID)
	}
	// The temporary file the write went through must not be left behind to be
	// mistaken for a label of its own.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("%d files in the state directory, want 1: %v", len(entries), entries)
	}
}

// The first report of a run has nothing to edit, and the caller has to be able
// to tell that apart from a broken state directory.
func TestLoadOfAnUnknownLabel(t *testing.T) {
	_, err := Load(t.TempDir(), "never-sent")
	if !errors.Is(err, ErrNoSuchLabel) {
		t.Fatalf("err = %v, want ErrNoSuchLabel", err)
	}
}

// The label comes from a command line and becomes a filename, so anything that
// is a path rather than a name has to be refused before it is joined.
func TestLabelsThatAreNotNames(t *testing.T) {
	dir := t.TempDir()
	for _, label := range []string{
		"", "..", "../../.ssh/config", "a/b", `a\b`, ".hidden", "with space", "semi;colon",
	} {
		if err := Save(dir, Record{Label: label, MessageID: 1}); err == nil {
			t.Errorf("Save(%q) succeeded, want a refusal", label)
		}
		if _, err := Load(dir, label); err == nil || errors.Is(err, ErrNoSuchLabel) {
			t.Errorf("Load(%q) err = %v, want a refusal", label, err)
		}
	}
}

func TestLabelsThatAreNames(t *testing.T) {
	dir := t.TempDir()
	for _, label := range []string{"nightly-backup", "nightly-backup", "job_2", "v1.2"} {
		if err := Save(dir, Record{Label: label, MessageID: 1}); err != nil {
			t.Errorf("Save(%q): %v", label, err)
		}
	}
}

// Two labels are two files, so two callers tracking different things never
// write over each other and need no lock between them.
func TestLabelsDoNotShareAFile(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Record{Label: "one", MessageID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, Record{Label: "two", MessageID: 2}); err != nil {
		t.Fatal(err)
	}
	one, err := Load(dir, "one")
	if err != nil || one.MessageID != 1 {
		t.Fatalf("got (%+v, %v), want message id 1", one, err)
	}
}

func TestSaveCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "morse")
	if err := Save(dir, Record{Label: "job", MessageID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "job"); err != nil {
		t.Fatal(err)
	}
}

func TestDirFollowsXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/var/lib/xdg")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join("/var/lib/xdg", "morse") {
		t.Errorf("dir = %q", dir)
	}

	t.Setenv("XDG_STATE_HOME", "")
	dir, err = Dir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dir, filepath.Join(".local", "state", "morse")) {
		t.Errorf("dir = %q, want ~/.local/state/morse", dir)
	}
}
