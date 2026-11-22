package tokenbroker

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

const bodyLimit = 65536

var errInvalid = errors.New("invalid broker input")

var providerID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type request struct {
	tokenType, provider, identity string
	invalidate                    bool
}

// uniqueJSON rejects duplicate members at every nesting depth, not just at the
// top level. It is also used for reviewed policy and upstream security fields.
func uniqueJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errInvalid
		}
		t, err := d.Token()
		if err != nil {
			return errInvalid
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				s, ok := k.(string)
				if err != nil || !ok || seen[s] {
					return errInvalid
				}
				seen[s] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errInvalid
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return errInvalid
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return errInvalid
	}
	return nil
}

func decodeRequest(w http.ResponseWriter, r *http.Request) (request, int) {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return request{}, http.StatusUnsupportedMediaType
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bodyLimit))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return request{}, http.StatusRequestEntityTooLarge
	}
	if err != nil || uniqueJSON(body) != nil {
		return request{}, http.StatusBadRequest
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return request{}, http.StatusBadRequest
	}
	// job_id is diagnostic only. Never retain it in the authorization or
	// credential registry, whose job key comes from signed identity evidence.
	for _, raw := range fields {
		var value string
		if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
			return request{}, http.StatusBadRequest
		}
	}
	delete(fields, "job_id")
	read := func(name string) string {
		var s string
		_ = json.Unmarshal(fields[name], &s)
		return s
	}
	out := request{tokenType: read("token_type"), provider: read("provider"), identity: read("oidc_token")}
	if out.identity == "" {
		return request{}, http.StatusBadRequest
	}
	if _, exists := fields["operation"]; exists {
		if read("operation") != "invalidate" || len(fields) != 2 {
			return request{}, http.StatusBadRequest
		}
		out.invalidate = true
		return out, 0
	}
	if out.tokenType == "" {
		return request{}, http.StatusBadRequest
	}
	switch out.tokenType {
	case "copilot-request":
		if len(fields) != 3 || !providerID.MatchString(out.provider) {
			return request{}, http.StatusBadRequest
		}
	case "copilot-task", "github-app":
		if len(fields) != 2 {
			return request{}, http.StatusBadRequest
		}
	default:
		// Unknown types still have to satisfy the string-member envelope.
		for name, raw := range fields {
			var s string
			if (name != "token_type" && name != "provider" && name != "oidc_token") || json.Unmarshal(raw, &s) != nil {
				return request{}, http.StatusBadRequest
			}
		}
		return request{}, http.StatusUnprocessableEntity
	}
	return out, 0
}

func safeToken(token string) bool {
	if len(token) == 0 || len(token) > bodyLimit/2 {
		return false
	}
	for _, c := range token {
		if c < '!' || c > '~' {
			return false
		}
	}
	return true
}
