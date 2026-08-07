package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"morse/notifier"
	"morse/plugin"
)

// Entry pairs a plugin with its polling interval.
type Entry struct {
	Plugin   plugin.Plugin
	Interval time.Duration
}

// Scheduler runs plugins on their configured intervals.
type Scheduler struct {
	entries  []Entry
	notifier notifier.Notifier
}

func New(notifier notifier.Notifier) *Scheduler {
	return &Scheduler{notifier: notifier}
}

func (s *Scheduler) Add(p plugin.Plugin, interval time.Duration) {
	s.entries = append(s.entries, Entry{Plugin: p, Interval: interval})
}

// Entries returns the registered plugin entries.
func (s *Scheduler) Entries() []Entry {
	return s.entries
}

// Run starts all plugin goroutines and blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup

	for _, entry := range s.entries {
		wg.Add(1)
		go func(e Entry) {
			defer wg.Done()
			s.runPlugin(ctx, e)
		}(entry)
	}

	wg.Wait()
}

func (s *Scheduler) runPlugin(ctx context.Context, e Entry) {
	log := slog.With("plugin", e.Plugin.Name())
	log.Info("starting plugin", "interval", e.Interval)

	// Run once immediately on startup.
	s.check(ctx, e.Plugin, log)

	ticker := time.NewTicker(e.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("plugin stopping")
			return
		case <-ticker.C:
			s.check(ctx, e.Plugin, log)
		}
	}
}

func (s *Scheduler) check(ctx context.Context, p plugin.Plugin, log *slog.Logger) {
	alerts, err := p.Check(ctx)
	if err != nil {
		log.Error("check failed", "error", err)
		return
	}

	for _, alert := range alerts {
		log.Info("sending alert", "title", alert.Title)
		if err := s.notifier.Send(alert.Title, alert.Message, alert.Severity); err != nil {
			log.Error("failed to send alert", "title", alert.Title, "error", err)
		}
	}
}
