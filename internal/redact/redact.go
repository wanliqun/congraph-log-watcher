// Package redact removes sensitive values before a log entry enters context,
// aggregation, deduplication, or notification state.
package redact

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// Rule replaces every match of Pattern with Replace.
type Rule struct {
	Pattern string
	Replace string
}

type compiledRule struct {
	pattern *regexp.Regexp
	replace string
}

// Redactor applies an ordered sequence of replacement rules.
type Redactor struct {
	rules []compiledRule
}

// New compiles redaction rules once at startup.
func New(rules []Rule) (*Redactor, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for index, rule := range rules {
		if rule.Pattern == "" {
			return nil, fmt.Errorf("redaction rule %d: pattern must not be empty", index)
		}

		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("redaction rule %d: compile pattern: %w", index, err)
		}
		compiled = append(compiled, compiledRule{pattern: pattern, replace: rule.Replace})
	}

	return &Redactor{rules: compiled}, nil
}

// Redact returns a deep-copied, sanitized entry. It never mutate the caller's
// entry or field map, so callers may safely retain the unredacted input only
// until this boundary is crossed.
func (r *Redactor) Redact(entry logentry.LogEntry) logentry.LogEntry {
	sanitized := entry
	sanitized.Raw = r.RedactString(entry.Raw)
	sanitized.Message = r.RedactString(entry.Message)
	if entry.Fields == nil {
		return sanitized
	}

	sanitized.Fields = make(map[string]string, len(entry.Fields))
	for key, value := range entry.Fields {
		sanitized.Fields[key] = r.redactField(key, value)
	}
	return sanitized
}

// RedactString applies configured replacements in rule order.
func (r *Redactor) RedactString(value string) string {
	if r == nil {
		return value
	}
	for _, rule := range r.rules {
		value = rule.pattern.ReplaceAllString(value, rule.replace)
	}
	return value
}

func (r *Redactor) redactField(key, value string) string {
	redacted := r.RedactString(value)
	combined := key + ": " + value
	redactedCombined := r.RedactString(combined)
	if redactedCombined == combined {
		return redacted
	}

	if _, fieldValue, found := strings.Cut(redactedCombined, ": "); found {
		return fieldValue
	}
	return redactedCombined
}
