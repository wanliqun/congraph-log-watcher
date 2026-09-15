package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthAndMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg)
	_ = New(reg)
	m.Logs.WithLabelValues("node", "INFO").Inc()
	h := NewHealth([]string{"a", "b"}, time.Minute, time.Minute)
	now := time.Now()
	h.now = func() time.Time { return now }
	h.DockerOK(now)
	h.Attached("a", true)
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	Handler(reg, h).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	h.Attached("a", false)
	now = now.Add(2 * time.Minute)
	w = httptest.NewRecorder()
	Handler(reg, h).ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("%d", w.Code)
	}
	h.Attached("a", true)
	h.DockerOK(now)
	w = httptest.NewRecorder()
	Handler(reg, h).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("partial recovery status = %d", w.Code)
	}
	h.CheckpointOK(false)
	w = httptest.NewRecorder()
	Handler(reg, h).ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("checkpoint status = %d", w.Code)
	}
}
