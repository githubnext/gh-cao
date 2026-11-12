package webhookgateway_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

func TestBatchAtomicReservationAndSingleRateCharge(t *testing.T) {
	for _, backend := range []string{"postgres"} {
		t.Run(backend+"/BG-04", func(t *testing.T) {
			rig := newBatchRig(t, backend, func(p *wg.PolicySnapshot) {
				rate := wg.RateBudget{Capacity: 1, Period: time.Hour}
				p.Policy.Queue.Rates = wg.RateBudgets{Global: rate, App: rate, Tenant: rate, Destination: rate, Workflow: rate}
			})
			for i := range 3 {
				rig.admit(t, "issues", i+1)
			}
			jobs := rig.claim(t, 0)
			if len(jobs) != 3 {
				t.Fatalf("claim split same-kind members: %d", len(jobs))
			}
			fences := wg.BatchFences(jobs)
			evidence := rig.evidence(t, jobs)
			fences[2].Version++
			if _, err := rig.queue.BeginDispatchBatch(t.Context(), fences, evidence); !errors.Is(err, wg.ErrConflict) {
				t.Fatal("stale sibling reserved partial dispatch", err)
			}
			for i, job := range jobs {
				renewed, err := rig.queue.Renew(t.Context(), wg.BatchFences([]wg.Job{job})[0], time.Minute)
				if err != nil || renewed.State != wg.Claimed || renewed.Attempt != 0 {
					t.Fatal("failed reservation mutated a member", err)
				}
				jobs[i] = renewed
			}
			if _, err := rig.queue.BeginDispatchBatch(t.Context(), wg.BatchFences(jobs[:2]), rig.evidence(t, jobs[:2])); !errors.Is(err, wg.ErrConflict) {
				t.Fatal("partial claim group dispatched", err)
			}
			started, err := rig.queue.BeginDispatchBatch(t.Context(), wg.BatchFences(jobs), rig.evidence(t, jobs))
			if err != nil || len(started) != 3 {
				t.Fatal("one dispatch charged more than one rate token or stale attempt spent quota", err)
			}
			if _, err := rig.queue.Renew(t.Context(), wg.BatchFences(started)[0], time.Minute); !errors.Is(err, wg.ErrConflict) {
				t.Fatal("one member extended an already-reserved batch lease", err)
			}
			stale := wg.BatchFences(started)
			stale[2].Version++
			if _, err := rig.queue.FinishBatch(t.Context(), stale, wg.Outcome{State: wg.Succeeded, AcceptedStatus: 204}); !errors.Is(err, wg.ErrConflict) {
				t.Fatal("stale completion partially settled batch", err)
			}
			finished, err := rig.queue.FinishBatch(t.Context(), wg.BatchFences(started), wg.Outcome{State: wg.Succeeded, AcceptedStatus: 204})
			if err != nil || len(finished) != 3 {
				t.Fatal("valid atomic completion failed", err)
			}
			for _, job := range finished {
				if job.State != wg.Succeeded || job.Attempt != 1 || job.AcceptedStatus != 204 {
					t.Fatal("member lost common dispatch outcome", job.State)
				}
			}
			rig.admit(t, "issues", 4)
			next := rig.claim(t, 0)
			if _, err := rig.queue.BeginDispatchBatch(t.Context(), wg.BatchFences(next), rig.evidence(t, next)); !errors.Is(err, wg.ErrCapacity) {
				t.Fatal("batch failed to consume its one durable rate token", err)
			}
		})
	}
}

func TestBatchFreshEvidenceAndPolicyInvalidationFenceEveryMember(t *testing.T) {
	for _, backend := range []string{"postgres"} {
		t.Run(backend+"/BG-04-revocation", func(t *testing.T) {
			rig := newBatchRig(t, backend, nil)
			rig.admit(t, "issues", 1)
			rig.admit(t, "issues", 2)
			jobs := rig.claim(t, 0)
			evidence := rig.evidence(t, jobs)
			evidence[1].Snapshot.Complete = false
			if _, err := rig.queue.BeginDispatchBatch(t.Context(), wg.BatchFences(jobs), evidence); !errors.Is(err, wg.ErrUnauthorized) {
				t.Fatal("incomplete sibling authority partially reserved the batch", err)
			}
			next := jobs[0].QueueRevision
			next.Revision++
			next.Digest = strings.Repeat("f", 64)
			next.Enabled = false
			if err := rig.queue.ApplyRevision(t.Context(), next); err != nil {
				t.Fatal(err)
			}
			if _, err := rig.queue.BeginDispatchBatch(t.Context(), wg.BatchFences(jobs), rig.evidence(t, jobs)); !errors.Is(err, wg.ErrConflict) {
				t.Fatal("observed policy invalidation failed to fence batch reservation", err)
			}
			depth, err := rig.queue.(wg.QueueDepthProvider).QueueDepth(t.Context())
			if err != nil || depth != 0 {
				t.Fatal("revocation left a sibling dispatchable", depth, err)
			}
		})
	}
}

func TestBatchRecoveryNeverReplaysUncertainAcceptance(t *testing.T) {
	for _, backend := range []string{"postgres"} {
		for _, expire := range []bool{false, true} {
			t.Run(backend+"/BG-05/"+map[bool]string{false: "unknown", true: "lost-completion"}[expire], func(t *testing.T) {
				rig := newBatchRig(t, backend, func(p *wg.PolicySnapshot) { p.Policy.Queue.LeaseDuration = time.Second })
				rig.admit(t, "issues", 1)
				rig.admit(t, "issues", 2)
				jobs := rig.claim(t, 0)
				started, err := rig.queue.BeginDispatchBatch(t.Context(), wg.BatchFences(jobs), rig.evidence(t, jobs))
				if err != nil {
					t.Fatal(err)
				}
				if expire {
					time.Sleep(1100 * time.Millisecond)
					maintenance, err := rig.queue.Maintain(t.Context(), 100)
					if err != nil || maintenance.Ambiguous != 2 {
						t.Fatal("lost batch completion was not uniformly ambiguous", maintenance, err)
					}
				} else {
					finished, err := rig.queue.FinishBatch(t.Context(), wg.BatchFences(started), wg.Outcome{State: wg.Ambiguous, Reason: "transport-unknown"})
					if err != nil || len(finished) != 2 {
						t.Fatal("unknown batch failed to settle", err)
					}
				}
				if replay := rig.claim(t, 0); len(replay) != 0 {
					t.Fatal("uncertain batch replayed")
				}
				if _, err := rig.queue.FinishBatch(t.Context(), wg.BatchFences(started),
					wg.Outcome{State: wg.Ready, Reason: "not-sent"}); !errors.Is(err, wg.ErrConflict) {
					t.Fatal("late stale completion made an ambiguous member ready", err)
				}
			})
		}
	}
}

func TestBatchTenantPrefixCannotStarveAnotherRoute(t *testing.T) {
	for _, backend := range []string{"postgres"} {
		t.Run(backend+"/BG-06-tenant-prefix", func(t *testing.T) {
			rig := newBatchRig(t, backend, func(p *wg.PolicySnapshot) {
				p.Policy.Queue.MaxConcurrency = 2
				for i := range p.Subscriptions {
					p.Subscriptions[i].TenantID = "a"
					if p.Subscriptions[i].Source.Event == "issue_comment" {
						p.Subscriptions[i].TenantID = "a-b"
					}
				}
			})
			rig.admit(t, "issues", 1)
			rig.admit(t, "issue_comment", 1)
			first := rig.claim(t, 0)
			if len(first) != 1 || first[0].Subscription.TenantID != "a" {
				t.Fatal("route cursor did not begin at the lexically first tenant")
			}
			rig.admit(t, "issues", 2)
			second := rig.claim(t, 0)
			if len(second) != 1 || second[0].Subscription.TenantID != "a-b" {
				t.Fatal("continuously busy prefix tenant starved its sibling")
			}
		})
	}
}

func TestBatchSchedulingBoundsAndReplicaExclusion(t *testing.T) {
	for _, backend := range []string{"postgres"} {
		t.Run(backend+"/BG-06", func(t *testing.T) {
			rig := newBatchRig(t, backend, func(p *wg.PolicySnapshot) { p.Policy.Queue.MaxConcurrency = 2 })
			rig.admit(t, "issues", 1)
			rig.admit(t, "issue_comment", 1)
			if delayed := rig.claim(t, time.Second); len(delayed) != 0 {
				t.Fatal("coalescing window dispatched immediately")
			}

			time.Sleep(1100 * time.Millisecond)
			first := rig.claim(t, time.Second)
			if len(first) != 1 {
				t.Fatal("bounded window did not flush singleton")
			}
			rig.admit(t, first[0].Event.Name, 2)
			second := rig.claim(t, 0)
			if len(second) != 1 || second[0].Event.Name == first[0].Event.Name {
				t.Fatal("hot workflow starved another route")
			}
			if overflow := rig.claim(t, 0); len(overflow) != 0 {
				t.Fatal("active batch concurrency ceiling exceeded")
			}
		})
		t.Run(backend+"/BG-07", func(t *testing.T) {
			rig := newBatchRig(t, backend, nil)
			for i := range 10 {
				rig.admit(t, "issues", i+1)
			}
			var workers sync.WaitGroup
			var mu sync.Mutex
			var claimed []wg.Job
			for range 4 {
				workers.Go(func() {
					jobs, err := rig.queue.ClaimBatch(t.Context(), wg.ClaimRequest{Owner: "replica", LeaseDuration: time.Minute})
					if err != nil {
						t.Error(err)
					}
					mu.Lock()
					claimed = append(claimed, jobs...)
					mu.Unlock()
				})
			}
			workers.Wait()
			seen := make(map[string]bool)
			for _, job := range claimed {
				if seen[job.ID] {
					t.Fatal("two replicas claimed the same member")
				}
				seen[job.ID] = true
			}
			if len(seen) != 10 {
				t.Fatalf("lost batch members across replicas: %d", len(seen))
			}
		})
	}
}

func TestBatchPackingRetainsEveryOverflowMember(t *testing.T) {
	for _, backend := range []string{"postgres"} {
		for _, large := range []bool{false, true} {
			t.Run(backend+"/BG-08/"+map[bool]string{false: "items", true: "bytes"}[large], func(t *testing.T) {
				rig := newBatchRig(t, backend, func(p *wg.PolicySnapshot) {
					if large {
						for i := range p.Subscriptions {
							p.Subscriptions[i].Destination.Fields = append(p.Subscriptions[i].Destination.Fields,
								wg.PayloadField{Name: "padding", Literal: []byte(`"` + strings.Repeat("x", 40000) + `"`), Required: true})
						}
					}
				})
				count, expected := 65, wg.MaxDispatchBatchItems
				if large {
					count, expected = 2, 1
				}
				for i := range count {
					rig.admit(t, "issues", i+1)
				}
				first := rig.claim(t, 0)
				if len(first) != expected {
					t.Fatalf("batch packing bound: got=%d expected=%d", len(first), expected)
				}
				second := rig.claim(t, 0)
				if len(first)+len(second) != count {
					t.Fatal("overflow dropped or oversubscribed a member")
				}
			})
		}
	}
}
