//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/aggregate"
	"github.com/wanliqun/congraph-log-watcher/internal/checkpoint"
	"github.com/wanliqun/congraph-log-watcher/internal/config"
	contextbuf "github.com/wanliqun/congraph-log-watcher/internal/context"
	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
	"github.com/wanliqun/congraph-log-watcher/internal/notify"
	"github.com/wanliqun/congraph-log-watcher/internal/parser"
	"github.com/wanliqun/congraph-log-watcher/internal/processor"
	"github.com/wanliqun/congraph-log-watcher/internal/redact"
	"github.com/wanliqun/congraph-log-watcher/internal/router"
	"github.com/wanliqun/congraph-log-watcher/internal/rule"
	docker "github.com/wanliqun/congraph-log-watcher/internal/source/docker"
)

func TestMVPDetectionCheckpointReplayAndRecreate(t *testing.T) {
	lines := []string{
		"2026-01-01T00:00:00Z INFO service ready secret-token",
		"2026-01-01T00:00:01Z ERROR rpc failed request 1 secret-token",
		"2026-01-01T00:00:02Z ERROR rpc failed request 2 secret-token",
		"2026-01-01T00:00:03Z ERROR rpc failed request 3 secret-token",
		"2026-01-01T00:00:04Z CRIT database unavailable secret-token",
	}
	api := newFakeDockerAPI("container-a", strings.Join(lines, "\n")+"\n")
	rawLogs := make(chan logentry.RawLog, 32)
	source, err := docker.New(docker.Config{API: api, Containers: []string{"index-node-0"}, Output: rawLogs, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	store := openStore(t, 100)
	alerts := &recordingNotifier{}
	worker, err := notify.NewWorker(notify.Config{Notifier: alerts, QueueSize: 10, MaxAlertsPerMinute: 20, StormCooldown: time.Minute, RetryDelays: []time.Duration{0}})
	if err != nil {
		t.Fatal(err)
	}
	p := newProcessor(t, worker)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.Run(ctx) }()
	consumed := make([]logentry.RawLog, 0, len(lines))
	for len(consumed) < len(lines) {
		select {
		case raw := <-rawLogs:
			consumed = append(consumed, raw)
			if _, err := p.Process(raw); err != nil {
				t.Fatal(err)
			}
			if err := store.Save(toCheckpoint(raw)); err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out reading fake Docker logs")
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("source shutdown: %v", err)
	}
	p.Close()
	if err := worker.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	gotAlerts := alerts.all()
	if len(gotAlerts) != 2 {
		t.Fatalf("alerts = %d, want rpc burst and critical", len(gotAlerts))
	}
	encoded, _ := json.Marshal(gotAlerts)
	if strings.Contains(string(encoded), "secret-token") {
		t.Fatalf("secret escaped redaction boundary: %s", encoded)
	}
	if gotAlerts[0].RuleID != "rpc-error" || gotAlerts[0].WindowCount != 3 {
		t.Fatalf("rpc alert = %+v", gotAlerts[0])
	}
	if gotAlerts[1].RuleID != "critical-log" || len(gotAlerts[1].Context) == 0 {
		t.Fatalf("critical alert = %+v", gotAlerts[1])
	}

	cp, ok, err := store.Load("index-node-0")
	if err != nil || !ok || !cp.LastTimestamp.Equal(consumed[len(consumed)-1].Timestamp) {
		t.Fatalf("checkpoint = %+v, present=%t, err=%v", cp, ok, err)
	}
	gate := checkpoint.NewReplayGate(cp)
	if replayed := gate.Accept(consumed[len(consumed)-1]); replayed != nil {
		t.Fatalf("checkpoint marker replayed: %+v", replayed)
	}
	next := logentry.RawLog{ContainerName: "index-node-0", ContainerID: "container-a", Timestamp: cp.LastTimestamp.Add(time.Second), Stream: "stdout", Raw: "INFO after restart"}
	if replayed := gate.Accept(next); len(replayed) != 1 || replayed[0] != next {
		t.Fatalf("post-marker event = %+v", replayed)
	}
	recreated := checkpoint.NewReplayGate(cp)
	newContainer := next
	newContainer.ContainerID = "container-b"
	if replayed := recreated.Accept(newContainer); len(replayed) != 1 || replayed[0].ContainerID != "container-b" {
		t.Fatalf("recreated container event = %+v", replayed)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHundredThousandInfoLinesKeepStateBounded(t *testing.T) {
	store := openStore(t, 1000)
	contexts, err := contextbuf.NewStore(100)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := aggregate.New(aggregate.Config{MaxGroups: 100, GroupTTL: time.Hour, MaxSamples: 3})
	if err != nil {
		t.Fatal(err)
	}
	p, err := processor.New(processor.Config{Parser: parser.NewGraphNodeParser(), Context: contexts, Router: router.Default(), Aggregator: agg, Sink: discardSink{}})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 100_000; i++ {
		raw := logentry.RawLog{ContainerName: "index-node-0", ContainerID: "container-a", Timestamp: base.Add(time.Duration(i) * time.Nanosecond), Stream: "stdout", Raw: fmt.Sprintf("INFO block %d", i)}
		if _, err := p.Process(raw); err != nil {
			t.Fatal(err)
		}
		if err := store.Save(toCheckpoint(raw)); err != nil {
			t.Fatal(err)
		}
	}
	p.Close()
	if contexts.Len("index-node-0") != 100 || agg.Len() != 0 {
		t.Fatalf("context=%d groups=%d", contexts.Len("index-node-0"), agg.Len())
	}
	cp, ok, err := store.Load("index-node-0")
	if err != nil || !ok || !cp.LastTimestamp.Equal(base.Add(99_999*time.Nanosecond)) {
		t.Fatalf("checkpoint = %+v, present=%t, err=%v", cp, ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func newProcessor(t *testing.T, sink processor.AlertSink) *processor.Processor {
	t.Helper()
	redactor, err := redact.New([]redact.Rule{{Pattern: `secret-[A-Za-z]+`, Replace: "[REDACTED]"}})
	if err != nil {
		t.Fatal(err)
	}
	contexts, _ := contextbuf.NewStore(20)
	agg, _ := aggregate.New(aggregate.Config{MaxGroups: 100, GroupTTL: time.Hour, MaxSamples: 3})
	enabled := true
	configs := []config.RuleConfig{
		{ID: "critical-log", Enabled: &enabled, Severity: "critical", Match: config.MatchConfig{Levels: []string{"CRITICAL"}}, Threshold: 1, Window: config.Duration(time.Minute), Cooldown: config.Duration(time.Minute), Dedup: config.DedupConfig{Mode: "rule"}, Context: config.RuleContextConfig{Before: 10, MaxBytes: 4096}, StopOnMatch: true},
		{ID: "rpc-error", Enabled: &enabled, Severity: "high", Match: config.MatchConfig{Levels: []string{"ERROR"}, Pattern: "rpc failed"}, Threshold: 3, Window: config.Duration(time.Minute), Cooldown: config.Duration(time.Minute), Dedup: config.DedupConfig{Mode: "rule"}, Context: config.RuleContextConfig{Before: 10, MaxBytes: 4096}},
	}
	rules := make([]rule.Rule, 0, len(configs))
	for _, item := range configs {
		compiled, err := rule.Compile(item)
		if err != nil {
			t.Fatal(err)
		}
		rules = append(rules, compiled)
	}
	p, err := processor.New(processor.Config{Parser: parser.NewGraphNodeParser(), Redactor: redactor, Context: contexts, Router: router.Default(), Rules: rule.NewEngine(rules), Aggregator: agg, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func openStore(t *testing.T, flushEvents int) *checkpoint.Store {
	t.Helper()
	store, err := checkpoint.Open(checkpoint.Config{Path: filepath.Join(t.TempDir(), "state.db"), FlushInterval: time.Hour, FlushEvents: flushEvents})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func toCheckpoint(raw logentry.RawLog) checkpoint.ContainerCheckpoint {
	return checkpoint.ContainerCheckpoint{ContainerName: raw.ContainerName, ContainerID: raw.ContainerID, LastTimestamp: raw.Timestamp, LastEventHash: checkpoint.EventHash(raw)}
}

type discardSink struct{}

func (discardSink) Alert(processor.Alert) {}

type recordingNotifier struct {
	mu     sync.Mutex
	alerts []processor.Alert
}

func (n *recordingNotifier) Notify(_ context.Context, alert processor.Alert) error {
	n.mu.Lock()
	n.alerts = append(n.alerts, alert)
	n.mu.Unlock()
	return nil
}
func (n *recordingNotifier) all() []processor.Alert {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]processor.Alert(nil), n.alerts...)
}

type fakeDockerAPI struct {
	id     string
	logs   string
	events chan docker.Event
}

func newFakeDockerAPI(id, logs string) *fakeDockerAPI {
	return &fakeDockerAPI{id: id, logs: logs, events: make(chan docker.Event)}
}
func (f *fakeDockerAPI) List(context.Context) ([]docker.Container, error) {
	return []docker.Container{{ID: f.id, Names: []string{"/index-node-0"}, Running: true, TTY: true}}, nil
}
func (f *fakeDockerAPI) Inspect(context.Context, string) (docker.Container, error) {
	return docker.Container{ID: f.id, Names: []string{"/index-node-0"}, Running: true, TTY: true}, nil
}
func (f *fakeDockerAPI) Logs(context.Context, string, time.Time) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.logs)), nil
}
func (f *fakeDockerAPI) Events(context.Context) (<-chan docker.Event, <-chan error) {
	return f.events, nil
}
