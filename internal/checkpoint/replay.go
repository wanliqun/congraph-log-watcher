package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// ReplayStart returns the inclusive Docker log start time for a checkpoint.
func ReplayStart(checkpoint ContainerCheckpoint, overlap time.Duration) time.Time {
	if checkpoint.LastTimestamp.IsZero() {
		return time.Time{}
	}
	return checkpoint.LastTimestamp.Add(-overlap)
}

// EventHash derives the replay marker from stable Docker log properties. It
// is persisted instead of raw content so the checkpoint database cannot leak
// the log line itself.
func EventHash(raw logentry.RawLog) string {
	hash := sha256.New()
	for _, value := range []string{raw.ContainerID, raw.Timestamp.UTC().Format(time.RFC3339Nano), raw.Stream, raw.Raw} {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// ReplayGate buffers overlap logs while looking for a previous checkpoint
// marker. If the marker is found, all buffered records through it are skipped.
// If it is absent (for example after log rotation), Finish returns the entire
// overlap so callers preserve At-Least-Once behavior rather than losing data.
type ReplayGate struct {
	checkpoint ContainerCheckpoint
	looking    bool
	buffered   []logentry.RawLog
}

func NewReplayGate(checkpoint ContainerCheckpoint) *ReplayGate {
	return &ReplayGate{checkpoint: checkpoint, looking: checkpoint.ContainerID != "" && !checkpoint.LastTimestamp.IsZero() && checkpoint.LastEventHash != ""}
}

// Accept returns immediately processable records. While searching for a
// marker it returns nothing; call Finish if the historical replay ends without
// finding that marker.
func (g *ReplayGate) Accept(raw logentry.RawLog) []logentry.RawLog {
	if !g.looking {
		return []logentry.RawLog{raw}
	}
	if raw.ContainerID != g.checkpoint.ContainerID {
		g.looking = false
		g.buffered = nil
		return []logentry.RawLog{raw}
	}
	if raw.Timestamp.Equal(g.checkpoint.LastTimestamp) && EventHash(raw) == g.checkpoint.LastEventHash {
		g.looking = false
		g.buffered = nil
		return nil
	}
	g.buffered = append(g.buffered, raw)
	return nil
}

// Finish releases buffered overlap only when the marker was not found.
func (g *ReplayGate) Finish() []logentry.RawLog {
	if !g.looking {
		return nil
	}
	g.looking = false
	result := append([]logentry.RawLog(nil), g.buffered...)
	g.buffered = nil
	return result
}
