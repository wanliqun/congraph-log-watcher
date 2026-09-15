// Package metrics exposes bounded-cardinality Prometheus metrics and health.
package metrics

import (
	"encoding/json"
	"errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"sync"
	"time"
)

type Metrics struct {
	Logs         *prometheus.CounterVec
	ParseErrors  prometheus.Counter
	RuleMatches  *prometheus.CounterVec
	Alerts       *prometheus.CounterVec
	Suppressed   *prometheus.CounterVec
	NotifyErrors prometheus.Counter
	Reconnects   *prometheus.CounterVec
	Checkpoint   *prometheus.GaugeVec
	Groups       prometheus.Gauge
	Queue        prometheus.Gauge
}

func New(reg prometheus.Registerer) *Metrics {
	return &Metrics{
		Logs:         register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_logs_received_total"}, []string{"container", "level"})).(*prometheus.CounterVec),
		ParseErrors:  register(reg, prometheus.NewCounter(prometheus.CounterOpts{Name: "graph_log_watcher_parse_errors_total"})).(prometheus.Counter),
		RuleMatches:  register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_rule_matches_total"}, []string{"rule"})).(*prometheus.CounterVec),
		Alerts:       register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_alerts_sent_total"}, []string{"rule", "severity"})).(*prometheus.CounterVec),
		Suppressed:   register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_alerts_suppressed_total"}, []string{"rule"})).(*prometheus.CounterVec),
		NotifyErrors: register(reg, prometheus.NewCounter(prometheus.CounterOpts{Name: "graph_log_watcher_notifier_errors_total"})).(prometheus.Counter),
		Reconnects:   register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_docker_reconnect_total"}, []string{"container"})).(*prometheus.CounterVec),
		Checkpoint:   register(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "graph_log_watcher_checkpoint_timestamp"}, []string{"container"})).(*prometheus.GaugeVec),
		Groups:       register(reg, prometheus.NewGauge(prometheus.GaugeOpts{Name: "graph_log_watcher_active_groups"})).(prometheus.Gauge),
		Queue:        register(reg, prometheus.NewGauge(prometheus.GaugeOpts{Name: "graph_log_watcher_notifier_queue_size"})).(prometheus.Gauge),
	}
}

func register(reg prometheus.Registerer, collector prometheus.Collector) prometheus.Collector {
	if err := reg.Register(collector); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			return already.ExistingCollector
		}
		panic(err)
	}
	return collector
}

type Health struct {
	mu                         sync.RWMutex
	lastDockerOK               time.Time
	attached                   map[string]bool
	detachedSince              map[string]time.Time
	checkpointErr              bool
	dockerAfter, detachedAfter time.Duration
	now                        func() time.Time
}

func NewHealth(containers []string, dockerAfter, detachedAfter time.Duration) *Health {
	now := time.Now()
	a := map[string]bool{}
	d := map[string]time.Time{}
	for _, c := range containers {
		a[c] = false
		d[c] = now
	}
	return &Health{lastDockerOK: now, attached: a, detachedSince: d, dockerAfter: dockerAfter, detachedAfter: detachedAfter, now: time.Now}
}
func (h *Health) DockerOK(now time.Time) { h.mu.Lock(); h.lastDockerOK = now; h.mu.Unlock() }
func (h *Health) Attached(n string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ok {
		h.attached[n] = true
		delete(h.detachedSince, n)
		return
	}
	if h.attached[n] || h.detachedSince[n].IsZero() {
		h.detachedSince[n] = h.now()
	}
	h.attached[n] = false
}
func (h *Health) CheckpointOK(ok bool) { h.mu.Lock(); h.checkpointErr = !ok; h.mu.Unlock() }
func (h *Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	now := h.now()
	bad := []string{}
	if now.Sub(h.lastDockerOK) > h.dockerAfter {
		bad = append(bad, "docker")
	}
	allDetachedLong := len(h.attached) > 0
	for name, ok := range h.attached {
		since := h.detachedSince[name]
		allDetachedLong = allDetachedLong && !ok && !since.IsZero() && now.Sub(since) > h.detachedAfter
	}
	if allDetachedLong {
		bad = append(bad, "containers")
	}
	if h.checkpointErr {
		bad = append(bad, "checkpoint")
	}
	w.Header().Set("Content-Type", "application/json")
	if len(bad) > 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": map[bool]string{true: "ok", false: "unhealthy"}[len(bad) == 0], "failed_components": bad})
}
func Handler(reg *prometheus.Registry, h *Health) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.Handle("/healthz", h)
	return mux
}
