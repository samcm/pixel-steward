package policy

import (
	"fmt"
	"time"
)

// DailyWindow is a timezone-aware half-open interval repeated every local day.
// It supports both ordinary windows (09:00-21:00) and windows spanning midnight
// (21:00-09:00).
type DailyWindow struct {
	startMinutes int
	endMinutes   int
	location     *time.Location
}

func NewDailyWindow(start, end string, location *time.Location) (DailyWindow, error) {
	if location == nil {
		return DailyWindow{}, fmt.Errorf("location is required")
	}

	startMinutes, err := clockMinutes(start)
	if err != nil {
		return DailyWindow{}, fmt.Errorf("start: %w", err)
	}
	endMinutes, err := clockMinutes(end)
	if err != nil {
		return DailyWindow{}, fmt.Errorf("end: %w", err)
	}
	if startMinutes == endMinutes {
		return DailyWindow{}, fmt.Errorf("start and end must differ")
	}

	return DailyWindow{startMinutes: startMinutes, endMinutes: endMinutes, location: location}, nil
}

func (w DailyWindow) Contains(at time.Time) bool {
	local := at.In(w.location)
	minute := local.Hour()*60 + local.Minute()
	if w.startMinutes < w.endMinutes {
		return minute >= w.startMinutes && minute < w.endMinutes
	}

	return minute >= w.startMinutes || minute < w.endMinutes
}

func (w DailyWindow) NextTransition(at time.Time) time.Time {
	local := at.In(w.location)
	year, month, day := local.Date()
	start := time.Date(year, month, day, w.startMinutes/60, w.startMinutes%60, 0, 0, w.location)
	end := time.Date(year, month, day, w.endMinutes/60, w.endMinutes%60, 0, 0, w.location)

	candidates := []time.Time{start, end, start.AddDate(0, 0, 1), end.AddDate(0, 0, 1)}
	var next time.Time
	for _, candidate := range candidates {
		if candidate.After(local) && (next.IsZero() || candidate.Before(next)) {
			next = candidate
		}
	}

	return next
}

func clockMinutes(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, fmt.Errorf("invalid HH:MM value %q: %w", value, err)
	}

	return parsed.Hour()*60 + parsed.Minute(), nil
}
