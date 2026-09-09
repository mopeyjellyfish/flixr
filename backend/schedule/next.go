// Package schedule calculates durable interval and local-time schedules.
package schedule

import "time"

func Next(now time.Time, kind string, interval time.Duration, prior time.Time, localTime, timezone string, minimum time.Duration) time.Time {
	if kind == "interval" {
		if interval < minimum {
			interval = minimum
		}
		if prior.IsZero() {
			return now.Add(interval)
		}
		for !prior.After(now) {
			prior = prior.Add((now.Sub(prior)/interval + 1) * interval)
		}
		return prior
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	parsed, err := time.Parse("15:04", localTime)
	if err != nil {
		parsed = time.Date(0, 1, 1, 3, 0, 0, 0, time.UTC)
	}
	localNow := now.In(location)
	for offset := 0; offset < 3; offset++ {
		date := localNow.AddDate(0, 0, offset)
		var exact, fallback time.Time
		start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location).Add(-3 * time.Hour)
		for candidate := start; candidate.Before(start.Add(32 * time.Hour)); candidate = candidate.Add(time.Minute) {
			wall := candidate.In(location)
			if wall.Year() != date.Year() || wall.YearDay() != date.YearDay() {
				continue
			}
			minutes, target := wall.Hour()*60+wall.Minute(), parsed.Hour()*60+parsed.Minute()
			if minutes >= target && fallback.IsZero() {
				fallback = candidate
			}
			if minutes == target && exact.IsZero() {
				exact = candidate
			}
		}
		scheduled := exact
		if scheduled.IsZero() {
			scheduled = fallback
		}
		if !scheduled.IsZero() && scheduled.After(now) {
			return scheduled
		}
	}
	return now.Add(24 * time.Hour)
}
