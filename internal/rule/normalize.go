package rule

import (
	"fmt"
	"regexp"

	"github.com/wanliqun/congraph-log-watcher/internal/config"
)

type replacement struct {
	pattern *regexp.Regexp
	replace string
}

// Normalizer applies configured replacements in stable declaration order.
type Normalizer struct {
	replacements []replacement
}

func NewNormalizer(rules []config.NormalizationRuleConfig) (Normalizer, error) {
	replacements := make([]replacement, 0, len(rules))
	for index, rule := range rules {
		if rule.Pattern == "" {
			return Normalizer{}, fmt.Errorf("normalization rule %d pattern must not be empty", index)
		}
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return Normalizer{}, fmt.Errorf("normalization rule %d: %w", index, err)
		}
		replacements = append(replacements, replacement{pattern: pattern, replace: rule.Replace})
	}
	return Normalizer{replacements: replacements}, nil
}

func (n Normalizer) Normalize(value string) string {
	for _, replacement := range n.replacements {
		value = replacement.pattern.ReplaceAllString(value, replacement.replace)
	}
	return value
}
