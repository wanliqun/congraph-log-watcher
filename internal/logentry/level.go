package logentry

import (
	"fmt"
	"strings"
)

// LogLevel is the normalized severity of a graph-node log entry.
type LogLevel int

const (
	LevelUnknown LogLevel = iota
	LevelTrace
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
	LevelCritical
)

var levelNames = map[LogLevel]string{
	LevelUnknown:  "UNKNOWN",
	LevelTrace:    "TRACE",
	LevelDebug:    "DEBUG",
	LevelInfo:     "INFO",
	LevelWarn:     "WARN",
	LevelError:    "ERROR",
	LevelCritical: "CRITICAL",
}

// String returns the canonical spelling of a log level.
func (l LogLevel) String() string {
	if name, ok := levelNames[l]; ok {
		return name
	}

	return "UNKNOWN"
}

// ParseLogLevel parses graph-node's canonical and abbreviated level names.
// Input is matched case-insensitively after trimming surrounding whitespace.
func ParseLogLevel(value string) (LogLevel, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "UNKNOWN":
		return LevelUnknown, nil
	case "TRCE", "TRACE":
		return LevelTrace, nil
	case "DEBG", "DEBUG":
		return LevelDebug, nil
	case "INFO":
		return LevelInfo, nil
	case "WARN":
		return LevelWarn, nil
	case "ERRO", "ERROR":
		return LevelError, nil
	case "CRIT", "CRITICAL":
		return LevelCritical, nil
	default:
		return LevelUnknown, fmt.Errorf("unknown log level %q", value)
	}
}

// MarshalText implements encoding.TextMarshaler using the canonical level
// spelling.
func (l LogLevel) MarshalText() ([]byte, error) {
	if _, ok := levelNames[l]; !ok {
		return nil, fmt.Errorf("invalid log level %d", l)
	}

	return []byte(l.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (l *LogLevel) UnmarshalText(text []byte) error {
	if l == nil {
		return fmt.Errorf("cannot unmarshal log level into nil receiver")
	}

	parsed, err := ParseLogLevel(string(text))
	if err != nil {
		return err
	}

	*l = parsed
	return nil
}
