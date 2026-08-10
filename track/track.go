// Package track remembers which message a label stands for.
//
// morse sends one message and exits, so nothing of its own is around to recall
// what it said last. A caller that wants to keep one line in the chat up to
// date therefore has to carry the message id between runs itself — through a
// shell variable, a file of its own, a restart of the service. A label spares it
// that: morse writes the id down under a name the caller chooses, and looks it
// up again on the next run.
//
// This is a lookup table on the caller's behalf, not state morse acts on.
// Nothing here runs, watches, or expires; a stale label costs one failed edit,
// which the caller recovers from by sending again.
package track

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record is what morse knows about a labelled message: enough to edit it again,
// and enough for a person looking at the file to see what it stands for.
//
// It is morse's own record of what it sent, not a report from Telegram. A bot
// cannot ask the Bot API what a message currently says, whether it still exists,
// or whether anyone read it — so this says what morse last did, and no more.
type Record struct {
	Label     string    `json:"label"`
	MessageID int64     `json:"message_id"`
	ChatID    int64     `json:"chat_id"`
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrNoSuchLabel reports a label morse has never written down.
var ErrNoSuchLabel = errors.New("no such label")

// Dir is where labels live: $XDG_STATE_HOME/morse, else ~/.local/state/morse.
// State rather than config, because morse writes it and the user does not, and
// losing it costs a message's continuity rather than the setup.
func Dir() (string, error) {
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		return filepath.Join(state, "morse"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "morse"), nil
}

// Load reads what a label stands for.
func Load(dir, label string) (*Record, error) {
	path, err := pathFor(dir, label)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoSuchLabel, label)
	}
	if err != nil {
		return nil, fmt.Errorf("reading label %s: %w", label, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("reading label %s: %w", label, err)
	}
	return &rec, nil
}

// Save writes down what a label now stands for, replacing whatever it stood for
// before. The write goes through a temporary file in the same directory so a
// crash or a full disk leaves the previous record intact rather than a
// half-written one: a truncated file would strand the message it named, which is
// the one thing the label exists to prevent.
func Save(dir string, rec Record) error {
	path, err := pathFor(dir, rec.Label)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("writing label %s: %w", rec.Label, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".morse-*")
	if err != nil {
		return fmt.Errorf("writing label %s: %w", rec.Label, err)
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename below has succeeded
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("writing label %s: %w", rec.Label, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing label %s: %w", rec.Label, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing label %s: %w", rec.Label, err)
	}
	return nil
}

// pathFor names the file a label lives in — one file per label, so two callers
// tracking different things never write to the same file and need no lock
// between them.
//
// A label becomes a filename, so it may not be anything that would escape the
// directory or name something else: the label comes from a caller's command
// line, and "../../.ssh/config" is a path, not a name.
func pathFor(dir, label string) (string, error) {
	if label == "" {
		return "", errors.New("empty label")
	}
	// The allowlist below already refuses a separator, so what is left to rule
	// out by hand is a leading dot: "." and ".." are directories, and a dotfile
	// is hidden from the person looking for their labels.
	bad := strings.HasPrefix(label, ".")
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			bad = true
		}
	}
	if bad {
		return "", fmt.Errorf("label %q: use letters, digits, and - _ .", label)
	}
	return filepath.Join(dir, label+".json"), nil
}
