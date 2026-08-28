package policy

import (
	"testing"
	"time"
)

func TestDailyWindowAcrossMidnight(t *testing.T) {
	location, err := time.LoadLocation("Australia/Brisbane")
	if err != nil {
		t.Fatal(err)
	}
	window, err := NewDailyWindow("21:00", "09:00", location)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		at   string
		want bool
	}{
		{"2026-08-28T20:59:59+10:00", false},
		{"2026-08-28T21:00:00+10:00", true},
		{"2026-08-29T03:00:00+10:00", true},
		{"2026-08-29T08:59:59+10:00", true},
		{"2026-08-29T09:00:00+10:00", false},
	}

	for _, test := range tests {
		at, err := time.Parse(time.RFC3339, test.at)
		if err != nil {
			t.Fatal(err)
		}
		if got := window.Contains(at); got != test.want {
			t.Errorf("Contains(%s) = %v, want %v", test.at, got, test.want)
		}
	}
}

func TestDailyWindowOrdinary(t *testing.T) {
	window, err := NewDailyWindow("09:00", "21:00", time.UTC)
	if err != nil {
		t.Fatal(err)
	}

	if !window.Contains(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("noon should be inside")
	}
	if window.Contains(time.Date(2026, 8, 28, 22, 0, 0, 0, time.UTC)) {
		t.Fatal("22:00 should be outside")
	}
}

func TestNextTransition(t *testing.T) {
	window, err := NewDailyWindow("21:00", "09:00", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 28, 20, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 28, 21, 0, 0, 0, time.UTC)
	if got := window.NextTransition(at); !got.Equal(want) {
		t.Fatalf("NextTransition() = %s, want %s", got, want)
	}
}
