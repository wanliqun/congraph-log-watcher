package rule

import (
	"strings"
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/config"
	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestFieldsFingerprintGroupsStructuredIdentity(t *testing.T) {
	t.Parallel()

	rule := compileRule(t, config.RuleConfig{
		ID:        "indexing-error",
		Severity:  "high",
		Match:     config.MatchConfig{Levels: []string{"ERROR"}},
		Threshold: 1,
		Window:    duration(time.Second),
		Cooldown:  duration(time.Minute),
		Dedup: config.DedupConfig{
			Mode:   "fields",
			Fields: []string{"subgraph_id", "component", "container", "rule"},
		},
	})

	first := logentry.LogEntry{
		ContainerName: "index-node-0",
		Message:       "failed block 262560262",
		Fields: map[string]string{
			"component":    "SubgraphInstanceManager",
			"subgraph_id":  "QmExample",
			"block_number": "262560262",
		},
	}
	second := cloneLogEntry(first)
	second.Message = "failed block 262560263"
	second.Fields["block_number"] = "262560263"
	if firstFingerprint, secondFingerprint := rule.Fingerprint(first), rule.Fingerprint(second); firstFingerprint != secondFingerprint {
		t.Fatalf("field fingerprints differ: %s != %s", firstFingerprint, secondFingerprint)
	}

	second.Fields["subgraph_id"] = "QmOther"
	if firstFingerprint, secondFingerprint := rule.Fingerprint(first), rule.Fingerprint(second); firstFingerprint == secondFingerprint {
		t.Fatal("different subgraph IDs produced the same fingerprint")
	}
}

func TestFingerprintModes(t *testing.T) {
	t.Parallel()

	entry := logentry.LogEntry{
		ContainerName: "index-node-0",
		Raw:           "ERROR failed block 100",
		Message:       "failed block 100",
	}
	changed := entry
	changed.Raw = "ERROR failed block 101"
	changed.Message = "failed block 101"

	exact := fingerprintRule(t, "exact", nil)
	if exact.Fingerprint(entry) == exact.Fingerprint(changed) {
		t.Fatal("exact mode grouped different raw messages")
	}

	normalized := fingerprintRule(t, "normalized", []config.NormalizationRuleConfig{
		{Pattern: `block \d+`, Replace: "block <N>"},
	})
	if normalized.Fingerprint(entry) != normalized.Fingerprint(changed) {
		t.Fatal("normalized mode did not group equivalent messages")
	}

	ruleMode := fingerprintRule(t, "rule", nil)
	if ruleMode.Fingerprint(entry) != ruleMode.Fingerprint(changed) {
		t.Fatal("rule mode did not group messages by rule and container")
	}
	if got := ruleMode.Fingerprint(entry); len(got) != 64 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("fingerprint = %q, want SHA-256 hex", got)
	}
}

func TestCompileRejectsInvalidLocalConfiguration(t *testing.T) {
	t.Parallel()

	input := config.RuleConfig{
		ID:       "bad",
		Severity: "high",
		Match:    config.MatchConfig{Levels: []string{"NOTICE"}},
		Dedup:    config.DedupConfig{Mode: "not-a-mode"},
	}
	if _, err := Compile(input); err == nil {
		t.Fatal("Compile() accepted invalid configuration")
	}
}

func fingerprintRule(t *testing.T, mode string, normalize []config.NormalizationRuleConfig) Rule {
	t.Helper()
	return compileRule(t, config.RuleConfig{
		ID:        "generic-error",
		Severity:  "high",
		Match:     config.MatchConfig{Levels: []string{"ERROR"}},
		Threshold: 1,
		Window:    duration(time.Second),
		Cooldown:  duration(time.Minute),
		Dedup: config.DedupConfig{
			Mode:      mode,
			Normalize: normalize,
		},
	})
}
