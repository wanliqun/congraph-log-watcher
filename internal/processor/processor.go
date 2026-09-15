// Package processor contains the source-independent log detection pipeline.
package processor

import (
	"fmt"
	"sync"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/aggregate"
	contextbuf "github.com/wanliqun/congraph-log-watcher/internal/context"
	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
	"github.com/wanliqun/congraph-log-watcher/internal/parser"
	"github.com/wanliqun/congraph-log-watcher/internal/redact"
	"github.com/wanliqun/congraph-log-watcher/internal/router"
	"github.com/wanliqun/congraph-log-watcher/internal/rule"
)

// Alert is the complete, sanitized incident payload produced by Processor.
type Alert struct {
	RuleID          string
	Severity        string
	ContainerName   string
	ContainerID     string
	Level           logentry.LogLevel
	Component       string
	SubgraphID      string
	Fingerprint     string
	FirstSeen       time.Time
	LastSeen        time.Time
	Window          time.Duration
	WindowCount     int
	TotalCount      int
	SuppressedCount int
	Samples         []string
	Context         []string
}

// AlertSink receives alerts independently of checkpoint acknowledgement.
type AlertSink interface {
	Alert(Alert)
}

// Ack confirms a raw log has completed local detection processing and may be
// checkpointed. Delivery success is deliberately not part of this boundary.
type Ack struct {
	Raw logentry.RawLog
}

// Config wires the independently testable detection components together.
type Config struct {
	Parser     parser.Parser
	Redactor   *redact.Redactor
	Context    *contextbuf.Store
	Router     router.Router
	Rules      rule.Engine
	Aggregator *aggregate.Aggregator
	Sink       AlertSink
}

// Processor executes Parse → Redact → Context → Route → Match → Fingerprint
// → Aggregate → Alert for one raw log at a time.
type Processor struct {
	parser     parser.Parser
	redactor   *redact.Redactor
	context    *contextbuf.Store
	router     router.Router
	rules      rule.Engine
	aggregator *aggregate.Aggregator
	sink       AlertSink

	mu      sync.Mutex
	sinkMu  sync.Mutex
	pending map[string]map[*pendingAlert]struct{}
	closed  bool
	wait    sync.WaitGroup
}

type pendingAlert struct {
	collector *contextbuf.AfterCollector
	alert     Alert
	maxBytes  int
}

// New validates the required processing dependencies.
func New(config Config) (*Processor, error) {
	if config.Parser == nil {
		return nil, fmt.Errorf("parser must not be nil")
	}
	if config.Context == nil {
		return nil, fmt.Errorf("context store must not be nil")
	}
	if config.Aggregator == nil {
		return nil, fmt.Errorf("aggregator must not be nil")
	}
	if config.Sink == nil {
		return nil, fmt.Errorf("alert sink must not be nil")
	}
	return &Processor{
		parser:     config.Parser,
		redactor:   config.Redactor,
		context:    config.Context,
		router:     config.Router,
		rules:      config.Rules,
		aggregator: config.Aggregator,
		sink:       config.Sink,
		pending:    make(map[string]map[*pendingAlert]struct{}),
	}, nil
}

// Process consumes one raw log and always returns its checkpoint ack, even
// when no rule matches or alert delivery is pending after-context collection.
func (p *Processor) Process(raw logentry.RawLog) (Ack, error) {
	ack := Ack{Raw: raw}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ack, fmt.Errorf("processor is closed")
	}

	entry := p.parser.Parse(raw)
	if p.redactor != nil {
		entry = p.redactor.Redact(entry)
	}

	route := p.router.Route(entry)
	matches := []rule.Rule(nil)
	if route.EntersRuleEngine() || entry.Level == logentry.LevelUnknown {
		matches = p.rules.Match(entry)
	}
	keepsContext := route.KeepsContext() || len(matches) > 0
	if keepsContext {
		p.addAfterContext(entry)
	}

	var processErr error
	for _, matched := range matches {
		fingerprint := matched.Fingerprint(entry)
		result, err := p.aggregator.Record(aggregate.Event{Rule: matched, Fingerprint: fingerprint, Entry: entry})
		if err != nil {
			processErr = err
			continue
		}
		if !result.Alert {
			continue
		}
		p.queueAlert(matched, entry, fingerprint, result)
	}

	if keepsContext {
		p.context.Append(entry)
	}
	return ack, processErr
}

func (p *Processor) queueAlert(matched rule.Rule, entry logentry.LogEntry, fingerprint string, result aggregate.Result) {
	state := result.State
	alert := Alert{
		RuleID:          matched.ID,
		Severity:        matched.Severity,
		ContainerName:   entry.ContainerName,
		ContainerID:     entry.ContainerID,
		Level:           entry.Level,
		Component:       entry.Fields["component"],
		SubgraphID:      entry.Fields["subgraph_id"],
		Fingerprint:     fingerprint,
		FirstSeen:       state.FirstSeen,
		LastSeen:        state.LastSeen,
		Window:          matched.Window,
		WindowCount:     state.WindowCount,
		TotalCount:      state.TotalCount,
		SuppressedCount: result.SuppressedCount,
		Samples:         append([]string(nil), state.Samples...),
	}
	before := p.context.Last(containerKey(entry), matched.Context.Before)
	collector := contextbuf.NewAfterCollector(before, entry, contextbuf.CaptureConfig{
		After:     matched.Context.After,
		AfterWait: matched.Context.AfterWait,
		MaxBytes:  matched.Context.MaxBytes,
	})
	pending := &pendingAlert{collector: collector, alert: alert, maxBytes: matched.Context.MaxBytes}
	if matched.Context.After <= 0 {
		capture := <-collector.Done()
		pending.alert.Context = capture.Lines(pending.maxBytes)
		p.emit(pending.alert)
		return
	}

	key := containerKey(entry)
	if p.pending[key] == nil {
		p.pending[key] = make(map[*pendingAlert]struct{})
	}
	p.pending[key][pending] = struct{}{}
	p.wait.Add(1)
	go p.awaitContext(key, pending)
}

func (p *Processor) addAfterContext(entry logentry.LogEntry) {
	for pending := range p.pending[containerKey(entry)] {
		pending.collector.Add(entry)
	}
}

func (p *Processor) awaitContext(key string, pending *pendingAlert) {
	defer p.wait.Done()
	capture, ok := <-pending.collector.Done()
	if !ok {
		return
	}
	p.mu.Lock()
	if items := p.pending[key]; items != nil {
		delete(items, pending)
		if len(items) == 0 {
			delete(p.pending, key)
		}
	}
	p.mu.Unlock()
	pending.alert.Context = capture.Lines(pending.maxBytes)
	p.emit(pending.alert)
}

func (p *Processor) emit(alert Alert) {
	p.sinkMu.Lock()
	defer p.sinkMu.Unlock()
	p.sink.Alert(alert)
}

// Close stops waiting for after-context and waits for all already-produced
// alerts to be handed to the sink. It does not create any notification I/O.
func (p *Processor) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	for _, items := range p.pending {
		for pending := range items {
			pending.collector.Cancel()
		}
	}
	p.mu.Unlock()
	p.wait.Wait()
}

func containerKey(entry logentry.LogEntry) string {
	if entry.ContainerName != "" {
		return entry.ContainerName
	}
	return entry.ContainerID
}
