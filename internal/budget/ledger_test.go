package budget

import (
	"errors"
	"testing"
	"time"
)

func TestReservationPreventsConcurrentOverspend(t *testing.T) {
	limit := int64(2_000_000)
	ledger, err := New(Limits{
		InputTokens:   100,
		OutputTokens:  20,
		ModelCalls:    2,
		ActiveRuntime: time.Minute,
		SceneCommits:  2,
		CostMicros:    &limit,
		PerCallOutput: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1000, 0)
	if _, err := ledger.Reserve(now, Estimate{InputTokens: 60, MaxOutputTokens: 15, MeteredCostMicros: 1_500_000}); err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}
	if _, err := ledger.Reserve(now, Estimate{InputTokens: 50, MaxOutputTokens: 10, MeteredCostMicros: 600_000}); !errors.Is(err, ErrExhausted) {
		t.Fatalf("second Reserve() error = %v, want ErrExhausted", err)
	}
}

func TestCompleteReconcilesReservation(t *testing.T) {
	ledger, err := New(Limits{
		InputTokens:   100,
		OutputTokens:  50,
		ModelCalls:    2,
		ActiveRuntime: time.Minute,
		SceneCommits:  2,
		PerCallOutput: 40,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1000, 0)
	reservation, err := ledger.Reserve(now, Estimate{InputTokens: 80, MaxOutputTokens: 40})
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Complete(reservation.ID, Actual{
		Tokens:        TokenUsage{Input: 30, Output: 5, Reasoning: 2, CacheRead: 20},
		ActiveRuntime: 2 * time.Second,
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	snapshot := ledger.Snapshot(now.Add(2 * time.Second))
	if snapshot.InputTokens.Used != 30 || snapshot.InputTokens.Reserved != 0 || snapshot.InputTokens.Remaining != 70 {
		t.Fatalf("input snapshot = %+v", snapshot.InputTokens)
	}
	if snapshot.Observed.CacheRead != 20 || snapshot.Observed.Reasoning != 2 {
		t.Fatalf("observed = %+v", snapshot.Observed)
	}
}

func TestCompleteRecordsAggregateModelCalls(t *testing.T) {
	ledger, err := New(Limits{InputTokens: 100, OutputTokens: 100, ModelCalls: 5,
		ActiveRuntime: time.Minute, SceneCommits: 1, PerCallOutput: 100})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	reservation, err := ledger.Reserve(now, Estimate{InputTokens: 1, MaxOutputTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Complete(reservation.ID, Actual{ModelCalls: 3}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if calls := ledger.Snapshot(now.Add(time.Second)).Calls; calls.Used != 3 || calls.Remaining != 2 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnknownCostIsNotZero(t *testing.T) {
	ledger, err := New(Limits{InputTokens: 1, OutputTokens: 1, ModelCalls: 1, ActiveRuntime: time.Second, SceneCommits: 1, PerCallOutput: 1})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := ledger.Snapshot(time.Now())
	if snapshot.Cost.LimitMicros != nil || snapshot.Cost.RemainingMicros != nil {
		t.Fatalf("unknown cost limit must remain nil: %+v", snapshot.Cost)
	}
}

func TestSceneCommitLimit(t *testing.T) {
	ledger, err := New(Limits{InputTokens: 1, OutputTokens: 1, ModelCalls: 1, ActiveRuntime: time.Second, SceneCommits: 1, PerCallOutput: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.CommitScene(); err != nil {
		t.Fatal(err)
	}
	if err := ledger.CommitScene(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("CommitScene() error = %v, want ErrExhausted", err)
	}
}
