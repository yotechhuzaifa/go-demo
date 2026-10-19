// Package dates centralizes the "messy" parts of date handling.
//
// Three rules keep date handling predictable:
//
//  1. API input may be flexible ("2026-12-31", "31/12/2026", RFC3339, or a
//     relative phrase understood by araddon/dateparse).
//  2. Everything stored and compared is UTC.
//  3. Human-facing descriptions are computed at the edge (DTO mapping), never
//     stored, so they cannot become stale in the database.
//
// Flexible parsing uses github.com/araddon/dateparse. Human descriptions use
// github.com/dustin/go-humanize. Calendar math (start of day, business days,
// ISO week) is implemented with the standard library, which is the idiomatic
// Go choice for date arithmetic.
package dates

import (
	"fmt"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/dustin/go-humanize"
)

// ParseFlexible converts common date/time spellings into a UTC instant.
//
// Accepted examples:
//
//	2026-12-31
//	2026-12-31T18:30:00Z
//	31/12/2026
//	Dec 31, 2026
//
// An empty string is treated as "no date" and returns (nil, nil). Callers can
// therefore map an optional JSON field directly.
func ParseFlexible(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	if parsed, ok := parseWithLayouts(trimmed, time.UTC); ok {
		utc := parsed.UTC()
		return &utc, nil
	}

	parsed, err := dateparse.ParseAny(trimmed)
	if err != nil {
		return nil, fmt.Errorf("must be a date such as 2026-12-31 or 2026-12-31T18:30:00Z: %w", err)
	}

	utc := parsed.UTC()
	return &utc, nil
}

// ParseInLocation parses a flexible date and interprets date-only values in the
// caller's timezone. This matters for deadlines: "2026-12-31" means the end of
// that day in the user's timezone, not in UTC.
func ParseInLocation(raw, timezone string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	location := time.UTC
	if strings.TrimSpace(timezone) != "" {
		loaded, err := time.LoadLocation(timezone)
		if err != nil {
			return nil, fmt.Errorf("timezone: unknown IANA timezone %q", timezone)
		}
		location = loaded
	}

	if parsed, ok := parseWithLayouts(trimmed, location); ok {
		utc := parsed.UTC()
		return &utc, nil
	}

	parsed, err := dateparse.ParseIn(trimmed, location)
	if err != nil {
		return nil, fmt.Errorf("must be a date such as 2026-12-31: %w", err)
	}

	utc := parsed.UTC()
	return &utc, nil
}

// commonLayouts are tried before dateparse.
//
// dateparse is excellent at many formats but treats slash dates as
// month/day/year, which rejects values such as "31/12/2026". Trying an explicit
// day-first layout first makes the documented regional format work
// consistently. Ambiguous values such as 01/02/2026 therefore mean 1 February;
// clients that need month-first should send ISO 8601 (2026-02-01).
var commonLayouts = []string{
	"2006-01-02",
	"2006-01-02 15:04",
	"2006-01-02 15:04:05",
	time.RFC3339,
	time.RFC3339Nano,
	"02/01/2006",
	"2/1/2006",
	"02-01-2006",
	"02.01.2006",
	"Jan 2, 2006",
	"January 2, 2006",
	"2 Jan 2006",
}

// parseWithLayouts tries every explicit layout in order. The bool reports
// whether one matched.
func parseWithLayouts(raw string, location *time.Location) (time.Time, bool) {
	for _, layout := range commonLayouts {
		if parsed, err := time.ParseInLocation(layout, raw, location); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// StartOfDay returns midnight in the timestamp's own location.
func StartOfDay(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

// EndOfDay returns the last nanosecond of the timestamp's day.
func EndOfDay(value time.Time) time.Time {
	return StartOfDay(value).AddDate(0, 0, 1).Add(-time.Nanosecond)
}

// DaysUntil returns the number of calendar days from now to due, ignoring the
// time of day. A deadline later today returns 0; yesterday returns -1.
func DaysUntil(due, now time.Time) int {
	dueDay := StartOfDay(due.UTC())
	nowDay := StartOfDay(now.UTC())
	return int(dueDay.Sub(nowDay).Hours() / 24)
}

// AddBusinessDays skips Saturdays and Sundays. It is used when seeding demo
// data so sample deadlines look like real work deadlines.
func AddBusinessDays(value time.Time, days int) time.Time {
	result := value
	for days > 0 {
		result = result.AddDate(0, 0, 1)
		if result.Weekday() != time.Saturday && result.Weekday() != time.Sunday {
			days--
		}
	}
	return result
}

// Describe produces the human-readable fields returned by the HTTP DTOs.
//
// Human contains a relative phrase such as "in 3 days" or "2 weeks ago".
// IsOverdue compares instants, not calendar days, so a deadline at 09:00 is
// overdue at 09:01 even though DaysUntil is 0.
type Description struct {
	Human     string
	IsOverdue bool
	DaysUntil int
}

// DescribeDue builds a Description for an optional deadline.
func DescribeDue(due *time.Time, now time.Time) Description {
	if due == nil {
		return Description{}
	}
	return Description{
		Human:     humanize.Time(*due),
		IsOverdue: due.Before(now),
		DaysUntil: DaysUntil(*due, now),
	}
}

// WeekLabel returns an ISO-8601 week label such as "2026-W53". It is useful in
// grouping endpoints and demonstrates the standard library's ISOWeek helper.
func WeekLabel(value time.Time) string {
	year, week := value.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}
