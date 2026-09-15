package processor

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/aggregate"
	"github.com/wanliqun/congraph-log-watcher/internal/config"
	contextbuf "github.com/wanliqun/congraph-log-watcher/internal/context"
	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
	"github.com/wanliqun/congraph-log-watcher/internal/parser"
	"github.com/wanliqun/congraph-log-watcher/internal/redact"
	"github.com/wanliqun/congraph-log-watcher/internal/router"
	"github.com/wanliqun/congraph-log-watcher/internal/rule"
)

type alerts struct {
	mu     sync.Mutex
	items  []Alert
	notify chan struct{}
}

func newAlerts() *alerts { return &alerts{notify: make(chan struct{}, 10)} }

func (s *alerts) Alert(alert Alert) {
	s.mu.Lock()
	s.items = append(s.items, alert)
	s.mu.Unlock()
	s.notify <- struct{}{}
}

func (s *alerts) all() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Alert(nil), s.items...)
}

func TestProcessAcknowledgesInfoAndUnmatchedErrors(t *testing.T) {
	sink := newAlerts()
	p := newProcessor(t, sink, testRule(t, 2, 0, 0), router.Default())
	defer p.Close()
	info := raw("INFO healthy")
	ack, err := p.Process(info)
	if err != nil || ack.Raw != info {
		t.Fatalf("ack=%+v err=%v", ack, err)
	}
	unmatched := raw("ERROR different")
	ack, err = p.Process(unmatched)
	if err != nil || ack.Raw != unmatched || len(sink.all()) != 0 {
		t.Fatalf("ack=%+v err=%v alerts=%+v", ack, err, sink.all())
	}
}

func TestBurstCriticalCooldownAndUnknownRule(t *testing.T) {
	sink := newAlerts()
	r := testRule(t, 2, 0, 0)
	p := newProcessor(t, sink, r, router.Default())
	defer p.Close()
	first := raw("ERROR rpc failed")
	p.Process(first)
	p.Process(withTime(first, first.Timestamp.Add(time.Second)))
	waitAlert(t, sink)
	p.Process(withTime(first, first.Timestamp.Add(2*time.Second)))
	if got := len(sink.all()); got != 1 {
		t.Fatalf("alerts during cooldown = %d", got)
	}
	p.Process(withTime(first, first.Timestamp.Add(61*time.Second)))
	waitAlert(t, sink)
	items := sink.all()
	if items[1].SuppressedCount != 1 || items[1].WindowCount != 4 {
		t.Fatalf("second alert = %+v", items[1])
	}

	unknown := testRuleForLevel(t, "UNKNOWN", 1, 0, 0)
	pUnknown := newProcessor(t, sink, unknown, router.Default())
	defer pUnknown.Close()
	if _, err := pUnknown.Process(raw("unstructured rpc failed")); err != nil {
		t.Fatal(err)
	}
	waitAlert(t, sink)
}

func TestAfterContextAndTimeoutDoNotBlockAcknowledgement(t *testing.T) {
	sink := newAlerts()
	r := testRule(t, 1, 2, 50*time.Millisecond)
	p := newProcessor(t, sink, r, router.Default())
	defer p.Close()
	trigger := raw("ERROR rpc failed")
	start := time.Now()
	ack, err := p.Process(trigger)
	if err != nil || ack.Raw != trigger || time.Since(start) > 25*time.Millisecond {
		t.Fatalf("Process blocked: ack=%+v err=%v", ack, err)
	}
	p.Process(withTime(raw("INFO first after"), trigger.Timestamp.Add(time.Second)))
	p.Process(withTime(raw("INFO second after"), trigger.Timestamp.Add(2*time.Second)))
	waitAlert(t, sink)
	got := sink.all()[0]
	if len(got.Context) != 3 || got.Context[0] != "ERROR rpc failed" || got.Context[1] != "INFO first after" || got.Context[2] != "INFO second after" {
		t.Fatalf("context = %#v", got.Context)
	}

	timeoutSink := newAlerts()
	timeoutProcessor := newProcessor(t, timeoutSink, testRule(t, 1, 1, 10*time.Millisecond), router.Default())
	defer timeoutProcessor.Close()
	timeoutProcessor.Process(raw("ERROR rpc failed"))
	waitAlert(t, timeoutSink)
	if got := len(timeoutSink.all()[0].Context); got != 1 {
		t.Fatalf("timeout context lines = %d, want 1", got)
	}
}

func TestRemoveContainerDropsOldContextAndPendingAlert(t *testing.T) {
	sink := newAlerts()
	p := newProcessor(t, sink, testRule(t, 1, 1, time.Second), router.Default())
	p.Process(raw("INFO old context"))
	p.Process(raw("ERROR rpc failed"))
	p.RemoveContainer("node")
	if got := p.context.Len("node"); got != 0 {
		t.Fatalf("context lines after remove = %d", got)
	}
	select {
	case <-sink.notify:
		t.Fatal("recreated container emitted old pending alert")
	case <-time.After(20 * time.Millisecond):
	}
	p.Close()
}

func TestDryRunSinkIsStable(t *testing.T) {
	var output bytes.Buffer
	sink := NewDryRunSink(&output)
	sink.Alert(Alert{RuleID: "rpc", Severity: "high", ContainerName: "node", Fingerprint: "abc", WindowCount: 2, TotalCount: 3, SuppressedCount: 1})
	const want = "WOULD ALERT rule=rpc severity=high container=node fingerprint=abc window_count=2 total_count=3 suppressed_count=1\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestProcessorRedactsBeforeContextAndAggregation(t *testing.T) {
	sink := newAlerts()
	store, err := contextbuf.NewStore(10)
	if err != nil {
		t.Fatal(err)
	}
	aggregator, err := aggregate.New(aggregate.Config{MaxGroups: 10, GroupTTL: time.Hour, MaxSamples: 3})
	if err != nil {
		t.Fatal(err)
	}
	redactor, err := redact.New([]redact.Rule{{Pattern: `secret-[A-Za-z]+`, Replace: "***"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{Parser: parser.NewGraphNodeParser(), Redactor: redactor, Context: store, Router: router.Default(), Rules: rule.NewEngine([]rule.Rule{testRule(t, 1, 0, 0)}), Aggregator: aggregator, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Process(raw("ERROR rpc failed secret-token")); err != nil {
		t.Fatal(err)
	}
	waitAlert(t, sink)
	alert := sink.all()[0]
	if alert.Samples[0] != "ERROR rpc failed ***" || alert.Context[0] != "ERROR rpc failed ***" {
		t.Fatalf("redaction did not cross pipeline boundary: %+v", alert)
	}
}

func newProcessor(t *testing.T, sink AlertSink, r rule.Rule, gate router.Router) *Processor {
	t.Helper()
	store, err := contextbuf.NewStore(10)
	if err != nil {
		t.Fatal(err)
	}
	aggregator, err := aggregate.New(aggregate.Config{MaxGroups: 10, GroupTTL: time.Hour, MaxSamples: 3})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{Parser: parser.NewGraphNodeParser(), Context: store, Router: gate, Rules: rule.NewEngine([]rule.Rule{r}), Aggregator: aggregator, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testRule(t *testing.T, threshold, after int, afterWait time.Duration) rule.Rule {
	t.Helper()
	return testRuleForLevelAndContext(t, "ERROR", threshold, after, afterWait)
}

func testRuleForLevel(t *testing.T, level string, threshold, after int, afterWait time.Duration) rule.Rule {
	t.Helper()
	return testRuleForLevelAndContext(t, level, threshold, after, afterWait)
}

func testRuleForLevelAndContext(t *testing.T, level string, threshold, after int, afterWait time.Duration) rule.Rule {
	t.Helper()
	enabled := true
	r, err := rule.Compile(config.RuleConfig{ID: "rpc", Enabled: &enabled, Severity: "high", Threshold: threshold, Window: config.Duration(time.Hour), Cooldown: config.Duration(time.Minute), Dedup: config.DedupConfig{Mode: "normalized"}, Context: config.RuleContextConfig{After: after, AfterWait: config.Duration(afterWait), MaxBytes: 1024}, Match: config.MatchConfig{Levels: []string{level}, Pattern: "rpc failed"}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func raw(line string) logentry.RawLog {
	return logentry.RawLog{ContainerID: "id", ContainerName: "node", Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Raw: line}
}

func withTime(value logentry.RawLog, at time.Time) logentry.RawLog {
	value.Timestamp = at
	return value
}

func waitAlert(t *testing.T, sink *alerts) {
	t.Helper()
	select {
	case <-sink.notify:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for alert")
	}
}
