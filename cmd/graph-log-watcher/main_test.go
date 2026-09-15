package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCheckConfig(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(validConfigYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"check-config", "--config", configPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "configuration is valid\n" {
		t.Fatalf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCheckConfigRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"check-config", "--config", configPath}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "invalid configuration:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunReportsCommandUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing command", want: "usage:"},
		{name: "unknown command", args: []string{"unknown"}, want: "unknown command"},
		{name: "missing config", args: []string{"check-config"}, want: "--config is required"},
		{name: "run requires config", args: []string{"run"}, want: "--config is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			if exitCode := run(tt.args, &stdout, &stderr); exitCode != 2 {
				t.Fatalf("run() exit code = %d, want 2", exitCode)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), tt.want)
			}
		})
	}
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
