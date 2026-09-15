// Package router applies the low-cost level gate before rule matching.
package router

import (
	"fmt"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// Route describes how a parsed entry moves through the detection pipeline.
type Route int

const (
	RouteDiscard Route = iota
	RouteContextOnly
	RouteAlertCandidate
)

// Router determines whether an entry is discarded, retained for context, or
// sent to the Rule Engine. Alert candidates always retain context, even if the
// context level list omits their level.
type Router struct {
	contextLevels   map[logentry.LogLevel]struct{}
	alertCandidates map[logentry.LogLevel]struct{}
}

// New constructs a Router from normalized levels.
func New(contextLevels, alertCandidates []logentry.LogLevel) Router {
	return Router{
		contextLevels:   toSet(contextLevels),
		alertCandidates: toSet(alertCandidates),
	}
}

// NewFromNames constructs a Router from YAML-level strings without importing
// the config package, avoiding a config-to-runtime dependency cycle.
func NewFromNames(contextLevels, alertCandidates []string) (Router, error) {
	context, err := parseLevels(contextLevels)
	if err != nil {
		return Router{}, fmt.Errorf("context levels: %w", err)
	}
	candidates, err := parseLevels(alertCandidates)
	if err != nil {
		return Router{}, fmt.Errorf("alert candidate levels: %w", err)
	}
	return New(context, candidates), nil
}

// Default returns the MVP default level policy.
func Default() Router {
	return New(
		[]logentry.LogLevel{logentry.LevelInfo, logentry.LevelWarn, logentry.LevelError, logentry.LevelCritical},
		[]logentry.LogLevel{logentry.LevelWarn, logentry.LevelError, logentry.LevelCritical},
	)
}

// Route returns the pipeline action for entry.
func (r Router) Route(entry logentry.LogEntry) Route {
	if _, ok := r.alertCandidates[entry.Level]; ok {
		return RouteAlertCandidate
	}
	if _, ok := r.contextLevels[entry.Level]; ok {
		return RouteContextOnly
	}
	return RouteDiscard
}

func (r Route) KeepsContext() bool {
	return r == RouteContextOnly || r == RouteAlertCandidate
}

func (r Route) EntersRuleEngine() bool {
	return r == RouteAlertCandidate
}

func parseLevels(values []string) ([]logentry.LogLevel, error) {
	levels := make([]logentry.LogLevel, 0, len(values))
	for _, value := range values {
		level, err := logentry.ParseLogLevel(value)
		if err != nil {
			return nil, err
		}
		levels = append(levels, level)
	}
	return levels, nil
}

func toSet(levels []logentry.LogLevel) map[logentry.LogLevel]struct{} {
	set := make(map[logentry.LogLevel]struct{}, len(levels))
	for _, level := range levels {
		set[level] = struct{}{}
	}
	return set
}
