package store

import "testing"

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
