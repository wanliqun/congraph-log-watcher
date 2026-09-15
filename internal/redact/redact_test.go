package redact

import (
	"strings"
	"testing"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

func TestRedactorRedactsEveryEntrySurfaceWithoutMutatingInput(t *testing.T) {
	t.Parallel()

	redactor, err := New([]Rule{
		{Pattern: `(?i)authorization:\s*bearer\s+\S+`, Replace: "Authorization: Bearer ***"},
		{Pattern: `(?i)(api[_-]?key=)[^&\s]+`, Replace: "${1}***"},
	})
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	entry := logentry.LogEntry{
		Raw:     "ERROR authorization: bearer raw-token, api_key=raw-key",
		Message: "request failed authorization: bearer message-token",
		Fields: map[string]string{
			"authorization": "Bearer field-token",
			"endpoint":      "https://example.test?api-key=field-key",
		},
	}

	sanitized := redactor.Redact(entry)
	for name, value := range map[string]string{
		"raw":                 sanitized.Raw,
		"message":             sanitized.Message,
		"authorization field": sanitized.Fields["authorization"],
		"endpoint field":      sanitized.Fields["endpoint"],
	} {
		for _, secret := range []string{"raw-token", "raw-key", "message-token", "field-token", "field-key"} {
			if strings.Contains(value, secret) {
				t.Errorf("%s leaked %q: %q", name, secret, value)
			}
		}
	}

	if entry.Fields["endpoint"] != "https://example.test?api-key=field-key" {
		t.Fatalf("input field map was mutated: %#v", entry.Fields)
	}
}

func TestRedactorRejectsInvalidRules(t *testing.T) {
	t.Parallel()

	for _, rules := range [][]Rule{
		{{Pattern: ""}},
		{{Pattern: "["}},
	} {
		if _, err := New(rules); err == nil {
			t.Fatalf("New(%#v) returned nil error", rules)
		}
	}
}

func TestNilRedactorIsNoOp(t *testing.T) {
	t.Parallel()

	var redactor *Redactor
	if got := redactor.RedactString("value"); got != "value" {
		t.Fatalf("RedactString() = %q", got)
	}
}
