// Package config loads and validates graph-log-watcher configuration files.
package config

import "time"

const (
	defaultDockerSocket            = "unix:///var/run/docker.sock"
	defaultCheckpointPath          = "/var/lib/graph-log-watcher/state.db"
	defaultMetricsListen           = ":9108"
	defaultLogChannelSize          = 10000
	defaultNotificationQueueSize   = 100
	defaultMaxAlertsPerMinute      = 20
	defaultContextBufferLines      = 100
	defaultMaxGroups               = 10000
	defaultMaxSamples              = 3
	defaultRuleContextMaxBytes     = 16384
	defaultDockerUnavailableAfter  = time.Minute
	defaultAllDetachedAfter        = time.Minute
	defaultStormCooldown           = time.Minute
	defaultShutdownTimeout         = 10 * time.Second
	defaultReplayOverlap           = 2 * time.Second
	defaultCheckpointFlushInterval = time.Second
	defaultCheckpointFlushEvents   = 100
	defaultGroupTTL                = 24 * time.Hour
)

// Config is the complete service configuration.
type Config struct {
	Docker      DockerConfig      `yaml:"docker"`
	Containers  []string          `yaml:"containers"`
	Parser      ParserConfig      `yaml:"parser"`
	Levels      LevelsConfig      `yaml:"levels"`
	Checkpoint  CheckpointConfig  `yaml:"checkpoint"`
	Aggregation AggregationConfig `yaml:"aggregation"`
	Context     ContextConfig     `yaml:"context"`
	Redaction   RedactionConfig   `yaml:"redaction"`
	Rules       []RuleConfig      `yaml:"rules"`
	Alert       AlertConfig       `yaml:"alert"`
	Notifier    NotifierConfig    `yaml:"notifier"`
	Metrics     MetricsConfig     `yaml:"metrics"`
	Health      HealthConfig      `yaml:"health"`
	Runtime     RuntimeConfig     `yaml:"runtime"`
	Shutdown    ShutdownConfig    `yaml:"shutdown"`
}

type DockerConfig struct {
	Socket string `yaml:"socket"`
}

type ParserConfig struct {
	Type string `yaml:"type"`
}

type LevelsConfig struct {
	Context         []string `yaml:"context"`
	AlertCandidates []string `yaml:"alert_candidates"`
}

type CheckpointConfig struct {
	Path          string   `yaml:"path"`
	ReplayOverlap Duration `yaml:"replay_overlap"`
	FlushInterval Duration `yaml:"flush_interval"`
	FlushEvents   int      `yaml:"flush_events"`
}

type AggregationConfig struct {
	MaxGroups  int      `yaml:"max_groups"`
	GroupTTL   Duration `yaml:"group_ttl"`
	MaxSamples int      `yaml:"max_samples"`
}

type ContextConfig struct {
	BufferLines int `yaml:"buffer_lines"`
}

type RedactionConfig struct {
	Rules []RedactionRuleConfig `yaml:"rules"`
}

type RedactionRuleConfig struct {
	Pattern string `yaml:"pattern"`
	Replace string `yaml:"replace"`
}

type RuleConfig struct {
	ID          string            `yaml:"id"`
	Enabled     *bool             `yaml:"enabled"`
	Severity    string            `yaml:"severity"`
	Containers  []string          `yaml:"containers"`
	Match       MatchConfig       `yaml:"match"`
	Threshold   int               `yaml:"threshold"`
	Window      Duration          `yaml:"window"`
	Cooldown    Duration          `yaml:"cooldown"`
	Dedup       DedupConfig       `yaml:"dedup"`
	Context     RuleContextConfig `yaml:"context"`
	StopOnMatch bool              `yaml:"stop_on_match"`
}

// IsEnabled returns the effective rule setting. Rules default to enabled.
func (r RuleConfig) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

type MatchConfig struct {
	Levels      []string          `yaml:"levels"`
	Pattern     string            `yaml:"pattern"`
	Excludes    []string          `yaml:"excludes"`
	Components  []string          `yaml:"components"`
	FieldEquals map[string]string `yaml:"field_equals"`
}

type DedupConfig struct {
	Mode      string                    `yaml:"mode"`
	Fields    []string                  `yaml:"fields"`
	Normalize []NormalizationRuleConfig `yaml:"normalize"`
}

type NormalizationRuleConfig struct {
	Pattern string `yaml:"pattern"`
	Replace string `yaml:"replace"`
}

type RuleContextConfig struct {
	Before    int      `yaml:"before"`
	After     int      `yaml:"after"`
	AfterWait Duration `yaml:"after_wait"`
	MaxBytes  int      `yaml:"max_bytes"`
}

type AlertConfig struct {
	CustomTags []string                             `yaml:"custom_tags"`
	Channels   map[string]NotificationChannelConfig `yaml:"channels"`
}

type NotificationChannelConfig struct {
	Platform  string   `yaml:"platform"`
	Webhook   string   `yaml:"webhook"`
	Secret    string   `yaml:"secret"`
	AtMobiles []string `yaml:"atMobiles"`
	IsAtAll   bool     `yaml:"isAtAll"`
}

type NotifierConfig struct {
	QueueSize          int      `yaml:"queue_size"`
	MaxAlertsPerMinute int      `yaml:"max_alerts_per_minute"`
	StormCooldown      Duration `yaml:"storm_cooldown"`
}

type MetricsConfig struct {
	Listen string `yaml:"listen"`
}

type HealthConfig struct {
	DockerUnavailableAfter     Duration `yaml:"docker_unavailable_after"`
	AllContainersDetachedAfter Duration `yaml:"all_containers_detached_after"`
}

type RuntimeConfig struct {
	LogChannelSize int `yaml:"log_channel_size"`
}

type ShutdownConfig struct {
	Timeout Duration `yaml:"timeout"`
}

// DefaultConfig returns the defaults used as the base for YAML decoding. The
// base must exist before decoding so an explicitly supplied zero is preserved
// and can be rejected by validation instead of being mistaken for an omission.
func DefaultConfig() Config {
	return Config{
		Docker: DockerConfig{Socket: defaultDockerSocket},
		Parser: ParserConfig{Type: "graph-node"},
		Levels: LevelsConfig{
			Context:         []string{"INFO", "WARN", "ERROR", "CRITICAL"},
			AlertCandidates: []string{"WARN", "ERROR", "CRITICAL"},
		},
		Checkpoint: CheckpointConfig{
			Path:          defaultCheckpointPath,
			ReplayOverlap: Duration(defaultReplayOverlap),
			FlushInterval: Duration(defaultCheckpointFlushInterval),
			FlushEvents:   defaultCheckpointFlushEvents,
		},
		Aggregation: AggregationConfig{
			MaxGroups:  defaultMaxGroups,
			GroupTTL:   Duration(defaultGroupTTL),
			MaxSamples: defaultMaxSamples,
		},
		Context: ContextConfig{BufferLines: defaultContextBufferLines},
		Notifier: NotifierConfig{
			QueueSize:          defaultNotificationQueueSize,
			MaxAlertsPerMinute: defaultMaxAlertsPerMinute,
			StormCooldown:      Duration(defaultStormCooldown),
		},
		Metrics: MetricsConfig{Listen: defaultMetricsListen},
		Health: HealthConfig{
			DockerUnavailableAfter:     Duration(defaultDockerUnavailableAfter),
			AllContainersDetachedAfter: Duration(defaultAllDetachedAfter),
		},
		Runtime:  RuntimeConfig{LogChannelSize: defaultLogChannelSize},
		Shutdown: ShutdownConfig{Timeout: Duration(defaultShutdownTimeout)},
	}
}

func (c *Config) applyRuleDefaults() {
	for index := range c.Rules {
		rule := &c.Rules[index]
		if rule.Enabled == nil {
			enabled := true
			rule.Enabled = &enabled
		}
		if rule.Dedup.Mode == "" {
			rule.Dedup.Mode = "normalized"
		}
		if rule.Context.MaxBytes == 0 {
			rule.Context.MaxBytes = defaultRuleContextMaxBytes
		}
	}
}
