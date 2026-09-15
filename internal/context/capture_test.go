package contextbuf

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestAfterCollectorCompletesWithRequestedEntries(t *testing.T) {
	t.Parallel()

	collector := NewAfterCollector(
		[]logentry.LogEntry{{Raw: "before-1"}, {Raw: "before-2"}},
		logentry.LogEntry{Raw: "trigger"},
		CaptureConfig{After: 2, AfterWait: time.Second},
	)
	collector.Add(logentry.LogEntry{Raw: "after-1"})
	collector.Add(logentry.LogEntry{Raw: "after-2"})

	result := <-collector.Done()
	if result.TimedOut {
		t.Fatal("capture unexpectedly timed out")
	}
	if got := result.Lines(1024); !equalStrings(got, []string{"before-1", "before-2", "trigger", "after-1", "after-2"}) {
		t.Fatalf("Lines() = %#v", got)
	}
}

func TestAfterCollectorTimesOut(t *testing.T) {
	t.Parallel()

	collector := NewAfterCollector(
		nil,
		logentry.LogEntry{Raw: "trigger"},
		CaptureConfig{After: 2, AfterWait: 10 * time.Millisecond},
	)
	collector.Add(logentry.LogEntry{Raw: "after-1"})

	select {
	case result := <-collector.Done():
		if !result.TimedOut || len(result.After) != 1 {
			t.Fatalf("result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("collector did not time out")
	}
}

func TestCaptureLinesPreservesTriggerWithinByteLimit(t *testing.T) {
	t.Parallel()

	capture := Capture{
		Before:  []logentry.LogEntry{{Raw: "far-away"}, {Raw: "nearby"}},
		Trigger: logentry.LogEntry{Raw: "trigger"},
		After:   []logentry.LogEntry{{Raw: "after"}},
	}

	lines := capture.Lines(len("nearby") + 1 + len("trigger"))
	if !equalStrings(lines, []string{"nearby", "trigger"}) {
		t.Fatalf("Lines() = %#v", lines)
	}

	oversized := Capture{Trigger: logentry.LogEntry{Raw: "错误-trigger"}}.Lines(5)
	if len(oversized) != 1 || oversized[0] == "" || !utf8.ValidString(oversized[0]) {
		t.Fatalf("truncated lines = %#v", oversized)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
