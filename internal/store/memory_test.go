package store

import (
	"context"
	"encoding/json"
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

func TestMemoryListsJournalEntriesNewestFirst(t *testing.T) {
	database := NewMemory()
	ctx := context.Background()
	for index, value := range []string{"first", "second"} {
		payload, _ := json.Marshal(map[string]string{"entry": value})
		if _, err := database.AppendEvent(ctx, domain.Event{At: time.Unix(int64(index), 0), LeaseID: "lease",
			PersonaID: "persona", Actor: "agent", Type: "journal.entry", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := database.ListJournalEntries(ctx, "persona", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Entry != "second" || entries[1].Entry != "first" {
		t.Fatalf("journal entries = %+v", entries)
	}
}
