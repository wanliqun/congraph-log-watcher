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
	m.Logs.WithLabelValues("node", "INFO").Inc()
	h := NewHealth([]string{"a", "b"}, time.Minute, time.Minute)
	h.DockerOK(time.Now())
	h.Attached("a", true)
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	Handler(reg, h).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	h.Attached("a", false)
	w = httptest.NewRecorder()
	Handler(reg, h).ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("%d", w.Code)
	}
}
