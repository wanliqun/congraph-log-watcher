// Package rule classifies alert-candidate log entries and derives their
// incident identities.
package rule

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/config"
	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// DedupMode determines which sanitized identity represents an incident.
type DedupMode string

const (
	DedupExact      DedupMode = "exact"
	DedupNormalized DedupMode = "normalized"
	DedupFields     DedupMode = "fields"
	DedupRule       DedupMode = "rule"
)

// Rule is the immutable, compiled form of a configured alert rule.
type Rule struct {
	ID          string
	Enabled     bool
	Severity    string
	Threshold   int
	Window      time.Duration
	Cooldown    time.Duration
	StopOnMatch bool
	Dedup       Dedup
	Context     Context

	containers  map[string]struct{}
	levels      map[logentry.LogLevel]struct{}
	components  map[string]struct{}
	fieldEquals map[string]string
	pattern     *regexp.Regexp
	excludes    []*regexp.Regexp
}

// Dedup contains compiled incident identity behavior.
type Dedup struct {
	Mode       DedupMode
	Fields     []string
	Normalizer Normalizer
}

// Context keeps rule-local context settings available to the pipeline.
type Context struct {
	Before    int
	After     int
	AfterWait time.Duration
	MaxBytes  int
}

// Compile converts validated config into an immutable rule. It repeats local
// validation so callers cannot bypass config.Load by constructing a bad rule.
func Compile(input config.RuleConfig) (Rule, error) {
	if strings.TrimSpace(input.ID) == "" {
		return Rule{}, fmt.Errorf("rule ID must not be empty")
	}

	levels, err := compileLevels(input.Match.Levels)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q levels: %w", input.ID, err)
	}
	if len(levels) == 0 {
		return Rule{}, fmt.Errorf("rule %q must specify at least one level", input.ID)
	}

	pattern, err := compileOptionalPattern(input.Match.Pattern)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q pattern: %w", input.ID, err)
	}
	excludes := make([]*regexp.Regexp, 0, len(input.Match.Excludes))
	for index, value := range input.Match.Excludes {
		exclude, err := compileOptionalPattern(value)
		if err != nil {
			return Rule{}, fmt.Errorf("rule %q exclude %d: %w", input.ID, index, err)
		}
		if exclude != nil {
			excludes = append(excludes, exclude)
		}
	}

	mode, err := parseDedupMode(input.Dedup.Mode)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q dedup: %w", input.ID, err)
	}
	if mode == DedupFields && len(input.Dedup.Fields) == 0 {
		return Rule{}, fmt.Errorf("rule %q dedup fields must not be empty", input.ID)
	}
	normalizer, err := NewNormalizer(input.Dedup.Normalize)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q normalization: %w", input.ID, err)
	}

	return Rule{
		ID:          input.ID,
		Enabled:     input.IsEnabled(),
		Severity:    strings.ToLower(input.Severity),
		Threshold:   input.Threshold,
		Window:      input.Window.Duration(),
		Cooldown:    input.Cooldown.Duration(),
		StopOnMatch: input.StopOnMatch,
		Dedup: Dedup{
			Mode:       mode,
			Fields:     copyStrings(input.Dedup.Fields),
			Normalizer: normalizer,
		},
		Context: Context{
			Before:    input.Context.Before,
			After:     input.Context.After,
			AfterWait: input.Context.AfterWait.Duration(),
			MaxBytes:  input.Context.MaxBytes,
		},
		containers:  stringSet(input.Containers),
		levels:      levels,
		components:  stringSet(input.Match.Components),
		fieldEquals: copyFields(input.Match.FieldEquals),
		pattern:     pattern,
		excludes:    excludes,
	}, nil
}

func compileLevels(values []string) (map[logentry.LogLevel]struct{}, error) {
	levels := make(map[logentry.LogLevel]struct{}, len(values))
	for _, value := range values {
		level, err := logentry.ParseLogLevel(value)
		if err != nil {
			return nil, err
		}
		levels[level] = struct{}{}
	}
	return levels, nil
}

func compileOptionalPattern(value string) (*regexp.Regexp, error) {
	if value == "" {
		return nil, nil
	}
	return regexp.Compile(value)
}

func parseDedupMode(value string) (DedupMode, error) {
	switch DedupMode(strings.ToLower(strings.TrimSpace(value))) {
	case DedupExact:
		return DedupExact, nil
	case DedupNormalized:
		return DedupNormalized, nil
	case DedupFields:
		return DedupFields, nil
	case DedupRule:
		return DedupRule, nil
	default:
		return "", fmt.Errorf("unsupported mode %q", value)
	}
}

func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func copyStrings(values []string) []string {
	return append([]string(nil), values...)
}

func copyFields(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
