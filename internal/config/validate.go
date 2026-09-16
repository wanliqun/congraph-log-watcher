package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

var allowedSeverities = map[string]struct{}{
	"low":      {},
	"medium":   {},
	"high":     {},
	"critical": {},
}

var allowedDedupModes = map[string]struct{}{
	"exact":      {},
	"normalized": {},
	"fields":     {},
	"rule":       {},
}

var allowedLogLevels = map[string]struct{}{
	"debug": {},
	"info":  {},
	"warn":  {},
	"error": {},
}

// Validate checks semantic constraints after defaults have been applied.
func (c Config) Validate() error {
	var validationErrors []error
	add := func(format string, args ...any) {
		validationErrors = append(validationErrors, fmt.Errorf(format, args...))
	}

	if _, ok := allowedLogLevels[strings.ToLower(strings.TrimSpace(c.LogLevel))]; !ok {
		add("log_level: unsupported level %q", c.LogLevel)
	}
	if strings.TrimSpace(c.Docker.Socket) == "" {
		add("docker.socket: must not be empty")
	}
	if c.Parser.Type != "graph-node" {
		add("parser.type: unsupported parser %q", c.Parser.Type)
	}

	validateNames(add, "containers", c.Containers, true)
	validateLevels(add, "levels.context", c.Levels.Context, true)
	validateLevels(add, "levels.alert_candidates", c.Levels.AlertCandidates, true)

	if strings.TrimSpace(c.Checkpoint.Path) == "" {
		add("checkpoint.path: must not be empty")
	}
	if c.Checkpoint.ReplayOverlap.Duration() < 0 {
		add("checkpoint.replay_overlap: must be greater than or equal to zero")
	}
	if c.Checkpoint.FlushInterval.Duration() <= 0 {
		add("checkpoint.flush_interval: must be greater than zero")
	}
	if c.Checkpoint.FlushEvents <= 0 {
		add("checkpoint.flush_events: must be greater than zero")
	}
	if c.Aggregation.MaxGroups <= 0 {
		add("aggregation.max_groups: must be greater than zero")
	}
	if c.Aggregation.GroupTTL.Duration() <= 0 {
		add("aggregation.group_ttl: must be greater than zero")
	}
	if c.Aggregation.MaxSamples <= 0 {
		add("aggregation.max_samples: must be greater than zero")
	}
	if c.Context.BufferLines <= 0 {
		add("context.buffer_lines: must be greater than zero")
	}
	if c.Notifier.QueueSize <= 0 {
		add("notifier.queue_size: must be greater than zero")
	}
	if c.Notifier.MaxAlertsPerMinute <= 0 {
		add("notifier.max_alerts_per_minute: must be greater than zero")
	}
	if c.Notifier.StormCooldown.Duration() < 0 {
		add("notifier.storm_cooldown: must be greater than or equal to zero")
	}
	if strings.TrimSpace(c.Metrics.Listen) == "" {
		add("metrics.listen: must not be empty")
	}
	if c.Health.DockerUnavailableAfter.Duration() <= 0 {
		add("health.docker_unavailable_after: must be greater than zero")
	}
	if c.Health.AllContainersDetachedAfter.Duration() <= 0 {
		add("health.all_containers_detached_after: must be greater than zero")
	}
	if c.Runtime.LogChannelSize <= 0 {
		add("runtime.log_channel_size: must be greater than zero")
	}
	if c.Shutdown.Timeout.Duration() <= 0 {
		add("shutdown.timeout: must be greater than zero")
	}

	validateRegexRules(add, "redaction.rules", c.Redaction.Rules)
	validateRules(add, c.Rules)
	validateChannels(add, c.Alert.Channels)

	return errors.Join(validationErrors...)
}

func validateNames(add func(string, ...any), path string, values []string, required bool) {
	if required && len(values) == 0 {
		add("%s: must contain at least one value", path)
		return
	}

	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			add("%s[%d]: must not be empty", path, index)
			continue
		}
		if _, ok := seen[trimmed]; ok {
			add("%s[%d]: duplicate value %q", path, index, trimmed)
			continue
		}
		seen[trimmed] = struct{}{}
	}
}

func validateLevels(add func(string, ...any), path string, values []string, required bool) {
	if required && len(values) == 0 {
		add("%s: must contain at least one level", path)
		return
	}

	seen := make(map[logentry.LogLevel]struct{}, len(values))
	for index, value := range values {
		level, err := logentry.ParseLogLevel(value)
		if err != nil {
			add("%s[%d]: %v", path, index, err)
			continue
		}
		if _, ok := seen[level]; ok {
			add("%s[%d]: duplicate level %q", path, index, level)
			continue
		}
		seen[level] = struct{}{}
	}
}

func validateRegexRules(add func(string, ...any), path string, rules []RedactionRuleConfig) {
	for index, rule := range rules {
		if strings.TrimSpace(rule.Pattern) == "" {
			add("%s[%d].pattern: must not be empty", path, index)
			continue
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			add("%s[%d].pattern: invalid regexp: %v", path, index, err)
		}
	}
}

func validateRules(add func(string, ...any), rules []RuleConfig) {
	if len(rules) == 0 {
		add("rules: must contain at least one rule")
		return
	}

	ids := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		path := fmt.Sprintf("rules[%d]", index)
		id := strings.TrimSpace(rule.ID)
		if id == "" {
			add("%s.id: must not be empty", path)
		} else if _, ok := ids[id]; ok {
			add("%s.id: duplicate rule ID %q", path, id)
		} else {
			ids[id] = struct{}{}
		}

		severity := strings.ToLower(strings.TrimSpace(rule.Severity))
		if _, ok := allowedSeverities[severity]; !ok {
			add("%s.severity: unsupported severity %q", path, rule.Severity)
		}

		validateNames(add, path+".containers", rule.Containers, false)
		validateLevels(add, path+".match.levels", rule.Match.Levels, true)
		validatePatterns(add, path+".match.pattern", rule.Match.Pattern)
		for excludeIndex, pattern := range rule.Match.Excludes {
			validatePatterns(add, fmt.Sprintf("%s.match.excludes[%d]", path, excludeIndex), pattern)
		}
		for field, value := range rule.Match.FieldEquals {
			if strings.TrimSpace(field) == "" {
				add("%s.match.field_equals: field name must not be empty", path)
			}
			if strings.TrimSpace(value) == "" {
				add("%s.match.field_equals[%q]: value must not be empty", path, field)
			}
		}

		if rule.Threshold <= 0 {
			add("%s.threshold: must be greater than zero", path)
		}
		if rule.Window.Duration() <= 0 {
			add("%s.window: must be greater than zero", path)
		}
		if rule.Cooldown.Duration() < 0 {
			add("%s.cooldown: must be greater than or equal to zero", path)
		}
		validateDedup(add, path+".dedup", rule.Dedup)
		if rule.Context.Before < 0 {
			add("%s.context.before: must be greater than or equal to zero", path)
		}
		if rule.Context.After < 0 {
			add("%s.context.after: must be greater than or equal to zero", path)
		}
		if rule.Context.AfterWait.Duration() < 0 {
			add("%s.context.after_wait: must be greater than or equal to zero", path)
		}
		if rule.Context.After > 0 && rule.Context.AfterWait.Duration() <= 0 {
			add("%s.context.after_wait: must be greater than zero when after is set", path)
		}
		if rule.Context.MaxBytes <= 0 {
			add("%s.context.max_bytes: must be greater than zero", path)
		}
	}
}

func validatePatterns(add func(string, ...any), path, pattern string) {
	if pattern == "" {
		return
	}
	if _, err := regexp.Compile(pattern); err != nil {
		add("%s: invalid regexp: %v", path, err)
	}
}

func validateDedup(add func(string, ...any), path string, dedup DedupConfig) {
	mode := strings.ToLower(strings.TrimSpace(dedup.Mode))
	if _, ok := allowedDedupModes[mode]; !ok {
		add("%s.mode: unsupported mode %q", path, dedup.Mode)
	}
	if mode == "fields" {
		validateNames(add, path+".fields", dedup.Fields, true)
	}
	for index, normalize := range dedup.Normalize {
		if strings.TrimSpace(normalize.Pattern) == "" {
			add("%s.normalize[%d].pattern: must not be empty", path, index)
			continue
		}
		if _, err := regexp.Compile(normalize.Pattern); err != nil {
			add("%s.normalize[%d].pattern: invalid regexp: %v", path, index, err)
		}
	}
}

func validateChannels(add func(string, ...any), channels map[string]NotificationChannelConfig) {
	if len(channels) == 0 {
		add("alert.channels: must contain at least one channel")
		return
	}

	for id, channel := range channels {
		path := fmt.Sprintf("alert.channels[%q]", id)
		if strings.TrimSpace(id) == "" {
			add("alert.channels: channel ID must not be empty")
		}
		if strings.ToLower(strings.TrimSpace(channel.Platform)) != "dingtalk" {
			add("%s.platform: unsupported platform %q", path, channel.Platform)
		}
		if strings.TrimSpace(channel.Webhook) == "" {
			add("%s.webhook: must not be empty", path)
		}
	}
}
