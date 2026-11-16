package collectnative

import (
	"net/url"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
)

func lifecycleTargetURL(value string) string {
	address, err := url.Parse(value)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" ||
		address.User != nil || address.RawQuery != "" || address.Fragment != "" ||
		address.RawPath != "" || address.Port() != "" {
		return ""
	}
	return address.String()
}

func (p *Projection) addOutputLifecycle(sourceID, observed string, row model.Row, targetRepository string) error {
	outputType := strings.TrimSpace(text(row["safeOutputType"]))
	status := strings.ToLower(strings.TrimSpace(text(row["status"])))
	if outputType == "" || status == "error" || status == "failure" ||
		status == "failed" || status == "skipped" {
		return nil
	}
	deliveredAt, err := time.Parse(time.RFC3339Nano, firstText(row["timestamp"], observed))
	if err != nil {
		return err
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observed)
	if err != nil {
		return err
	}
	run := p.Collections["runs"][0]
	lifecycle, err := mining.OutputLifecycleRow(
		sourceID,
		text(run["repositoryId"]),
		text(run["workflowId"]),
		p.RunID,
		outputType,
		text(row["summary"]),
		targetRepository,
		lifecycleTargetURL(firstText(row["safeOutputUrl"], row["correlationId"])),
		deliveredAt,
		observedAt,
		firstText(row["source"], "gh-aw-logs"),
	)
	if err != nil {
		return err
	}
	p.Collections["outputLifecycles"] = append(p.Collections["outputLifecycles"], lifecycle)
	return nil
}
