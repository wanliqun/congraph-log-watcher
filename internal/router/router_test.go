package router

import (
	"testing"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestDefaultRouter(t *testing.T) {
	t.Parallel()

	router := Default()
	tests := []struct {
		level logentry.LogLevel
		want  Route
	}{
		{level: logentry.LevelTrace, want: RouteDiscard},
		{level: logentry.LevelDebug, want: RouteDiscard},
		{level: logentry.LevelUnknown, want: RouteDiscard},
		{level: logentry.LevelInfo, want: RouteContextOnly},
		{level: logentry.LevelWarn, want: RouteAlertCandidate},
		{level: logentry.LevelError, want: RouteAlertCandidate},
		{level: logentry.LevelCritical, want: RouteAlertCandidate},
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			t.Parallel()

			got := router.Route(logentry.LogEntry{Level: tt.level})
			if got != tt.want {
				t.Fatalf("Route(%s) = %v, want %v", tt.level, got, tt.want)
			}
			if got.KeepsContext() != (tt.want != RouteDiscard) {
				t.Fatalf("KeepsContext() = %t", got.KeepsContext())
			}
			if got.EntersRuleEngine() != (tt.want == RouteAlertCandidate) {
				t.Fatalf("EntersRuleEngine() = %t", got.EntersRuleEngine())
			}
		})
	}
}

func TestRouterSupportsConfiguredDebugAndUnknown(t *testing.T) {
	t.Parallel()

	router, err := NewFromNames(
		[]string{"DEBUG", "UNKNOWN"},
		[]string{"UNKNOWN"},
	)
	if err != nil {
		t.Fatalf("NewFromNames() returned error: %v", err)
	}

	if got := router.Route(logentry.LogEntry{Level: logentry.LevelDebug}); got != RouteContextOnly {
		t.Fatalf("DEBUG route = %v, want context only", got)
	}
	if got := router.Route(logentry.LogEntry{Level: logentry.LevelUnknown}); got != RouteAlertCandidate {
		t.Fatalf("UNKNOWN route = %v, want alert candidate", got)
	}
}

func TestNewFromNamesRejectsUnknownLevel(t *testing.T) {
	t.Parallel()

	if _, err := NewFromNames([]string{"NOTICE"}, nil); err == nil {
		t.Fatal("NewFromNames() accepted an unknown level")
	}
}
