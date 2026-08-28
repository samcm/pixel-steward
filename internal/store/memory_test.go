package store

import (
	"context"
	"testing"
	"time"

	"github.com/samcm/pixel-steward/internal/domain"
)

func TestMemoryAllowsOnlyOneActiveLease(t *testing.T) {
	store := NewMemory()
	ctx := context.Background()
	lease := domain.Lease{ID: "one", Status: "active", StartedAt: time.Now()}
	if err := store.CreateLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateLease(ctx, domain.Lease{ID: "two", Status: "active"}); err == nil {
		t.Fatal("second active lease should fail")
	}
}
