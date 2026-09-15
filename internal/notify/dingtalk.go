package notify

import (
	"context"
	"fmt"
	"strings"

	upstream "github.com/Conflux-Chain/go-conflux-util/alert"
	"github.com/wanliqun/congraph-log-watcher/internal/processor"
)

// DingTalkConfig is the non-global configuration required to create a single
// upstream DingTalk channel.
type DingTalkConfig struct {
	ID         string
	Webhook    string
	Secret     string
	AtMobiles  []string
	IsAtAll    bool
	CustomTags []string
}

// DingTalkNotifier adapts the upstream Channel.Send API without calling its
// Viper-based global initialization.
type DingTalkNotifier struct{ channel upstream.Channel }

func NewDingTalkNotifier(config DingTalkConfig) (*DingTalkNotifier, error) {
	if config.ID == "" || config.Webhook == "" {
		return nil, fmt.Errorf("DingTalk ID and webhook must not be empty")
	}
	formatter, err := upstream.NewDingtalkMarkdownFormatter(config.CustomTags, config.AtMobiles)
	if err != nil {
		return nil, fmt.Errorf("create DingTalk formatter: %w", err)
	}
	channel := upstream.NewDingTalkChannel(config.ID, formatter, upstream.DingTalkConfig{Webhook: config.Webhook, Secret: config.Secret, AtMobiles: append([]string(nil), config.AtMobiles...), IsAtAll: config.IsAtAll})
	return &DingTalkNotifier{channel: channel}, nil
}

func (n *DingTalkNotifier) Notify(ctx context.Context, alert processor.Alert) error {
	return n.channel.Send(ctx, &upstream.Notification{Title: fmt.Sprintf("[%s] graph-node log alert", strings.ToUpper(alert.Severity)), Content: FormatMarkdown(alert), Severity: mapSeverity(alert.Severity)})
}

// FormatMarkdown creates the operator-facing incident details supplied to the
// upstream DingTalk Markdown formatter.
func FormatMarkdown(alert processor.Alert) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("**Node:** %s", alert.ContainerName), fmt.Sprintf("**Rule:** %s", alert.RuleID))
	if alert.Component != "" {
		lines = append(lines, fmt.Sprintf("**Component:** %s", alert.Component))
	}
	if alert.SubgraphID != "" {
		lines = append(lines, fmt.Sprintf("**Subgraph:** %s", alert.SubgraphID))
	}
	lines = append(lines, fmt.Sprintf("**Count:** %d / %s", alert.WindowCount, alert.Window), fmt.Sprintf("**Total:** %d", alert.TotalCount))
	if alert.SuppressedCount > 0 {
		lines = append(lines, fmt.Sprintf("**Suppressed since previous alert:** %d", alert.SuppressedCount))
	}
	if len(alert.Samples) > 0 {
		lines = append(lines, "**Samples:**\n"+strings.Join(alert.Samples, "\n"))
	}
	if len(alert.Context) > 0 {
		lines = append(lines, "**Context:**\n```\n"+strings.Join(alert.Context, "\n")+"\n```")
	}
	lines = append(lines, fmt.Sprintf("**Fingerprint:** `%s`", alert.Fingerprint))
	return strings.Join(lines, "\n\n")
}

func mapSeverity(value string) upstream.Severity {
	switch strings.ToLower(value) {
	case "low":
		return upstream.SeverityLow
	case "medium":
		return upstream.SeverityMedium
	case "critical":
		return upstream.SeverityCritical
	default:
		return upstream.SeverityHigh
	}
}
