package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/ghcli"
)

const evidenceResponseLimit = 2 * 1024 * 1024

var (
	evidenceRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	evidenceHashPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	evidenceDigestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	evidenceRepoPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	evidenceImagePattern    = regexp.MustCompile(`^ghcr\.io/([a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*)/cao-server@sha256:([0-9a-f]{64})$`)
)

type evidenceState struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func evidenceUnknown(reason string) evidenceState {
	return evidenceState{Status: "unknown", Reason: reason}
}

type evidenceDeps struct {
	gh       func(context.Context, ...string) (string, error)
	now      func() time.Time
	version  string
	identity func() (string, error)
}

func defaultEvidenceDeps(version string) evidenceDeps {
	return evidenceDeps{
		gh: func(ctx context.Context, args ...string) (string, error) {
			return ghcli.ExecBounded(ctx, evidenceResponseLimit, args...)
		},
		now: time.Now, version: version, identity: evidenceBinaryHash,
	}
}

type evidenceReleaseOptions struct {
	Repository, Revision, Image, Output string
	PackageRun, PromotionRun            int64
	Verify                              bool
}

func (o evidenceReleaseOptions) validate() error {
	match := evidenceImagePattern.FindStringSubmatch(o.Image)
	if !evidenceRepoPattern.MatchString(o.Repository) || !evidenceRevisionPattern.MatchString(o.Revision) ||
		!evidenceID(o.PackageRun) || o.PromotionRun < 0 || o.PromotionRun > 1<<53-1 ||
		len(match) != 3 || match[1] != strings.ToLower(o.Repository) || o.Output == "" {
		return errors.New("explicit repository, full source SHA, positive run IDs, exact repository GHCR image digest and new output path are required")
	}
	return nil
}

func evidenceCommand(deps evidenceDeps) *cobra.Command {
	group := &cobra.Command{
		Use: "evidence", Short: "Collect bounded operator evidence without changing services or authority",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	options := evidenceReleaseOptions{}
	release := &cobra.Command{
		Use: "release", Short: "Collect exact release identities, check references and available verification receipts",
		Long: `Collect bounded, read-only release evidence from selected GitHub.com workflow runs.

This native Go command requires no Node.js or repository checkout. It writes a new
JSON file (mode 0600 where supported) and never publishes, promotes, deploys or
changes authority. Operators must restrict the destination directory's ACLs.
Evidence remains incomplete: approval history is not proof of independent approval,
configured image read-back is not running-image measurement, and artifact references
do not establish independent retention, governance or Type 2 effectiveness.

Optional provenance verification uses gh attestation verify with the exact image,
source digest and reusable publisher.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("promotion-run") && !evidenceID(options.PromotionRun) {
				return errors.New("--promotion-run must be a positive run ID")
			}
			if err := options.validate(); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
			defer cancel()
			report, err := collectReleaseEvidence(ctx, deps, options)
			if err != nil {
				return err
			}
			if err := writeReleaseEvidence(ctx, options.Output, report); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Release evidence written; evidence remains incomplete.")
			return err
		},
	}
	flags := release.Flags()
	flags.StringVarP(&options.Repository, "repo", "R", "", "Explicit OWNER/REPO whose evidence to collect")
	flags.StringVar(&options.Revision, "revision", "", "Exact 40-character product source SHA")
	flags.StringVar(&options.Image, "image", "", "Exact ghcr.io/OWNER/REPO/cao-server@sha256:DIGEST")
	flags.Int64Var(&options.PackageRun, "package-run", 0, "Existing CAO package workflow run ID")
	flags.Int64Var(&options.PromotionRun, "promotion-run", 0, "Optional existing prerelease-to-latest promotion run ID")
	flags.BoolVar(&options.Verify, "verify-provenance", false, "Verify exact image/source/publisher/run-attempt provenance with gh")
	flags.StringVar(&options.Output, "output", "", "New JSON file to write (mode 0600 where supported); never overwritten")
	group.AddCommand(release)
	return group
}

func evidenceID(value int64) bool { return value > 0 && value <= 1<<53-1 }

func evidenceSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func evidenceBinaryHash() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", errors.New("collector executable identity unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("collector executable identity unavailable")
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	const limit = 512 * 1024 * 1024
	count, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil || count > limit {
		return "", errors.New("collector executable identity unavailable")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeReleaseEvidence(ctx context.Context, path string, report evidenceReleaseReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return errors.New("cannot encode release evidence")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("cannot create release evidence output; choose a new writable path")
	}
	_, writeErr := file.Write(append(content, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		if err := os.Remove(path); err != nil {
			return errors.New("release evidence output failed; incomplete file could not be removed")
		}
		return errors.New("release evidence output failed; incomplete file removed")
	}
	return nil
}

func evidenceConclusion(value string) string {
	switch value {
	case "success", "failure", "neutral", "cancelled", "skipped", "timed_out", "action_required", "stale", "startup_failure":
		return value
	default:
		return "unknown"
	}
}

func evidenceTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Location() == time.UTC && strings.HasSuffix(value, "Z")
}

type evidenceClient struct {
	ctx        context.Context
	deps       evidenceDeps
	repository string
}

func (c evidenceClient) command(args []string, result any) ([]byte, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
	defer cancel()
	raw, err := c.deps.gh(ctx, args...)
	if c.ctx.Err() != nil {
		return nil, c.ctx.Err()
	}
	if err != nil {
		return nil, errors.New("GitHub evidence unavailable; subprocess diagnostics withheld")
	}
	if len(raw) > evidenceResponseLimit || json.Unmarshal([]byte(raw), result) != nil {
		return nil, errors.New("GitHub evidence response is invalid or exceeds its bound")
	}
	return []byte(raw), nil
}

func (c evidenceClient) get(path string, result any) error {
	_, err := c.command([]string{"api", "--hostname", "github.com", "--method", "GET", "repos/" + c.repository + "/" + path}, result)
	return err
}
