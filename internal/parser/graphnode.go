package parser

import (
	"regexp"
	"strings"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

var (
	graphNodeLinePattern = regexp.MustCompile(`^(?:(?:[A-Z][a-z]{2}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}(?:\.\d+)?\s+))?([A-Za-z]+)(?:\s+(.*))?$`)
	fieldPattern         = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*):\s*(.*)$`)
)

// GraphNodeParser parses the human-readable log format emitted by graph-node.
// It deliberately uses the Docker timestamp from RawLog as the normalized
// entry timestamp; graph-node's prefix lacks a year and timezone.
type GraphNodeParser struct{}

var _ Parser = GraphNodeParser{}

func NewGraphNodeParser() GraphNodeParser {
	return GraphNodeParser{}
}

// Parse returns a normalized graph-node entry. Unrecognized input is retained
// unchanged as a LevelUnknown entry so an explicit UNKNOWN rule can still
// detect it later in the pipeline.
func (GraphNodeParser) Parse(raw logentry.RawLog) logentry.LogEntry {
	entry := logentry.LogEntry{
		ContainerID:   raw.ContainerID,
		ContainerName: raw.ContainerName,
		Timestamp:     raw.Timestamp,
		Stream:        raw.Stream,
		Level:         logentry.LevelUnknown,
		Message:       raw.Raw,
		Raw:           raw.Raw,
	}

	matches := graphNodeLinePattern.FindStringSubmatch(raw.Raw)
	if matches == nil {
		return entry
	}

	level, err := logentry.ParseLogLevel(matches[1])
	if err != nil {
		return entry
	}

	entry.Level = level
	entry.Message, entry.Fields = parseMessageAndFields(matches[2])
	return entry
}

func parseMessageAndFields(value string) (string, map[string]string) {
	parts := strings.Split(value, ",")
	messageParts := make([]string, 0, len(parts))
	fields := make(map[string]string)
	lastField := ""
	fieldSectionStarted := false

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		matches := fieldPattern.FindStringSubmatch(trimmed)
		if matches != nil {
			fieldSectionStarted = true
			lastField = matches[1]
			fields[lastField] = matches[2]
			continue
		}

		if fieldSectionStarted && lastField != "" {
			fields[lastField] += "," + part
			continue
		}

		messageParts = append(messageParts, trimmed)
	}

	if len(fields) == 0 {
		fields = nil
	}
	return strings.Join(messageParts, ", "), fields
}
