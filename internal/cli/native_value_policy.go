package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	nativeCollectorPath = regexp.MustCompile(`^[A-Za-z0-9._/-]+\.mjs$`)
	nativeEvidencePath  = regexp.MustCompile(`^\.github/value-evidence/[A-Za-z0-9._/-]+\.jsonl(?:\.gz)?$`)
	nativeSHA256        = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func nativeWorkflowValuesRule(value any, at string) error {
	entries, ok := value.([]any)
	if !ok || len(entries) > 1000 {
		return fmt.Errorf("%s must be a bounded workflow-values list", at)
	}
	seen := map[string]bool{}
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return errors.New("workflow-values entries must be objects")
		}
		for key := range entry {
			if key != "repository" && key != "workflow" && key != "collector" && key != "evidence" && key != "api-requests" && key != "enabled" {
				return errors.New("unknown workflow-values entry field")
			}
		}
		enabled := true
		if value, exists := entry["enabled"]; exists {
			var valid bool
			enabled, valid = value.(bool)
			if !valid {
				return errors.New("workflow-values enabled must be boolean")
			}
		}
		repository, _ := entry["repository"].(string)
		workflow, _ := entry["workflow"].(string)
		collector, _ := entry["collector"].(string)
		if !repositoryPattern.MatchString(repository) || !slugPattern.MatchString(workflow) {
			return errors.New("invalid workflow-values identity")
		}
		_, hasCollector := entry["collector"]
		if (enabled || hasCollector) && !nativeCollectorPath.MatchString(collector) {
			return errors.New("invalid workflow-values identity or collector")
		}
		if value, exists := entry["api-requests"]; exists {
			if nativeInteger(1, 1000)(value, "workflow-values api-requests") != nil || !hasCollector {
				return errors.New("workflow-values api-requests must be an integer from 1 through 1000 with a collector")
			}
		}
		for _, part := range strings.Split(collector, "/") {
			if hasCollector && (part == "" || part == "." || part == "..") {
				return errors.New("collector must be a confined repository-relative .mjs path")
			}
		}
		if value, exists := entry["evidence"]; exists {
			evidence, ok := value.(map[string]any)
			if !ok || len(evidence) != 2 {
				return errors.New("workflow-values evidence must contain path and sha256")
			}
			evidencePath, pathOK := evidence["path"].(string)
			digest, digestOK := evidence["sha256"].(string)
			if !pathOK || !digestOK || !nativeEvidencePath.MatchString(evidencePath) || !nativeSHA256.MatchString(digest) {
				return errors.New("invalid workflow-values evidence reference")
			}
			for _, part := range strings.Split(evidencePath, "/") {
				if part == "" || part == "." || part == ".." {
					return errors.New("evidence must be a confined reviewed repository-relative path")
				}
			}
		}
		key := strings.ToLower(repository) + ":" + workflow
		if seen[key] {
			return errors.New("duplicate workflow-values identity")
		}
		seen[key] = true
	}
	return nil
}

func nativeValueConfigure(cmd *cobra.Command, args []string, enable bool) error {
	if len(args) != 2 || !repositoryPattern.MatchString(args[0]) || !slugPattern.MatchString(args[1]) {
		return errors.New("value enable/disable requires exact OWNER/REPO WORKFLOW")
	}
	policy, err := nativeReadValidatedPolicy()
	if err != nil {
		return err
	}
	control, ok := policy["control-plane"].(map[string]any)
	if !ok {
		return errors.New("a reviewed local control-plane policy is required")
	}
	campaigns, _ := control["campaigns"].(map[string]any)
	if campaigns == nil {
		campaigns = map[string]any{}
		control["campaigns"] = campaigns
	}
	activity, _ := campaigns["activity"].(map[string]any)
	if activity == nil {
		activity = map[string]any{}
		campaigns["activity"] = activity
	}
	entries, _ := activity["workflow-values"].([]any)
	updated := []any{}
	var existing map[string]any
	for _, value := range entries {
		entry := value.(map[string]any)
		if !strings.EqualFold(entry["repository"].(string), args[0]) || entry["workflow"] != args[1] {
			updated = append(updated, entry)
		} else {
			existing = entry
		}
	}
	if enable && nativeBool(cmd, "if-absent") && existing != nil {
		return printJSON(cmd, map[string]any{
			"repository": args[0], "workflow": args[1], "enabled": existing["enabled"] != false,
			"policy": policyPath, "message": "Existing registration or explicit disable preserved; local policy is unchanged.",
		})
	}
	if enable {
		collector := nativeString(cmd, "collector")
		entry := map[string]any{"repository": args[0], "workflow": args[1], "collector": collector}
		if existing != nil && existing["evidence"] != nil {
			entry["evidence"] = existing["evidence"]
		}
		requests := nativeString(cmd, "api-requests")
		if requests != "" {
			value, err := strconv.Atoi(requests)
			if err != nil || value < 1 || value > 1000 {
				return errors.New("--api-requests must be an integer from 1 through 1000")
			}
			entry["api-requests"] = value
		} else if existing != nil && existing["api-requests"] != nil {
			entry["api-requests"] = existing["api-requests"]
		}
		if err := nativeWorkflowValuesRule([]any{entry}, "workflow-values"); err != nil {
			return err
		}
		enrollment, _ := control["activity"].(map[string]any)
		enrolled, _ := enrollment["repositories"].([]any)
		found := false
		for _, repository := range enrolled {
			found = found || strings.EqualFold(repository.(string), args[0])
		}
		if !found {
			return errors.New("repository must already be explicitly enrolled in control-plane.activity.repositories; value enable never enrolls repositories")
		}
		current := "."
		for _, part := range strings.Split(collector, "/") {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("collector must exist locally without symlink path components")
			}
		}
		info, err := os.Stat(collector)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("collector must be a regular local .mjs file")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
		defer cancel()
		inspect := exec.CommandContext(ctx, "node", "--input-type=module", "-e", `
import {pathToFileURL} from 'node:url';
import path from 'node:path';
const {definition:d,collectValueEvidence:c}=await import(pathToFileURL(path.resolve(process.argv[1])).href);
if(d?.schemaVersion!==3 || d.repository?.toLowerCase()!==process.argv[2].toLowerCase()
 || d.slug!==process.argv[3] || d.sourcePath!=='.github/workflows/'+process.argv[3]+'.md'
 || !Array.isArray(d.evidence?.repositories) || !d.evidence.repositories.length
 || d.evidence.repositories.some(r=>r.toLowerCase()!==process.argv[2].toLowerCase())
 || !Number.isFinite(Date.parse(d.adoption?.adoptedAt))
 || !Number.isSafeInteger(d.evidence?.window?.maturationDays) || d.evidence.window.maturationDays<0
 || !(d.evidence?.window?.cadenceDays>0) || typeof c!=='function') process.exit(1);
`, collector, args[0], args[1])
		inspect.Env = []string{"PATH=" + os.Getenv("PATH")}
		if err := inspect.Run(); err != nil {
			return errors.New("collector must export the exact schemaVersion 3 frozen definition and collectValueEvidence; Node inspection failed")
		}
		updated = append(updated, entry)
	} else {
		if existing == nil {
			existing = map[string]any{"repository": args[0], "workflow": args[1]}
		}
		existing["enabled"] = false
		updated = append(updated, existing)
	}
	activity["workflow-values"] = updated
	if err := nativeValidatePolicy(policy); err != nil {
		return err
	}
	if err := writePolicy(policy, false); err != nil {
		return err
	}
	return printJSON(cmd, map[string]any{
		"repository": args[0], "workflow": args[1], "enabled": enable, "policy": policyPath,
		"message": "Local policy only. Review and commit the policy and collector, then deploy/push before recurring collection changes. No repository enrollment or workflow activation was changed; historical evidence is retained.",
	})
}
