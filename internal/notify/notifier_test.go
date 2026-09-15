package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/processor"
)

type fakeNotifier struct {
	mu       sync.Mutex
	calls    int
	failures int
	block    chan struct{}
}

func (f *fakeNotifier) Notify(ctx context.Context, _ processor.Alert) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return errors.New("failed")
	}
	return nil
}
func (f *fakeNotifier) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestRetryAndQueuePressure(t *testing.T) {
	n := &fakeNotifier{failures: 2}
	w, err := NewWorker(Config{Notifier: n, QueueSize: 1, MaxAlertsPerMinute: 20, StormCooldown: time.Minute, RetryDelays: []time.Duration{0, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if !w.Submit(processor.Alert{RuleID: "a"}) {
		t.Fatal("Submit failed")
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.count() != 3 || w.Stats().Sent != 1 {
		t.Fatalf("calls=%d stats=%+v", n.count(), w.Stats())
	}
	block := make(chan struct{})
	full, _ := NewWorker(Config{Notifier: &fakeNotifier{block: block}, QueueSize: 1, MaxAlertsPerMinute: 20, StormCooldown: time.Minute, RetryDelays: []time.Duration{0}})
	full.Submit(processor.Alert{})
	full.Submit(processor.Alert{})
	if full.Submit(processor.Alert{}) {
		t.Fatal("full queue accepted alert")
	}
	close(block)
	_ = full.Close(context.Background())
}

func TestMultiNotifierRetriesOnlyFailedChannel(t *testing.T) {
	success := &fakeNotifier{}
	failsOnce := &fakeNotifier{failures: 1}
	w, err := NewWorker(Config{Notifier: MultiNotifier{success, failsOnce}, QueueSize: 1, MaxAlertsPerMinute: 20, StormCooldown: time.Minute, RetryDelays: []time.Duration{0}})
	if err != nil {
		t.Fatal(err)
	}
	if !w.Submit(processor.Alert{RuleID: "a"}) {
		t.Fatal("Submit failed")
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := success.count(); got != 1 {
		t.Fatalf("successful channel calls = %d, want 1", got)
	}
	if got := failsOnce.count(); got != 2 {
		t.Fatalf("failed channel calls = %d, want 2", got)
	}
}
func TestSafetyValveAndMarkdown(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	n := &fakeNotifier{}
	w, _ := NewWorker(Config{Notifier: n, QueueSize: 4, MaxAlertsPerMinute: 1, StormCooldown: time.Hour, RetryDelays: []time.Duration{0}, Clock: c})
	w.Submit(processor.Alert{RuleID: "one"})
	w.Submit(processor.Alert{RuleID: "two", ContainerName: "node"})
	time.Sleep(10 * time.Millisecond)
	_ = w.Close(context.Background())
	if w.Stats().StormAlerts != 1 {
		t.Fatalf("stats=%+v", w.Stats())
	}
	text := FormatMarkdown(processor.Alert{RuleID: "r", ContainerName: "n", Fingerprint: "f", Samples: []string{"bad"}})
	if text == "" {
		t.Fatal("empty markdown")
	}
}
