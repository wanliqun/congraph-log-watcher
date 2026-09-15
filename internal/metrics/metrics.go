// Package metrics exposes bounded-cardinality Prometheus metrics and health.
package metrics

import (
	"encoding/json"
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
	m := &Metrics{Logs: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_logs_received_total"}, []string{"container", "level"}), ParseErrors: prometheus.NewCounter(prometheus.CounterOpts{Name: "graph_log_watcher_parse_errors_total"}), RuleMatches: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_rule_matches_total"}, []string{"rule"}), Alerts: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_alerts_sent_total"}, []string{"rule", "severity"}), Suppressed: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_alerts_suppressed_total"}, []string{"rule"}), NotifyErrors: prometheus.NewCounter(prometheus.CounterOpts{Name: "graph_log_watcher_notifier_errors_total"}), Reconnects: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "graph_log_watcher_docker_reconnect_total"}, []string{"container"}), Checkpoint: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "graph_log_watcher_checkpoint_timestamp"}, []string{"container"}), Groups: prometheus.NewGauge(prometheus.GaugeOpts{Name: "graph_log_watcher_active_groups"}), Queue: prometheus.NewGauge(prometheus.GaugeOpts{Name: "graph_log_watcher_notifier_queue_size"})}
	reg.MustRegister(m.Logs, m.ParseErrors, m.RuleMatches, m.Alerts, m.Suppressed, m.NotifyErrors, m.Reconnects, m.Checkpoint, m.Groups, m.Queue)
	return m
}

type Health struct {
	mu                         sync.RWMutex
	docker                     time.Time
	attached                   map[string]bool
	dockerAfter, detachedAfter time.Duration
}

func NewHealth(containers []string, dockerAfter, detachedAfter time.Duration) *Health {
	a := map[string]bool{}
	for _, c := range containers {
		a[c] = false
	}
	return &Health{attached: a, dockerAfter: dockerAfter, detachedAfter: detachedAfter}
}
func (h *Health) DockerOK(now time.Time)     { h.mu.Lock(); h.docker = now; h.mu.Unlock() }
func (h *Health) Attached(n string, ok bool) { h.mu.Lock(); h.attached[n] = ok; h.mu.Unlock() }
func (h *Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	now := time.Now()
	bad := []string{}
	if !h.docker.IsZero() && now.Sub(h.docker) > h.dockerAfter {
		bad = append(bad, "docker")
	}
	all := len(h.attached) > 0
	for _, ok := range h.attached {
		all = all && !ok
	}
	if all {
		bad = append(bad, "containers")
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
