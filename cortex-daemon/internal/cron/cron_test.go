// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package cron

import (
	"testing"
	"time"
)

// Fixed reference: Wednesday 2026-10-07, 10:30:15 local.
func ref() time.Time {
	return time.Date(2026, 10, 7, 10, 30, 15, 0, time.Local)
}

func mustNext(t *testing.T, expr string, after time.Time) time.Time {
	t.Helper()
	schedule, err := Parse(expr)
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	next, err := schedule.Next(after)
	if err != nil {
		t.Fatalf("next for %q: %v", expr, err)
	}
	return next
}

func assertAt(t *testing.T, got time.Time, y int, m time.Month, d, h, min int) {
	t.Helper()
	// Schedules run on the daemon's local clock; expectations are built in
	// the same location so the test is machine-zone independent.
	wantAt := time.Date(y, m, d, h, min, 0, 0, time.Local)
	if !got.Equal(wantAt) {
		t.Fatalf("next = %s, want %s", got.Format(time.RFC3339), wantAt.Format(time.RFC3339))
	}
}

func TestParseAndNextBasics(t *testing.T) {
	// Fire times are strictly AFTER the reference: 10:30 already passed at
	// 10:30:15, so the next match is tomorrow.
	assertAt(t, mustNext(t, "30 10 * * *", ref()), 2026, time.October, 8, 10, 30)
	// Same minute, later second: strictly-after still rolls forward.
	assertAt(t, mustNext(t, "30 10 * * *", time.Date(2026, 10, 7, 10, 30, 0, 0, time.Local)), 2026, time.October, 8, 10, 30)

	// Every minute: the next whole minute.
	assertAt(t, mustNext(t, "* * * * *", ref()), 2026, time.October, 7, 10, 31)
	// Steps, ranges, and lists in the minute field.
	assertAt(t, mustNext(t, "*/15 * * * *", ref()), 2026, time.October, 7, 10, 45)
	assertAt(t, mustNext(t, "0,20,40 * * * *", ref()), 2026, time.October, 7, 10, 40)
	assertAt(t, mustNext(t, "45-59 * * * *", ref()), 2026, time.October, 7, 10, 45)
	// Hour field jumps whole hours.
	assertAt(t, mustNext(t, "0 9-17 * * *", ref()), 2026, time.October, 7, 11, 0)
	// Month names and day names.
	// Day 1 of October, after Oct 7: next year's Oct 1.
	assertAt(t, mustNext(t, "0 0 1 OCT *", ref()), 2027, time.October, 1, 0, 0)
	assertAt(t, mustNext(t, "0 12 * * MON", ref()), 2026, time.October, 12, 12, 0)
}

func TestDayOfWeekAndMonthBoundaries(t *testing.T) {
	// 0 and 7 are both Sunday.
	assertAt(t, mustNext(t, "0 0 * * 7", ref()), 2026, time.October, 11, 0, 0)
	assertAt(t, mustNext(t, "0 0 * * 0", ref()), 2026, time.October, 11, 0, 0)

	// Month rollover: Oct 7 -> Nov 1 for a day-of-month that Oct lacks.
	assertAt(t, mustNext(t, "0 0 31 * *", ref()), 2026, time.October, 31, 0, 0)
	assertAt(t, mustNext(t, "0 0 1 11 *", ref()), 2026, time.November, 1, 0, 0)

	// Leap day: the only fire day of the schedule. 2027-2031 contains
	// 2028-02-29 (and 2032 after that); the bounded search must find the
	// FIRST one after the reference.
	assertAt(t, mustNext(t, "0 0 29 2 *", time.Date(2026, 10, 7, 0, 0, 0, 0, time.Local)), 2028, time.February, 29, 0, 0)

	// Year rollover.
	assertAt(t, mustNext(t, "0 0 1 1 *", time.Date(2026, 12, 15, 0, 0, 0, 0, time.Local)), 2027, time.January, 1, 0, 0)
}

func TestDomDowOrSemantics(t *testing.T) {
	// Both restricted: EITHER matching day fires (vixie OR rule).
	// Reference is Wednesday Oct 7. DOM 13 (a Tuesday), DOW Friday(5):
	// the next fire is Friday Oct 9.
	assertAt(t, mustNext(t, "0 0 13 * 5", ref()), 2026, time.October, 9, 0, 0)

	// One restricted, one star: AND reads through (the star field matches
	// every day, so the restricted one decides).
	// */2 in day-of-month means odd days (1,3,...): next after Oct 7 is Oct 9.
	assertAt(t, mustNext(t, "0 0 */2 * *", ref()), 2026, time.October, 9, 0, 0)
}

func TestInvalidExpressionsAreRejected(t *testing.T) {
	for _, expr := range []string{
		"",            // empty
		"* * * *",     // too few fields
		"* * * * * *", // too many fields
		"60 * * * *",  // minute out of range
		"* 24 * * *",  // hour out of range
		"* * 0 * *",   // day-of-month out of range
		"* * * 13 *",  // month out of range
		"* * * * XYZ", // unknown day name
		"5-1 * * * *", // descending range
		"*/0 * * * *", // zero step
		"a * * * *",   // non-numeric, no name
	} {
		if _, err := Parse(expr); err == nil {
			t.Fatalf("%q must be rejected", expr)
		}
	}
}

func TestImpossibleSchedulesErrorInsteadOfHanging(t *testing.T) {
	// Feb 31 exists in no year: the bounded search must give up.
	schedule, err := Parse("0 0 31 2 *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := schedule.Next(ref()); err == nil {
		t.Fatal("an impossible schedule must error, not hang")
	}
}

func TestNextPreservesLocation(t *testing.T) {
	// The fire time stays in the reference's location (the daemon's local
	// clock defines the schedule).
	zoned := time.Date(2026, 10, 7, 10, 30, 15, 0, time.FixedZone("CET", 3600))
	next := mustNext(t, "0 12 * * *", zoned)
	if next.Location() != zoned.Location() {
		t.Fatalf("location changed: %v", next.Location())
	}
}
