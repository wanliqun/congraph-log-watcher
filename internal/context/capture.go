package contextbuf

import (
	"sync"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// CaptureConfig controls context collected around one alert candidate.
type CaptureConfig struct {
	After     int
	AfterWait time.Duration
	MaxBytes  int
}

// Capture contains sanitized context captured around a trigger entry.
type Capture struct {
	Before   []logentry.LogEntry
	Trigger  logentry.LogEntry
	After    []logentry.LogEntry
	TimedOut bool
}

// Entries returns context in chronological order.
func (c Capture) Entries() []logentry.LogEntry {
	entries := make([]logentry.LogEntry, 0, len(c.Before)+1+len(c.After))
	for _, entry := range c.Before {
		entries = append(entries, cloneEntry(entry))
	}
	entries = append(entries, cloneEntry(c.Trigger))
	for _, entry := range c.After {
		entries = append(entries, cloneEntry(entry))
	}
	return entries
}

// Lines returns bounded, chronological Raw lines. It prioritizes the nearest
// before-context and always retains the trigger, then uses remaining space for
// after-context. A single oversized trigger is safely truncated.
func (c Capture) Lines(maxBytes int) []string {
	if maxBytes <= 0 {
		return nil
	}

	trigger := lineFor(c.Trigger)
	if len(trigger) >= maxBytes {
		return []string{truncateUTF8(trigger, maxBytes)}
	}

	remaining := maxBytes - len(trigger)
	before := make([]string, 0, len(c.Before))
	for index := len(c.Before) - 1; index >= 0; index-- {
		line := lineFor(c.Before[index])
		if len(line)+1 > remaining {
			break
		}
		before = append(before, line)
		remaining -= len(line) + 1
	}
	for left, right := 0, len(before)-1; left < right; left, right = left+1, right-1 {
		before[left], before[right] = before[right], before[left]
	}

	lines := append(before, trigger)
	for _, entry := range c.After {
		line := lineFor(entry)
		if len(line)+1 > remaining {
			break
		}
		lines = append(lines, line)
		remaining -= len(line) + 1
	}
	return lines
}

// AfterCollector waits for the requested number of subsequent entries without
// blocking the producer. Call Add for entries after the trigger and consume
// the single result from Done.
type AfterCollector struct {
	mu       sync.Mutex
	capture  Capture
	after    int
	timer    *time.Timer
	done     chan Capture
	finished bool
}

func NewAfterCollector(before []logentry.LogEntry, trigger logentry.LogEntry, config CaptureConfig) *AfterCollector {
	collector := &AfterCollector{
		capture: Capture{
			Before:  cloneEntries(before),
			Trigger: cloneEntry(trigger),
		},
		after: config.After,
		done:  make(chan Capture, 1),
	}

	if config.After <= 0 {
		collector.finish(false)
		return collector
	}
	timer := time.AfterFunc(config.AfterWait, func() {
		collector.finish(true)
	})
	collector.mu.Lock()
	collector.timer = timer
	if collector.finished {
		timer.Stop()
	}
	collector.mu.Unlock()
	return collector
}

// Add supplies a log entry emitted after the trigger.
func (c *AfterCollector) Add(entry logentry.LogEntry) {
	if c == nil {
		return
	}

	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.capture.After = append(c.capture.After, cloneEntry(entry))
	shouldFinish := len(c.capture.After) >= c.after
	c.mu.Unlock()

	if shouldFinish {
		c.finish(false)
	}
}

// Done delivers exactly one immutable Capture result.
func (c *AfterCollector) Done() <-chan Capture {
	return c.done
}

// Cancel completes the collector without waiting for more entries.
func (c *AfterCollector) Cancel() {
	if c != nil {
		c.finish(true)
	}
}

func (c *AfterCollector) finish(timedOut bool) {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	if c.timer != nil {
		c.timer.Stop()
	}
	c.capture.TimedOut = timedOut
	result := Capture{
		Before:   cloneEntries(c.capture.Before),
		Trigger:  cloneEntry(c.capture.Trigger),
		After:    cloneEntries(c.capture.After),
		TimedOut: c.capture.TimedOut,
	}
	c.mu.Unlock()

	c.done <- result
	close(c.done)
}

func cloneEntries(entries []logentry.LogEntry) []logentry.LogEntry {
	cloned := make([]logentry.LogEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = cloneEntry(entry)
	}
	return cloned
}

func lineFor(entry logentry.LogEntry) string {
	if entry.Raw != "" {
		return entry.Raw
	}
	return entry.Message
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !isUTF8Boundary(value, maxBytes) {
		maxBytes--
	}
	return value[:maxBytes]
}

func isUTF8Boundary(value string, index int) bool {
	if index == len(value) {
		return true
	}
	return value[index]&0xc0 != 0x80
}
