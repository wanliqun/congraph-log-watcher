package contextbuf

import (
	"fmt"
	"testing"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestBufferEvictsOldestEntriesAndCopiesFields(t *testing.T) {
	t.Parallel()

	buffer, err := NewBuffer(2)
	if err != nil {
		t.Fatalf("NewBuffer() returned error: %v", err)
	}
	first := logentry.LogEntry{Raw: "first", Fields: map[string]string{"key": "original"}}
	buffer.Append(first)
	first.Fields["key"] = "changed"
	if got := buffer.Last(1)[0].Fields["key"]; got != "original" {
		t.Fatalf("stored fields = %q, want deep copy", got)
	}
	buffer.Append(logentry.LogEntry{Raw: "second"})
	buffer.Append(logentry.LogEntry{Raw: "third"})

	entries := buffer.Last(10)
	if len(entries) != 2 {
		t.Fatalf("Last() returned %d entries", len(entries))
	}
	if entries[0].Raw != "second" || entries[1].Raw != "third" {
		t.Fatalf("Last() = %#v", entries)
	}
	if got := buffer.Last(0); got != nil {
		t.Fatalf("Last(0) = %#v, want nil", got)
	}
}

func TestStoreIsolatesAndRemovesContainers(t *testing.T) {
	t.Parallel()

	store, err := NewStore(3)
	if err != nil {
		t.Fatalf("NewStore() returned error: %v", err)
	}
	store.Append(logentry.LogEntry{ContainerName: "index-node-0", Raw: "index"})
	store.Append(logentry.LogEntry{ContainerName: "query-node-0", Raw: "query"})

	index := store.Last("index-node-0", 3)
	if len(index) != 1 || index[0].Raw != "index" {
		t.Fatalf("index context = %#v", index)
	}
	if store.Len("query-node-0") != 1 {
		t.Fatalf("query context length = %d", store.Len("query-node-0"))
	}

	store.Remove("index-node-0")
	if got := store.Last("index-node-0", 3); got != nil {
		t.Fatalf("removed context = %#v", got)
	}
}

func TestBufferConcurrentAccess(t *testing.T) {
	buffer, err := NewBuffer(32)
	if err != nil {
		t.Fatalf("NewBuffer() returned error: %v", err)
	}

	done := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			for index := 0; index < 100; index++ {
				buffer.Append(logentry.LogEntry{Raw: fmt.Sprintf("%d-%d", worker, index)})
				_ = buffer.Last(10)
			}
		}(worker)
	}
	for worker := 0; worker < 4; worker++ {
		<-done
	}
	if got := buffer.Len(); got != 32 {
		t.Fatalf("Len() = %d, want 32", got)
	}
}
