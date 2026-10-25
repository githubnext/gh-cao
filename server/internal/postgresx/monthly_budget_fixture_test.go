package postgresx

import (
	"math"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

const (
	monthlyRepositories = 10_000
	monthlyDays         = 30
	monthlyBudgetCents  = 2_500_000
	monthlySeed         = 42
)

type monthlyBudgetDay struct {
	Date          string `json:"date"`
	Runs          int    `json:"runs"`
	SpentCents    int    `json:"spentCents"`
	UnspentCents  int    `json:"unspentCents"`
	AicHundredths int64  `json:"aicHundredths"`
	CostBuckets   [4]int `json:"costBuckets"`
}

type monthlyBudgetRun struct {
	ID            int
	Repository    int
	At            time.Time
	CostCents     int
	AicHundredths int
}

// A half-normal bell has its mode at $1; rejection above $5 avoids a clipped
// spike at the upper bound. USD controls fixture volume, not canonical AIC.
func monthlyRunCost(random *rand.Rand) int {
	for {
		cents := 100 + int(math.Round(math.Abs(random.NormFloat64())*125))
		if cents <= 500 {
			return cents
		}
	}
}

func monthlyBudgetWorkload(end time.Time, emit func(monthlyBudgetRun)) []monthlyBudgetDay {
	// #nosec G404 -- reproducible synthetic load, never security-sensitive randomness.
	random := rand.New(rand.NewPCG(monthlySeed, 1))
	days := make([]monthlyBudgetDay, 0, monthlyDays)
	id := 0
	for day := range monthlyDays {
		start := end.AddDate(0, 0, day-monthlyDays)
		ledger := monthlyBudgetDay{Date: start.Format(time.DateOnly)}
		for {
			cost := monthlyRunCost(random)
			if cost > monthlyBudgetCents-ledger.SpentCents {
				break
			}
			id++
			run := monthlyBudgetRun{
				ID: id, Repository: ledger.Runs % monthlyRepositories,
				At:        start.Add(time.Duration(ledger.Runs) * 3 * time.Second),
				CostCents: cost, AicHundredths: 100 + id%300,
			}
			emit(run)
			ledger.Runs++
			ledger.SpentCents += cost
			ledger.AicHundredths += int64(run.AicHundredths)
			ledger.CostBuckets[min((cost-100)/100, 3)]++
		}
		ledger.UnspentCents = monthlyBudgetCents - ledger.SpentCents
		days = append(days, ledger)
	}
	return days
}

func TestMonthlyBudgetWorkload(t *testing.T) {
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var previous []monthlyBudgetDay
	for range 2 {
		seen := make([]int, monthlyRepositories)
		costs := [4]int{}
		total, spent := 0, 0
		days := monthlyBudgetWorkload(end, func(run monthlyBudgetRun) {
			total++
			spent += run.CostCents
			costs[min((run.CostCents-100)/100, 3)]++
			seen[run.Repository]++
			if run.ID != total || run.CostCents < 100 || run.CostCents > 500 ||
				run.At.Before(end.AddDate(0, 0, -monthlyDays)) || !run.At.Before(end) {
				t.Fatalf("invalid run identity, cost or month boundary: %+v", run)
			}
		})
		for _, day := range days {
			if day.Runs < monthlyRepositories || day.SpentCents > monthlyBudgetCents ||
				day.UnspentCents < 0 || day.UnspentCents >= 500 ||
				day.SpentCents+day.UnspentCents != monthlyBudgetCents {
				t.Fatalf("invalid daily budget or incomplete fleet coverage: %+v", day)
			}
		}
		for _, count := range seen {
			if count < monthlyDays {
				t.Fatal("every repository must run on every simulated day")
			}
		}
		if total < 300_000 || total > 750_000 || float64(spent)/float64(total) >= 250 ||
			costs[0] <= costs[1] || costs[1] <= costs[2] || costs[2] <= costs[3] || costs[3] == 0 {
			t.Fatalf("workload must favor $1 with a decreasing bell tail: runs=%d spend=%d histogram=%v", total, spent, costs)
		}
		if previous != nil && !reflect.DeepEqual(previous, days) {
			t.Fatal("fixed-seed monthly workload is not reproducible")
		}
		previous = days
	}
}
