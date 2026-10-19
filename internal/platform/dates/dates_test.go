package dates

import (
	"testing"
	"time"
)

func TestParseFlexibleAcceptsCommonFormats(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Time
	}{
		{
			name: "ISO date",
			raw:  "2026-12-31",
			want: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "RFC3339",
			raw:  "2026-12-31T18:30:00Z",
			want: time.Date(2026, 12, 31, 18, 30, 0, 0, time.UTC),
		},
		{
			name: "regional date",
			raw:  "31/12/2026",
			want: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseFlexible(test.raw)
			if err != nil {
				t.Fatalf("ParseFlexible(%q) error = %v", test.raw, err)
			}
			if got == nil || !got.Equal(test.want) {
				t.Fatalf("ParseFlexible(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestParseFlexibleEmptyMeansNoDate(t *testing.T) {
	got, err := ParseFlexible("   ")
	if err != nil {
		t.Fatalf("ParseFlexible returned an error for empty input: %v", err)
	}
	if got != nil {
		t.Fatalf("ParseFlexible returned %v, want nil", got)
	}
}

func TestParseInLocationUsesRequestTimezone(t *testing.T) {
	got, err := ParseInLocation("2026-12-31", "Asia/Karachi")
	if err != nil {
		t.Fatalf("ParseInLocation error = %v", err)
	}

	// Karachi is UTC+5, so midnight local is 19:00 UTC on the previous day.
	want := time.Date(2026, 12, 30, 19, 0, 0, 0, time.UTC)
	if got == nil || !got.Equal(want) {
		t.Fatalf("ParseInLocation = %v, want %v", got, want)
	}
}

func TestDaysUntilIgnoresTimeOfDay(t *testing.T) {
	now := time.Date(2026, 10, 19, 23, 30, 0, 0, time.UTC)
	due := time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)

	if got := DaysUntil(due, now); got != 2 {
		t.Fatalf("DaysUntil = %d, want 2", got)
	}
}

func TestAddBusinessDaysSkipsWeekend(t *testing.T) {
	friday := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) // Friday.
	got := AddBusinessDays(friday, 1)
	want := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) // Monday.

	if !got.Equal(want) {
		t.Fatalf("AddBusinessDays = %v, want %v", got, want)
	}
}

func TestWeekLabel(t *testing.T) {
	// 2026-10-19 is in ISO week 43.
	value := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if got := WeekLabel(value); got != "2026-W43" {
		t.Fatalf("WeekLabel = %q, want %q", got, "2026-W43")
	}
}

func TestStartOfDayAndEndOfDay(t *testing.T) {
	value := time.Date(2026, 10, 19, 15, 45, 0, 0, time.UTC)

	start := StartOfDay(value)
	if !start.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("StartOfDay = %v", start)
	}

	end := EndOfDay(value)
	if end.Hour() != 23 || end.Minute() != 59 || end.Second() != 59 {
		t.Fatalf("EndOfDay = %v, want last moment of the day", end)
	}
}
