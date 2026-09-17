package runtime

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/wanliqun/congraph-log-watcher/internal/checkpoint"
	"github.com/wanliqun/congraph-log-watcher/internal/metrics"
)

func TestReplayStartUsesStableInitialWindowUntilCheckpoint(t *testing.T) {
	store, err := checkpoint.Open(checkpoint.Config{Path: filepath.Join(t.TempDir(), "state.db"), FlushInterval: time.Hour, FlushEvents: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	observer := newObserver(metrics.New(prometheus.NewRegistry()), metrics.NewHealth([]string{"node"}, time.Minute, time.Minute), store, nil, 2*time.Second, 5*time.Minute)
	observer.now = func() time.Time { return now }

	if got, want := observer.replayStart("node"), now.Add(-5*time.Minute); !got.Equal(want) {
		t.Fatalf("initial replay start = %s, want %s", got, want)
	}
	now = now.Add(time.Hour)
	if got, want := observer.replayStart("node"), now.Add(-time.Hour-5*time.Minute); !got.Equal(want) {
		t.Fatalf("retry replay start = %s, want original %s", got, want)
	}

	checkpointTime := now.Add(-10 * time.Minute)
	if err := store.Save(checkpoint.ContainerCheckpoint{ContainerName: "node", ContainerID: "id", LastTimestamp: checkpointTime, LastEventHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if got, want := observer.replayStart("node"), checkpointTime.Add(-2*time.Second); !got.Equal(want) {
		t.Fatalf("checkpoint replay start = %s, want %s", got, want)
	}
}

func TestReplayStartWithZeroInitialWindowStartsNow(t *testing.T) {
	store, err := checkpoint.Open(checkpoint.Config{Path: filepath.Join(t.TempDir(), "state.db"), FlushInterval: time.Hour, FlushEvents: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	observer := newObserver(metrics.New(prometheus.NewRegistry()), metrics.NewHealth([]string{"node"}, time.Minute, time.Minute), store, nil, 2*time.Second, 0)
	observer.now = func() time.Time { return now }
	if got := observer.replayStart("node"); !got.Equal(now) {
		t.Fatalf("zero-window replay start = %s, want %s", got, now)
	}
}
