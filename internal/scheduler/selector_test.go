package scheduler

import (
	"errors"
	"testing"
	"time"

	"github.com/samcm/pixel-steward/internal/domain"
)

func TestSelectIsDeterministicAndAvoidsImmediateRepeat(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	personas := []domain.Persona{
		{ID: "a", Enabled: true, Weight: 100},
		{ID: "b", Enabled: true, Weight: 1},
	}
	leases := []domain.Lease{{PersonaID: "a", EndsAt: now.Add(-time.Hour), Status: "complete"}}

	decision, err := (Selector{AvoidImmediateRepeat: true}).Select(now, 42, personas, leases)
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedID != "b" {
		t.Fatalf("selected %q, want b", decision.SelectedID)
	}
	if decision.Seed != 42 {
		t.Fatalf("seed = %d", decision.Seed)
	}
}

func TestSelectHonorsCooldown(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	ended := now.Add(-time.Hour)
	personas := []domain.Persona{{ID: "a", Enabled: true, Weight: 1, Cooldown: 2 * time.Hour}}
	leases := []domain.Lease{{PersonaID: "a", EndedAt: &ended, Status: "complete"}}

	_, err := (Selector{}).Select(now, 1, personas, leases)
	if !errors.Is(err, ErrNoEligiblePersona) {
		t.Fatalf("error = %v", err)
	}
}
