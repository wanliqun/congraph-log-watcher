package aggregate

import (
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
	"github.com/wanliqun/congraph-log-watcher/internal/rule"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestThresholdWindowAndExactBoundary(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	aggregator := newAggregator(t, clock, 10, time.Hour, 3)
	r := testRule(3, time.Minute, 10*time.Minute)

	for _, offset := range []time.Duration{0, 30 * time.Second} {
		result := record(t, aggregator, r, "a", base.Add(offset), "same")
		if result.Alert {
			t.Fatal("alert before threshold")
		}
	}
	result := record(t, aggregator, r, "a", base.Add(time.Minute), "same")
	if !result.Alert || result.State.WindowCount != 3 {
		t.Fatalf("result = %+v, want alert with 3 events", result)
	}
	result = record(t, aggregator, r, "a", base.Add(time.Minute+time.Nanosecond), "same")
	if result.State.WindowCount != 3 || result.Alert {
		t.Fatalf("result = %+v, want oldest event evicted after boundary", result)
	}
}

func TestCooldownSuppressionAndRealert(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	aggregator := newAggregator(t, clock, 10, time.Hour, 3)
	r := testRule(2, time.Hour, time.Minute)
	record(t, aggregator, r, "a", base, "one")
	first := record(t, aggregator, r, "a", base.Add(time.Second), "two")
	if !first.Alert || first.SuppressedCount != 0 {
		t.Fatalf("first = %+v", first)
	}
	suppressed := record(t, aggregator, r, "a", base.Add(2*time.Second), "three")
	if suppressed.Alert || suppressed.State.SuppressedCount != 1 {
		t.Fatalf("suppressed = %+v", suppressed)
	}
	second := record(t, aggregator, r, "a", base.Add(time.Minute+time.Second), "four")
	if !second.Alert || second.SuppressedCount != 1 || second.State.SuppressedCount != 0 {
		t.Fatalf("second = %+v", second)
	}
}

func TestSamplesAreDistinctAndBounded(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	aggregator := newAggregator(t, clock, 10, time.Hour, 2)
	r := testRule(10, time.Hour, time.Hour)
	for index, sample := range []string{"one", "one", "two", "three"} {
		record(t, aggregator, r, "a", base.Add(time.Duration(index)*time.Second), sample)
	}
	state, ok := aggregator.State("a")
	if !ok || state.FirstSample != "one" || state.LatestSample != "three" || len(state.Samples) != 2 || state.Samples[1] != "two" {
		t.Fatalf("state = %+v", state)
	}
}

func TestTTLAndCapacityEviction(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	aggregator := newAggregator(t, clock, 2, time.Minute, 3)
	r := testRule(2, time.Hour, time.Hour)
	record(t, aggregator, r, "b", base, "b")
	clock.now = base.Add(time.Second)
	record(t, aggregator, r, "a", base, "a")
	clock.now = base.Add(2 * time.Second)
	record(t, aggregator, r, "c", base, "c")
	if _, ok := aggregator.State("b"); ok {
		t.Fatal("oldest group was not evicted at capacity")
	}
	clock.now = base.Add(time.Minute + 2*time.Second)
	if got := aggregator.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0 after TTL", got)
	}
}

func TestOutOfOrderEventCannotReopenWindow(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	aggregator := newAggregator(t, clock, 10, time.Hour, 3)
	r := testRule(2, time.Minute, time.Hour)
	record(t, aggregator, r, "a", base, "old")
	record(t, aggregator, r, "a", base.Add(2*time.Minute), "new")
	late := record(t, aggregator, r, "a", base.Add(30*time.Second), "late")
	if late.State.WindowCount != 1 || late.Alert {
		t.Fatalf("late = %+v, want no re-opened window", late)
	}
}

func TestInvalidConfigAndEvent(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New accepted invalid config")
	}
	clock := &fakeClock{now: time.Now()}
	aggregator := newAggregator(t, clock, 1, time.Hour, 1)
	if _, err := aggregator.Record(Event{}); err == nil {
		t.Fatal("Record accepted empty fingerprint")
	}
}

func newAggregator(t *testing.T, clock Clock, groups int, ttl time.Duration, samples int) *Aggregator {
	t.Helper()
	aggregator, err := New(Config{MaxGroups: groups, GroupTTL: ttl, MaxSamples: samples, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	return aggregator
}

func record(t *testing.T, aggregator *Aggregator, r rule.Rule, fingerprint string, at time.Time, sample string) Result {
	t.Helper()
	result, err := aggregator.Record(Event{Rule: r, Fingerprint: fingerprint, Entry: logentry.LogEntry{Timestamp: at, Raw: sample}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func testRule(threshold int, window, cooldown time.Duration) rule.Rule {
	return rule.Rule{ID: "test", Threshold: threshold, Window: window, Cooldown: cooldown}
}
