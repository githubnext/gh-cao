package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var kustoIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,100}$`)

// Kusto bridges only the streaming transport missing from Microsoft's emulator.
// Queries, mappings, deduplication, hashes and functions run in the real engine.
// This is not an authentication, streaming visibility or retention emulator.
type Kusto struct {
	endpoint, database string
	client             *http.Client
	tables             map[string]bool
}

func NewKusto(endpoint, database string, tables []string) (*Kusto, error) {
	if !kustoIdentifier.MatchString(database) {
		return nil, errors.New("invalid simulator database")
	}
	transport, err := NewLocalTransport(endpoint)
	if err != nil {
		return nil, err
	}
	if transport.target.Path != "" {
		return nil, errors.New("Kusto simulator endpoint must not contain a path")
	}
	k := &Kusto{endpoint: endpoint, database: database, tables: map[string]bool{},
		client: &http.Client{Transport: transport, Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("Kusto simulator redirects are forbidden")
			}}}
	for _, table := range tables {
		if !kustoIdentifier.MatchString(table) {
			return nil, errors.New("invalid simulator table")
		}
		k.tables[table] = true
	}
	return k, nil
}

func (k *Kusto) Close() { k.client.CloseIdleConnections() }

func (k *Kusto) Execute(ctx context.Context, path, query string) ([]byte, error) {
	if path != "/v1/rest/mgmt" && path != "/v1/rest/query" {
		return nil, errors.New("unsupported Kusto API")
	}
	body, err := json.Marshal(map[string]string{"db": k.database, "csl": query})
	if err != nil {
		return nil, err
	}
	status, content, err := k.post(ctx, path, body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Kusto emulator HTTP %d: %s", status, content)
	}
	return content, nil
}

func (k *Kusto) post(ctx context.Context, path string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, k.endpoint+path, bytes.NewReader(body)) // #nosec G704 -- LocalTransport confines requests to exact numeric loopback.
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := k.client.Do(request) // #nosec G704 -- LocalTransport confines all requests to the exact numeric-loopback emulator endpoint.
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return 0, nil, err
	}
	if len(content) > 4<<20 {
		return 0, nil, errors.New("Kusto emulator response exceeds simulator limit")
	}
	return response.StatusCode, content, nil
}

func (k *Kusto) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		http.Error(w, "request exceeds simulator limit", http.StatusRequestEntityTooLarge)
		return
	}
	path := r.URL.Path
	if table, ingest := strings.CutPrefix(path, "/v1/rest/ingest/"+k.database+"/"); ingest {
		if !k.tables[table] || r.URL.Query().Get("streamFormat") != "MultiJSON" ||
			r.URL.Query().Get("mappingName") != "cao_v1" {
			http.Error(w, "unsupported table or mapping", http.StatusBadRequest)
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		count := 0
		for {
			var row map[string]json.RawMessage
			err := decoder.Decode(&row)
			if err == io.EOF {
				break
			}
			if err != nil || row == nil {
				http.Error(w, "invalid MultiJSON row", http.StatusBadRequest)
				return
			}
			count++
		}
		if count == 0 {
			http.Error(w, "empty ingestion", http.StatusBadRequest)
			return
		}
		// The mapping and JSON conversion are performed by Kusto, not Go.
		query := ".ingest inline into table " + table +
			" with (format='multijson', ingestionMappingReference='cao_v1') <|\n" + string(body)
		body, err = json.Marshal(map[string]string{"db": k.database, "csl": query})
		if err != nil {
			http.Error(w, "cannot encode ingestion", http.StatusInternalServerError)
			return
		}
		path = "/v1/rest/mgmt"
	} else if path != "/v1/rest/mgmt" && path != "/v1/rest/query" {
		http.Error(w, "unsupported Kusto API", http.StatusNotFound)
		return
	} else {
		var envelope struct {
			DB string `json:"db"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.DB != k.database {
			http.Error(w, "wrong database", http.StatusBadRequest)
			return
		}
	}
	status, content, err := k.post(r.Context(), path, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(content) // #nosec G705 -- application/json engine protocol response, never rendered as HTML.
}
