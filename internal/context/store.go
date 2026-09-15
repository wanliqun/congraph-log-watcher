package contextbuf

import (
	"fmt"
	"sync"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// Store owns one ring buffer per logical container name.
type Store struct {
	mu       sync.RWMutex
	capacity int
	buffers  map[string]*Buffer
}

func NewStore(capacity int) (*Store, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("context store capacity must be greater than zero")
	}
	return &Store{capacity: capacity, buffers: make(map[string]*Buffer)}, nil
}

func (s *Store) Append(entry logentry.LogEntry) {
	if s == nil {
		return
	}

	container := containerKey(entry)
	s.mu.RLock()
	buffer := s.buffers[container]
	s.mu.RUnlock()
	if buffer == nil {
		s.mu.Lock()
		buffer = s.buffers[container]
		if buffer == nil {
			buffer, _ = NewBuffer(s.capacity)
			s.buffers[container] = buffer
		}
		s.mu.Unlock()
	}

	buffer.Append(entry)
}

func (s *Store) Last(container string, limit int) []logentry.LogEntry {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	buffer := s.buffers[container]
	s.mu.RUnlock()
	return buffer.Last(limit)
}

// Remove releases all retained context for a container, including when a
// container is destroyed and later recreated with a new ID.
func (s *Store) Remove(container string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.buffers, container)
	s.mu.Unlock()
}

func (s *Store) Len(container string) int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	buffer := s.buffers[container]
	s.mu.RUnlock()
	return buffer.Len()
}

func containerKey(entry logentry.LogEntry) string {
	if entry.ContainerName != "" {
		return entry.ContainerName
	}
	return entry.ContainerID
}
