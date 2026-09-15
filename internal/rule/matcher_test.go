package rule

import (
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/config"
	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestRuleMatchesEveryConfiguredDimension(t *testing.T) {
	t.Parallel()

	rule := compileRule(t, config.RuleConfig{
		ID:         "rpc-error",
		Severity:   "high",
		Containers: []string{"index-node-0"},
		Match: config.MatchConfig{
			Levels:      []string{"ERROR"},
			Components:  []string{"RpcAdapter"},
			FieldEquals: map[string]string{"network": "mainnet"},
			Pattern:     `(?i)timeout`,
			Excludes:    []string{`(?i)expected`},
		},
		Threshold: 5,
		Window:    duration(time.Minute),
		Cooldown:  duration(time.Minute),
		Dedup:     config.DedupConfig{Mode: "rule"},
	})

	matching := logentry.LogEntry{
		ContainerName: "index-node-0",
		Level:         logentry.LevelError,
		Message:       "RPC request timeout",
		Fields: map[string]string{
			"component": "RpcAdapter",
			"network":   "mainnet",
		},
	}
	if !rule.Matches(matching) {
		t.Fatal("matching entry did not match")
	}

	tests := []struct {
		name   string
		mutate func(*logentry.LogEntry)
	}{
		{name: "container", mutate: func(entry *logentry.LogEntry) { entry.ContainerName = "query-node-0" }},
		{name: "level", mutate: func(entry *logentry.LogEntry) { entry.Level = logentry.LevelWarn }},
		{name: "component", mutate: func(entry *logentry.LogEntry) { entry.Fields["component"] = "Store" }},
		{name: "field", mutate: func(entry *logentry.LogEntry) { entry.Fields["network"] = "testnet" }},
		{name: "pattern", mutate: func(entry *logentry.LogEntry) { entry.Message = "RPC request refused" }},
		{name: "exclude", mutate: func(entry *logentry.LogEntry) { entry.Message = "expected timeout" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := cloneLogEntry(matching)
			tt.mutate(&entry)
			if rule.Matches(entry) {
				t.Fatalf("entry unexpectedly matched: %#v", entry)
			}
		})
	}
}

func TestEngineHonorsRuleOrderAndStopOnMatch(t *testing.T) {
	t.Parallel()

	specific := compileRule(t, config.RuleConfig{
		ID:       "specific-rpc",
		Severity: "high",
		Match: config.MatchConfig{
			Levels:  []string{"ERROR"},
			Pattern: `(?i)rpc`,
		},
		Threshold:   1,
		Window:      duration(time.Second),
		Cooldown:    duration(time.Minute),
		Dedup:       config.DedupConfig{Mode: "rule"},
		StopOnMatch: true,
	})
	generic := compileRule(t, config.RuleConfig{
		ID:        "generic-error",
		Severity:  "high",
		Match:     config.MatchConfig{Levels: []string{"ERROR"}},
		Threshold: 1,
		Window:    duration(time.Second),
		Cooldown:  duration(time.Minute),
		Dedup:     config.DedupConfig{Mode: "rule"},
	})

	matches := NewEngine([]Rule{specific, generic}).Match(logentry.LogEntry{
		Level: logentry.LevelError, Message: "RPC request failed",
	})
	if len(matches) != 1 || matches[0].ID != "specific-rpc" {
		t.Fatalf("matches = %#v", matches)
	}

	specific.StopOnMatch = false
	matches = NewEngine([]Rule{specific, generic}).Match(logentry.LogEntry{
		Level: logentry.LevelError, Message: "RPC request failed",
	})
	if len(matches) != 2 || matches[0].ID != "specific-rpc" || matches[1].ID != "generic-error" {
		t.Fatalf("matches = %#v", matches)
	}
}

func TestDisabledRuleDoesNotMatch(t *testing.T) {
	t.Parallel()

	disabled := false
	rule := compileRule(t, config.RuleConfig{
		ID:        "disabled",
		Enabled:   &disabled,
		Severity:  "high",
		Match:     config.MatchConfig{Levels: []string{"ERROR"}},
		Threshold: 1,
		Window:    duration(time.Second),
		Cooldown:  duration(time.Second),
		Dedup:     config.DedupConfig{Mode: "rule"},
	})
	if rule.Matches(logentry.LogEntry{Level: logentry.LevelError}) {
		t.Fatal("disabled rule matched")
	}
}

func compileRule(t *testing.T, input config.RuleConfig) Rule {
	t.Helper()

	rule, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile(%q) returned error: %v", input.ID, err)
	}
	return rule
}

func duration(value time.Duration) config.Duration {
	return config.Duration(value)
}

func cloneLogEntry(entry logentry.LogEntry) logentry.LogEntry {
	cloned := entry
	cloned.Fields = make(map[string]string, len(entry.Fields))
	for key, value := range entry.Fields {
		cloned.Fields[key] = value
	}
	return cloned
}
