package logentry

import "time"

// RawLog is one log record received from a container runtime.
// Timestamp is supplied by the Docker Engine API and is the authoritative
// timestamp for event ordering, checkpointing, and replay.
type RawLog struct {
	ContainerID   string
	ContainerName string
	Timestamp     time.Time
	Stream        string
	Raw           string
}

// LogEntry is the normalized representation consumed by the detection
// pipeline. Any application timestamp embedded in Raw is diagnostic context
// only and must not replace Timestamp.
type LogEntry struct {
	ContainerID   string
	ContainerName string
	Timestamp     time.Time
	Stream        string
	Level         LogLevel
	Message       string
	Fields        map[string]string
	Raw           string
}
