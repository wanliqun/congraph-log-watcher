// Package contextbuf stores bounded, sanitized log context by container.
package contextbuf

import (
	"fmt"
	"sync"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// Buffer is a fixed-capacity, concurrency-safe ring buffer of log entries.
type Buffer struct {
	mu       sync.RWMutex
	entries  []logentry.LogEntry
	start    int
	length   int
	capacity int
}

func NewBuffer(capacity int) (*Buffer, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("context buffer capacity must be greater than zero")
	}
	return &Buffer{
		entries:  make([]logentry.LogEntry, capacity),
		capacity: capacity,
	}, nil
}

// Append adds a deep copy of entry and evicts the oldest entry when full.
func (b *Buffer) Append(entry logentry.LogEntry) {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	index := (b.start + b.length) % b.capacity
	if b.length == b.capacity {
		index = b.start
		b.start = (b.start + 1) % b.capacity
	} else {
		b.length++
	}
	b.entries[index] = cloneEntry(entry)
}

// Last returns up to limit entries in chronological order. A non-positive
// limit returns no entries.
func (b *Buffer) Last(limit int) []logentry.LogEntry {
	if b == nil || limit <= 0 {
		return nil
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if limit > b.length {
		limit = b.length
	}
	result := make([]logentry.LogEntry, limit)
	first := (b.start + b.length - limit) % b.capacity
	for index := range result {
		result[index] = cloneEntry(b.entries[(first+index)%b.capacity])
	}
	return result
}

func (b *Buffer) Len() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.length
}

func cloneEntry(entry logentry.LogEntry) logentry.LogEntry {
	cloned := entry
	if entry.Fields == nil {
		return cloned
	}

	cloned.Fields = make(map[string]string, len(entry.Fields))
	for key, value := range entry.Fields {
		cloned.Fields[key] = value
	}
	return cloned
}
