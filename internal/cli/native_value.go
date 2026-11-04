package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const (
	nativeValueMaxInput    = 8 * 1024 * 1024
	nativeValueMaxResponse = 64 * 1024
)

type nativeValueIdentity struct {
	id, observedAt string
}

type nativeValueResult struct {
	ID         string `json:"id"`
	Revision   int64  `json:"revision"`
	Updated    bool   `json:"updated"`
	ObservedAt string `json:"observedAt"`
}

func nativeValuePublish(cmd *cobra.Command, args []string) error {
	if len(args) != 2 || len(args[0]) > 200 || !repositoryPattern.MatchString(args[0]) || !slugPattern.MatchString(args[1]) {
		return errors.New("value publish requires OWNER/REPO and an exact lowercase workflow slug")
	}
	endpoint, local, err := nativeValueEndpoint(nativeString(cmd, "server"))
	if err != nil {
		return err
	}
	headers, err := nativeValueAuth(local, os.Getenv)
	if err != nil {
		return err
	}
	content, err := nativeValueReadInput(nativeString(cmd, "input"))
	if err != nil {
		return err
	}
	identity, err := nativeValuePacket(content, args[0], args[1])
	if err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if local {
		// Local credentials must not leave loopback via an environment proxy.
		transport.Proxy = nil
	}
	defer transport.CloseIdleConnections()
	result, err := nativeValuePost(cmd.Context(), endpoint, content, identity, headers, &http.Client{
		Timeout: 30 * time.Second, Transport: transport,
	})
	if err != nil {
		return err
	}
	return printJSON(cmd, result)
}

func nativeValueEndpoint(raw string) (*url.URL, bool, error) {
	return nativeServerEndpoint(raw, "/api/v1/value")
}

func nativeServerEndpoint(raw, endpoint string) (*url.URL, bool, error) {
	invalid := errors.New("--server requires an HTTPS base URL without credentials, query, fragment, or ambiguous path (HTTP is allowed only on loopback)")
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" || target.Opaque != "" || target.User != nil ||
		target.RawQuery != "" || target.ForceQuery || strings.Contains(raw, "#") ||
		target.RawPath != "" || strings.Contains(target.Path, `\`) {
		return nil, false, invalid
	}
	host := target.Hostname()
	local := strings.EqualFold(host, "localhost")
	if address, err := netip.ParseAddr(host); err == nil && address.Zone() == "" {
		local = address.Unmap().IsLoopback()
	}
	if target.Scheme != "https" && (target.Scheme != "http" || !local) {
		return nil, false, invalid
	}
	if target.Port() != "" {
		port, err := strconv.Atoi(target.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, false, invalid
		}
	} else if strings.HasSuffix(target.Host, ":") {
		return nil, false, invalid
	}
	base := strings.TrimSuffix(target.Path, "/")
	if base != "" && (path.Clean(base) != base || !strings.HasPrefix(base, "/")) {
		return nil, false, invalid
	}
	target.Path = base + endpoint
	return target, local, nil
}

func nativeValueAuth(local bool, getenv func(string) string) (http.Header, error) {
	session, csrf, token := getenv("CAO_SERVER_SESSION"), getenv("CAO_SERVER_CSRF"), getenv("CAO_SERVER_TOKEN")
	invalid := errors.New("publication requires safe CAO_SERVER_SESSION and CAO_SERVER_CSRF values, or only CAO_SERVER_TOKEN for loopback; do not combine authentication modes")
	headers := make(http.Header)
	if token != "" {
		if !local || session != "" || csrf != "" || !nativeValueSafeCredential(token) {
			return nil, invalid
		}
		headers.Set("Authorization", "Bearer "+token)
		return headers, nil
	}
	if !nativeValueSafeCredential(session) || !nativeValueSafeCredential(csrf) ||
		strings.ContainsAny(session, `";\,`) {
		return nil, invalid
	}
	headers.Set("Cookie", "cao_session="+session)
	headers.Set("X-CSRF-Token", csrf)
	return headers, nil
}

func nativeValueSafeCredential(value string) bool {
	if value == "" || len(value) > 16384 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func nativeValueReadInput(filename string) ([]byte, error) {
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("--input must be a readable regular evidence file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, errors.New("unable to open --input evidence file")
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("--input must be a regular evidence file")
	}
	if info.Size() > nativeValueMaxInput {
		return nil, errors.New("--input exceeds the 8 MiB publication limit")
	}
	content, err := io.ReadAll(io.LimitReader(file, nativeValueMaxInput+1))
	if err != nil {
		return nil, errors.New("unable to read --input evidence file")
	}
	if len(content) > nativeValueMaxInput {
		return nil, errors.New("--input exceeds the 8 MiB publication limit")
	}
	return content, nil
}

func nativeValuePacket(content []byte, repository, workflow string) (nativeValueIdentity, error) {
	invalid := errors.New("--input must contain exactly one schema-v2 operational_value_evidence envelope matching OWNER/REPO, WORKFLOW, and its Markdown source path")
	if len(content) > nativeValueMaxInput || !utf8.Valid(content) || !json.Valid(content) {
		return nativeValueIdentity{}, invalid
	}
	decoded, err := nativeDecodeJSON(content)
	if err != nil {
		return nativeValueIdentity{}, invalid
	}
	envelope, ok := decoded.(map[string]any)
	if !ok || len(envelope) != 3 || envelope["schema_version"] != json.Number("2") || envelope["kind"] != "operational_value_evidence" {
		return nativeValueIdentity{}, invalid
	}
	packet, ok := envelope["operational_value_evidence"].(map[string]any)
	if !ok || packet["schemaVersion"] != json.Number("1") {
		return nativeValueIdentity{}, invalid
	}
	packetRepo, _ := packet["repository"].(string)
	id, _ := packet["id"].(string)
	observedAt, _ := packet["observedAt"].(string)
	sourcePath := ".github/workflows/" + workflow + ".md"
	expectedID := nativeValuePacketID(packet, repository, workflow)
	if !strings.EqualFold(packetRepo, repository) || packet["workflow"] != workflow ||
		packet["sourcePath"] != sourcePath || expectedID == "" || id != expectedID || len(id) > 16384 ||
		!nativeValueInstant(observedAt) {
		return nativeValueIdentity{}, invalid
	}
	for field, expected := range map[string]string{
		"repositoryId": "repository:" + ncEncode(strings.ToLower(repository)),
		"workflowId":   "workflow:" + ncEncode(strings.ToLower(repository)+":"+sourcePath),
	} {
		if actual, exists := packet[field]; exists && actual != expected {
			return nativeValueIdentity{}, invalid
		}
	}
	// Fact and frozen-contract validation belongs to the server's typed boundary.
	// Retain original bytes, including numbers, whitespace and absent fields.
	return nativeValueIdentity{id: id, observedAt: observedAt}, nil
}

func nativeValuePacketID(packet map[string]any, repository, workflow string) string {
	facts, _ := packet["facts"].([]any)
	var metric, version string
	contracts := 0
	for _, value := range facts {
		fact, ok := value.(map[string]any)
		if ok && fact["fact-kind"] == "contract" {
			contracts++
			metric, _ = fact["metric-id"].(string)
			version, _ = fact["metric-version"].(string)
		}
	}
	if contracts != 1 || strings.TrimSpace(metric) == "" || strings.TrimSpace(version) == "" {
		return ""
	}
	return "operational-value-evidence:" + ncEncode(strings.ToLower(repository)+":"+workflow+":"+metric+":"+version)
}

func nativeValueInstant(value string) bool {
	instant, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && len(value) >= 20 && instant.Format("2006-01-02T15:04:05") == value[:19] &&
		strings.HasSuffix(value, "Z") && !strings.Contains(value, ",")
}

func nativeValuePost(ctx context.Context, endpoint *url.URL, content []byte, identity nativeValueIdentity, headers http.Header, client *http.Client) (*nativeValueResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(content))
	if err != nil {
		return nil, errors.New("unable to construct value publication request")
	}
	request.Header = headers.Clone()
	request.Header.Set("Content-Type", "application/x-ndjson")
	request.Header.Set("Accept", "application/json")
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("value publication failed: transport or timeout error")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, errors.New("value publication denied: authentication, administrator authorization, and OAuth step-up are required")
		case http.StatusNotFound:
			return nil, errors.New("value publication failed: unknown repository or workflow")
		case http.StatusConflict:
			return nil, errors.New("value publication conflict: stale capture, conflicting same-capture evidence, or changed frozen contract; prepare and review evidence before retrying")
		case http.StatusServiceUnavailable:
			return nil, errors.New("value publication unavailable: server storage is unavailable")
		default:
			return nil, fmt.Errorf("value publication failed: HTTP %d (redirects are not followed)", response.StatusCode)
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, nativeValueMaxResponse+1))
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(body) > nativeValueMaxResponse {
		return nil, errors.New("invalid value publication response: unreadable or oversized JSON")
	}
	decoded, err := nativeDecodeJSON(body)
	object, ok := decoded.(map[string]any)
	observedAt, _ := object["observedAt"].(string)
	receiptTime, receiptErr := time.Parse(time.RFC3339Nano, observedAt)
	preparedTime, preparedErr := time.Parse(time.RFC3339Nano, identity.observedAt)
	if err != nil || !ok || len(object) != 4 ||
		object["id"] != identity.id || !nativeValueInstant(observedAt) ||
		receiptErr != nil || preparedErr != nil || !receiptTime.Equal(preparedTime) {
		return nil, errors.New("invalid value publication response: receipt identity does not match the prepared evidence")
	}
	updated, updatedOK := object["updated"].(bool)
	revision, revisionOK := object["revision"].(json.Number)
	revisionNumber, revisionErr := revision.Int64()
	if !updatedOK || !revisionOK || revisionErr != nil || revisionNumber < 1 {
		return nil, errors.New("invalid value publication response: expected revision and updated fields")
	}
	return &nativeValueResult{ID: identity.id, Revision: revisionNumber, Updated: updated, ObservedAt: identity.observedAt}, nil
}
