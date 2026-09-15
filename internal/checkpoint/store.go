// Package checkpoint persists only replay boundaries for Docker log streams.
package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var bucketName = []byte("container_checkpoints")

// ContainerCheckpoint is the minimum safe replay boundary. It intentionally
// contains no raw log content, parsed fields, alert state, or credentials.
type ContainerCheckpoint struct {
	ContainerName string
	ContainerID   string
	LastTimestamp time.Time
	LastEventHash string
}

// StateStore is the persistence boundary used by the runtime.
type StateStore interface {
	Load(string) (ContainerCheckpoint, bool, error)
	Save(ContainerCheckpoint) error
	Flush() error
	Close() error
}

// Config controls durable batching behavior.
type Config struct {
	Path          string
	FlushInterval time.Duration
	FlushEvents   int
	OnFlushStatus func(error)
}

// Store batches checkpoint writes and flushes by either event count or time.
type Store struct {
	db            *bolt.DB
	interval      time.Duration
	limit         int
	onFlushStatus func(error)

	mu      sync.Mutex
	pending map[string]ContainerCheckpoint
	dirty   int
	lastErr error
	closing bool
	closed  bool
	stop    chan struct{}
	done    chan struct{}
}

// Open creates or opens a bbolt checkpoint database.
func Open(config Config) (*Store, error) {
	if config.Path == "" {
		return nil, fmt.Errorf("checkpoint path must not be empty")
	}
	if config.FlushInterval <= 0 {
		return nil, fmt.Errorf("checkpoint flush interval must be greater than zero")
	}
	if config.FlushEvents <= 0 {
		return nil, fmt.Errorf("checkpoint flush events must be greater than zero")
	}
	if err := os.MkdirAll(filepath.Dir(config.Path), 0o750); err != nil {
		return nil, fmt.Errorf("create checkpoint directory: %w", err)
	}
	db, err := bolt.Open(config.Path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("open checkpoint database: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketName)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize checkpoint database: %w", err)
	}
	store := &Store{db: db, interval: config.FlushInterval, limit: config.FlushEvents, onFlushStatus: config.OnFlushStatus, pending: make(map[string]ContainerCheckpoint), stop: make(chan struct{}), done: make(chan struct{})}
	go store.flushLoop()
	return store, nil
}

// Load returns the newest in-memory or durable checkpoint for one logical
// container name.
func (s *Store) Load(container string) (ContainerCheckpoint, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ContainerCheckpoint{}, false, fmt.Errorf("checkpoint store is closed")
	}
	if item, ok := s.pending[container]; ok {
		return item, true, nil
	}
	var checkpoint ContainerCheckpoint
	err := s.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket(bucketName).Get([]byte(container))
		if value == nil {
			return nil
		}
		return json.Unmarshal(value, &checkpoint)
	})
	if err != nil {
		return ContainerCheckpoint{}, false, fmt.Errorf("load checkpoint: %w", err)
	}
	return checkpoint, checkpoint.ContainerName != "", nil
}

// Save advances the in-memory boundary after a Processor acknowledgement.
func (s *Store) Save(checkpoint ContainerCheckpoint) error {
	if err := validate(checkpoint); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.closed {
		return fmt.Errorf("checkpoint store is closed")
	}
	s.pending[checkpoint.ContainerName] = checkpoint
	s.dirty++
	if s.dirty >= s.limit {
		return s.flushLocked()
	}
	return nil
}

// Flush synchronously commits every pending checkpoint.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("checkpoint store is closed")
	}
	return s.flushLocked()
}

func (s *Store) flushLocked() error {
	if len(s.pending) == 0 {
		return nil
	}
	pending := make(map[string]ContainerCheckpoint, len(s.pending))
	for name, checkpoint := range s.pending {
		pending[name] = checkpoint
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketName)
		for name, checkpoint := range pending {
			value, err := json.Marshal(checkpoint)
			if err != nil {
				return err
			}
			if err := bucket.Put([]byte(name), value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.lastErr = err
		return fmt.Errorf("flush checkpoints: %w", err)
	}
	s.pending = make(map[string]ContainerCheckpoint)
	s.dirty = 0
	s.lastErr = nil
	return nil
}

// Err returns the most recent asynchronous flush failure, if any.
func (s *Store) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Close stops timed flushing, force-flushes pending data, then closes bbolt.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed || s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	close(s.stop)
	s.mu.Unlock()
	<-s.done

	s.mu.Lock()
	defer s.mu.Unlock()
	flushErr := s.flushLocked()
	closeErr := s.db.Close()
	s.closed = true
	if flushErr != nil {
		return flushErr
	}
	if closeErr != nil {
		return fmt.Errorf("close checkpoint database: %w", closeErr)
	}
	return nil
}

func (s *Store) flushLoop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			err := s.Flush()
			if s.onFlushStatus != nil {
				if err != nil {
					s.onFlushStatus(err)
				} else {
					s.onFlushStatus(s.Err())
				}
			}
		}
	}
}

func validate(checkpoint ContainerCheckpoint) error {
	if checkpoint.ContainerName == "" {
		return fmt.Errorf("checkpoint container name must not be empty")
	}
	if checkpoint.ContainerID == "" {
		return fmt.Errorf("checkpoint container ID must not be empty")
	}
	if checkpoint.LastTimestamp.IsZero() {
		return fmt.Errorf("checkpoint timestamp must not be zero")
	}
	if checkpoint.LastEventHash == "" {
		return fmt.Errorf("checkpoint event hash must not be empty")
	}
	return nil
}
