package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestSaveLoadAndEventFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store := openStore(t, path, time.Hour, 2)
	checkpoint := testCheckpoint("node", "id", time.Now())
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := store.Load("node"); err != nil || !ok || !sameCheckpoint(got, checkpoint) {
		t.Fatalf("Load = %+v, %t, %v", got, ok, err)
	}
	if err := store.Save(testCheckpoint("other", "id", checkpoint.LastTimestamp.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStore(t, path, time.Hour, 2)
	defer reopened.Close()
	if got, ok, err := reopened.Load("node"); err != nil || !ok || !sameCheckpoint(got, checkpoint) {
		t.Fatalf("durable Load = %+v, %t, %v", got, ok, err)
	}
}

func TestTimedAndCloseFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store := openStore(t, path, 5*time.Millisecond, 100)
	checkpoint := testCheckpoint("node", "id", time.Now())
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStore(t, path, time.Hour, 100)
	defer reopened.Close()
	if got, ok, _ := reopened.Load("node"); !ok || !sameCheckpoint(got, checkpoint) {
		t.Fatalf("timed checkpoint = %+v, present=%t", got, ok)
	}
}

func TestCheckpointDoesNotPersistRawLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store := openStore(t, path, time.Hour, 1)
	raw := logentry.RawLog{ContainerID: "id", ContainerName: "node", Timestamp: time.Now(), Stream: "stderr", Raw: "Authorization: Bearer secret-token"}
	checkpoint := ContainerCheckpoint{ContainerName: raw.ContainerName, ContainerID: raw.ContainerID, LastTimestamp: raw.Timestamp, LastEventHash: EventHash(raw)}
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), raw.Raw) {
		t.Fatal("raw log was persisted")
	}
}

func TestReplayGateMarkerMissingAndContainerRecreation(t *testing.T) {
	marker := rawAt("id-a", 2, "marker")
	checkpoint := ContainerCheckpoint{ContainerName: "node", ContainerID: marker.ContainerID, LastTimestamp: marker.Timestamp, LastEventHash: EventHash(marker)}
	gate := NewReplayGate(checkpoint)
	first := rawAt("id-a", 1, "first")
	if got := gate.Accept(first); got != nil {
		t.Fatalf("before marker = %#v", got)
	}
	if got := gate.Accept(marker); got != nil {
		t.Fatalf("marker = %#v", got)
	}
	if got := gate.Accept(rawAt("id-a", 3, "after")); len(got) != 1 || got[0].Raw != "after" {
		t.Fatalf("after marker = %#v", got)
	}

	missing := NewReplayGate(checkpoint)
	missing.Accept(first)
	if got := missing.Finish(); len(got) != 1 || got[0].Raw != "first" {
		t.Fatalf("missing marker replay = %#v", got)
	}
	recreated := NewReplayGate(checkpoint)
	if got := recreated.Accept(rawAt("id-b", 1, "new")); len(got) != 1 || got[0].Raw != "new" {
		t.Fatalf("recreated container replay = %#v", got)
	}
}

func TestInvalidAndCorruptStore(t *testing.T) {
	if _, err := Open(Config{}); err == nil {
		t.Fatal("Open accepted invalid config")
	}
	path := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(path, []byte("not bbolt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Path: path, FlushInterval: time.Second, FlushEvents: 1}); err == nil {
		t.Fatal("Open accepted corrupt database")
	}
}

func openStore(t *testing.T, path string, interval time.Duration, events int) *Store {
	t.Helper()
	store, err := Open(Config{Path: path, FlushInterval: interval, FlushEvents: events})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testCheckpoint(name, id string, timestamp time.Time) ContainerCheckpoint {
	return ContainerCheckpoint{ContainerName: name, ContainerID: id, LastTimestamp: timestamp, LastEventHash: "hash"}
}

func rawAt(id string, second int, value string) logentry.RawLog {
	return logentry.RawLog{ContainerID: id, ContainerName: "node", Timestamp: time.Date(2026, 1, 1, 0, 0, second, 0, time.UTC), Stream: "stdout", Raw: value}
}

func sameCheckpoint(left, right ContainerCheckpoint) bool {
	return left.ContainerName == right.ContainerName && left.ContainerID == right.ContainerID && left.LastEventHash == right.LastEventHash && left.LastTimestamp.Equal(right.LastTimestamp)
}
