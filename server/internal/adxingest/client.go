package adxingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/githubnext/gh-cao/server/internal/model"
)

var ErrVisibility = errors.New("ADX projected rows are not yet exactly query-visible")
var ErrStorage = errors.New("ADX storage budget reached or unavailable")

type Sink interface {
	Capacity(context.Context) error
	Write(context.Context, string, []model.Row) error
	Verify(context.Context, Projection, bool) error
}

type Client struct {
	endpoint, database string
	http               *http.Client
	storageBudget      int64
}

func NewClient(config Config) (*Client, error) {
	if !config.Enabled {
		return nil, errors.New("cannot create disabled ADX client")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	credential := clientcredentials.Config{
		ClientID: config.ClientID, ClientSecret: config.ClientSecret,
		TokenURL:  "https://login.microsoftonline.com/" + config.TenantID + "/oauth2/v2.0/token",
		Scopes:    []string{"https://kusto.kusto.windows.net/.default"},
		AuthStyle: oauth2.AuthStyleInParams,
	}
	// TokenSource obtains tokens lazily and receives each request's bounded
	// context in tokenTransport; it never outlives a cancelled worker step.
	return &Client{endpoint: config.Endpoint, database: config.Database, storageBudget: config.MaxStorageBytes,
		http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirect,
			Transport: &tokenTransport{credential: credential, base: http.DefaultTransport}}}, nil
}

type tokenTransport struct {
	credential clientcredentials.Config
	base       http.RoundTripper
	mu         sync.Mutex
	token      *oauth2.Token
}

func (t *tokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	if !t.token.Valid() {
		token, err := t.credential.Token(request.Context())
		if err != nil {
			t.mu.Unlock()
			return nil, errors.New("ADX application token acquisition failed")
		}
		t.token = token
	}
	accessToken := t.token.AccessToken
	t.mu.Unlock()
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+accessToken)
	return t.base.RoundTrip(request)
}

func noRedirect(_ *http.Request, _ []*http.Request) error {
	return errors.New("ADX redirects are not allowed")
}

func (c *Client) Write(ctx context.Context, table string, rows []model.Row) error {
	if !validTable(table) || len(rows) == 0 {
		return errors.New("invalid ADX ingestion table or empty batch")
	}
	var body bytes.Buffer
	for _, row := range rows {
		if err := json.NewEncoder(&body).Encode(row); err != nil {
			return err
		}
	}
	if body.Len() > 1<<20 {
		return errors.New("ADX streaming batch exceeds 1 MiB")
	}
	path := "/v1/rest/ingest/" + c.database + "/" + table + "?streamFormat=MultiJSON&mappingName=cao_v1"
	_, err := c.request(ctx, path, body.Bytes())
	return err
}

func (c *Client) Capacity(ctx context.Context) error {
	// Extent OriginalSize is uncompressed data; account for all six owned
	// tables, including duplicate deliveries and superseded projections.
	query := ".show database ['" + c.database + "'] extents | where TableName in (" + tableNamesKQL() +
		") | summarize Bytes=sum(OriginalSize)"
	rows, err := c.query(ctx, "/v1/rest/mgmt", query)
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return ErrStorage
	}
	number, ok := rows[0][0].(json.Number)
	if !ok {
		return ErrStorage
	}
	bytes, err := number.Int64()
	if err != nil || bytes < 0 || bytes >= c.storageBudget {
		return ErrStorage
	}
	return nil
}

func (c *Client) Verify(ctx context.Context, projection Projection, manifest bool) error {
	var parts, expected []string
	tables := projection.Tables
	if manifest {
		tables = map[string][]model.Row{"SessionVersions": {projection.Manifest}}
	}
	for table, rows := range tables {
		if !validTable(table) {
			return errors.New("invalid ADX verification table")
		}
		condition := ""
		if manifest {
			condition = fmt.Sprintf(" and SourceRevision==%d", projection.Revision)
		}
		parts = append(parts, "("+table+" | where Namespace=="+literal(projection.Namespace)+
			" and RunId=="+literal(projection.RunID)+" and ProjectionId=="+literal(projection.ID)+condition+
			" | project Table="+literal(table)+", RowDigest)")
		for _, row := range rows {
			digest, ok := row["RowDigest"].(string)
			if !ok {
				return errors.New("missing ADX row digest")
			}
			expected = append(expected, table+":"+digest)
		}
	}
	slices.Sort(parts)
	rows, err := c.query(ctx, "/v1/rest/query", "union "+strings.Join(parts, ",")+" | summarize by Table, RowDigest")
	if err != nil {
		return err
	}
	actual := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) != 2 {
			return ErrVisibility
		}
		table, tableOK := row[0].(string)
		digest, digestOK := row[1].(string)
		if !tableOK || !digestOK {
			return ErrVisibility
		}
		actual = append(actual, table+":"+digest)
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		return ErrVisibility
	}
	return nil
}

func (c *Client) query(ctx context.Context, path, query string) ([][]any, error) {
	body, err := json.Marshal(map[string]any{
		"db": c.database, "csl": query,
		"properties": map[string]any{"Options": map[string]any{
			"servertimeout": "00:00:20", "truncationmaxrecords": 2048, "truncationmaxsize": 1 << 20,
		}},
	})
	if err != nil {
		return nil, err
	}
	content, err := c.request(ctx, path, body)
	if err != nil {
		return nil, err
	}
	var response struct {
		Tables []struct {
			TableName string
			Rows      [][]any
			Columns   []struct{ ColumnName string }
		}
		Error json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil || len(response.Error) > 0 {
		return nil, errors.New("invalid ADX query response")
	}
	var result [][]any
	found := false
	for _, table := range response.Tables {
		if table.TableName == "QueryStatus" {
			severity := -1
			for i, column := range table.Columns {
				if column.ColumnName == "Severity" {
					severity = i
				}
			}
			for _, row := range table.Rows {
				if severity < 0 || severity >= len(row) {
					return nil, errors.New("invalid ADX query status")
				}
				value, ok := row[severity].(json.Number)
				if !ok {
					return nil, errors.New("invalid ADX query severity")
				}
				number, err := value.Int64()
				if err != nil || number <= 2 {
					return nil, errors.New("ADX query reported partial execution")
				}
			}
			continue
		}
		if table.TableName == "PrimaryResult" || table.TableName == "Table_0" {
			if found {
				return nil, errors.New("ambiguous ADX query result")
			}
			result, found = table.Rows, true
		}
	}
	if !found || len(result) > 2048 {
		return nil, errors.New("missing or oversized ADX query result")
	}
	return result, nil
}

func (c *Client) request(ctx context.Context, path string, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("construct ADX request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-ms-app", "cao-adx")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, errors.New("ADX request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("ADX request returned HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(content) > 1<<20 {
		return nil, errors.New("ADX response unreadable or exceeds 1 MiB")
	}
	return content, nil
}

func literal(value string) string {
	content, _ := json.Marshal(value)
	return string(content)
}

func validTable(table string) bool {
	if table == "SessionVersions" {
		return true
	}
	for _, family := range families {
		if table == family.table {
			return true
		}
	}
	return false
}

func tableNamesKQL() string {
	names := make([]string, 0, 1+len(families))
	names = append(names, "'SessionVersions'")
	for _, family := range families {
		names = append(names, "'"+family.table+"'")
	}
	return strings.Join(names, ",")
}
