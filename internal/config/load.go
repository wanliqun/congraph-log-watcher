package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var environmentVariablePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads a YAML configuration file, expands environment variables, applies
// defaults, and validates the resulting configuration.
func Load(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read configuration: %w", err)
	}

	expanded, err := expandEnvironment(string(content), os.LookupEnv)
	if err != nil {
		return nil, err
	}

	decoder := yaml.NewDecoder(bytes.NewBufferString(expanded))
	decoder.KnownFields(true)

	cfg := DefaultConfig()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode configuration: multiple YAML documents are not supported")
		}
		return nil, fmt.Errorf("decode configuration: %w", err)
	}

	cfg.applyRuleDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func expandEnvironment(input string, lookup func(string) (string, bool)) (string, error) {
	matches := environmentVariablePattern.FindAllStringSubmatchIndex(input, -1)
	var output strings.Builder
	output.Grow(len(input))

	last := 0
	for _, match := range matches {
		output.WriteString(input[last:match[0]])
		name := input[match[2]:match[3]]
		value, ok := lookup(name)
		if !ok {
			return "", fmt.Errorf("environment variable %q is not set", name)
		}
		output.WriteString(value)
		last = match[1]
	}
	output.WriteString(input[last:])

	expanded := output.String()
	if strings.Contains(expanded, "${") {
		return "", fmt.Errorf("invalid environment variable placeholder")
	}

	return expanded, nil
}
