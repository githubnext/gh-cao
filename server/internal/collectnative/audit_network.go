package collectnative

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

var firewallFields = regexp.MustCompile(`(?:[^\s"]+|"[^"]*")+`)
var firewallTimestamp = regexp.MustCompile(`^\d+(\.\d+)?$`)
var agentAllowDomains = regexp.MustCompile(`--allow-domains\s+([^\s]+)`)

func firewallLogPath(name string) bool {
	if excludedAuditPath(name) || !strings.HasSuffix(name, ".log") {
		return false
	}
	dir, base := path.Dir(name), path.Base(name)
	return strings.Contains(dir, "/sandbox/firewall/logs") || strings.HasPrefix(dir, "sandbox/firewall/logs") ||
		strings.HasPrefix(path.Base(dir), "squid-logs") || strings.HasPrefix(path.Base(dir), "firewall-logs") ||
		strings.Contains(base, "firewall") || (strings.Contains(base, "access") && !strings.Contains(base, "access-"))
}

// Mirrors github/gh-aw v1.0.1 firewall_log.go. Raw access logs take
// precedence over the activity summary; neither URLs nor user agents survive.
func enrichNetworkAudit(ctx context.Context, fields map[string]any, files map[string][]byte, limits Limits) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	totalBytes, work := 0, 0
	for name, content := range files {
		if firewallLogPath(name) || (path.Base(name) == "agent-stdio.log" && engineEvidencePath(name)) {
			totalBytes += len(content)
			work += bytes.Count(content, []byte{'\n'})
			if len(content) > 0 && content[len(content)-1] != '\n' {
				work++
			}
		}
	}
	if totalBytes > limits.defaults().Bytes || work > limits.defaults().Entries {
		return nil, ErrCapacity
	}
	summary := fields["firewall_analysis"]
	delete(fields, "firewall_analysis")
	gaps, err := enrichRawNetworkAudit(ctx, fields, files, limits)
	if err != nil {
		return nil, err
	}
	names := candidates(files, "agent-stdio.log")
	names = slices.DeleteFunc(names, func(name string) bool { return !engineEvidencePath(name) })
	if len(names) > 1 {
		gaps = append(gaps, "ambiguous-agent-firewall-log")
	} else if len(names) == 1 {
		domains := map[string]bool{}
		content := files[names[0]]
		valid := utf8.Valid(content) && bytes.IndexByte(content, 0) < 0
		err = scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
			match := agentAllowDomains.FindSubmatch(raw)
			if len(match) > 1 {
				for domain := range strings.SplitSeq(strings.Trim(string(match[1]), `"`), ",") {
					domain = strings.TrimSpace(domain)
					if domain != "" {
						if !safeFirewallDomain(domain) {
							valid = false
						}
						domains[domain] = true
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if !valid {
			gaps = append(gaps, "invalid-agent-firewall-log")
		} else if len(domains) > 0 {
			analysis := object(fields["firewall_analysis"])
			if analysis == nil {
				analysis = map[string]any{}
				fields["firewall_analysis"] = analysis
			}
			requests := object(analysis["requests_by_domain"])
			if requests == nil {
				requests = map[string]any{}
			}
			analysis["total_requests"] = engineCounter(analysis["total_requests"]) + len(domains)
			analysis["blocked_requests"] = engineCounter(analysis["blocked_requests"]) + len(domains)
			analysis["requests_by_domain"] = requests
			for domain := range domains {
				stats := object(requests[domain])
				if stats == nil {
					stats = map[string]any{"allowed": 0}
				}
				stats["blocked"] = engineCounter(stats["blocked"]) + 1
				requests[domain] = stats
			}
		}
	}
	if fields["firewall_analysis"] == nil && summary != nil {
		fields["firewall_analysis"] = summary
	}
	return gaps, nil
}

func enrichRawNetworkAudit(ctx context.Context, fields map[string]any, files map[string][]byte, limits Limits) ([]string, error) {
	limits = limits.defaults()
	groups := map[string][]string{}
	bestRank := 5
	for name := range files {
		if !firewallLogPath(name) {
			continue
		}
		dir := path.Dir(name)
		rank := 4
		switch {
		case strings.HasSuffix(dir, "sandbox/firewall/logs/squid-logs"):
			rank = 1
		case strings.HasSuffix(dir, "sandbox/firewall/logs"):
			rank = 2
		case strings.HasPrefix(path.Base(dir), "squid-logs"), strings.HasPrefix(path.Base(dir), "firewall-logs"):
			rank = 3
		}
		if rank < bestRank {
			groups, bestRank = map[string][]string{}, rank
		}
		if rank == bestRank {
			groups[dir] = append(groups[dir], name)
		}
	}
	if len(groups) == 0 {
		return nil, nil
	}
	if len(groups) != 1 {
		return []string{"ambiguous-firewall-logs"}, nil
	}
	names := groups[slices.Collect(maps.Keys(groups))[0]]
	slices.Sort(names)
	if bestRank == 4 {
		names = names[:1]
	}
	total, allowed, blocked, bytesUsed, work := 0, 0, 0, 0, 0
	domains := map[string]any{}
	for _, name := range names {
		bytesUsed += len(files[name])
		if bytesUsed > limits.Bytes {
			return nil, ErrCapacity
		}
		err := scanEvidence(ctx, files[name], limits, func(_ int, raw []byte) error {
			work++
			if work > limits.Entries {
				return ErrCapacity
			}
			line := strings.TrimSpace(string(raw))
			if strings.HasPrefix(line, "#") {
				return nil
			}
			parts := firewallFields.FindAllString(line, -1)
			if len(parts) < 10 || !firewallTimestamp.MatchString(parts[0]) {
				return nil
			}
			client, domain, destination, status, decision := parts[1], parts[2], parts[3], parts[6], parts[7]
			if strings.HasPrefix(client, "::1:") && domain == "-" && (destination == "-:-" || destination == "-") {
				return nil
			}
			if domain == "-" {
				domain = destination
				if destination == "-" || destination == "-:-" {
					domain = "(unknown)"
				}
			}
			if domain != "(unknown)" && !safeFirewallDomain(domain) {
				return errors.New("invalid firewall destination")
			}
			permit := false
			switch code, _ := strconv.Atoi(status); code {
			case 200, 206, 304:
				permit = true
			case 403, 407:
			default:
				permit = strings.Contains(decision, "TCP_TUNNEL") || strings.Contains(decision, "TCP_HIT") || strings.Contains(decision, "TCP_MISS")
			}
			stats := object(domains[domain])
			if stats == nil {
				stats = map[string]any{"allowed": 0, "blocked": 0}
				domains[domain] = stats
			}
			total++
			if permit {
				allowed++
				stats["allowed"] = engineCounter(stats["allowed"]) + 1
			} else {
				blocked++
				stats["blocked"] = engineCounter(stats["blocked"]) + 1
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, err
			}
			return []string{"invalid-firewall-logs"}, nil
		}
	}
	fields["firewall_analysis"] = map[string]any{"total_requests": total, "allowed_requests": allowed,
		"blocked_requests": blocked, "requests_by_domain": domains}
	return nil, nil
}

func safeFirewallDomain(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && !strings.ContainsRune(".-_:[]*", char) {
			return false
		}
	}
	return true
}

func enrichPrivacyAudit(ctx context.Context, fields map[string]any, files map[string][]byte, limits Limits) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := candidates(files, "redacted-urls.log")
	names = slices.DeleteFunc(names, excludedAuditPath)
	if len(names) == 0 {
		return nil, nil
	}
	if len(names) > 1 {
		return []string{"ambiguous-redacted-domains-log"}, nil
	}
	domains := map[string]bool{}
	err := scanEvidence(ctx, files[names[0]], limits, func(_ int, raw []byte) error {
		domain := strings.TrimSpace(string(raw))
		if domain != "" && !strings.HasPrefix(domain, "#") {
			domains[domain] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	fields["redacted_domains_analysis"] = map[string]any{"total_domains": len(domains)}
	return nil, nil
}
