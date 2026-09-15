package parser

import (
	"testing"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestGraphNodeParserParsesEntryAndFields(t *testing.T) {
	t.Parallel()

	timestamp := time.Date(2026, time.September, 14, 15, 17, 34, 0, time.UTC)
	raw := logentry.RawLog{
		ContainerID:   "container-id",
		ContainerName: "index-node-0",
		Timestamp:     timestamp,
		Stream:        "stderr",
		Raw:           "Sep 14 15:17:33.743 INFO Committed write batch, time_ms: 5, weight: 312, entities: 0, block_count: 1, block_number: 262560262, sgd: 15, subgraph_id: QmExample, component: SubgraphInstanceManager",
	}

	entry := NewGraphNodeParser().Parse(raw)

	if entry.Level != logentry.LevelInfo {
		t.Fatalf("Level = %v, want %v", entry.Level, logentry.LevelInfo)
	}
	if entry.Message != "Committed write batch" {
		t.Fatalf("Message = %q", entry.Message)
	}
	if entry.Timestamp != timestamp {
		t.Fatalf("Timestamp = %s, want Docker timestamp %s", entry.Timestamp, timestamp)
	}
	if entry.ContainerID != raw.ContainerID || entry.ContainerName != raw.ContainerName || entry.Stream != raw.Stream {
		t.Fatalf("entry metadata = %#v, raw = %#v", entry, raw)
	}

	for key, want := range map[string]string{
		"time_ms":      "5",
		"weight":       "312",
		"entities":     "0",
		"block_count":  "1",
		"block_number": "262560262",
		"sgd":          "15",
		"subgraph_id":  "QmExample",
		"component":    "SubgraphInstanceManager",
	} {
		if got := entry.Fields[key]; got != want {
			t.Errorf("Fields[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestGraphNodeParserRecognizesLevelAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want logentry.LogLevel
	}{
		{line: "WARN warning", want: logentry.LevelWarn},
		{line: "ERRO error", want: logentry.LevelError},
		{line: "ERROR error", want: logentry.LevelError},
		{line: "CRIT critical", want: logentry.LevelCritical},
		{line: "CRITICAL critical", want: logentry.LevelCritical},
		{line: "DEBG debug", want: logentry.LevelDebug},
		{line: "TRCE trace", want: logentry.LevelTrace},
	}

	parser := NewGraphNodeParser()
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			t.Parallel()

			entry := parser.Parse(logentry.RawLog{Raw: tt.line})
			if entry.Level != tt.want {
				t.Fatalf("Parse(%q).Level = %v, want %v", tt.line, entry.Level, tt.want)
			}
		})
	}
}

func TestGraphNodeParserPreservesUnknownInput(t *testing.T) {
	t.Parallel()

	raw := logentry.RawLog{Raw: "an unstructured message, token=secret"}
	entry := NewGraphNodeParser().Parse(raw)
	if entry.Level != logentry.LevelUnknown {
		t.Fatalf("Level = %v, want UNKNOWN", entry.Level)
	}
	if entry.Message != raw.Raw || entry.Raw != raw.Raw {
		t.Fatalf("entry = %#v, want raw fallback", entry)
	}
	if entry.Fields != nil {
		t.Fatalf("Fields = %#v, want nil", entry.Fields)
	}
}

func TestGraphNodeParserPreservesCommasInsideFieldValues(t *testing.T) {
	t.Parallel()

	entry := NewGraphNodeParser().Parse(logentry.RawLog{
		Raw: "ERROR request failed, error: upstream returned timeout, retry later, component: RpcAdapter",
	})

	if entry.Message != "request failed" {
		t.Fatalf("Message = %q", entry.Message)
	}
	if got, want := entry.Fields["error"], "upstream returned timeout, retry later"; got != want {
		t.Fatalf("Fields[error] = %q, want %q", got, want)
	}
	if got, want := entry.Fields["component"], "RpcAdapter"; got != want {
		t.Fatalf("Fields[component] = %q, want %q", got, want)
	}
}

func FuzzGraphNodeParser(f *testing.F) {
	for _, seed := range []string{
		"INFO normal log",
		"Sep 14 15:17:33.743 ERRO failed, component: Store",
		"",
		"unstructured",
	} {
		f.Add(seed)
	}

	parser := NewGraphNodeParser()
	f.Fuzz(func(t *testing.T, line string) {
		entry := parser.Parse(logentry.RawLog{Raw: line})
		if entry.Raw != line {
			t.Fatalf("Raw = %q, want %q", entry.Raw, line)
		}
		if entry.Level == logentry.LevelUnknown && entry.Message != line {
			t.Fatalf("UNKNOWN Message = %q, want %q", entry.Message, line)
		}
	})
}
