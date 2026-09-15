package rule

import "github.com/wanliqun/congraph-log-watcher/internal/logentry"

// Engine processes rules in configuration order.
type Engine struct {
	rules []Rule
}

func NewEngine(rules []Rule) Engine {
	return Engine{rules: append([]Rule(nil), rules...)}
}

// Match returns every matching enabled rule until a matching StopOnMatch rule
// is encountered.
func (e Engine) Match(entry logentry.LogEntry) []Rule {
	matches := make([]Rule, 0, len(e.rules))
	for _, rule := range e.rules {
		if !rule.Matches(entry) {
			continue
		}
		matches = append(matches, rule)
		if rule.StopOnMatch {
			break
		}
	}
	return matches
}

// Matches applies the fixed match order: container, level, component, fields,
// pattern, then excludes.
func (r Rule) Matches(entry logentry.LogEntry) bool {
	if !r.Enabled {
		return false
	}
	if len(r.containers) > 0 {
		if _, ok := r.containers[entry.ContainerName]; !ok {
			return false
		}
	}
	if _, ok := r.levels[entry.Level]; !ok {
		return false
	}
	if len(r.components) > 0 {
		component := entry.Fields["component"]
		if _, ok := r.components[component]; !ok {
			return false
		}
	}
	for key, want := range r.fieldEquals {
		if entry.Fields[key] != want {
			return false
		}
	}
	if r.pattern != nil && !r.pattern.MatchString(entry.Message) {
		return false
	}
	for _, exclude := range r.excludes {
		if exclude.MatchString(entry.Message) {
			return false
		}
	}
	return true
}
