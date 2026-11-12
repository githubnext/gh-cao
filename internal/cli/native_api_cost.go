package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/apicost"
	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

const (
	nativeAPICostQuery       = "api-cost-daily-file-diet-population"
	nativeAPICostCostQuery   = "api-cost-daily-file-diet-observed"
	nativeAPICostMaxScenario = 1 << 20
	nativeAPICostMaxResponse = 1 << 20
)

func nativeAPICost(cmd *cobra.Command) error {
	input := nativeString(cmd, "input")
	profile := nativeString(cmd, "profile")
	if input != "" && profile != "" {
		return errors.New("--input cannot be combined with --profile")
	}
	if input != "" && nativeBool(cmd, "calibrate") {
		return errors.New("--calibrate cannot be combined with --input")
	}
	if profile != "" && profile != "daily-file-diet" {
		return errors.New("--profile must be daily-file-diet")
	}
	format := nativeString(cmd, "format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "markdown" {
		return errors.New("--format must be json or markdown")
	}
	var scenario apicost.Scenario
	var err error
	if input != "" {
		if nativeString(cmd, "server") != "" || cmd.Flags().Changed("postgres-url") {
			return errors.New("--input cannot be combined with --server or --postgres-url")
		}
		scenario, err = nativeAPICostReadScenario(input)
		if err == nil {
			err = nativeAPICostOverrideScenario(cmd, &scenario)
		}
	} else {
		options, optionsErr := nativeDailyFileDietOptions(cmd)
		if optionsErr != nil {
			return optionsErr
		}
		if options.Scenario == "" {
			options.Scenario = "retained"
		}
		if options.Scenario == "retained" {
			repository := nativeString(cmd, "repository")
			if repository == "" {
				repository = "github/gh-aw"
			}
			if !repositoryPattern.MatchString(repository) {
				return errors.New("--repository must use OWNER/REPO form")
			}
			population, populationErr := nativeDailyFileDietPopulation(cmd, repository)
			if populationErr != nil {
				return populationErr
			}
			options.Population = &population
			if nativeBool(cmd, "calibrate") {
				server := nativeString(cmd, "server")
				if server == "" {
					return errors.New("--calibrate requires --server")
				}
				calibration, calibrationErr := nativeDailyFileDietServerCalibration(
					cmd.Context(), server, http.DefaultClient, os.Getenv)
				if calibrationErr != nil {
					return calibrationErr
				}
				options.Calibration = &calibration
			}
		} else if nativeString(cmd, "server") != "" || cmd.Flags().Changed("postgres-url") || cmd.Flags().Changed("repository") {
			return errors.New("--server, --postgres-url, and --repository apply only to the retained scenario")
		} else if nativeBool(cmd, "calibrate") {
			return errors.New("--calibrate applies only to the retained scenario")
		}
		scenario, err = apicost.DailyFileDietScenario(options)
	}
	if err != nil {
		return err
	}
	report, err := apicost.Simulate(scenario)
	if err != nil {
		return err
	}
	if format == "markdown" {
		_, err = io.WriteString(cmd.OutOrStdout(), apicost.FormatMarkdown(report))
		return err
	}
	return printJSON(cmd, report)
}

func nativeAPICostReadScenario(filename string) (apicost.Scenario, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return apicost.Scenario{}, errors.New("--input must be a readable regular scenario file")
	}
	if info.Size() > nativeAPICostMaxScenario {
		return apicost.Scenario{}, errors.New("--input exceeds the 1 MiB scenario limit")
	}
	file, err := os.Open(filename)
	if err != nil {
		return apicost.Scenario{}, errors.New("unable to open --input scenario file")
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, nativeAPICostMaxScenario+1))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var scenario apicost.Scenario
	if err := decoder.Decode(&scenario); err != nil {
		return apicost.Scenario{}, errors.New("--input must contain one schema-v1 API cost scenario")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return apicost.Scenario{}, errors.New("--input must contain one schema-v1 API cost scenario")
	}
	return scenario, nil
}

func nativeAPICostOverrideScenario(cmd *cobra.Command, scenario *apicost.Scenario) error {
	if value, err := nativeOptionalInt(cmd, "repositories"); err != nil {
		return err
	} else if value != nil {
		scenario.Repositories = *value
	}
	if value, err := nativeOptionalFloat(cmd, "collection-window-hours"); err != nil {
		return err
	} else if value != nil {
		scenario.CollectionWindowHours = *value
	}
	if value, err := nativeOptionalFloat(cmd, "horizon-hours"); err != nil {
		return err
	} else if value != nil {
		scenario.HorizonHours = *value
	}
	for _, name := range []string{
		"scenario", "wall-minutes", "parallelism", "core-limit", "core-reserve",
		"search-limit", "search-reserve", "search-window-minutes", "graphql-limit",
		"graphql-reserve", "collector-grant", "tracked-issues", "new-issues",
		"mutable-pulls", "new-receipts", "search-pages-per-term",
		"timeline-pages-per-issue", "pull-file-pages", "snapshot-reads",
		"graphql-points-per-request", "repository",
		"calibrate",
	} {
		if cmd.Flags().Changed(name) {
			return fmt.Errorf("--%s cannot be combined with --input", name)
		}
	}
	return nil
}

func nativeDailyFileDietOptions(cmd *cobra.Command) (apicost.DailyFileDietOptions, error) {
	options := apicost.DailyFileDietOptions{Scenario: nativeString(cmd, "scenario")}
	integers := map[string]**int{
		"repositories": &options.Repositories, "parallelism": &options.Parallelism,
		"tracked-issues": &options.TrackedIssues, "new-issues": &options.NewIssues,
		"mutable-pulls": &options.MutablePulls, "new-receipts": &options.NewReceipts,
		"search-pages-per-term":    &options.SearchPagesPerTerm,
		"timeline-pages-per-issue": &options.TimelinePagesPerIssue,
		"pull-file-pages":          &options.PullFilePages, "snapshot-reads": &options.SnapshotReads,
	}
	for name, target := range integers {
		value, err := nativeOptionalInt(cmd, name)
		if err != nil {
			return options, err
		}
		*target = value
	}
	numbers := map[string]**float64{
		"horizon-hours":           &options.HorizonHours,
		"collection-window-hours": &options.CollectionWindowHours,
		"wall-minutes":            &options.WallMinutes, "core-limit": &options.CoreLimit,
		"core-reserve": &options.CoreReserve, "search-limit": &options.SearchLimit,
		"search-reserve":        &options.SearchReserve,
		"search-window-minutes": &options.SearchWindowMinutes,
		"graphql-limit":         &options.GraphQLLimit, "graphql-reserve": &options.GraphQLReserve,
		"collector-grant":            &options.CollectorGrant,
		"graphql-points-per-request": &options.GraphQLPointsPerRequest,
	}
	for name, target := range numbers {
		value, err := nativeOptionalFloat(cmd, name)
		if err != nil {
			return options, err
		}
		*target = value
	}
	return options, nil
}

func nativeOptionalInt(cmd *cobra.Command, name string) (*int, error) {
	if !cmd.Flags().Changed(name) {
		return nil, nil
	}
	value, err := strconv.Atoi(nativeString(cmd, name))
	if err != nil {
		return nil, fmt.Errorf("--%s must be an integer", name)
	}
	return &value, nil
}

func nativeOptionalFloat(cmd *cobra.Command, name string) (*float64, error) {
	if !cmd.Flags().Changed(name) {
		return nil, nil
	}
	value, err := strconv.ParseFloat(nativeString(cmd, name), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, fmt.Errorf("--%s must be a finite number", name)
	}
	return &value, nil
}

func nativeDailyFileDietWorkflowRoute(repository string) string {
	return strings.ToLower(repository) + ":.github/workflows/daily-file-diet.md"
}

func nativeDailyFileDietQuery(repository string) map[string]any {
	route := nativeDailyFileDietWorkflowRoute(repository)
	return map[string]any{
		"name":       nativeAPICostQuery,
		"subject":    "Retained Daily File Diet collector population from canonical Value evidence.",
		"objective":  "Count the exact latest packet's output and accepted-implementation facts for API capacity planning.",
		"acceptance": "Use database evidence only; missing packets remain unavailable and never fall back to source files or constants.",
		"from":       "operational-value-evidence",
		"time":       map[string]any{"range": "all"},
		"filter": map[string]any{"predicates": []any{
			map[string]any{"field": "workflow-route", "equals": route},
		}},
		"aggregate": map[string]any{
			"by": []any{"report-id", "workflow-route", "observed-at", "window-end"},
			"values": []any{
				map[string]any{
					"field": "fact-id", "as": "tracked-issues", "reducer": "count",
					"filter": map[string]any{"predicates": []any{
						map[string]any{"field": "fact-kind", "equals": "output"},
					}},
				},
				map[string]any{
					"field": "fact-id", "as": "retained-implementations", "reducer": "count",
					"filter": map[string]any{"predicates": []any{
						map[string]any{"field": "fact-kind", "equals": "implementation"},
					}},
				},
			},
		},
		"order-by": []any{
			map[string]any{"field": "window-end", "direction": "desc"},
			map[string]any{"field": "observed-at", "direction": "desc"},
			map[string]any{"field": "report-id", "direction": "desc"},
		},
		"limit": 1,
	}
}

func nativeDailyFileDietCostQuery() map[string]any {
	return map[string]any{
		"name":       nativeAPICostCostQuery,
		"subject":    "Observed hosted Daily File Diet acquisition costs.",
		"objective":  "Calibrate request-pool and wall-time assumptions from private daily aggregates.",
		"acceptance": "Use bounded administrator-authorized aggregates only; never expose credentials or proof payloads.",
		"from":       "value-collection-costs",
		"time":       map[string]any{"range": "all"},
		"filter": map[string]any{"predicates": []any{
			map[string]any{"field": "workflow", "equals": "daily-file-diet"},
		}},
		"aggregate": map[string]any{"values": []any{
			map[string]any{"field": "attempts", "as": "attempts", "reducer": "sum"},
			map[string]any{"field": "core-requests", "as": "core-requests", "reducer": "sum"},
			map[string]any{"field": "search-requests", "as": "search-requests", "reducer": "sum"},
			map[string]any{"field": "graphql-requests", "as": "graphql-requests", "reducer": "sum"},
			map[string]any{"field": "graphql-points-observed", "as": "graphql-points-observed", "reducer": "sum"},
			map[string]any{"field": "wall-ms", "as": "wall-ms", "reducer": "sum"},
			map[string]any{"field": "max-wall-ms", "as": "max-wall-ms", "reducer": "max"},
			map[string]any{"field": "output-bytes", "as": "output-bytes", "reducer": "sum"},
			map[string]any{"field": "transport-bytes", "as": "transport-bytes", "reducer": "sum"},
		}},
		"limit": 1,
	}
}

func nativeDailyFileDietPopulation(cmd *cobra.Command, repository string) (apicost.Population, error) {
	if server := nativeString(cmd, "server"); server != "" {
		return nativeDailyFileDietServerPopulation(cmd.Context(), server, repository, http.DefaultClient, os.Getenv)
	}
	if err := requireNativeSnapshot(cmd, false); err != nil {
		return apicost.Population{}, err
	}
	db, err := openNativeSnapshot(nativeSnapshotPath(cmd))
	if err != nil {
		return apicost.Population{}, err
	}
	defer func() { _ = db.Close() }()
	return nativeDailyFileDietLocalPopulation(cmd.Context(), db, repository, nativeSnapshotPath(cmd))
}

func nativeDailyFileDietLocalPopulation(ctx context.Context, db dashboardquery.CanonicalReader, repository, source string) (apicost.Population, error) {
	catalog, err := loadNativeCatalog("")
	if err != nil {
		return apicost.Population{}, err
	}
	catalog.index[nativeAPICostQuery] = nativeDailyFileDietQuery(repository)
	result, err := catalog.runQueryWithEvidenceSelections(ctx, db, nativeAPICostQuery, nil, 1,
		[]dashboardquery.EvidenceSelection{{
			Field: "workflow-route", Value: nativeDailyFileDietWorkflowRoute(repository),
		}})
	if err != nil {
		return apicost.Population{}, err
	}
	return nativeAPICostPopulation(nativeAPICostRows(result["rows"]), nativeObject(result["metadata"]),
		"canonical PostgreSQL")
}

func nativeDailyFileDietServerPopulation(
	ctx context.Context,
	server, repository string,
	client *http.Client,
	getenv func(string) string,
) (apicost.Population, error) {
	sources, revision, err := nativeAPICostServerQueries(ctx, server, client, getenv,
		[]map[string]any{nativeDailyFileDietQuery(repository)}, []string{nativeAPICostQuery})
	if err != nil {
		return apicost.Population{}, err
	}
	source, exists := sources[nativeAPICostQuery]
	if !exists {
		return apicost.Population{}, errors.New("invalid API cost database response: query result is absent")
	}
	if revision == "" {
		revision = "unknown"
	}
	return nativeAPICostPopulation(source.Rows, source.Metadata,
		"canonical server database revision "+revision)
}

type nativeAPICostServerSource struct {
	Rows     []any
	Metadata map[string]any
}

func nativeAPICostServerQueries(
	ctx context.Context,
	server string,
	client *http.Client,
	getenv func(string) string,
	queries []map[string]any,
	sourceNames []string,
) (map[string]nativeAPICostServerSource, string, error) {
	endpoint, local, err := nativeServerEndpoint(server, "/api/v1/query")
	if err != nil {
		return nil, "", err
	}
	headers, err := nativeAPICostAuth(local, getenv)
	if err != nil {
		return nil, "", err
	}
	body, err := json.Marshal(map[string]any{
		"sourceNames": sourceNames,
		"queries":     queries,
	})
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, "", errors.New("unable to construct API cost database query")
	}
	request.Header = headers.Clone()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	copyClient := *client
	copyClient.Timeout = 30 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", errors.New("API cost database query failed: transport or timeout error")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, "", errors.New("API cost database query denied: server authentication is required")
		case http.StatusServiceUnavailable:
			return nil, "", errors.New("API cost database query unavailable: server data is unavailable")
		default:
			return nil, "", fmt.Errorf("API cost database query failed: HTTP %d", response.StatusCode)
		}
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, nativeAPICostMaxResponse+1))
	if err != nil || len(content) > nativeAPICostMaxResponse {
		return nil, "", errors.New("invalid API cost database response: unreadable or oversized JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var result struct {
		Revision json.Number `json:"revision"`
		Sources  map[string]struct {
			Rows     []any          `json:"rows"`
			Metadata map[string]any `json:"metadata"`
		} `json:"sources"`
	}
	if err := decoder.Decode(&result); err != nil {
		return nil, "", errors.New("invalid API cost database response")
	}
	sources := make(map[string]nativeAPICostServerSource, len(result.Sources))
	for name, source := range result.Sources {
		sources[name] = nativeAPICostServerSource{Rows: source.Rows, Metadata: source.Metadata}
	}
	return sources, result.Revision.String(), nil
}

func nativeDailyFileDietServerCalibration(
	ctx context.Context,
	server string,
	client *http.Client,
	getenv func(string) string,
) (apicost.DailyFileDietCalibration, error) {
	sources, revision, err := nativeAPICostServerQueries(ctx, server, client, getenv,
		[]map[string]any{nativeDailyFileDietCostQuery()}, []string{nativeAPICostCostQuery})
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	source, exists := sources[nativeAPICostCostQuery]
	if !exists || nativeText(source.Metadata["availability"]) == "unavailable" {
		return apicost.DailyFileDietCalibration{}, errors.New("hosted Value collection cost observations are unavailable")
	}
	if len(source.Rows) != 1 {
		return apicost.DailyFileDietCalibration{}, errors.New("hosted Value collection cost aggregation returned an invalid row count")
	}
	row := nativeObject(source.Rows[0])
	integer := func(field string) (int64, error) {
		number, ok := row[field].(json.Number)
		value, err := number.Int64()
		if !ok || err != nil || value < 0 {
			return 0, errors.New("hosted Value collection cost observations are invalid")
		}
		return value, nil
	}
	attempts, err := integer("attempts")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	if attempts == 0 {
		return apicost.DailyFileDietCalibration{}, errors.New("no hosted Daily File Diet collection cost observations exist")
	}
	core, err := integer("core-requests")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	search, err := integer("search-requests")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	graphQL, err := integer("graphql-requests")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	points, err := integer("graphql-points-observed")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	wallMS, err := integer("wall-ms")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	maxWallMS, err := integer("max-wall-ms")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	outputBytes, err := integer("output-bytes")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	transportBytes, err := integer("transport-bytes")
	if err != nil {
		return apicost.DailyFileDietCalibration{}, err
	}
	result := apicost.DailyFileDietCalibration{
		Attempts: int(attempts), CoreRequests: int(core), SearchRequests: int(search),
		GraphQLRequests: int(graphQL), GraphQLPointsObserved: int(points),
		WallMS: wallMS, MaxWallMS: maxWallMS, OutputBytes: outputBytes, TransportBytes: transportBytes,
	}
	if revision == "" {
		revision = "unknown"
	}
	result.Source = "hosted cost aggregates at canonical database revision " + revision
	return result, nil
}

func nativeAPICostAuth(local bool, getenv func(string) string) (http.Header, error) {
	session, csrf, token := getenv("CAO_SERVER_SESSION"), getenv("CAO_SERVER_CSRF"), getenv("CAO_SERVER_TOKEN")
	invalid := errors.New("server query requires safe CAO_SERVER_SESSION and CAO_SERVER_CSRF values, or only CAO_SERVER_TOKEN for loopback; do not combine authentication modes")
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

func nativeAPICostPopulation(rows []any, metadata map[string]any, source string) (apicost.Population, error) {
	availability := nativeText(metadata["availability"])
	if availability == "unavailable" {
		return apicost.Population{}, errors.New("the Daily File Diet canonical Value evidence is unavailable")
	}
	if len(rows) == 0 {
		return apicost.Population{}, errors.New("no retained Daily File Diet evidence exists in the selected database")
	}
	if len(rows) != 1 {
		return apicost.Population{}, errors.New("invalid Daily File Diet population query row count")
	}
	row := nativeObject(rows[0])
	outputs, outputsValid := nativeAPICostCount(row["tracked-issues"])
	implementations, implementationsValid := nativeAPICostCount(row["retained-implementations"])
	if !outputsValid || !implementationsValid {
		return apicost.Population{}, errors.New("invalid Daily File Diet population counts")
	}
	observedAt, windowEnd := nativeText(row["observed-at"]), nativeText(row["window-end"])
	if observedAt == "" || windowEnd == "" {
		return apicost.Population{}, errors.New("invalid Daily File Diet population query evidence clocks")
	}
	return apicost.Population{
		Outputs: outputs, Implementations: implementations,
		ObservedAt: observedAt, WindowEnd: windowEnd, Source: source,
	}, nil
}

func nativeAPICostCount(value any) (int, bool) {
	number := nativeNumber(value)
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number != math.Trunc(number) {
		return 0, false
	}
	count, err := strconv.Atoi(strconv.FormatFloat(number, 'f', -1, 64))
	return count, err == nil
}

func nativeAPICostRows(value any) []any {
	if rows, ok := value.([]any); ok {
		return rows
	}
	if rows, ok := value.([]map[string]any); ok {
		result := make([]any, len(rows))
		for index := range rows {
			result[index] = rows[index]
		}
		return result
	}
	return nil
}
