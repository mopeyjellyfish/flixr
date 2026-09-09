package schedule

import (
	"testing"
	"time"
)

func TestNextDailyHandlesDSTGapAndRepeatedHour(t *testing.T) {
	spring := Next(time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), "daily", 0, time.Time{}, "01:30", "Europe/London", time.Hour)
	wall := spring.In(mustLocation(t, "Europe/London"))
	if wall.Hour() != 2 || wall.Minute() != 0 {
		t.Fatalf("spring schedule=%v", spring)
	}
	fall := Next(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), "daily", 0, time.Time{}, "01:30", "Europe/London", time.Hour)
	if !fall.Equal(time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)) {
		t.Fatalf("fall schedule=%v", fall)
	}
}
func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
