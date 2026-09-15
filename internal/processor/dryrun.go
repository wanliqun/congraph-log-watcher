package processor

import (
	"fmt"
	"io"
	"sync"
)

// DryRunSink prints stable, human-readable alerts without constructing an
// external notification channel.
type DryRunSink struct {
	mu     sync.Mutex
	writer io.Writer
}

func NewDryRunSink(writer io.Writer) *DryRunSink {
	return &DryRunSink{writer: writer}
}

func (s *DryRunSink) Alert(alert Alert) {
	if s == nil || s.writer == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.writer, "WOULD ALERT rule=%s severity=%s container=%s fingerprint=%s window_count=%d total_count=%d suppressed_count=%d\n",
		alert.RuleID,
		alert.Severity,
		alert.ContainerName,
		alert.Fingerprint,
		alert.WindowCount,
		alert.TotalCount,
		alert.SuppressedCount,
	)
}
