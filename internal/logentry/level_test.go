package logentry

import "testing"

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  LogLevel
	}{
		{name: "unknown", input: "UNKNOWN", want: LevelUnknown},
		{name: "trace abbreviated", input: "TRCE", want: LevelTrace},
		{name: "trace canonical", input: "TRACE", want: LevelTrace},
		{name: "debug abbreviated", input: "DEBG", want: LevelDebug},
		{name: "debug canonical", input: "DEBUG", want: LevelDebug},
		{name: "info", input: "INFO", want: LevelInfo},
		{name: "warn", input: "WARN", want: LevelWarn},
		{name: "error abbreviated", input: "ERRO", want: LevelError},
		{name: "error canonical", input: "ERROR", want: LevelError},
		{name: "critical abbreviated", input: "CRIT", want: LevelCritical},
		{name: "critical canonical", input: "CRITICAL", want: LevelCritical},
		{name: "case insensitive", input: "error", want: LevelError},
		{name: "surrounding whitespace", input: "  warn\t", want: LevelWarn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseLogLevel(tt.input)
			if err != nil {
				t.Fatalf("ParseLogLevel(%q) returned error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseLogLevelRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	got, err := ParseLogLevel("NOTICE")
	if err == nil {
		t.Fatal("ParseLogLevel(NOTICE) returned nil error")
	}
	if got != LevelUnknown {
		t.Fatalf("ParseLogLevel(NOTICE) = %v, want %v", got, LevelUnknown)
	}
}

func TestLogLevelCanonicalString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level LogLevel
		want  string
	}{
		{level: LevelUnknown, want: "UNKNOWN"},
		{level: LevelTrace, want: "TRACE"},
		{level: LevelDebug, want: "DEBUG"},
		{level: LevelInfo, want: "INFO"},
		{level: LevelWarn, want: "WARN"},
		{level: LevelError, want: "ERROR"},
		{level: LevelCritical, want: "CRITICAL"},
	}

	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("LogLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestLogLevelTextRoundTrip(t *testing.T) {
	t.Parallel()

	for level := LevelUnknown; level <= LevelCritical; level++ {
		text, err := level.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText(%v) returned error: %v", level, err)
		}

		var got LogLevel
		if err := got.UnmarshalText(text); err != nil {
			t.Fatalf("UnmarshalText(%q) returned error: %v", text, err)
		}
		if got != level {
			t.Fatalf("round trip = %v, want %v", got, level)
		}
	}
}

func TestLogLevelMarshalTextRejectsInvalidValue(t *testing.T) {
	t.Parallel()

	if _, err := LogLevel(99).MarshalText(); err == nil {
		t.Fatal("MarshalText on invalid level returned nil error")
	}
}

func TestLogLevelUnmarshalTextNilReceiver(t *testing.T) {
	t.Parallel()

	var level *LogLevel
	if err := level.UnmarshalText([]byte("INFO")); err == nil {
		t.Fatal("UnmarshalText on nil receiver returned nil error")
	}
}
