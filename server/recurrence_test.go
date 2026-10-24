// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRecurringMeeting(rule string) *Meeting {
	m := testMeeting()
	m.RecurrenceRule = rule
	return m
}

// TestParseRecurrenceRuleRefusesTooFrequentOrInvalid checks that an RRULE more frequent than
// daily, or one that doesn't parse, is refused with a 400, and that daily/weekly/monthly/yearly
// are accepted.
func TestParseRecurrenceRuleRefusesTooFrequentOrInvalid(t *testing.T) {
	now := time.Now()
	for _, rule := range []string{"FREQ=DAILY", "FREQ=WEEKLY", "FREQ=MONTHLY", "FREQ=YEARLY", "FREQ=WEEKLY;BYDAY=MO,WE", ""} {
		t.Run(rule, func(t *testing.T) {
			_, err := parseRecurrenceRule(rule, now)
			assert.NoError(t, err)
		})
	}

	for name, rule := range map[string]string{
		"hourly":            "FREQ=HOURLY",
		"minutely":          "FREQ=MINUTELY",
		"secondly":          "FREQ=SECONDLY",
		"garbage":           "not an rrule",
		"count too high":    "FREQ=DAILY;COUNT=100000",
		"until too far out": "FREQ=DAILY;UNTIL=21000101T000000Z",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseRecurrenceRule(rule, now)
			assert.Error(t, err)
			assert.Equal(t, 400, statusFor(err))
		})
	}
}

// TestMeetingOccurrencesAcrossDST checks that a weekly call anchored before a DST change keeps
// its local time of day after it, when expanded in its own time zone.
func TestMeetingOccurrencesAcrossDST(t *testing.T) {
	paris := mustLoad(t, "Europe/Paris")
	// 2026-03-29 is the last Sunday of March: Europe/Paris springs forward that night.
	anchor := time.Date(2026, 3, 22, 9, 0, 0, 0, paris)
	m := testRecurringMeeting("FREQ=WEEKLY")
	m.TimeZone = "Europe/Paris"
	m.Start = anchor.UnixMilli()
	m.End = anchor.Add(30 * time.Minute).UnixMilli()

	starts, err := meetingOccurrences(m, anchor, anchor.AddDate(0, 0, 21))
	require.NoError(t, err)
	require.Len(t, starts, 3)

	for i, start := range starts {
		local := start.In(paris)
		assert.Equal(t, 9, local.Hour(), "occurrence %d keeps 09:00 local time across the DST change", i)
		assert.Equal(t, 0, local.Minute())
	}
	// The second occurrence (2026-03-29) is after the clocks spring forward, so its UTC instant
	// is an hour earlier relative to the first, even though the local wall time is unchanged.
	utcGapFirstToSecond := starts[1].Sub(starts[0])
	utcGapSecondToThird := starts[2].Sub(starts[1])
	assert.Equal(t, 7*24*time.Hour, utcGapSecondToThird, "no DST change between the second and third")
	assert.Equal(t, 7*24*time.Hour-time.Hour, utcGapFirstToSecond, "the DST change shortens the UTC gap by an hour")
}

// TestMeetingOccurrencesBounded checks that meetingOccurrences respects maxMeetingOccurrences and
// maxMeetingHorizon, so an unbounded request can't make the server expand without limit.
func TestMeetingOccurrencesBounded(t *testing.T) {
	m := testRecurringMeeting("FREQ=DAILY")
	from := time.UnixMilli(m.Start)
	starts, err := meetingOccurrences(m, from, from.AddDate(10, 0, 0))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(starts), maxMeetingOccurrences)
	if len(starts) > 0 {
		assert.True(t, starts[len(starts)-1].Before(from.Add(maxMeetingHorizon+24*time.Hour)))
	}
}

// TestNextMeetingOccurrence checks that it returns the series' first occurrence before it starts,
// the same one while it's running, the next one once it's ended, and false once the series (or a
// one-off meeting) is over.
func TestNextMeetingOccurrence(t *testing.T) {
	m := testRecurringMeeting("FREQ=WEEKLY")
	// The series has no time zone, so it's expanded in UTC: compute the expected dates there too,
	// or a run in a zone whose clocks change within the week sees an hour's difference.
	first := time.UnixMilli(m.Start).UTC()
	second := first.AddDate(0, 0, 7)

	start, end, ok := nextMeetingOccurrence(m, first.Add(-time.Hour))
	require.True(t, ok)
	assert.True(t, start.Equal(first))
	assert.True(t, end.Equal(time.UnixMilli(m.End)))

	// While the first occurrence is running, it's still "next".
	start, _, ok = nextMeetingOccurrence(m, first.Add(10*time.Minute))
	require.True(t, ok)
	assert.True(t, start.Equal(first))

	// Once it's ended, the next one is the following week's.
	start, _, ok = nextMeetingOccurrence(m, time.UnixMilli(m.End).Add(time.Minute))
	require.True(t, ok)
	assert.True(t, start.Equal(second))

	// A one-off meeting that already ended has no next occurrence.
	oneOff := testMeeting()
	_, _, ok = nextMeetingOccurrence(oneOff, time.UnixMilli(oneOff.End).Add(time.Hour))
	assert.False(t, ok)
}

// TestLastMeetingOccurrence checks that a bounded series (COUNT or UNTIL) has a last occurrence,
// and that an endless one, or a one-off meeting, doesn't.
func TestLastMeetingOccurrence(t *testing.T) {
	bounded := testRecurringMeeting("FREQ=DAILY;COUNT=3")
	last, ok := lastMeetingOccurrence(bounded)
	require.True(t, ok)
	assert.True(t, last.Equal(time.UnixMilli(bounded.Start).UTC().AddDate(0, 0, 2)), "expanded in UTC (no time zone): %s", last)

	endless := testRecurringMeeting("FREQ=DAILY")
	_, ok = lastMeetingOccurrence(endless)
	assert.False(t, ok)

	oneOff := testMeeting()
	_, ok = lastMeetingOccurrence(oneOff)
	assert.False(t, ok)
}
