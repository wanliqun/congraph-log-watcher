package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestDecodeMultiplexedPreservesStreamAndDockerTimestamp(t *testing.T) {
	stamp := "2026-01-02T03:04:05.000000006Z"
	payload := append(frame(1, stamp+" INFO one\n"), frame(2, stamp+" ERROR two\n")...)
	output := make(chan logentry.RawLog, 2)
	if err := decodeMultiplexed(context.Background(), bytes.NewReader(payload), "node", "id", output); err != nil {
		t.Fatal(err)
	}
	first, second := <-output, <-output
	if first.Stream != "stdout" || first.Raw != "INFO one" || !first.Timestamp.Equal(time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)) {
		t.Fatalf("first = %+v", first)
	}
	if second.Stream != "stderr" || second.Raw != "ERROR two" {
		t.Fatalf("second = %+v", second)
	}
}

func TestDecodePlainAndBackpressureCancellation(t *testing.T) {
	output := make(chan logentry.RawLog, 1)
	if err := decodePlain(context.Background(), strings.NewReader("2026-01-02T03:04:05Z INFO tty\n"), "node", "id", output); err != nil {
		t.Fatal(err)
	}
	if got := (<-output).Raw; got != "INFO tty" {
		t.Fatalf("raw = %q", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := decodePlain(ctx, strings.NewReader("2026-01-02T03:04:05Z blocked\n"), "node", "id", make(chan logentry.RawLog))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context cancellation", err)
	}
}

func TestDiscoverNormalizesNameAndInspectsRunningContainer(t *testing.T) {
	api := &fakeAPI{containers: []Container{{ID: "A", Names: []string{"/index-node-0"}}}, inspected: map[string]Container{"A": {ID: "A", Names: []string{"/index-node-0"}, Running: true}}}
	source, err := New(Config{API: api, Containers: []string{"/index-node-0"}, Output: make(chan logentry.RawLog, 1)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := source.discover(context.Background(), "index-node-0")
	if err != nil || got.ID != "A" || api.inspectCalls != 1 {
		t.Fatalf("container=%+v err=%v inspectCalls=%d", got, err, api.inspectCalls)
	}
}

func TestLifecycleEventReattachesRecreatedContainer(t *testing.T) {
	api := &fakeAPI{
		containers: []Container{{ID: "A", Names: []string{"/node"}}},
		inspected:  map[string]Container{"A": {ID: "A", Names: []string{"/node"}, Running: true, TTY: true}, "B": {ID: "B", Names: []string{"/node"}, Running: true, TTY: true}},
		logs:       map[string]string{"A": "2026-01-01T00:00:00Z one\n", "B": "2026-01-01T00:00:01Z two\n"},
		events:     make(chan Event, 1),
	}
	output := make(chan logentry.RawLog, 4)
	source, err := New(Config{API: api, Containers: []string{"node"}, Output: output, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.Run(ctx) }()
	select {
	case first := <-output:
		if first.ContainerID != "A" {
			t.Fatalf("first ID = %q", first.ContainerID)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive first stream")
	}
	api.setContainer(Container{ID: "B", Names: []string{"/node"}})
	api.events <- Event{Action: "start", Name: "node", ID: "B"}
	deadline := time.After(time.Second)
	for {
		select {
		case item := <-output:
			if item.ContainerID == "B" {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("Run error = %v", err)
				}
				return
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatal("did not reattach container B")
		}
	}
}

func TestNewRejectsInvalidBounds(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New accepted invalid config")
	}
	if _, err := New(Config{API: &fakeAPI{}, Containers: []string{"node"}, Output: make(chan logentry.RawLog), InitialBackoff: time.Second, MaxBackoff: time.Millisecond}); err == nil {
		t.Fatal("New accepted inverted backoff")
	}
}

func TestSDKClientUsesReadOnlyDockerHTTPAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if strings.HasPrefix(path, "/v") {
			parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
			if len(parts) == 2 {
				path = "/" + parts[1]
			}
		}
		switch path {
		case "/_ping":
			writer.Header().Set("API-Version", "1.45")
		case "/version":
			_, _ = io.WriteString(writer, `{"ApiVersion":"1.45"}`)
		case "/containers/json":
			_, _ = io.WriteString(writer, `[{"Id":"id","Names":["/node"],"State":"running"}]`)
		case "/containers/id/json":
			_, _ = io.WriteString(writer, `{"Id":"id","Name":"/node","State":{"Running":true},"Config":{"Tty":false}}`)
		case "/containers/id/logs":
			if request.URL.Query().Get("follow") != "1" || request.URL.Query().Get("timestamps") != "1" || request.URL.Query().Get("stdout") != "1" || request.URL.Query().Get("stderr") != "1" {
				t.Errorf("unexpected logs query: %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(writer, "2026-01-01T00:00:00Z hello\n")
		default:
			t.Errorf("unexpected Docker API path %q", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewSDKClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	items, err := client.List(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != "id" {
		t.Fatalf("List = %+v, %v", items, err)
	}
	inspected, err := client.Inspect(context.Background(), "id")
	if err != nil || !inspected.Running || inspected.TTY {
		t.Fatalf("Inspect = %+v, %v", inspected, err)
	}
	logs, err := client.Logs(context.Background(), "id", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	if value, _ := io.ReadAll(logs); string(value) != "2026-01-01T00:00:00Z hello\n" {
		t.Fatalf("logs = %q", value)
	}
}

func frame(stream byte, value string) []byte {
	result := make([]byte, 8+len(value))
	result[0] = stream
	binary.BigEndian.PutUint32(result[4:], uint32(len(value)))
	copy(result[8:], value)
	return result
}

type fakeAPI struct {
	mu           sync.Mutex
	containers   []Container
	inspected    map[string]Container
	logs         map[string]string
	events       chan Event
	inspectCalls int
}

func (f *fakeAPI) List(context.Context) ([]Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Container(nil), f.containers...), nil
}
func (f *fakeAPI) Inspect(_ context.Context, id string) (Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls++
	item, ok := f.inspected[id]
	if !ok {
		return Container{}, errors.New("not found")
	}
	return item, nil
}
func (f *fakeAPI) Logs(_ context.Context, id string, _ time.Time) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return io.NopCloser(strings.NewReader(f.logs[id])), nil
}
func (f *fakeAPI) Events(context.Context) (<-chan Event, <-chan error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.events == nil {
		f.events = make(chan Event)
	}
	return f.events, nil
}
func (f *fakeAPI) setContainer(container Container) {
	f.mu.Lock()
	f.containers = []Container{container}
	f.mu.Unlock()
}
