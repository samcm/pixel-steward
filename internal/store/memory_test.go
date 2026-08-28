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

// appendEvents writes one event per named type, in order, so a test can reason
// about ids as a contiguous ascending run.
func appendEvents(t *testing.T, database *Memory, leaseID string, types ...string) {
	t.Helper()
	ctx := context.Background()
	for index, value := range types {
		if _, err := database.AppendEvent(ctx, domain.Event{At: time.Unix(int64(index), 0), LeaseID: leaseID,
			PersonaID: "persona", Actor: "controller", Type: value, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
}

func eventIDs(events []domain.Event) []int64 {
	ids := make([]int64, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}

func TestMemoryEventQueryWalksForwardFromAfterID(t *testing.T) {
	database := NewMemory()
	appendEvents(t, database, "lease", "a", "b", "c", "d", "e", "f")
	events, err := database.ListEventsQuery(context.Background(), domain.EventQuery{AfterID: 2, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(events); len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("after_id ids = %v, want the oldest three above 2 ascending", got)
	}
}

func TestMemoryEventQueryPagesBackwardFromBeforeID(t *testing.T) {
	database := NewMemory()
	appendEvents(t, database, "lease", "a", "b", "c", "d", "e", "f")
	events, err := database.ListEventsQuery(context.Background(), domain.EventQuery{BeforeID: 5, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(events); len(got) != 3 || got[0] != 4 || got[1] != 3 || got[2] != 2 {
		t.Fatalf("before_id ids = %v, want the newest three below 5 descending", got)
	}
	older, err := database.ListEventsQuery(context.Background(), domain.EventQuery{BeforeID: 2, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(older); len(got) != 1 || got[0] != 1 {
		t.Fatalf("second page ids = %v, want the single remaining oldest row", got)
	}
}

func TestMemoryEventQueryAppliesBothCursorsAscending(t *testing.T) {
	database := NewMemory()
	appendEvents(t, database, "lease", "a", "b", "c", "d", "e", "f")
	events, err := database.ListEventsQuery(context.Background(), domain.EventQuery{AfterID: 2, BeforeID: 5, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(events); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("bounded ids = %v, want 3 then 4 ascending between the cursors", got)
	}
}

func TestMemoryEventQueryWithoutCursorReturnsNewestFirst(t *testing.T) {
	database := NewMemory()
	appendEvents(t, database, "lease", "a", "b", "c")
	events, err := database.ListEventsQuery(context.Background(), domain.EventQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(events); len(got) != 2 || got[0] != 3 || got[1] != 2 {
		t.Fatalf("uncursored ids = %v, want newest first", got)
	}
}

func TestMemoryEventQueryIsolatesOneLease(t *testing.T) {
	database := NewMemory()
	appendEvents(t, database, "lease-a", "runtime.text", "runtime.text")
	appendEvents(t, database, "lease-b", "runtime.text", "runtime.text")
	events, err := database.ListEventsQuery(context.Background(), domain.EventQuery{LeaseID: "lease-b", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("lease-b events = %d, want 2", len(events))
	}
	for _, event := range events {
		if event.LeaseID != "lease-b" {
			t.Fatalf("lease filter leaked %+v", event)
		}
	}
}

// A renderer loop emits frame.submitted roughly once a second. Without a type
// filter the two rows the operator actually wants fall out of any bounded
// window, which is the regression this query exists to prevent.
func TestMemoryEventQueryKeepsTranscriptUnderFrameNoise(t *testing.T) {
	database := NewMemory()
	noise := make([]string, 150)
	for index := range noise {
		noise[index] = "frame.submitted"
	}
	appendEvents(t, database, "lease", noise...)
	appendEvents(t, database, "lease", "runtime.text", "runtime.text")
	appendEvents(t, database, "lease", noise...)

	unfiltered, err := database.ListEventsQuery(context.Background(), domain.EventQuery{LeaseID: "lease", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range unfiltered {
		if event.Type == "runtime.text" {
			t.Fatal("fixture is wrong: runtime.text should be buried past the unfiltered window")
		}
	}

	events, err := database.ListEventsQuery(context.Background(), domain.EventQuery{LeaseID: "lease",
		Types: domain.TranscriptEventTypes, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("transcript events = %d, want exactly the 2 runtime.text rows", len(events))
	}
	for _, event := range events {
		if event.Type != "runtime.text" {
			t.Fatalf("transcript filter kept %q", event.Type)
		}
	}
	if got := eventIDs(events); got[0] != 152 || got[1] != 151 {
		t.Fatalf("transcript ids = %v, want 152 then 151 newest first", got)
	}
}

func TestMemoryEventQueryNormalisesLimit(t *testing.T) {
	database := NewMemory()
	noise := make([]string, 120)
	for index := range noise {
		noise[index] = "runtime.text"
	}
	appendEvents(t, database, "lease", noise...)
	for _, limit := range []int{0, 5000} {
		events, err := database.ListEventsQuery(context.Background(), domain.EventQuery{Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 100 {
			t.Fatalf("limit %d returned %d events, want the 100 default", limit, len(events))
		}
	}
}

func TestMemoryEventQueryAlwaysReturnsNonNil(t *testing.T) {
	events, err := NewMemory().ListEventsQuery(context.Background(), domain.EventQuery{LeaseID: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if events == nil {
		t.Fatal("empty result must be a non-nil slice so it encodes as []")
	}
}
