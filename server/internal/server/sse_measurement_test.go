package server

import (
	"bufio"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSSEFanoutAcrossReplicasAndDrain(t *testing.T) {
	database := integrationDatabase(t)
	type stream struct {
		response *http.Response
		reader   *bufio.Reader
	}
	apps := make([]*App, 0, 2)
	streams := make([]stream, 0, 6)
	for range 2 {
		app := &App{services: newTestOperationalStore(t).OperationalServices(), hub: newEventHub(),
			database: database, drain: make(chan struct{}),
			config: Config{HostProfile: localHostProfile()}, accessToken: testAccessToken,
		}
		apps = append(apps, app)
		server := httptest.NewServer(app.Handler())
		defer server.Close()
		for range 3 {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/events", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+testAccessToken)
			response, err := (&http.Client{Timeout: 4 * time.Second}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			reader := bufio.NewReader(response.Body)
			if _, err := reader.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			streams = append(streams, stream{response: response, reader: reader})
		}
	}
	seedDatabase(t, database, map[string]model.Source{})
	apps[0].hub.Broadcast(1)
	for _, stream := range streams {
		for {
			line, err := stream.reader.ReadString('\n')
			if err != nil {
				t.Fatalf("cross-replica stream did not receive update: %v", err)
			}
			if strings.Contains(line, `"revision":1`) {
				break
			}
		}
	}
	for _, app := range apps {
		app.Drain()
	}
	for _, stream := range streams {
		if _, err := stream.reader.ReadString('\n'); err == nil {
			// Blank line from the last data frame is allowed.
			if _, err := stream.reader.ReadString('\n'); err == nil {
				t.Fatal("stream continued after drain")
			}
		}
	}
}

// TestSSEFanoutMeasurements compares connected-stream cost across process-local
// replicas. Run with POSTGRES_URL and CAO_SSE_MEASURE=1 against disposable data.
func TestSSEFanoutMeasurements(t *testing.T) {
	if os.Getenv("CAO_SSE_MEASURE") != "1" {
		t.Skip("set CAO_SSE_MEASURE=1 and POSTGRES_URL for the SSE measurement")
	}
	database := integrationDatabase(t)
	admin, err := sql.Open("pgx", os.Getenv("POSTGRES_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	for _, replicas := range []int{1, 2} {
		servers := make([]*httptest.Server, replicas)
		apps := make([]*App, replicas)
		for i := range servers {
			apps[i] = &App{services: newTestOperationalStore(t).OperationalServices(), hub: newEventHub(),
				database: database, drain: make(chan struct{}),
				config: Config{HostProfile: localHostProfile()}, accessToken: testAccessToken,
			}
			servers[i] = httptest.NewServer(apps[i].Handler())
		}
		for _, perReplica := range []int{1, 12} {
			type stream struct {
				response *http.Response
			}
			streams := make([]stream, 0, replicas*perReplica)
			transport := &http.Transport{MaxIdleConnsPerHost: replicas * perReplica}
			httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			for _, server := range servers {
				for range perReplica {
					req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/events", nil)
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer "+testAccessToken)
					resp, err := httpClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = resp.Body.Close() }()
					if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
						t.Fatal(err)
					}
					streams = append(streams, stream{response: resp})
				}
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			var start, end int64
			if err := admin.QueryRowContext(t.Context(), "SELECT xact_commit FROM pg_stat_database WHERE datname = current_database()").Scan(&start); err != nil {
				t.Fatal(err)
			}
			time.Sleep(4 * time.Second)
			// PostgreSQL flushes per-backend statistics asynchronously.
			time.Sleep(time.Second)
			if err := admin.QueryRowContext(t.Context(), "SELECT pg_stat_clear_snapshot()").Err(); err != nil {
				t.Fatal(err)
			}
			if err := admin.QueryRowContext(t.Context(), "SELECT xact_commit FROM pg_stat_database WHERE datname = current_database()").Scan(&end); err != nil {
				t.Fatal(err)
			}
			var connections int
			if err := admin.QueryRowContext(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()").Scan(&connections); err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			drainStart := time.Now()
			if perReplica == 12 {
				for _, app := range apps {
					app.Drain()
				}
				for _, stream := range streams {
					if _, err := io.Copy(io.Discard, stream.response.Body); err != nil {
						t.Fatal(err)
					}
				}
			}
			drainDuration := time.Since(drainStart)
			for _, stream := range streams {
				_ = stream.response.Body.Close()
			}
			t.Logf("replicas=%d clients=%d connections=%d transactions_per_second=%.1f heap_before=%d heap_after=%d drain_duration=%s",
				replicas, replicas*perReplica, connections, float64(end-start)/5, before.HeapAlloc, after.HeapAlloc, drainDuration)
			transport.CloseIdleConnections()
		}
		for _, server := range servers {
			server.Close()
		}
	}
}
