package apicost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

const maxRepositories = 1_000_000

type Quota struct {
	Limit       float64 `json:"limit"`
	Reserve     float64 `json:"reserve,omitempty"`
	WindowHours float64 `json:"windowHours,omitempty"`
}

type Scale struct {
	Fixed         float64 `json:"fixed,omitempty"`
	PerRepository float64 `json:"perRepository,omitempty"`
	PerWorkflow   float64 `json:"perWorkflow,omitempty"`
}

type Operation struct {
	ID           string  `json:"id"`
	Pool         string  `json:"pool"`
	Scale        Scale   `json:"scale,omitempty"`
	CadenceHours float64 `json:"cadenceHours,omitempty"`
	BurstHours   float64 `json:"burstHours,omitempty"`
	RequestCount float64 `json:"requestCount,omitempty"`
	UnitCost     float64 `json:"unitCost,omitempty"`
	CacheHitRate float64 `json:"cacheHitRate,omitempty"`
}

type Admission struct {
	ID                         string  `json:"id"`
	Pool                       string  `json:"pool"`
	Grant                      float64 `json:"grant"`
	ActualPoolUnitsPerInstance float64 `json:"actualPoolUnitsPerInstance"`
	ActualRequestsPerInstance  float64 `json:"actualRequestsPerInstance"`
	InstancesPerRepository     float64 `json:"instancesPerRepository,omitempty"`
	CadenceHours               float64 `json:"cadenceHours,omitempty"`
}

type Runner struct {
	MinutesPerInstance     float64 `json:"minutesPerInstance"`
	InstancesPerRepository float64 `json:"instancesPerRepository,omitempty"`
	Parallelism            int     `json:"parallelism,omitempty"`
}

type Scenario struct {
	SchemaVersion          int              `json:"schemaVersion"`
	Name                   string           `json:"name"`
	Profile                string           `json:"profile,omitempty"`
	Scenario               string           `json:"scenario,omitempty"`
	Repositories           int              `json:"repositories,omitempty"`
	WorkflowsPerRepository int              `json:"workflowsPerRepository,omitempty"`
	HorizonHours           float64          `json:"horizonHours,omitempty"`
	CollectionWindowHours  float64          `json:"collectionWindowHours,omitempty"`
	Quotas                 map[string]Quota `json:"quotas"`
	Operations             []Operation      `json:"operations"`
	Admissions             []Admission      `json:"admissions,omitempty"`
	Runner                 *Runner          `json:"runner,omitempty"`
	Assumptions            []string         `json:"assumptions,omitempty"`
	Detail                 map[string]any   `json:"detail,omitempty"`
}

type quotaJSON struct {
	Limit       float64  `json:"limit"`
	Reserve     float64  `json:"reserve,omitempty"`
	WindowHours *float64 `json:"windowHours,omitempty"`
}

type operationJSON struct {
	ID           string   `json:"id"`
	Pool         string   `json:"pool"`
	Scale        Scale    `json:"scale,omitempty"`
	CadenceHours *float64 `json:"cadenceHours,omitempty"`
	BurstHours   *float64 `json:"burstHours,omitempty"`
	RequestCount *float64 `json:"requestCount,omitempty"`
	UnitCost     *float64 `json:"unitCost,omitempty"`
	CacheHitRate float64  `json:"cacheHitRate,omitempty"`
}

type admissionJSON struct {
	ID                         string   `json:"id"`
	Pool                       string   `json:"pool"`
	Grant                      float64  `json:"grant"`
	ActualPoolUnitsPerInstance float64  `json:"actualPoolUnitsPerInstance"`
	ActualRequestsPerInstance  float64  `json:"actualRequestsPerInstance"`
	InstancesPerRepository     *float64 `json:"instancesPerRepository,omitempty"`
	CadenceHours               *float64 `json:"cadenceHours,omitempty"`
}

type runnerJSON struct {
	MinutesPerInstance     float64  `json:"minutesPerInstance"`
	InstancesPerRepository *float64 `json:"instancesPerRepository,omitempty"`
	Parallelism            *int     `json:"parallelism,omitempty"`
}

type scenarioJSON struct {
	SchemaVersion          int                  `json:"schemaVersion"`
	Name                   string               `json:"name"`
	Profile                string               `json:"profile,omitempty"`
	Scenario               string               `json:"scenario,omitempty"`
	Repositories           *int                 `json:"repositories,omitempty"`
	WorkflowsPerRepository *int                 `json:"workflowsPerRepository,omitempty"`
	HorizonHours           *float64             `json:"horizonHours,omitempty"`
	CollectionWindowHours  *float64             `json:"collectionWindowHours,omitempty"`
	Quotas                 map[string]quotaJSON `json:"quotas"`
	Operations             []operationJSON      `json:"operations"`
	Admissions             []admissionJSON      `json:"admissions,omitempty"`
	Runner                 *runnerJSON          `json:"runner,omitempty"`
	Assumptions            []string             `json:"assumptions,omitempty"`
	Detail                 map[string]any       `json:"detail,omitempty"`
}

func jsonValue[T any](pointer *T, fallback T) T {
	if pointer != nil {
		return *pointer
	}
	return fallback
}

func (scenario *Scenario) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var input scenarioJSON
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("API cost scenario must contain one JSON object")
	}
	result := Scenario{
		SchemaVersion:          input.SchemaVersion,
		Name:                   input.Name,
		Profile:                input.Profile,
		Scenario:               input.Scenario,
		Repositories:           jsonValue(input.Repositories, 1),
		WorkflowsPerRepository: jsonValue(input.WorkflowsPerRepository, 1),
		HorizonHours:           jsonValue(input.HorizonHours, 24),
		CollectionWindowHours:  jsonValue(input.CollectionWindowHours, 1),
		Quotas:                 make(map[string]Quota, len(input.Quotas)),
		Operations:             make([]Operation, len(input.Operations)),
		Admissions:             make([]Admission, len(input.Admissions)),
		Assumptions:            input.Assumptions,
		Detail:                 input.Detail,
	}
	for id, quota := range input.Quotas {
		result.Quotas[id] = Quota{
			Limit: quota.Limit, Reserve: quota.Reserve,
			WindowHours: jsonValue(quota.WindowHours, 1),
		}
	}
	for index, operation := range input.Operations {
		result.Operations[index] = Operation{
			ID: operation.ID, Pool: operation.Pool, Scale: operation.Scale,
			CadenceHours: jsonValue(operation.CadenceHours, result.HorizonHours),
			BurstHours:   jsonValue(operation.BurstHours, result.CollectionWindowHours),
			RequestCount: jsonValue(operation.RequestCount, 1),
			UnitCost:     jsonValue(operation.UnitCost, 1),
			CacheHitRate: operation.CacheHitRate,
		}
	}
	for index, admission := range input.Admissions {
		result.Admissions[index] = Admission{
			ID: admission.ID, Pool: admission.Pool, Grant: admission.Grant,
			ActualPoolUnitsPerInstance: admission.ActualPoolUnitsPerInstance,
			ActualRequestsPerInstance:  admission.ActualRequestsPerInstance,
			InstancesPerRepository:     jsonValue(admission.InstancesPerRepository, float64(result.WorkflowsPerRepository)),
			CadenceHours:               jsonValue(admission.CadenceHours, result.HorizonHours),
		}
	}
	if input.Runner != nil {
		result.Runner = &Runner{
			MinutesPerInstance:     input.Runner.MinutesPerInstance,
			InstancesPerRepository: jsonValue(input.Runner.InstancesPerRepository, float64(result.WorkflowsPerRepository)),
			Parallelism:            jsonValue(input.Runner.Parallelism, 1),
		}
	}
	*scenario = result
	return nil
}

type OperationReport struct {
	ID               string  `json:"id"`
	Pool             string  `json:"pool"`
	CountPerCycle    float64 `json:"countPerCycle"`
	UnitsPerCycle    float64 `json:"unitsPerCycle"`
	RequestsPerCycle float64 `json:"requestsPerCycle"`
	CadenceHours     float64 `json:"cadenceHours"`
	BurstHours       float64 `json:"burstHours"`
	PeakUnits        float64 `json:"peakUnits"`
}

type PoolReport struct {
	ID                   string  `json:"id"`
	Limit                float64 `json:"limit"`
	Reserve              float64 `json:"reserve"`
	WindowHours          float64 `json:"windowHours"`
	AvailablePerWindow   float64 `json:"availablePerWindow"`
	CapacityOverHorizon  float64 `json:"capacityOverHorizon"`
	CollectionWindows    float64 `json:"collectionWindows"`
	DemandOverHorizon    float64 `json:"demandOverHorizon"`
	DemandPerCycle       float64 `json:"demandPerCycle"`
	RequestsOverHorizon  float64 `json:"requestsOverHorizon"`
	RequestsPerCycle     float64 `json:"requestsPerCycle"`
	PeakDemandPerWindow  float64 `json:"peakDemandPerWindow"`
	SustainedUtilization float64 `json:"sustainedUtilization"`
	PeakUtilization      float64 `json:"peakUtilization"`
}

type AdmissionReport struct {
	ID                         string  `json:"id"`
	Pool                       string  `json:"pool"`
	Grant                      float64 `json:"grant"`
	Instances                  float64 `json:"instances"`
	PeakInstances              float64 `json:"peakInstances"`
	ActualPoolUnitsPerInstance float64 `json:"actualPoolUnitsPerInstance"`
	ActualRequestsPerInstance  float64 `json:"actualRequestsPerInstance"`
	ModeledPerWindow           int     `json:"modeledPerWindow"`
	GuaranteedPerWindow        int     `json:"guaranteedPerWindow"`
	GrantExceeded              bool    `json:"grantExceeded"`
}

type RunnerReport struct {
	Instances          float64 `json:"instances"`
	DemandMinutes      float64 `json:"demandMinutes"`
	CapacityMinutes    float64 `json:"capacityMinutes"`
	Utilization        float64 `json:"utilization"`
	MinutesPerInstance float64 `json:"minutesPerInstance"`
	Parallelism        int     `json:"parallelism"`
}

type Evaluation struct {
	Repositories int                    `json:"repositories"`
	Workflows    int                    `json:"workflows"`
	Supported    bool                   `json:"supported"`
	Violations   []string               `json:"violations"`
	Pools        map[string]*PoolReport `json:"pools"`
	Operations   []OperationReport      `json:"operations"`
	Admissions   []AdmissionReport      `json:"admissions"`
	Runner       *RunnerReport          `json:"runner,omitempty"`
}

type Capacity struct {
	ModeledMaximumRepositories    int      `json:"modeledMaximumRepositories"`
	ModeledMaximumWorkflows       int      `json:"modeledMaximumWorkflows"`
	ModeledMaximumIsLowerBound    bool     `json:"modeledMaximumIsLowerBound"`
	GuaranteedMaximumRepositories int      `json:"guaranteedMaximumRepositories"`
	GuaranteedMaximumWorkflows    int      `json:"guaranteedMaximumWorkflows"`
	GuaranteedMaximumIsLowerBound bool     `json:"guaranteedMaximumIsLowerBound"`
	Bottlenecks                   []string `json:"bottlenecks"`
}

type Report struct {
	SchemaVersion int            `json:"schemaVersion"`
	Name          string         `json:"name"`
	Profile       string         `json:"profile"`
	Scenario      string         `json:"scenario"`
	Assumptions   []string       `json:"assumptions"`
	Detail        map[string]any `json:"detail,omitempty"`
	Inputs        struct {
		Repositories           int     `json:"repositories"`
		WorkflowsPerRepository int     `json:"workflowsPerRepository"`
		HorizonHours           float64 `json:"horizonHours"`
		CollectionWindowHours  float64 `json:"collectionWindowHours"`
	} `json:"inputs"`
	Requested Evaluation `json:"requested"`
	Capacity  Capacity   `json:"capacity"`
}

func finite(value float64, name string, minimum float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < minimum {
		return 0, fmt.Errorf("%s must be at least %g", name, minimum)
	}
	return value, nil
}

func integer(value int, name string, minimum int) (int, error) {
	if value < minimum {
		return 0, fmt.Errorf("%s must be at least %d", name, minimum)
	}
	return value, nil
}

func normalizedScenario(input Scenario) (Scenario, error) {
	if input.SchemaVersion != 1 {
		return Scenario{}, errors.New("API cost scenario schemaVersion must be 1")
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return Scenario{}, errors.New("API cost scenario requires a name")
	}
	scenario := input
	scenario.Quotas = make(map[string]Quota, len(input.Quotas))
	for id, quota := range input.Quotas {
		scenario.Quotas[id] = quota
	}
	scenario.Operations = append([]Operation(nil), input.Operations...)
	scenario.Admissions = append([]Admission(nil), input.Admissions...)
	scenario.Assumptions = append([]string(nil), input.Assumptions...)
	if input.Detail != nil {
		scenario.Detail = make(map[string]any, len(input.Detail))
		for key, value := range input.Detail {
			scenario.Detail[key] = value
		}
	}
	if _, err := integer(scenario.Repositories, "repositories", 1); err != nil {
		return Scenario{}, err
	}
	if _, err := integer(scenario.WorkflowsPerRepository, "workflowsPerRepository", 1); err != nil {
		return Scenario{}, err
	}
	if _, err := finite(scenario.HorizonHours, "horizonHours", math.SmallestNonzeroFloat64); err != nil {
		return Scenario{}, err
	}
	if _, err := finite(scenario.CollectionWindowHours, "collectionWindowHours", math.SmallestNonzeroFloat64); err != nil {
		return Scenario{}, err
	}
	for id, quota := range scenario.Quotas {
		if _, err := finite(quota.Limit, id+".limit", math.SmallestNonzeroFloat64); err != nil {
			return Scenario{}, err
		}
		if _, err := finite(quota.Reserve, id+".reserve", 0); err != nil {
			return Scenario{}, err
		}
		if _, err := finite(quota.WindowHours, id+".windowHours", math.SmallestNonzeroFloat64); err != nil {
			return Scenario{}, err
		}
		if quota.Reserve >= quota.Limit {
			return Scenario{}, fmt.Errorf("%s.reserve must be below its limit", id)
		}
		scenario.Quotas[id] = quota
	}
	if len(scenario.Operations) == 0 {
		return Scenario{}, errors.New("API cost scenario requires operations")
	}
	for index := range scenario.Operations {
		operation := &scenario.Operations[index]
		if operation.ID == "" {
			return Scenario{}, errors.New("each operation requires an id")
		}
		if _, exists := scenario.Quotas[operation.Pool]; !exists {
			return Scenario{}, fmt.Errorf("%s references unknown pool %s", operation.ID, operation.Pool)
		}
		if _, err := finite(operation.CadenceHours, operation.ID+".cadenceHours", math.SmallestNonzeroFloat64); err != nil {
			return Scenario{}, err
		}
		if _, err := finite(operation.BurstHours, operation.ID+".burstHours", math.SmallestNonzeroFloat64); err != nil {
			return Scenario{}, err
		}
		for name, number := range map[string]float64{
			"scale.fixed": operation.Scale.Fixed, "scale.perRepository": operation.Scale.PerRepository,
			"scale.perWorkflow": operation.Scale.PerWorkflow,
		} {
			if _, err := finite(number, operation.ID+"."+name, 0); err != nil {
				return Scenario{}, err
			}
		}
		if _, err := finite(operation.RequestCount, operation.ID+".requestCount", 0); err != nil {
			return Scenario{}, err
		}
		if _, err := finite(operation.UnitCost, operation.ID+".unitCost", 0); err != nil {
			return Scenario{}, err
		}
		if _, err := finite(operation.CacheHitRate, operation.ID+".cacheHitRate", 0); err != nil {
			return Scenario{}, err
		}
		if operation.CacheHitRate > 1 {
			return Scenario{}, fmt.Errorf("%s.cacheHitRate must not exceed 1", operation.ID)
		}
	}
	for index := range scenario.Admissions {
		admission := &scenario.Admissions[index]
		if _, exists := scenario.Quotas[admission.Pool]; !exists {
			return Scenario{}, fmt.Errorf("%s references unknown pool %s", admission.ID, admission.Pool)
		}
		for name, value := range map[string]float64{
			"grant": admission.Grant, "actualPoolUnitsPerInstance": admission.ActualPoolUnitsPerInstance,
			"actualRequestsPerInstance": admission.ActualRequestsPerInstance,
		} {
			if _, err := finite(value, admission.ID+"."+name, math.SmallestNonzeroFloat64); err != nil {
				return Scenario{}, err
			}
		}
		if _, err := finite(admission.InstancesPerRepository, admission.ID+".instancesPerRepository", math.SmallestNonzeroFloat64); err != nil {
			return Scenario{}, err
		}
		if _, err := finite(admission.CadenceHours, admission.ID+".cadenceHours", math.SmallestNonzeroFloat64); err != nil {
			return Scenario{}, err
		}
	}
	if scenario.Runner != nil {
		runner := *scenario.Runner
		if _, err := finite(runner.MinutesPerInstance, "runner.minutesPerInstance", math.SmallestNonzeroFloat64); err != nil {
			return Scenario{}, err
		}
		if _, err := finite(runner.InstancesPerRepository, "runner.instancesPerRepository", math.SmallestNonzeroFloat64); err != nil {
			return Scenario{}, err
		}
		if _, err := integer(runner.Parallelism, "runner.parallelism", 1); err != nil {
			return Scenario{}, err
		}
		scenario.Runner = &runner
	}
	return scenario, nil
}

func operationDemand(operation Operation, repositories, workflowsPerRepository int) float64 {
	raw := operation.Scale.Fixed +
		operation.Scale.PerRepository*float64(repositories) +
		operation.Scale.PerWorkflow*float64(repositories*workflowsPerRepository)
	return raw * (1 - operation.CacheHitRate)
}

func evaluateAt(scenario Scenario, repositories int, guaranteed bool) Evaluation {
	workflows := repositories * scenario.WorkflowsPerRepository
	pools := make(map[string]*PoolReport, len(scenario.Quotas))
	for id, quota := range scenario.Quotas {
		available := quota.Limit - quota.Reserve
		pools[id] = &PoolReport{
			ID: id, Limit: quota.Limit, Reserve: quota.Reserve, WindowHours: quota.WindowHours,
			AvailablePerWindow:  available,
			CapacityOverHorizon: available * scenario.HorizonHours / quota.WindowHours,
			CollectionWindows:   math.Max(1, scenario.CollectionWindowHours/quota.WindowHours),
		}
	}
	operations := make([]OperationReport, 0, len(scenario.Operations))
	for _, operation := range scenario.Operations {
		count := operationDemand(operation, repositories, scenario.WorkflowsPerRepository)
		single := operationDemand(operation, 1, scenario.WorkflowsPerRepository)
		cycles := scenario.HorizonHours / operation.CadenceHours
		units := count * operation.UnitCost
		requests := count * operation.RequestCount
		pool := pools[operation.Pool]
		fleetWindows := math.Max(1, scenario.CollectionWindowHours/pool.WindowHours)
		operationWindows := math.Max(1, operation.BurstHours/pool.WindowHours)
		peak := math.Ceil(math.Max(units/fleetWindows, single*operation.UnitCost/operationWindows))
		pool.DemandPerCycle += units
		pool.DemandOverHorizon += units * cycles
		pool.RequestsPerCycle += requests
		pool.RequestsOverHorizon += requests * cycles
		pool.PeakDemandPerWindow += peak
		operations = append(operations, OperationReport{
			ID: operation.ID, Pool: operation.Pool, CountPerCycle: count, UnitsPerCycle: units,
			RequestsPerCycle: requests, CadenceHours: operation.CadenceHours,
			BurstHours: operation.BurstHours, PeakUnits: peak,
		})
	}
	violations := []string{}
	for _, id := range sortedPoolIDs(pools) {
		pool := pools[id]
		pool.SustainedUtilization = pool.DemandOverHorizon / pool.CapacityOverHorizon
		pool.PeakUtilization = pool.PeakDemandPerWindow / pool.AvailablePerWindow
		if pool.SustainedUtilization > 1 {
			violations = append(violations, pool.ID+":sustained")
		}
		if pool.PeakUtilization > 1 {
			violations = append(violations, pool.ID+":peak")
		}
	}
	admissions := make([]AdmissionReport, 0, len(scenario.Admissions))
	for _, admission := range scenario.Admissions {
		quota := scenario.Quotas[admission.Pool]
		available := quota.Limit - quota.Reserve
		collectionWindows := math.Max(1, scenario.CollectionWindowHours/quota.WindowHours)
		instances := float64(repositories) * admission.InstancesPerRepository
		peakInstances := math.Ceil(instances / collectionWindows)
		modeledPerWindow := 0
		if admission.Grant <= available {
			modeledPerWindow = int(math.Floor((available-admission.Grant)/admission.ActualPoolUnitsPerInstance)) + 1
		}
		guaranteedPerWindow := int(math.Floor(available / admission.Grant))
		grantExceeded := admission.ActualRequestsPerInstance > admission.Grant
		supported := modeledPerWindow
		mode := "modeled"
		if guaranteed {
			supported, mode = guaranteedPerWindow, "guaranteed"
		}
		if grantExceeded {
			violations = append(violations, admission.ID+":collector-grant")
		}
		if peakInstances > float64(supported) {
			violations = append(violations, admission.ID+":"+mode+"-admission")
		}
		admissions = append(admissions, AdmissionReport{
			ID: admission.ID, Pool: admission.Pool, Grant: admission.Grant,
			Instances: instances, PeakInstances: peakInstances,
			ActualPoolUnitsPerInstance: admission.ActualPoolUnitsPerInstance,
			ActualRequestsPerInstance:  admission.ActualRequestsPerInstance,
			ModeledPerWindow:           modeledPerWindow, GuaranteedPerWindow: guaranteedPerWindow,
			GrantExceeded: grantExceeded,
		})
	}
	var runner *RunnerReport
	if scenario.Runner != nil {
		instances := float64(repositories) * scenario.Runner.InstancesPerRepository
		demand := instances * scenario.Runner.MinutesPerInstance
		capacity := scenario.CollectionWindowHours * 60 * float64(scenario.Runner.Parallelism)
		runner = &RunnerReport{
			Instances: instances, DemandMinutes: demand, CapacityMinutes: capacity,
			Utilization: demand / capacity, MinutesPerInstance: scenario.Runner.MinutesPerInstance,
			Parallelism: scenario.Runner.Parallelism,
		}
		if runner.Utilization > 1 {
			violations = append(violations, "runner:peak")
		}
	}
	return Evaluation{
		Repositories: repositories, Workflows: workflows, Supported: len(violations) == 0,
		Violations: violations, Pools: pools, Operations: operations, Admissions: admissions, Runner: runner,
	}
}

func maximumRepositories(scenario Scenario, guaranteed bool) (int, bool) {
	if !evaluateAt(scenario, 1, guaranteed).Supported {
		return 0, false
	}
	low, high := 1, 2
	for high < maxRepositories && evaluateAt(scenario, high, guaranteed).Supported {
		low, high = high, high*2
	}
	if high >= maxRepositories && evaluateAt(scenario, maxRepositories, guaranteed).Supported {
		return maxRepositories, true
	}
	if high > maxRepositories {
		high = maxRepositories
	}
	for low+1 < high {
		middle := (low + high) / 2
		if evaluateAt(scenario, middle, guaranteed).Supported {
			low = middle
		} else {
			high = middle
		}
	}
	return low, false
}

func Simulate(input Scenario) (Report, error) {
	scenario, err := normalizedScenario(input)
	if err != nil {
		return Report{}, err
	}
	requested := evaluateAt(scenario, scenario.Repositories, false)
	modeled, modeledCapped := maximumRepositories(scenario, false)
	guaranteed, guaranteedCapped := maximumRepositories(scenario, true)
	bottlenecks := []string{}
	if !modeledCapped {
		bottlenecks = evaluateAt(scenario, modeled+1, false).Violations
	}
	profile := scenario.Profile
	if profile == "" {
		profile = "custom"
	}
	scenarioName := scenario.Scenario
	if scenarioName == "" {
		scenarioName = "custom"
	}
	report := Report{
		SchemaVersion: 1, Name: scenario.Name, Profile: profile, Scenario: scenarioName,
		Assumptions: scenario.Assumptions, Detail: scenario.Detail, Requested: requested,
		Capacity: Capacity{
			ModeledMaximumRepositories:    modeled,
			ModeledMaximumWorkflows:       modeled * scenario.WorkflowsPerRepository,
			ModeledMaximumIsLowerBound:    modeledCapped,
			GuaranteedMaximumRepositories: guaranteed,
			GuaranteedMaximumWorkflows:    guaranteed * scenario.WorkflowsPerRepository,
			GuaranteedMaximumIsLowerBound: guaranteedCapped,
			Bottlenecks:                   bottlenecks,
		},
	}
	report.Inputs.Repositories = scenario.Repositories
	report.Inputs.WorkflowsPerRepository = scenario.WorkflowsPerRepository
	report.Inputs.HorizonHours = scenario.HorizonHours
	report.Inputs.CollectionWindowHours = scenario.CollectionWindowHours
	return report, nil
}

type Population struct {
	Outputs         int
	Implementations int
	ObservedAt      string
	WindowEnd       string
	Source          string
}

type DailyFileDietOptions struct {
	Scenario                string
	Population              *Population
	Repositories            *int
	HorizonHours            *float64
	CollectionWindowHours   *float64
	WallMinutes             *float64
	Parallelism             *int
	CoreLimit               *float64
	CoreReserve             *float64
	SearchLimit             *float64
	SearchReserve           *float64
	SearchWindowMinutes     *float64
	GraphQLLimit            *float64
	GraphQLReserve          *float64
	CollectorGrant          *float64
	TrackedIssues           *int
	NewIssues               *int
	MutablePulls            *int
	NewReceipts             *int
	SearchPagesPerTerm      *int
	TimelinePagesPerIssue   *int
	PullFilePages           *int
	SnapshotReads           *int
	GraphQLPointsPerRequest *float64
	Calibration             *DailyFileDietCalibration
}

type DailyFileDietCalibration struct {
	Attempts              int
	CoreRequests          int
	SearchRequests        int
	GraphQLRequests       int
	GraphQLPointsObserved int
	WallMS                int64
	MaxWallMS             int64
	OutputBytes           int64
	TransportBytes        int64
	Source                string
}

func value[T any](pointer *T, fallback T) T {
	if pointer != nil {
		return *pointer
	}
	return fallback
}

func DailyFileDietScenario(options DailyFileDietOptions) (Scenario, error) {
	scenarioName := options.Scenario
	if scenarioName == "" {
		scenarioName = "retained"
	}
	if scenarioName != "retained" && scenarioName != "hard-bound" {
		return Scenario{}, errors.New("the Daily File Diet scenario must be retained or hard-bound")
	}
	hard := scenarioName == "hard-bound"
	population := Population{}
	if !hard {
		if options.Population == nil || options.Population.Source == "" {
			return Scenario{}, errors.New("the Daily File Diet retained scenario requires canonical database evidence")
		}
		population = *options.Population
	}
	trackedIssues := value(options.TrackedIssues, population.Outputs)
	if hard {
		trackedIssues = value(options.TrackedIssues, 200)
	}
	newIssues := value(options.NewIssues, 0)
	mutablePulls := value(options.MutablePulls, 0)
	newReceipts := value(options.NewReceipts, 0)
	searchPages := value(options.SearchPagesPerTerm, 1)
	timelinePages := value(options.TimelinePagesPerIssue, 1)
	pullPages := value(options.PullFilePages, 1)
	if hard {
		newIssues = value(options.NewIssues, 200)
		mutablePulls = value(options.MutablePulls, 100)
		newReceipts = value(options.NewReceipts, 200)
		searchPages = value(options.SearchPagesPerTerm, 10)
		timelinePages = value(options.TimelinePagesPerIssue, 10)
		pullPages = value(options.PullFilePages, 10)
	}
	for name, number := range map[string]int{
		"trackedIssues": trackedIssues, "newIssues": newIssues, "mutablePulls": mutablePulls,
		"newReceipts": newReceipts, "snapshotReads": value(options.SnapshotReads, 1),
	} {
		if _, err := integer(number, name, 0); err != nil {
			return Scenario{}, err
		}
	}
	for name, number := range map[string]int{
		"searchPagesPerTerm": searchPages, "timelinePagesPerIssue": timelinePages, "pullFilePages": pullPages,
	} {
		if _, err := integer(number, name, 1); err != nil {
			return Scenario{}, err
		}
	}
	wallMinutes := value(options.WallMinutes, 10)
	if _, err := finite(wallMinutes, "wallMinutes", math.SmallestNonzeroFloat64); err != nil {
		return Scenario{}, err
	}
	graphQLPoints := value(options.GraphQLPointsPerRequest, 1)
	if _, err := finite(graphQLPoints, "graphqlPointsPerRequest", math.SmallestNonzeroFloat64); err != nil {
		return Scenario{}, err
	}
	snapshotReads := value(options.SnapshotReads, 1)
	corePerWorkflow := 3 + trackedIssues*timelinePages + newIssues*2 +
		mutablePulls*(1+pullPages) + newReceipts*2 + snapshotReads
	searchRequests := 6 * searchPages
	graphQLRequests := int(math.Ceil(float64(trackedIssues)/10)) + mutablePulls
	totalRequests := corePerWorkflow + searchRequests + graphQLRequests
	assumptions := []string{
		"All due collectors are evenly spread across the collection window; peak demand is still checked per quota window.",
		"The retained scenario refreshes every tracked issue timeline but reuses merged pull, receipt, Git inventory, and blob evidence.",
		fmt.Sprintf("The scenario uses %d Search page(s) per term, %d timeline page(s) per issue, and %g GraphQL point(s) per request.", searchPages, timelinePages, graphQLPoints),
		fmt.Sprintf("It includes %d new issue(s), %d mutable pull request(s), and %d new receipt(s) per repository and cadence.", newIssues, mutablePulls, newReceipts),
		"Git transport is excluded from GitHub REST, Search, and GraphQL quotas and remains separately bounded.",
		"The guaranteed capacity assumes every admitted collector consumes its full reviewed request grant.",
	}
	if !hard {
		assumptions = append(assumptions, "The retained population came from "+population.Source+".")
		if population.ObservedAt != "" {
			assumptions = append(assumptions,
				fmt.Sprintf("Retained evidence observed %s with cutoff %s.", population.ObservedAt, population.WindowEnd))
		}
	}
	actualCore := float64(corePerWorkflow)
	actualSearch := float64(searchRequests)
	actualGraphQL := float64(graphQLRequests)
	actualRequests := float64(totalRequests)
	calibrated := options.Calibration
	if calibrated != nil {
		if calibrated.Attempts <= 0 || calibrated.CoreRequests < 0 || calibrated.SearchRequests < 0 ||
			calibrated.GraphQLRequests < 0 || calibrated.GraphQLPointsObserved < 0 ||
			calibrated.WallMS < 0 || calibrated.MaxWallMS < 0 || calibrated.OutputBytes < 0 ||
			calibrated.TransportBytes < 0 || calibrated.Source == "" {
			return Scenario{}, errors.New("invalid Daily File Diet calibration")
		}
		attempts := float64(calibrated.Attempts)
		actualCore = float64(calibrated.CoreRequests) / attempts
		actualSearch = float64(calibrated.SearchRequests) / attempts
		actualGraphQL = float64(calibrated.GraphQLRequests) / attempts
		actualRequests = actualCore + actualSearch + actualGraphQL
		if options.WallMinutes == nil {
			wallMinutes = float64(calibrated.MaxWallMS) / 60000
			if wallMinutes <= 0 {
				wallMinutes = float64(calibrated.WallMS) / attempts / 60000
			}
			if wallMinutes <= 0 {
				wallMinutes = 10
			}
		}
		if options.GraphQLPointsPerRequest == nil && calibrated.GraphQLRequests > 0 {
			graphQLPoints = float64(calibrated.GraphQLPointsObserved) / float64(calibrated.GraphQLRequests)
			if graphQLPoints <= 0 {
				graphQLPoints = 1
			}
		}
		assumptions = append(assumptions,
			fmt.Sprintf("Observed calibration uses %d successful or failed attempt(s) from hosted deployments with the daily-file-diet workflow slug at %s.", calibrated.Attempts, calibrated.Source),
			"Observed per-pool request and maximum wall-time costs replace the retained population estimate; the reviewed grant remains the fail-safe upper bound.")
	}
	burstHours := wallMinutes / 60
	operations := []Operation{
		{ID: "repository-aliases-and-source-history", Pool: "core", Scale: Scale{PerWorkflow: 3}, CadenceHours: 24, BurstHours: burstHours},
		{ID: "tracked-issue-timelines", Pool: "core", Scale: Scale{PerWorkflow: float64(trackedIssues * timelinePages)}, CadenceHours: 24, BurstHours: burstHours},
		{ID: "new-output-provenance", Pool: "core", Scale: Scale{PerWorkflow: float64(newIssues * 2)}, CadenceHours: 24, BurstHours: burstHours},
		{ID: "mutable-pull-details-and-files", Pool: "core", Scale: Scale{PerWorkflow: float64(mutablePulls * (1 + pullPages))}, CadenceHours: 24, BurstHours: burstHours},
		{ID: "new-receipt-metadata-and-download", Pool: "core", Scale: Scale{PerWorkflow: float64(newReceipts * 2)}, CadenceHours: 24, BurstHours: burstHours},
		{ID: "snapshot-cutoff", Pool: "core", Scale: Scale{PerWorkflow: float64(snapshotReads)}, CadenceHours: 24, BurstHours: burstHours},
		{ID: "output-discovery-search", Pool: "search", Scale: Scale{PerWorkflow: float64(searchRequests)}, CadenceHours: 24, BurstHours: burstHours},
		{ID: "issue-and-pull-relationship-graphs", Pool: "graphql", Scale: Scale{PerWorkflow: float64(graphQLRequests)}, UnitCost: graphQLPoints, CadenceHours: 24, BurstHours: burstHours},
	}
	if calibrated != nil {
		operations = []Operation{
			{ID: "observed-core-requests", Pool: "core", Scale: Scale{PerWorkflow: actualCore}, CadenceHours: 24, BurstHours: burstHours},
			{ID: "observed-search-requests", Pool: "search", Scale: Scale{PerWorkflow: actualSearch}, CadenceHours: 24, BurstHours: burstHours},
			{ID: "observed-graphql-requests", Pool: "graphql", Scale: Scale{PerWorkflow: actualGraphQL}, UnitCost: graphQLPoints, CadenceHours: 24, BurstHours: burstHours},
		}
	}
	scenario := Scenario{
		SchemaVersion: 1,
		Name:          fmt.Sprintf("Daily File Diet GitHub API capacity (%s)", scenarioName),
		Profile:       "daily-file-diet", Scenario: scenarioName,
		Repositories: value(options.Repositories, 1), WorkflowsPerRepository: 1,
		HorizonHours:          value(options.HorizonHours, 24),
		CollectionWindowHours: value(options.CollectionWindowHours, 1),
		Quotas: map[string]Quota{
			"core":    {Limit: value(options.CoreLimit, 5000), Reserve: value(options.CoreReserve, 2000), WindowHours: 1},
			"search":  {Limit: value(options.SearchLimit, 30), Reserve: value(options.SearchReserve, 0), WindowHours: value(options.SearchWindowMinutes, 1) / 60},
			"graphql": {Limit: value(options.GraphQLLimit, 5000), Reserve: value(options.GraphQLReserve, 500), WindowHours: 1},
		},
		Operations: operations,
		Admissions: []Admission{{
			ID: "reviewed-collector-grant", Pool: "core", Grant: value(options.CollectorGrant, 700),
			ActualPoolUnitsPerInstance: actualCore,
			ActualRequestsPerInstance:  actualRequests,
			InstancesPerRepository:     1, CadenceHours: 24,
		}},
		Runner:      &Runner{MinutesPerInstance: wallMinutes, InstancesPerRepository: 1, Parallelism: value(options.Parallelism, 1)},
		Assumptions: assumptions,
		Detail: map[string]any{
			"trackedIssues": trackedIssues, "retainedImplementations": population.Implementations,
			"newIssues": newIssues, "mutablePulls": mutablePulls, "newReceipts": newReceipts,
			"searchPagesPerTerm": searchPages, "timelinePagesPerIssue": timelinePages,
			"pullFilePages": pullPages, "snapshotReads": snapshotReads,
			"corePerWorkflow": corePerWorkflow, "searchRequestsPerWorkflow": searchRequests,
			"graphqlRequestsPerWorkflow": graphQLRequests, "totalRequestsPerWorkflow": totalRequests,
		},
	}
	if calibrated != nil {
		scenario.Detail["observedAttempts"] = calibrated.Attempts
		scenario.Detail["observedCoreRequestsPerWorkflow"] = actualCore
		scenario.Detail["observedSearchRequestsPerWorkflow"] = actualSearch
		scenario.Detail["observedGraphQLRequestsPerWorkflow"] = actualGraphQL
		scenario.Detail["observedTotalRequestsPerWorkflow"] = actualRequests
		scenario.Detail["observedMaxWallMinutes"] = wallMinutes
		scenario.Detail["observedOutputBytes"] = calibrated.OutputBytes
		scenario.Detail["observedTransportBytes"] = calibrated.TransportBytes
	}
	for index := range scenario.Operations {
		scenario.Operations[index].RequestCount = 1
		if scenario.Operations[index].UnitCost == 0 {
			scenario.Operations[index].UnitCost = 1
		}
	}
	return normalizedScenario(scenario)
}

func sortedPoolIDs(pools map[string]*PoolReport) []string {
	ids := make([]string, 0, len(pools))
	for id := range pools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func FormatMarkdown(report Report) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", report.Name)
	fmt.Fprintf(&builder, "Requested scale: **%d repositories / %d workflows**.\n", report.Inputs.Repositories, report.Requested.Workflows)
	fmt.Fprintf(&builder, "Modeled maximum: **%d repositories**.\n", report.Capacity.ModeledMaximumRepositories)
	fmt.Fprintf(&builder, "Guaranteed at declared grants: **%d repositories**.\n", report.Capacity.GuaranteedMaximumRepositories)
	bottlenecks := strings.Join(report.Capacity.Bottlenecks, ", ")
	if bottlenecks == "" {
		bottlenecks = "none within search bound"
	}
	fmt.Fprintf(&builder, "Bottleneck at the next repository: **%s**.\n\n", bottlenecks)
	builder.WriteString("| Pool | Horizon demand | Horizon capacity | Sustained | Peak demand/window | Available/window | Peak |\n")
	builder.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, id := range sortedPoolIDs(report.Requested.Pools) {
		pool := report.Requested.Pools[id]
		fmt.Fprintf(&builder, "| %s | %.1f | %.1f | %.1f%% | %.1f | %.1f | %.1f%% |\n",
			pool.ID, pool.DemandOverHorizon, pool.CapacityOverHorizon, pool.SustainedUtilization*100,
			pool.PeakDemandPerWindow, pool.AvailablePerWindow, pool.PeakUtilization*100)
	}
	builder.WriteString("\n")
	if report.Requested.Runner != nil {
		fmt.Fprintf(&builder, "Runner: %.1f / %.1f minutes (%.1f%%).\n\n",
			report.Requested.Runner.DemandMinutes, report.Requested.Runner.CapacityMinutes,
			report.Requested.Runner.Utilization*100)
	}
	for _, assumption := range report.Assumptions {
		fmt.Fprintf(&builder, "- %s\n", assumption)
	}
	builder.WriteString("\n")
	return builder.String()
}
