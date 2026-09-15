// Package notify delivers incident alerts asynchronously and independently of
// log consumption and checkpoint advancement.
package notify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/processor"
)

// Notifier is the project's stable external-notification boundary.
type Notifier interface {
	Notify(context.Context, processor.Alert) error
}

// Clock makes safety-valve behavior deterministic in tests.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Config controls notification isolation and global alert-storm protection.
type Config struct {
	Notifier           Notifier
	QueueSize          int
	MaxAlertsPerMinute int
	StormCooldown      time.Duration
	RetryDelays        []time.Duration
	Clock              Clock
}

// Stats is a snapshot of observable worker outcomes.
type Stats struct {
	Sent        uint64
	Failed      uint64
	Dropped     uint64
	StormAlerts uint64
}

// Worker accepts alerts without blocking and sends them on one isolated
// goroutine. It implements processor.AlertSink directly.
type Worker struct {
	notifier Notifier
	queue    chan processor.Alert
	retries  []time.Duration
	limit    int
	stormFor time.Duration
	clock    Clock

	mu      sync.Mutex
	closed  bool
	stats   Stats
	recent  []time.Time
	stormAt time.Time
	stop    context.CancelFunc
	done    chan struct{}
}

// NewWorker starts an asynchronous notification worker.
func NewWorker(config Config) (*Worker, error) {
	if config.Notifier == nil {
		return nil, fmt.Errorf("notifier must not be nil")
	}
	if config.QueueSize <= 0 {
		return nil, fmt.Errorf("notification queue size must be greater than zero")
	}
	if config.MaxAlertsPerMinute <= 0 {
		return nil, fmt.Errorf("max alerts per minute must be greater than zero")
	}
	if config.StormCooldown < 0 {
		return nil, fmt.Errorf("storm cooldown must not be negative")
	}
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	if len(config.RetryDelays) == 0 {
		config.RetryDelays = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}
	}
	for _, delay := range config.RetryDelays {
		if delay < 0 {
			return nil, fmt.Errorf("retry delay must not be negative")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &Worker{notifier: config.Notifier, queue: make(chan processor.Alert, config.QueueSize), retries: append([]time.Duration(nil), config.RetryDelays...), limit: config.MaxAlertsPerMinute, stormFor: config.StormCooldown, clock: config.Clock, stop: cancel, done: make(chan struct{})}
	go worker.run(ctx)
	return worker, nil
}

// Alert implements processor.AlertSink. Queue pressure drops the newest alert
// rather than blocking the detection pipeline.
func (w *Worker) Alert(alert processor.Alert) { _ = w.Submit(alert) }

// Submit returns false if the worker is closed or its bounded queue is full.
func (w *Worker) Submit(alert processor.Alert) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		w.stats.Dropped++
		return false
	}
	select {
	case w.queue <- alert:
		return true
	default:
		w.stats.Dropped++
		return false
	}
}

// Close drains queued alerts until ctx expires. On expiry it cancels in-flight
// retries and returns ctx.Err(); remaining alerts are counted as dropped.
func (w *Worker) Close(ctx context.Context) error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		w.stop()
		<-w.done
		return ctx.Err()
	}
}

func (w *Worker) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	for alert := range w.queue {
		if !w.allow(alert) {
			continue
		}
		if w.send(ctx, alert) {
			w.mu.Lock()
			w.stats.Sent++
			w.mu.Unlock()
		} else {
			w.mu.Lock()
			w.stats.Failed++
			w.mu.Unlock()
		}
	}
}

func (w *Worker) allow(alert processor.Alert) bool {
	now := w.clock.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	index := 0
	for _, at := range w.recent {
		if !at.Before(cutoff) {
			w.recent[index] = at
			index++
		}
	}
	w.recent = w.recent[:index]
	if len(w.recent) < w.limit {
		w.recent = append(w.recent, now)
		return true
	}
	w.stats.Dropped++
	if alert.RuleID == "alert-storm" || (w.stormFor > 0 && now.Before(w.stormAt.Add(w.stormFor))) {
		return false
	}
	w.stormAt = now
	w.stats.StormAlerts++
	go w.sendStorm(alert)
	return false
}

func (w *Worker) sendStorm(source processor.Alert) {
	storm := processor.Alert{RuleID: "alert-storm", Severity: "high", ContainerName: source.ContainerName, FirstSeen: w.clock.Now(), LastSeen: w.clock.Now(), Samples: []string{"Alert storm detected: normal alerts are rate limited."}}
	if w.send(context.Background(), storm) {
		w.mu.Lock()
		w.stats.Sent++
		w.mu.Unlock()
		return
	}
	w.mu.Lock()
	w.stats.Failed++
	w.mu.Unlock()
}

func (w *Worker) send(ctx context.Context, alert processor.Alert) bool {
	for attempt := 0; ; attempt++ {
		if err := w.notifier.Notify(ctx, alert); err == nil {
			return true
		}
		if attempt == len(w.retries) {
			return false
		}
		if !sleep(ctx, w.retries[attempt]) {
			return false
		}
	}
}

func sleep(ctx context.Context, delay time.Duration) bool {
	if delay == 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
