package adxtrace

import (
	"slices"
	"testing"
	"time"
)

func newFrameTestWorker(mode string, sampleCount int, now time.Time) *Worker {
	return &Worker{
		spool: &spool{config: Config{Mode: mode, SampleCount: sampleCount, Seed: "frame-seed"}},
		now:   func() time.Time { return now },
	}
}

func frameTestRecords() []*record {
	return []*record{
		{ID: "a", Version: "va", Repository: "o/r", Day: "2026-01-02", State: "eligible"},
		{ID: "b", Version: "vb", Repository: "o/r", Day: "2026-01-02", State: "eligible"},
		{ID: "c", Version: "vc", Repository: "o/r", Day: "2026-01-02", State: "eligible"},
		{ID: "d", Version: "vd", Repository: "o/r", Day: "2026-01-02", State: "excluded"},
		{ID: "e", Version: "ve", Repository: "o/r", Day: "2026-01-02", State: "withdrawn"},
		{ID: "f", Version: "vf", Repository: "o/r", Day: "2026-01-02", State: "expired"},
	}
}

func TestNewFrameFullModeSelectsEveryEligibleRecord(t *testing.T) {
	now := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	w := newFrameTestWorker("full", 0, now)
	f, err := w.newFrame("frame-1", frameTestRecords())
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "frame-1" || f.Repository != "o/r" || f.Day != "2026-01-02" || f.State != "pending" || !f.Cutoff.Equal(now) {
		t.Fatalf("frame identity = %+v", f)
	}
	if f.Population != 3 || f.Excluded != 1 || f.SourceGaps != 2 {
		t.Fatalf("counts population=%d excluded=%d gaps=%d, want 3/1/2", f.Population, f.Excluded, f.SourceGaps)
	}
	want := map[string]string{"a": "va", "b": "vb", "c": "vc"}
	if len(f.Members) != len(want) {
		t.Fatalf("Members = %v, want %v", f.Members, want)
	}
	for id, version := range want {
		if f.Members[id] != version {
			t.Fatalf("Members = %v, want %v", f.Members, want)
		}
	}
	if f.MembershipDigest == "" {
		t.Fatal("membership digest is empty")
	}
}

func TestNewFrameSampleIsDeterministicAndBounded(t *testing.T) {
	now := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	w := newFrameTestWorker("sample", 2, now)
	first, err := w.newFrame("frame-1", frameTestRecords())
	if err != nil {
		t.Fatal(err)
	}
	reversed := frameTestRecords()
	slices.Reverse(reversed)
	second, err := w.newFrame("frame-1", reversed)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Members) != 2 || first.Population != 3 {
		t.Fatalf("members=%d population=%d, want 2 of 3", len(first.Members), first.Population)
	}
	if first.MembershipDigest != second.MembershipDigest {
		t.Fatalf("sample depends on input order: %v vs %v", first.Members, second.Members)
	}
	for id, version := range first.Members {
		if second.Members[id] != version {
			t.Fatalf("sample depends on input order: %v vs %v", first.Members, second.Members)
		}
	}
}

func TestNewFrameSampleCountLargerThanPopulationSelectsAll(t *testing.T) {
	w := newFrameTestWorker("sample", 10, time.Now())
	f, err := w.newFrame("frame-1", frameTestRecords())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Members) != 3 || f.Population != 3 {
		t.Fatalf("members=%d population=%d, want 3 of 3", len(f.Members), f.Population)
	}
}

func TestNewFrameWithoutEligibleRecordsHasEmptyMembership(t *testing.T) {
	w := newFrameTestWorker("sample", 2, time.Now())
	records := []*record{
		{ID: "x", Repository: "o/r", Day: "2026-01-02", State: "excluded"},
		{ID: "y", Repository: "o/r", Day: "2026-01-02", State: "expired"},
	}
	f, err := w.newFrame("frame-1", records)
	if err != nil {
		t.Fatal(err)
	}
	if f.Population != 0 || len(f.Members) != 0 || f.Excluded != 1 || f.SourceGaps != 1 {
		t.Fatalf("frame = %+v, want empty membership with one exclusion and one gap", f)
	}
}

func TestNewFrameSeedChangesRanking(t *testing.T) {
	now := time.Now()
	a := newFrameTestWorker("sample", 1, now)
	b := newFrameTestWorker("sample", 1, now)
	b.spool.config.Seed = "other-seed"
	if a.rank("frame-1", "a") == b.rank("frame-1", "a") {
		t.Fatal("rank ignores the seed")
	}
}
