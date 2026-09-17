package metrics

import (
	"encoding/json"
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

func TestHealthReportsStaleDockerAndRecoversAfterProbe(t *testing.T) {
	h := NewHealth([]string{"node"}, time.Minute, time.Hour)
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	h.Attached("node", true)
	h.DockerOK(now)

	now = now.Add(2 * time.Minute)
	response := healthResponse(h)
	if response.Code != http.StatusServiceUnavailable || !containsFailedComponent(response.Body.String(), "docker") {
		t.Fatalf("stale Docker health = %d %s", response.Code, response.Body.String())
	}

	h.DockerOK(now)
	response = healthResponse(h)
	if response.Code != http.StatusOK {
		t.Fatalf("health after Docker probe = %d %s", response.Code, response.Body.String())
	}
}

func healthResponse(h *Health) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	return response
}

func containsFailedComponent(body, want string) bool {
	var response struct {
		Failed []string `json:"failed_components"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return false
	}
	for _, component := range response.Failed {
		if component == want {
			return true
		}
	}
	return false
}
