package store

import (
	"strings"
	"testing"
)

func TestValidateReadOnlySQL(t *testing.T) {
	allowed := []string{"SELECT * FROM history_events", "WITH recent AS (SELECT 1) SELECT * FROM recent;"}
	for _, query := range allowed {
		if err := validateReadOnlySQL(query); err != nil {
			t.Errorf("%q: %v", query, err)
		}
	}
	denied := []string{"DELETE FROM events", "SELECT 1; DROP TABLE events", "UPDATE personas SET enabled=true"}
	for _, query := range denied {
		if err := validateReadOnlySQL(query); err == nil {
			t.Errorf("%q was accepted", query)
		}
	}
}

// TestMarkScheduleRunSQLCastsNextRun guards the exact revert that broke the live
// controller: dropping the ::timestamptz casts makes PostgreSQL fail every
// scheduler tick with "could not determine data type of parameter $4", because
// $4 otherwise appears only as a bare assignment target and inside IS NOT NULL.
// This asserts the statement text only -- it does not exercise a database, so it
// proves the cast survives refactoring, not that the UPDATE itself is correct.
func TestMarkScheduleRunSQLCastsNextRun(t *testing.T) {
	normalized := strings.Join(strings.Fields(markScheduleRunSQL), " ")
	for _, want := range []string{"next_run_at=$4::timestamptz", "enabled=($4::timestamptz IS NOT NULL)"} {
		if !strings.Contains(normalized, want) {
			t.Errorf("markScheduleRunSQL is missing %q: %s", want, normalized)
		}
	}
	if strings.Contains(normalized, "$4 IS NOT NULL") {
		t.Errorf("markScheduleRunSQL still has an untyped $4 predicate: %s", normalized)
	}
	if strings.Contains(normalized, "next_run_at=$4,") || strings.HasSuffix(normalized, "next_run_at=$4") {
		t.Errorf("markScheduleRunSQL still assigns an untyped $4: %s", normalized)
	}
}
