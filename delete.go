package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/leue21/morse/config"
	"github.com/leue21/morse/track"
)

// cmdDelete removes messages from the chat: by id, or the message a label
// stands for, which also forgets the label.
//
// It is the end of a tracked status's life — a job that finished cleanly can
// take its "running" line out of the chat rather than leave it there forever —
// and the way to clean up after anything else morse sent.
func cmdDelete(ctx context.Context, defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	configPath := fs.String("config", defaultConfig, "path to config file")
	label := fs.String("track", "", "delete the message this label stands for, and forget the label")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var ids []int64
	for _, arg := range fs.Args() {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("%q is not a message id", arg)
		}
		ids = append(ids, id)
	}
	switch {
	case *label != "" && len(ids) > 0:
		return errors.New("delete takes message ids or --track <label>, not both")
	case *label == "" && len(ids) == 0:
		return errors.New("delete needs a message id, or --track <label>")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	if *label != "" {
		rec, err := recall(*label)
		if err != nil {
			return err
		}
		// The id means a message in the chat it was sent to. In any other it
		// is some unrelated message, and deleting that is not something to
		// get wrong quietly.
		if rec.ChatID != cfg.Telegram.ChatID {
			return fmt.Errorf("label %s names a message in chat %d, but the config points at chat %d; nothing deleted",
				*label, rec.ChatID, cfg.Telegram.ChatID)
		}
		ids = []int64{rec.MessageID}
	}

	if err := client(cfg).Delete(ctx, ids...); err != nil {
		return explainRights(err, "delete messages")
	}
	// Forgotten only once the message is gone: a label whose delete failed
	// still names a message worth retrying, or rewriting.
	if *label != "" {
		dir, err := track.Dir()
		if err != nil {
			return err
		}
		return track.Remove(dir, *label)
	}
	return nil
}
