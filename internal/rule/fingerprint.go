package rule

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// Fingerprint derives a deterministic incident identity from a sanitized entry.
func (r Rule) Fingerprint(entry logentry.LogEntry) string {
	hash := sha256.New()
	writePart(hash, "rule")
	writePart(hash, r.ID)
	writePart(hash, "container")
	writePart(hash, entry.ContainerName)
	writePart(hash, "mode")
	writePart(hash, string(r.Dedup.Mode))

	switch r.Dedup.Mode {
	case DedupExact:
		writePart(hash, "raw")
		writePart(hash, rawIdentity(entry))
	case DedupNormalized:
		writePart(hash, "message")
		writePart(hash, r.Dedup.Normalizer.Normalize(entry.Message))
	case DedupFields:
		fields := copyStrings(r.Dedup.Fields)
		sort.Strings(fields)
		for _, field := range fields {
			writePart(hash, field)
			writePart(hash, fieldValue(r.ID, entry, field))
		}
	case DedupRule:
		// Rule ID and logical container are already part of every fingerprint.
	}

	return hex.EncodeToString(hash.Sum(nil))
}

func rawIdentity(entry logentry.LogEntry) string {
	if entry.Raw != "" {
		return entry.Raw
	}
	return entry.Message
}

func fieldValue(ruleID string, entry logentry.LogEntry, field string) string {
	switch field {
	case "rule":
		return ruleID
	case "container":
		return entry.ContainerName
	default:
		return entry.Fields[field]
	}
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writePart(writer hashWriter, value string) {
	length := len(value)
	// The binary representation avoids ambiguous separators while retaining a
	// compact, stable encoding for all UTF-8 input.
	var size [8]byte
	for index := len(size) - 1; index >= 0; index-- {
		size[index] = byte(length)
		length >>= 8
	}
	_, _ = writer.Write(size[:])
	_, _ = writer.Write([]byte(value))
}
