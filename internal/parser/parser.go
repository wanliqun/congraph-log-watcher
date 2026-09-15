// Package parser transforms raw container output into normalized log entries.
package parser

import "github.com/wanliqun/congraph-log-watcher/internal/logentry"

// Parser normalizes one container-runtime log record. A parser must preserve a
// record as LevelUnknown when it cannot recognize the application format.
type Parser interface {
	Parse(logentry.RawLog) logentry.LogEntry
}
