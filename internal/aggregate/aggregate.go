// Package aggregate turns matching log entries into bounded incident state.
package aggregate

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
	"github.com/wanliqun/congraph-log-watcher/internal/rule"
)

// Clock makes time-dependent eviction deterministic in tests.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Config constrains the in-memory state owned by an Aggregator.
type Config struct {
	MaxGroups  int
	GroupTTL   time.Duration
	MaxSamples int
	Clock      Clock
}

// Event is a matched, redacted log entry with its stable incident identity.
type Event struct {
	Rule        rule.Rule
	Fingerprint string
	Entry       logentry.LogEntry
}

// EventState is an immutable snapshot of one incident group.
type EventState struct {
	Fingerprint     string
	FirstSeen       time.Time
	LastSeen        time.Time
	WindowCount     int
	TotalCount      int
	LastAlertAt     time.Time
	CooldownUntil   time.Time
	SuppressedCount int
	FirstSample     string
	LatestSample    string
	Samples         []string
}

// Result describes the state transition caused by one event. Alert is true
// when the caller should construct and deliver an incident alert.
type Result struct {
	Alert           bool
	Suppressed      bool
	SuppressedCount int
	State           EventState
}

// Aggregator owns all mutable, per-fingerprint incident state.
type Aggregator struct {
	mu         sync.Mutex
	maxGroups  int
	groupTTL   time.Duration
	maxSamples int
	clock      Clock
	groups     map[string]*group
}

type group struct {
	state      EventState
	timestamps []time.Time
	watermark  time.Time
	updatedAt  time.Time
}

// New constructs an Aggregator with bounded memory behavior.
func New(config Config) (*Aggregator, error) {
	if config.MaxGroups <= 0 {
		return nil, fmt.Errorf("max groups must be greater than zero")
	}
	if config.GroupTTL <= 0 {
		return nil, fmt.Errorf("group TTL must be greater than zero")
	}
	if config.MaxSamples <= 0 {
		return nil, fmt.Errorf("max samples must be greater than zero")
	}
	if config.Clock == nil {
		config.Clock = systemClock{}
	}
	return &Aggregator{
		maxGroups:  config.MaxGroups,
		groupTTL:   config.GroupTTL,
		maxSamples: config.MaxSamples,
		clock:      config.Clock,
		groups:     make(map[string]*group),
	}, nil
}

// Record incorporates one event. Window boundaries are evaluated against the
// largest observed event timestamp, so a late record cannot reintroduce data
// already outside the active window.
func (a *Aggregator) Record(event Event) (Result, error) {
	if event.Fingerprint == "" {
		return Result{}, fmt.Errorf("fingerprint must not be empty")
	}
	if event.Rule.Threshold <= 0 || event.Rule.Window <= 0 || event.Rule.Cooldown < 0 {
		return Result{}, fmt.Errorf("rule %q has invalid aggregation settings", event.Rule.ID)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.clock.Now()
	a.evictExpired(now)
	item, exists := a.groups[event.Fingerprint]
	if !exists {
		a.evictForInsert()
		item = &group{state: EventState{Fingerprint: event.Fingerprint}}
		a.groups[event.Fingerprint] = item
	}

	eventTime := event.Entry.Timestamp
	if eventTime.IsZero() {
		eventTime = now
	}
	if item.watermark.Before(eventTime) {
		item.watermark = eventTime
	}
	cutoff := item.watermark.Add(-event.Rule.Window)
	item.timestamps = discardBefore(item.timestamps, cutoff)
	if !eventTime.Before(cutoff) {
		item.timestamps = insertTimestamp(item.timestamps, eventTime)
	}

	state := &item.state
	if state.FirstSeen.IsZero() || eventTime.Before(state.FirstSeen) {
		state.FirstSeen = eventTime
	}
	if state.LastSeen.IsZero() || eventTime.After(state.LastSeen) {
		state.LastSeen = eventTime
	}
	state.TotalCount++
	state.WindowCount = len(item.timestamps)
	addSample(state, sampleOf(event.Entry), a.maxSamples)
	item.updatedAt = now

	result := Result{State: snapshot(*state)}
	if state.LastAlertAt.IsZero() {
		if state.WindowCount >= event.Rule.Threshold {
			a.raise(state, eventTime, event.Rule.Cooldown, &result)
		}
	} else if eventTime.Before(state.CooldownUntil) {
		state.SuppressedCount++
		result.Suppressed = true
	} else if state.WindowCount >= event.Rule.Threshold {
		a.raise(state, eventTime, event.Rule.Cooldown, &result)
	}
	result.State = snapshot(*state)
	return result, nil
}

func (a *Aggregator) raise(state *EventState, at time.Time, cooldown time.Duration, result *Result) {
	result.Alert = true
	result.SuppressedCount = state.SuppressedCount
	state.LastAlertAt = at
	state.CooldownUntil = at.Add(cooldown)
	state.SuppressedCount = 0
}

// Len returns the number of active incident groups.
func (a *Aggregator) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.evictExpired(a.clock.Now())
	return len(a.groups)
}

// State returns a copy of the incident state, if it is still active.
func (a *Aggregator) State(fingerprint string) (EventState, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.evictExpired(a.clock.Now())
	item, ok := a.groups[fingerprint]
	if !ok {
		return EventState{}, false
	}
	return snapshot(item.state), true
}

func (a *Aggregator) evictExpired(now time.Time) {
	for fingerprint, item := range a.groups {
		if !now.Before(item.updatedAt.Add(a.groupTTL)) {
			delete(a.groups, fingerprint)
		}
	}
}

func (a *Aggregator) evictForInsert() {
	if len(a.groups) < a.maxGroups {
		return
	}
	var oldestKey string
	var oldestTime time.Time
	for fingerprint, item := range a.groups {
		if oldestKey == "" || item.updatedAt.Before(oldestTime) || (item.updatedAt.Equal(oldestTime) && fingerprint < oldestKey) {
			oldestKey, oldestTime = fingerprint, item.updatedAt
		}
	}
	delete(a.groups, oldestKey)
}

func discardBefore(values []time.Time, cutoff time.Time) []time.Time {
	index := sort.Search(len(values), func(index int) bool { return !values[index].Before(cutoff) })
	return append(values[:0], values[index:]...)
}

func insertTimestamp(values []time.Time, value time.Time) []time.Time {
	index := sort.Search(len(values), func(index int) bool { return values[index].After(value) })
	values = append(values, time.Time{})
	copy(values[index+1:], values[index:])
	values[index] = value
	return values
}

func sampleOf(entry logentry.LogEntry) string {
	if entry.Raw != "" {
		return entry.Raw
	}
	return entry.Message
}

func addSample(state *EventState, value string, max int) {
	if state.FirstSample == "" {
		state.FirstSample = value
	}
	state.LatestSample = value
	for _, sample := range state.Samples {
		if sample == value {
			return
		}
	}
	if len(state.Samples) < max {
		state.Samples = append(state.Samples, value)
	}
}

func snapshot(state EventState) EventState {
	state.Samples = append([]string(nil), state.Samples...)
	return state
}
