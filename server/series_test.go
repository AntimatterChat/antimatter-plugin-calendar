// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundTrip writes a calendar and reads it back, like another client would.
func roundTrip(t *testing.T, cal *ical.Calendar) *ical.Calendar {
	t.Helper()
	return decodeCalendar(t, strings.ReplaceAll(encodeCalendar(t, cal), "\r\n", "\n"))
}

func summaries(occurrences []Occurrence) []string {
	sortOccurrences(occurrences)
	var list []string
	for _, o := range occurrences {
		list = append(list, time.UnixMilli(o.Start).UTC().Format("01-02T15:04")+" "+o.Summary)
	}
	return list
}

func TestOverrideOccurrence(t *testing.T) {
	paris := mustLoad(t, "Europe/Paris")
	cal := decodeCalendar(t, standupCalendar)
	from, to := time.Date(2026, 10, 19, 0, 0, 0, 0, paris), time.Date(2026, 10, 27, 0, 0, 0, 0, paris)

	// A new override, then an edit of an existing one
	in := &EventInput{Summary: "Standup (demo)", Start: "2026-10-19T10:00:00+02:00", End: "2026-10-19T10:30:00+02:00", TimeZone: "Europe/Paris", Recurrence: RecurrenceWeekly}
	require.NoError(t, overrideOccurrence(cal, time.Date(2026, 10, 19, 9, 30, 0, 0, paris), paris, in, time.Now(), ""))
	in = &EventInput{Summary: "Standup (moved again)", Start: "2026-10-26T12:00:00+01:00", End: "2026-10-26T12:15:00+01:00", TimeZone: "Europe/Paris"}
	require.NoError(t, overrideOccurrence(cal, time.Date(2026, 10, 26, 9, 30, 0, 0, paris), paris, in, time.Now(), ""))

	// Written and read back like another client would
	cal = roundTrip(t, cal)
	assert.Equal(t, []string{
		"10-19T08:00 Standup (demo)",
		"10-26T11:00 Standup (moved again)",
	}, summaries(expandObject("", "", "", cal, from, to, paris)))

	override := findOverride(cal, time.Date(2026, 10, 19, 9, 30, 0, 0, paris), paris)
	require.NotNil(t, override)
	rid := override.Props.Get(ical.PropRecurrenceID)
	assert.Equal(t, "20261019T093000", rid.Value)
	assert.Equal(t, "/mozilla.org/20050126_1/Europe/Paris", rid.Params.Get(ical.PropTimezoneID), "written like the series' start")
	assert.Nil(t, override.Props.Get(ical.PropRecurrenceRule))
	assert.Equal(t, "standup@example.com", textProp(override, ical.PropUID), "it's of the series")
	assert.Equal(t, "1", override.Props.Get(ical.PropSequence).Value)
	assert.Len(t, cal.Children, 5, "a time zone, the series and three overrides")

	single := decodeCalendar(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:x
DTSTAMP:20260901T000000Z
DTSTART:20261001T020000Z
SUMMARY:Single
END:VEVENT
END:VCALENDAR
`)
	assert.Error(t, overrideOccurrence(single, time.Now(), paris, in, time.Now(), ""))
}

func TestOccurrenceDetails(t *testing.T) {
	paris := mustLoad(t, "Europe/Paris")
	cal := decodeCalendar(t, standupCalendar)

	details, err := occurrenceDetails(cal, time.Date(2026, 11, 2, 9, 30, 0, 0, paris), paris)
	require.NoError(t, err)
	assert.Equal(t, "Standup", details.Summary)
	assert.Equal(t, "2026-11-02T09:30:00+01:00", details.Start)
	assert.Equal(t, "2026-11-02T09:45:00+01:00", details.End)
	assert.Equal(t, RecurrenceCustom, details.Recurrence)
	assert.Equal(t, time.Date(2026, 11, 2, 9, 30, 0, 0, paris).UnixMilli(), details.RecurrenceID)

	details, err = occurrenceDetails(cal, time.Date(2026, 10, 26, 9, 30, 0, 0, paris), paris)
	require.NoError(t, err)
	assert.Equal(t, "Standup (moved)", details.Summary)
	assert.Equal(t, "2026-10-26T11:00:00+01:00", details.Start)
}

const dailyCalendar = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:beam@example.com
DTSTAMP:20260901T000000Z
DTSTART:20261001T070000Z
DTEND:20261001T080000Z
RRULE:FREQ=DAILY;COUNT=10
SUMMARY:Beam
END:VEVENT
BEGIN:VEVENT
UID:beam@example.com
DTSTAMP:20260901T000000Z
RECURRENCE-ID:20261008T070000Z
DTSTART:20261008T090000Z
DTEND:20261008T100000Z
SUMMARY:Beam (late)
END:VEVENT
END:VCALENDAR
`

func TestEndSeriesBefore(t *testing.T) {
	cal := decodeCalendar(t, dailyCalendar)
	split := time.Date(2026, 10, 4, 7, 0, 0, 0, time.UTC)

	rule, err := endSeriesBefore(cal, split, time.UTC, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "FREQ=DAILY;COUNT=7", rule, "seven occurrences are left")
	cal = roundTrip(t, cal)
	assert.Equal(t, "FREQ=DAILY;UNTIL=20261004T065959Z", masterEvent(cal).Props.Get(ical.PropRecurrenceRule).Value)
	assert.Len(t, cal.Children, 1, "the override of a following occurrence is gone")
	assert.Len(t, expandObject("", "", "", cal, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.UTC), 3)

	// The following occurrences continue in a new series
	in := &EventInput{Summary: "Beam (new hall)", Start: "2026-10-04T08:00:00Z", End: "2026-10-04T09:00:00Z", Recurrence: RecurrenceCustom}
	next, err := continueSeries(in, rule, "next", time.Now(), "")
	require.NoError(t, err)
	occurrences := expandObject("", "", "", next, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	require.Len(t, occurrences, 7)
	assert.Equal(t, "10-04T08:00 Beam (new hall)", summaries(occurrences)[0])

	_, err = endSeriesBefore(decodeCalendar(t, dailyCalendar), time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC), time.UTC, time.Now())
	assert.ErrorIs(t, err, errFirstOccurrence)
}

func TestEndAllDaySeriesBefore(t *testing.T) {
	newYork := mustLoad(t, "America/New_York")
	cal := decodeCalendar(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:gym@example.com
DTSTAMP:20260901T000000Z
DTSTART;VALUE=DATE:20261001
DTEND;VALUE=DATE:20261002
RRULE:FREQ=WEEKLY;UNTIL=20261231
SUMMARY:Gym
END:VEVENT
END:VCALENDAR
`)
	rule, err := endSeriesBefore(cal, time.Date(2026, 10, 15, 0, 0, 0, 0, newYork), newYork, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "FREQ=WEEKLY;UNTIL=20261231", rule)
	assert.Equal(t, "FREQ=WEEKLY;UNTIL=20261014", masterEvent(cal).Props.Get(ical.PropRecurrenceRule).Value)
	occurrences := expandObject("", "", "", cal, time.Date(2026, 9, 1, 0, 0, 0, 0, newYork), time.Date(2026, 12, 1, 0, 0, 0, 0, newYork), newYork)
	require.Len(t, occurrences, 2)
	assert.Equal(t, "2026-10-08", occurrences[1].StartDate)
}
