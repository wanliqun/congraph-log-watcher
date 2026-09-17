package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAppliesDefaultsAndExpandsEnvironment(t *testing.T) {
	webhook := "https://example.test/webhook"
	t.Setenv("DINGTALK_WEBHOOK", webhook)

	path := writeConfig(t, `
containers: [index-node-0]
rules:
  - id: critical-log
    severity: critical
    match:
      levels: [CRIT]
    threshold: 1
    window: 1s
    cooldown: 10m
    dedup:
      mode: rule
alert:
  channels:
    dingrobot:
      platform: dingtalk
      webhook: ${DINGTALK_WEBHOOK}
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Docker.Socket != defaultDockerSocket {
		t.Fatalf("Docker.Socket = %q, want %q", cfg.Docker.Socket, defaultDockerSocket)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, defaultLogLevel)
	}
	if cfg.Checkpoint.ReplayOverlap.Duration() != 2*time.Second {
		t.Fatalf("ReplayOverlap = %s", cfg.Checkpoint.ReplayOverlap)
	}
	if cfg.Checkpoint.InitialReplayWindow.Duration() != 5*time.Minute {
		t.Fatalf("InitialReplayWindow = %s", cfg.Checkpoint.InitialReplayWindow)
	}
	if cfg.Aggregation.MaxGroups != defaultMaxGroups {
		t.Fatalf("MaxGroups = %d, want %d", cfg.Aggregation.MaxGroups, defaultMaxGroups)
	}
	if cfg.Notifier.QueueSize != defaultNotificationQueueSize {
		t.Fatalf("QueueSize = %d, want %d", cfg.Notifier.QueueSize, defaultNotificationQueueSize)
	}
	if cfg.Alert.Channels["dingrobot"].Webhook != webhook {
		t.Fatalf("Webhook = %q, want %q", cfg.Alert.Channels["dingrobot"].Webhook, webhook)
	}
	if !cfg.Rules[0].IsEnabled() {
		t.Fatal("rule should default to enabled")
	}
	if cfg.Rules[0].Dedup.Mode != "rule" {
		t.Fatalf("Dedup.Mode = %q", cfg.Rules[0].Dedup.Mode)
	}
}

func TestLoadRejectsMissingEnvironmentVariableWithoutLeakingValue(t *testing.T) {
	path := writeConfig(t, strings.Replace(validConfigYAML, "https://example.test/webhook", "${MISSING_WEBHOOK}", 1))

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() returned nil error")
	}
	if !strings.Contains(err.Error(), `environment variable "MISSING_WEBHOOK" is not set`) {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRejectsUnknownAndMultipleYAMLDocuments(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "unknown field",
			content: validConfigYAML + "unknown: value\n",
			want:    "field unknown not found",
		},
		{
			name:    "multiple documents",
			content: validConfigYAML + "---\ncontainers: [query-node-0]\n",
			want:    "multiple YAML documents are not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsExplicitZeroInsteadOfReplacingItWithADefault(t *testing.T) {
	path := writeConfig(t, validConfigYAML+`
runtime:
  log_channel_size: 0
notifier:
  queue_size: 0
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() returned nil error")
	}
	for _, want := range []string{
		"runtime.log_channel_size: must be greater than zero",
		"notifier.queue_size: must be greater than zero",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error = %q, want substring %q", err, want)
		}
	}
}

func TestLoadAcceptsLogLevelAndRejectsUnsupportedLevel(t *testing.T) {
	cfg, err := Load(writeConfig(t, validConfigYAML+"log_level: DEBUG\n"))
	if err != nil || cfg.LogLevel != "DEBUG" {
		t.Fatalf("Load debug log level = %#v, %v", cfg, err)
	}
	_, err = Load(writeConfig(t, validConfigYAML+"log_level: verbose\n"))
	if err == nil || !strings.Contains(err.Error(), `log_level: unsupported level "verbose"`) {
		t.Fatalf("Load unsupported log level error = %v", err)
	}
}

func TestLoadAcceptsInitialReplayWindow(t *testing.T) {
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{value: "0s", want: 0},
		{value: "30m", want: 30 * time.Minute},
	} {
		cfg, err := Load(writeConfig(t, validConfigYAML+"checkpoint:\n  initial_replay_window: "+test.value+"\n"))
		if err != nil {
			t.Fatalf("Load initial_replay_window %q: %v", test.value, err)
		}
		if got := cfg.Checkpoint.InitialReplayWindow.Duration(); got != test.want {
			t.Fatalf("InitialReplayWindow = %s, want %s", got, test.want)
		}
	}
	_, err := Load(writeConfig(t, validConfigYAML+"checkpoint:\n  initial_replay_window: -1s\n"))
	if err == nil || !strings.Contains(err.Error(), "checkpoint.initial_replay_window: must be greater than or equal to zero") {
		t.Fatalf("Load negative initial_replay_window error = %v", err)
	}
}

func TestExpandEnvironment(t *testing.T) {
	lookup := func(name string) (string, bool) {
		values := map[string]string{"ONE": "first", "TWO": "second"}
		value, ok := values[name]
		return value, ok
	}

	got, err := expandEnvironment("${ONE}/${TWO}", lookup)
	if err != nil {
		t.Fatalf("expandEnvironment() returned error: %v", err)
	}
	if got != "first/second" {
		t.Fatalf("expandEnvironment() = %q", got)
	}

	if _, err := expandEnvironment("${MISSING}", lookup); err == nil {
		t.Fatal("expandEnvironment() accepted missing variable")
	}
	if _, err := expandEnvironment("${not-valid}", lookup); err == nil {
		t.Fatal("expandEnvironment() accepted invalid placeholder")
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	t.Setenv("DINGTALK_WEBHOOK", "https://example.test/webhook")
	t.Setenv("DINGTALK_SECRET", "test-secret")
	if _, err := Load(filepath.Join("..", "..", "config", "example.yaml")); err != nil {
		t.Fatalf("example config: %v", err)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const validConfigYAML = `
containers:
  - index-node-0
rules:
  - id: critical-log
    severity: critical
    match:
      levels: [CRITICAL]
    threshold: 1
    window: 1s
    cooldown: 10m
    dedup:
      mode: rule
alert:
  channels:
    dingrobot:
      platform: dingtalk
      webhook: https://example.test/webhook
`
