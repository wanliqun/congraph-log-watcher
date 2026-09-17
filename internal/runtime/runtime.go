// Package runtime wires the configured MVP components into one service.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/wanliqun/congraph-log-watcher/internal/aggregate"
	"github.com/wanliqun/congraph-log-watcher/internal/checkpoint"
	"github.com/wanliqun/congraph-log-watcher/internal/config"
	contextbuf "github.com/wanliqun/congraph-log-watcher/internal/context"
	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
	"github.com/wanliqun/congraph-log-watcher/internal/metrics"
	"github.com/wanliqun/congraph-log-watcher/internal/notify"
	"github.com/wanliqun/congraph-log-watcher/internal/parser"
	"github.com/wanliqun/congraph-log-watcher/internal/processor"
	"github.com/wanliqun/congraph-log-watcher/internal/redact"
	"github.com/wanliqun/congraph-log-watcher/internal/router"
	"github.com/wanliqun/congraph-log-watcher/internal/rule"
	docker "github.com/wanliqun/congraph-log-watcher/internal/source/docker"
)

// Run owns the production lifecycle and drains accepted work on cancellation.
func Run(ctx context.Context, cfg *config.Config, dry bool, output io.Writer) error {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	health := metrics.NewHealth(cfg.Containers, cfg.Health.DockerUnavailableAfter.Duration(), cfg.Health.AllContainersDetachedAfter.Duration())
	store, err := checkpoint.Open(checkpoint.Config{
		Path:          cfg.Checkpoint.Path,
		FlushInterval: cfg.Checkpoint.FlushInterval.Duration(),
		FlushEvents:   cfg.Checkpoint.FlushEvents,
		OnFlushStatus: func(err error) {
			health.CheckpointOK(err == nil)
		},
	})
	if err != nil {
		return err
	}
	storeClosed := false
	defer func() {
		if !storeClosed {
			_ = store.Close()
		}
	}()

	redactor, compiled, gate, contexts, agg, err := buildPipelineParts(cfg)
	if err != nil {
		return err
	}
	observer := newObserver(m, health, store, agg, cfg.Checkpoint.ReplayOverlap.Duration(), cfg.Checkpoint.InitialReplayWindow.Duration())

	var worker *notify.Worker
	var sink processor.AlertSink = processor.NewDryRunSink(output)
	if !dry {
		worker, err = buildNotifier(cfg, observer)
		if err != nil {
			return err
		}
		sink = worker
	}
	p, err := processor.New(processor.Config{Parser: parser.NewGraphNodeParser(), Redactor: redactor, Context: contexts, Router: gate, Rules: rule.NewEngine(compiled), Aggregator: agg, Sink: sink, Observer: observer})
	if err != nil {
		if worker != nil {
			_ = worker.Close(context.Background())
		}
		return err
	}
	observer.processor = p

	srv := &http.Server{Addr: cfg.Metrics.Listen, Handler: metrics.Handler(reg, health)}
	httpErr := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr <- fmt.Errorf("metrics server: %w", err)
		}
		close(httpErr)
	}()

	api, err := docker.NewSDKClient(cfg.Docker.Socket)
	if err != nil {
		_ = srv.Shutdown(context.Background())
		p.Close()
		if worker != nil {
			_ = worker.Close(context.Background())
		}
		return err
	}
	defer api.Close()

	logs := make(chan logentry.RawLog, cfg.Runtime.LogChannelSize)
	probeInterval := cfg.Health.DockerUnavailableAfter.Duration() / 2
	if probeInterval <= 0 {
		probeInterval = cfg.Health.DockerUnavailableAfter.Duration()
	}
	source, err := docker.New(docker.Config{API: api, Containers: cfg.Containers, Output: logs, ReplayStart: observer.replayStart, HealthProbeInterval: probeInterval, Observer: observer})
	if err != nil {
		_ = srv.Shutdown(context.Background())
		p.Close()
		if worker != nil {
			_ = worker.Close(context.Background())
		}
		return err
	}
	sourceCtx, cancelSource := context.WithCancel(context.Background())
	sourceDone := make(chan error, 1)
	go func() {
		sourceDone <- source.Run(sourceCtx)
		close(logs)
	}()

	var runErr error
running:
	for {
		select {
		case raw, ok := <-logs:
			if !ok {
				break running
			}
			if err := processRaw(p, store, observer, raw); err != nil {
				runErr = err
				cancelSource()
				break running
			}
		case <-ctx.Done():
			cancelSource()
			break running
		case err, ok := <-httpErr:
			if !ok {
				httpErr = nil
			} else if err != nil {
				runErr = err
				cancelSource()
				break running
			}
		}
	}

	// The configured timeout covers the complete shutdown sequence, including
	// source/channel drain, rather than only the notifier and HTTP tail.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.Shutdown.Timeout.Duration())
	defer cancelShutdown()
	cancelSource()
	logsOpen := true
	sourceRunning := true
	for logsOpen || sourceRunning {
		select {
		case raw, ok := <-logs:
			if !ok {
				logsOpen = false
				continue
			}
			if err := processRaw(p, store, observer, raw); err != nil && runErr == nil {
				runErr = err
			}
		case err, ok := <-sourceDone:
			sourceRunning = false
			if ok && err != nil && !errors.Is(err, context.Canceled) && runErr == nil {
				runErr = fmt.Errorf("stop Docker log source: %w", err)
			}
		case <-shutdownCtx.Done():
			runErr = errors.Join(runErr, fmt.Errorf("drain Docker log source: %w", shutdownCtx.Err()))
			logsOpen = false
			sourceRunning = false
		}
	}

	p.Close()
	if err := store.Flush(); err != nil {
		health.CheckpointOK(false)
		runErr = errors.Join(runErr, err)
	}
	if worker != nil {
		if err := worker.Close(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("drain notifier: %w", err))
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("shutdown metrics server: %w", err))
	}
	if err := store.Close(); err != nil {
		runErr = errors.Join(runErr, err)
	}
	storeClosed = true
	return runErr
}

func processRaw(p *processor.Processor, store *checkpoint.Store, observer *runtimeObserver, raw logentry.RawLog) error {
	for _, item := range observer.filter(raw) {
		if _, err := p.Process(item); err != nil {
			return err
		}
		cp := checkpoint.ContainerCheckpoint{ContainerName: item.ContainerName, ContainerID: item.ContainerID, LastTimestamp: item.Timestamp, LastEventHash: checkpoint.EventHash(item)}
		if err := store.Save(cp); err != nil {
			observer.health.CheckpointOK(false)
			slog.Error("checkpoint save failed", "component", "checkpoint", "container", item.ContainerName, "error", err)
			continue
		}
		observer.metrics.Checkpoint.WithLabelValues(item.ContainerName).Set(float64(item.Timestamp.Unix()))
		if err := store.Err(); err != nil {
			observer.health.CheckpointOK(false)
			slog.Error("checkpoint flush failed", "component", "checkpoint", "container", item.ContainerName, "error", err)
			continue
		}
		observer.health.CheckpointOK(true)
	}
	return nil
}

func buildPipelineParts(cfg *config.Config) (*redact.Redactor, []rule.Rule, router.Router, *contextbuf.Store, *aggregate.Aggregator, error) {
	redRules := make([]redact.Rule, len(cfg.Redaction.Rules))
	for i, r := range cfg.Redaction.Rules {
		redRules[i] = redact.Rule{Pattern: r.Pattern, Replace: r.Replace}
	}
	redactor, err := redact.New(redRules)
	if err != nil {
		return nil, nil, router.Router{}, nil, nil, err
	}
	compiled := make([]rule.Rule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		x, err := rule.Compile(r)
		if err != nil {
			return nil, nil, router.Router{}, nil, nil, err
		}
		compiled = append(compiled, x)
	}
	gate, err := router.NewFromNames(cfg.Levels.Context, cfg.Levels.AlertCandidates)
	if err != nil {
		return nil, nil, router.Router{}, nil, nil, err
	}
	contexts, err := contextbuf.NewStore(cfg.Context.BufferLines)
	if err != nil {
		return nil, nil, router.Router{}, nil, nil, err
	}
	agg, err := aggregate.New(aggregate.Config{MaxGroups: cfg.Aggregation.MaxGroups, GroupTTL: cfg.Aggregation.GroupTTL.Duration(), MaxSamples: cfg.Aggregation.MaxSamples})
	return redactor, compiled, gate, contexts, agg, err
}

func buildNotifier(cfg *config.Config, observer *runtimeObserver) (*notify.Worker, error) {
	ids := make([]string, 0, len(cfg.Alert.Channels))
	for id := range cfg.Alert.Channels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	notifiers := make(notify.MultiNotifier, 0, len(ids))
	for _, id := range ids {
		ch := cfg.Alert.Channels[id]
		n, err := notify.NewDingTalkNotifier(notify.DingTalkConfig{ID: id, Webhook: ch.Webhook, Secret: ch.Secret, AtMobiles: ch.AtMobiles, IsAtAll: ch.IsAtAll, CustomTags: cfg.Alert.CustomTags})
		if err != nil {
			return nil, err
		}
		notifiers = append(notifiers, n)
	}
	return notify.NewWorker(notify.Config{Notifier: notifiers, QueueSize: cfg.Notifier.QueueSize, MaxAlertsPerMinute: cfg.Notifier.MaxAlertsPerMinute, StormCooldown: cfg.Notifier.StormCooldown.Duration(), Observer: observer})
}

type runtimeObserver struct {
	metrics             *metrics.Metrics
	health              *metrics.Health
	store               *checkpoint.Store
	agg                 *aggregate.Aggregator
	overlap             time.Duration
	initialReplayWindow time.Duration
	now                 func() time.Time
	mu                  sync.Mutex
	gates               map[string]*checkpoint.ReplayGate
	ids                 map[string]string
	initialStarts       map[string]time.Time
	processor           *processor.Processor
}

func newObserver(m *metrics.Metrics, h *metrics.Health, store *checkpoint.Store, agg *aggregate.Aggregator, overlap, initialReplayWindow time.Duration) *runtimeObserver {
	return &runtimeObserver{metrics: m, health: h, store: store, agg: agg, overlap: overlap, initialReplayWindow: initialReplayWindow, now: time.Now, gates: make(map[string]*checkpoint.ReplayGate), ids: make(map[string]string), initialStarts: make(map[string]time.Time)}
}

func (o *runtimeObserver) DockerAvailable() { o.health.DockerOK(time.Now()) }
func (o *runtimeObserver) ContainerAttached(name, id string, attached bool) {
	o.health.Attached(name, attached)
	if !attached {
		return
	}
	o.mu.Lock()
	previousID := o.ids[name]
	o.ids[name] = id
	o.mu.Unlock()
	if previousID != "" && previousID != id && o.processor != nil {
		o.processor.RemoveContainer(name)
	}
	cp, ok, err := o.store.Load(name)
	if err != nil {
		o.health.CheckpointOK(false)
		return
	}
	o.mu.Lock()
	if ok {
		o.gates[name] = checkpoint.NewReplayGate(cp)
	} else {
		delete(o.gates, name)
	}
	o.mu.Unlock()
}
func (o *runtimeObserver) ContainerReconnect(name string) {
	o.metrics.Reconnects.WithLabelValues(name).Inc()
	slog.Warn("reconnecting Docker log stream", "component", "docker", "container", name)
}
func (o *runtimeObserver) replayStart(name string) time.Time {
	cp, ok, err := o.store.Load(name)
	if err != nil {
		o.health.CheckpointOK(false)
		return time.Time{}
	}
	if !ok {
		o.mu.Lock()
		defer o.mu.Unlock()
		if start, exists := o.initialStarts[name]; exists {
			return start
		}
		start := o.now().Add(-o.initialReplayWindow)
		o.initialStarts[name] = start
		return start
	}
	o.mu.Lock()
	delete(o.initialStarts, name)
	o.mu.Unlock()
	return checkpoint.ReplayStart(cp, o.overlap)
}
func (o *runtimeObserver) filter(raw logentry.RawLog) []logentry.RawLog {
	o.mu.Lock()
	defer o.mu.Unlock()
	if gate := o.gates[raw.ContainerName]; gate != nil {
		return gate.Accept(raw)
	}
	return []logentry.RawLog{raw}
}
func (o *runtimeObserver) Parsed(entry logentry.LogEntry) {
	o.metrics.Logs.WithLabelValues(entry.ContainerName, entry.Level.String()).Inc()
	if entry.Level == logentry.LevelUnknown {
		o.metrics.ParseErrors.Inc()
	}
}
func (o *runtimeObserver) RuleMatched(id string) { o.metrics.RuleMatches.WithLabelValues(id).Inc() }
func (o *runtimeObserver) Aggregated(id string, result aggregate.Result) {
	if result.Suppressed {
		o.metrics.Suppressed.WithLabelValues(id).Inc()
		slog.Info("alert suppressed by cooldown", "component", "aggregator", "rule", id, "fingerprint", result.State.Fingerprint)
	}
	o.metrics.Groups.Set(float64(o.agg.Len()))
}
func (o *runtimeObserver) NotificationSent(alert processor.Alert) {
	o.metrics.Alerts.WithLabelValues(alert.RuleID, alert.Severity).Inc()
}
func (o *runtimeObserver) NotificationFailed(alert processor.Alert) {
	o.metrics.NotifyErrors.Inc()
	slog.Error("notification failed", "component", "notifier", "container", alert.ContainerName, "rule", alert.RuleID, "fingerprint", alert.Fingerprint)
}
func (o *runtimeObserver) NotificationDropped(alert processor.Alert) {
	o.metrics.NotifyErrors.Inc()
	slog.Error("notification dropped", "component", "notifier", "container", alert.ContainerName, "rule", alert.RuleID, "fingerprint", alert.Fingerprint)
}
func (o *runtimeObserver) NotificationQueueSize(size int) { o.metrics.Queue.Set(float64(size)) }
