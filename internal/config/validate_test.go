package config

import (
	"strings"
	"testing"
)

func TestValidateReportsRuleAndRuntimeErrors(t *testing.T) {
	cfg := loadValidConfig(t)
	cfg.Rules = append(cfg.Rules, cfg.Rules[0])
	cfg.Rules[0].Match.Levels = []string{"NOTICE"}
	cfg.Rules[0].Match.Pattern = "["
	cfg.Rules[0].Threshold = 0
	cfg.Rules[0].Dedup = DedupConfig{Mode: "fields"}
	cfg.Runtime.LogChannelSize = -1
	cfg.Notifier.QueueSize = -1

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() returned nil error")
	}

	for _, want := range []string{
		"unknown log level",
		"invalid regexp",
		"threshold: must be greater than zero",
		"dedup.fields: must contain at least one value",
		"duplicate rule ID",
		"runtime.log_channel_size: must be greater than zero",
		"notifier.queue_size: must be greater than zero",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() error = %q, want substring %q", err, want)
		}
	}
}

func TestValidateRejectsInvalidDurationsAndChannels(t *testing.T) {
	cfg := loadValidConfig(t)
	cfg.Checkpoint.FlushEvents = -1
	cfg.Rules[0].Cooldown = Duration(-1)
	cfg.Rules[0].Context.After = 1
	cfg.Rules[0].Context.AfterWait = 0
	cfg.Alert.Channels = map[string]NotificationChannelConfig{
		"": {Platform: "email"},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() returned nil error")
	}

	for _, want := range []string{
		"checkpoint.flush_events: must be greater than zero",
		"cooldown: must be greater than or equal to zero",
		"after_wait: must be greater than zero when after is set",
		"channel ID must not be empty",
		"unsupported platform",
		"webhook: must not be empty",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() error = %q, want substring %q", err, want)
		}
	}
}

func TestDurationUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "number", content: "checkpoint:\n  replay_overlap: 2\n", want: "duration must be a string"},
		{name: "invalid", content: "checkpoint:\n  replay_overlap: forever\n", want: "invalid duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, validConfigYAML+tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func loadValidConfig(t *testing.T) Config {
	t.Helper()

	cfg, err := Load(writeConfig(t, validConfigYAML))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	return *cfg
}
