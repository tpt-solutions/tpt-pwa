// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package cron parses standard 5-field cron expressions and computes fire
// times, so task scripts can run on calendar schedules ("0 9 * * 1-5") with
// no external dependency. Semantics follow vixie cron where it matters:
//
//	fields:  minute hour day-of-month month day-of-week
//	syntax:  * | a | a-b | a/b | */b | lists of those; month/day names
//	         (JAN-DEC, SUN-SAT); day-of-week accepts 0-7 (0 and 7 = Sunday)
//	DOM/DOW: when BOTH are restricted (not *), a day matches if EITHER
//	         field matches (the classic OR semantics); otherwise BOTH
//	         fields must match
//	time:    the daemon's local clock; fire times are strictly AFTER the
//	         reference time, at whole minutes
//
// `Next` searches minute-by-minute with month/day/hour jumps, bounded at
// roughly 5.3 years -- enough to cross a full leap cycle, so an impossible
// expression (Feb 31) is an error rather than a hang.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is one parsed expression.
type Schedule struct {
	minute        map[int]bool // 0-59
	hour          map[int]bool // 0-23
	dom           map[int]bool // 1-31
	month         map[int]bool // 1-12
	dow           map[int]bool // 0-6 (0 = Sunday)
	domRestricted bool
	dowRestricted bool
}

// maxSearchMinutes bounds the Next scan: 5.3 years covers the 4-year leap
// cycle, so Feb-29-only schedules resolve and truly impossible ones error.
const maxSearchMinutes = 2_800_000

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// Parse compiles a cron expression. Errors name the offending field.
func Parse(expr string) (*Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: want 5 fields (minute hour day-of-month month day-of-week), got %d in %q", len(fields), expr)
	}

	minute, _, err := parseField(fields[0], 0, 59, nil)
	if err != nil {
		return nil, fmt.Errorf("cron: minute field: %w", err)
	}
	hour, _, err := parseField(fields[1], 0, 23, nil)
	if err != nil {
		return nil, fmt.Errorf("cron: hour field: %w", err)
	}
	dom, domStar, err := parseField(fields[2], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("cron: day-of-month field: %w", err)
	}
	month, _, err := parseField(fields[3], 1, 12, monthNames)
	if err != nil {
		return nil, fmt.Errorf("cron: month field: %w", err)
	}
	dow, dowStar, err := parseField(fields[4], 0, 7, dayNames)
	if err != nil {
		return nil, fmt.Errorf("cron: day-of-week field: %w", err)
	}
	// 7 is an accepted alias for Sunday; normalize onto 0.
	if dow[7] {
		dow[0] = true
		delete(dow, 7)
	}

	return &Schedule{
		minute:        minute,
		hour:          hour,
		dom:           dom,
		month:         month,
		dow:           dow,
		domRestricted: !domStar,
		dowRestricted: !dowStar,
	}, nil
}

// parseField parses one field into its allowed values. star reports whether
// the whole field was exactly "*" (unrestricted).
func parseField(field string, min, max int, names map[string]int) (map[int]bool, bool, error) {
	values := make(map[int]bool)
	star := true
	for _, part := range strings.Split(field, ",") {
		base, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return nil, false, fmt.Errorf("step %q must be a positive integer", stepText)
			}
			step = n
		}
		lo, hi := min, max
		switch {
		case base == "*":
			// full range
		case strings.Contains(base, "-"):
			bounds := strings.SplitN(base, "-", 2)
			var err error
			if lo, err = lookup(bounds[0], names, min, max); err != nil {
				return nil, false, err
			}
			if hi, err = lookup(bounds[1], names, min, max); err != nil {
				return nil, false, err
			}
			if lo > hi {
				return nil, false, fmt.Errorf("descending range %q", base)
			}
		default:
			single, err := lookup(base, names, min, max)
			if err != nil {
				return nil, false, err
			}
			lo = single
			hi = single
			if hasStep && step > 1 {
				// "5/10" means starting at 5, every 10 within the range
				hi = max
			}
		}
		for v := lo; v <= hi; v += step {
			values[v] = true
		}
		if part != "*" {
			star = false
		}
	}
	if len(values) == 0 {
		return nil, false, fmt.Errorf("field %q matches nothing", field)
	}
	return values, star, nil
}

func lookup(token string, names map[string]int, min, max int) (int, error) {
	if names != nil {
		if value, ok := names[strings.ToLower(token)]; ok {
			if value < min || value > max {
				return 0, fmt.Errorf("value %q out of range", token)
			}
			return value, nil
		}
	}
	value, err := strconv.Atoi(token)
	if err != nil {
		return 0, fmt.Errorf("value %q is not a number or known name", token)
	}
	if value < min || value > max {
		return 0, fmt.Errorf("value %q out of range (%d-%d)", token, min, max)
	}
	return value, nil
}

// Next returns the first minute strictly after `after` that matches the
// schedule, in `after`'s location. An expression that never matches (e.g.
// Feb 31) errors once the bounded search exhausts.
func (s *Schedule) Next(after time.Time) (time.Time, error) {
	loc := after.Location()
	// Start at the next whole minute, seconds dropped.
	candidate := time.Date(after.Year(), after.Month(), after.Day(), after.Hour(), after.Minute(), 0, 0, loc).Add(time.Minute)

	for i := 0; i < maxSearchMinutes; i++ {
		if !s.month[int(candidate.Month())] {
			candidate = firstOfNextMonth(candidate)
			continue
		}
		if !s.dayMatches(candidate) {
			candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			continue
		}
		if !s.hour[candidate.Hour()] {
			candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), candidate.Hour(), 0, 0, 0, loc).Add(time.Hour)
			continue
		}
		if !s.minute[candidate.Minute()] {
			candidate = candidate.Add(time.Minute)
			continue
		}
		return candidate, nil
	}
	return time.Time{}, fmt.Errorf("cron: expression never matches within the bounded search (~5.3 years)")
}

// dayMatches applies the DOM/DOW rule: both restricted -> OR; otherwise the
// unrestricted field's map is full, so plain AND reads through.
func (s *Schedule) dayMatches(candidate time.Time) bool {
	domOK := s.dom[candidate.Day()]
	dowOK := s.dow[int(candidate.Weekday())]
	if s.domRestricted && s.dowRestricted {
		return domOK || dowOK
	}
	return domOK && dowOK
}

func firstOfNextMonth(candidate time.Time) time.Time {
	year, month, _ := candidate.Date()
	if month == time.December {
		return time.Date(year+1, time.January, 1, 0, 0, 0, 0, candidate.Location())
	}
	return time.Date(year, month+1, 1, 0, 0, 0, 0, candidate.Location())
}
