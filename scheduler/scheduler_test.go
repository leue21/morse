package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"morse/plugin"
)

// mockPlugin records how many times Check was called and returns configured alerts/errors.
type mockPlugin struct {
	mu        sync.Mutex
	name      string
	alerts    []plugin.Alert
	err       error
	callCount int
}

func (m *mockPlugin) Name() string { return m.name }

func (m *mockPlugin) Describe() string { return "" }

func (m *mockPlugin) Check(ctx context.Context) ([]plugin.Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	return m.alerts, m.err
}

func (m *mockPlugin) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// mockNotifier records all messages sent.
type mockNotifier struct {
	mu       sync.Mutex
	messages []struct{ title, message string }
}

func (m *mockNotifier) Send(title, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, struct{ title, message string }{title, message})
	return nil
}

func (m *mockNotifier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

func TestSchedulerRunsImmediately(t *testing.T) {
	mp := &mockPlugin{name: "test"}
	mn := &mockNotifier{}

	sched := New(mn)
	sched.Add(mp, 1*time.Hour) // long interval so ticker won't fire

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	sched.Run(ctx)

	if mp.calls() < 1 {
		t.Errorf("expected at least 1 call, got %d", mp.calls())
	}
}

func TestSchedulerSendsAlerts(t *testing.T) {
	mp := &mockPlugin{
		name: "alerter",
		alerts: []plugin.Alert{
			{Title: "T1", Message: "M1"},
		},
	}
	mn := &mockNotifier{}

	sched := New(mn)
	sched.Add(mp, 1*time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	sched.Run(ctx)

	if mn.count() < 1 {
		t.Errorf("expected at least 1 notification, got %d", mn.count())
	}
}

func TestSchedulerTickerFires(t *testing.T) {
	mp := &mockPlugin{name: "ticker-test"}
	mn := &mockNotifier{}

	sched := New(mn)
	sched.Add(mp, 50*time.Millisecond) // short interval

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	sched.Run(ctx)

	// Should have at least 2 calls: immediate + at least one tick.
	if mp.calls() < 2 {
		t.Errorf("expected at least 2 calls (immediate + tick), got %d", mp.calls())
	}
}

func TestSchedulerHandlesErrors(t *testing.T) {
	mp := &mockPlugin{
		name: "error-test",
		err:  context.DeadlineExceeded,
	}
	mn := &mockNotifier{}

	sched := New(mn)
	sched.Add(mp, 1*time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Should not panic — errors are logged, not fatal.
	sched.Run(ctx)

	if mn.count() != 0 {
		t.Errorf("expected 0 notifications on error, got %d", mn.count())
	}
}

func TestSchedulerNoPlugins(t *testing.T) {
	mn := &mockNotifier{}
	sched := New(mn)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Should return immediately with no plugins.
	sched.Run(ctx)
}
