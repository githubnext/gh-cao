package auditdelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxObjectBytes = 128 << 10

// HTTPDestination uses an explicitly configured HTTPS object adapter. This
// protocol is not a claim that an arbitrary HTTP endpoint is durable or WORM.
type HTTPDestination struct {
	base   string
	token  string
	client *http.Client
}

func NewHTTPDestination(endpoint, token string) (*HTTPDestination, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, errors.New("audit delivery requires an HTTPS destination without URL credentials, query, or fragment")
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("audit delivery requires a destination bearer token from the secret environment")
	}
	return &HTTPDestination{base: strings.TrimRight(endpoint, "/"), token: token, client: &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}, nil
}

func (d *HTTPDestination) Load(ctx context.Context, stream string) (Checkpoint, string, bool, error) {
	var checkpoint Checkpoint
	body, version, status, err := d.request(ctx, http.MethodGet, stream+"/checkpoint.json", nil, "", "")
	if err != nil {
		return checkpoint, "", false, err
	}
	if status == http.StatusNotFound {
		return checkpoint, "", false, nil
	}
	if status != http.StatusOK || !strongETag(version) {
		return checkpoint, "", false, errors.New("audit destination checkpoint read requires 200 and a strong ETag")
	}
	if err := json.Unmarshal(body, &checkpoint); err != nil {
		return checkpoint, "", false, errors.New("audit destination checkpoint is not valid JSON")
	}
	return checkpoint, version, true, nil
}

func (d *HTTPDestination) Create(ctx context.Context, key string, body []byte) error {
	_, _, status, err := d.request(ctx, http.MethodPut, key, body, "If-None-Match", "*")
	if err != nil {
		return err
	}
	if err := createStatusError(status); err != nil {
		return err
	}
	// A duplicate is accepted only if every byte agrees. This also makes a lost
	// delivery acknowledgement safe to retry on a later invocation.
	return d.confirm(ctx, key, body, false)
}

func (d *HTTPDestination) Commit(ctx context.Context, stream string, checkpoint Checkpoint, version string) error {
	body, err := json.Marshal(checkpoint)
	if err != nil {
		return errors.New("audit destination checkpoint encoding failed")
	}
	header, condition := "If-Match", version
	if version == "" {
		header, condition = "If-None-Match", "*"
	}
	key := stream + "/checkpoint.json"
	_, _, status, err := d.request(ctx, http.MethodPut, key, body, header, condition)
	if err != nil {
		return err
	}
	if err := commitStatusError(status); err != nil {
		return err
	}
	return d.confirm(ctx, key, body, true)
}

// createStatusError accepts a precondition failure because the duplicate is
// verified byte-for-byte by the read-back that follows.
func createStatusError(status int) error {
	switch status {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusPreconditionFailed:
		return nil
	}
	deliveryLog.Printf("destination create rejected: status=%d", status)
	return fmt.Errorf("audit destination create rejected (HTTP %d)", status)
}

func commitStatusError(status int) error {
	switch status {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusPreconditionFailed:
		deliveryLog.Printf("destination checkpoint conflict: status=%d", status)
		return errors.New("audit destination checkpoint conflict; retry from the destination checkpoint")
	}
	deliveryLog.Printf("destination checkpoint rejected: status=%d", status)
	return fmt.Errorf("audit destination checkpoint rejected (HTTP %d)", status)
}

func (d *HTTPDestination) confirm(ctx context.Context, key string, expected []byte, checkpoint bool) error {
	body, version, status, err := d.request(ctx, http.MethodGet, key, nil, "", "")
	if err != nil {
		return err
	}
	if status != http.StatusOK || !bytes.Equal(body, expected) || (checkpoint && !strongETag(version)) {
		return errors.New("audit destination read-back acknowledgement missing or mismatched")
	}
	return nil
}

func strongETag(value string) bool {
	if len(value) < 2 || len(value) > 1024 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for _, b := range []byte(value[1 : len(value)-1]) {
		if b < 0x21 || b == '"' || b == 0x7f {
			return false
		}
	}
	return true
}

func (d *HTTPDestination) request(ctx context.Context, method, key string, body []byte, header, condition string) ([]byte, string, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", 0, err
	}
	if len(body) > maxObjectBytes {
		return nil, "", 0, errors.New("audit destination object exceeds the size bound")
	}
	// #nosec G704 -- destination is explicit operator configuration; keys are generated, never event attributes.
	req, err := http.NewRequestWithContext(ctx, method, d.base+"/"+key, bytes.NewReader(body))
	if err != nil {
		return nil, "", 0, errors.New("audit destination request construction failed")
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-store, no-cache")
	if header != "" {
		req.Header.Set(header, condition)
	}
	// #nosec G704 -- validated operator-selected HTTPS endpoint, bounded client, redirects disabled.
	response, err := d.client.Do(req)
	if err != nil {
		// net/http errors can include the URL. Neither it nor the receiver's
		// response body is appropriate in operational logs.
		return nil, "", 0, errors.New("audit destination transport failed or was interrupted")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxObjectBytes+1))
	if err != nil || len(data) > maxObjectBytes {
		return nil, "", 0, errors.New("audit destination response unreadable or exceeds the size bound")
	}
	return data, response.Header.Get("ETag"), response.StatusCode, nil
}
