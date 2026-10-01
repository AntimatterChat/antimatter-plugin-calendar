// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeCalendar(t *testing.T, data string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(data, "\n", "\r\n"))).Decode()
	require.NoError(t, err)
	return cal
}

func encodeCalendar(t *testing.T, cal *ical.Calendar) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, ical.NewEncoder(&buf).Encode(cal))
	return buf.String()
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

const standupCalendar = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:standup@example.com
DTSTAMP:20260901T000000Z
DTSTART;TZID=/mozilla.org/20050126_1/Europe/Paris:20261019T093000
DTEND;TZID=/mozilla.org/20050126_1/Europe/Paris:20261019T094500
RRULE:FREQ=WEEKLY;BYDAY=MO,WE
EXDATE;TZID=Europe/Paris:20261021T093000
SUMMARY:Standup
LOCATION:Control room
X-ANTIMATTER-CHANNEL-ID:abcdefghijklmnopqrstuvwxyz
X-ANTIMATTER-LINK:channel
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT15M
END:VALARM
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER;RELATED=END:-PT20M
END:VALARM
END:VEVENT
BEGIN:VEVENT
UID:standup@example.com
DTSTAMP:20260901T000000Z
RECURRENCE-ID;TZID=Europe/Paris:20261026T093000
DTSTART;TZID=Europe/Paris:20261026T110000
DTEND;TZID=Europe/Paris:20261026T111500
SUMMARY:Standup (moved)
END:VEVENT
BEGIN:VEVENT
UID:standup@example.com
DTSTAMP:20260901T000000Z
RECURRENCE-ID;TZID=Europe/Paris:20261028T093000
DTSTART;TZID=Europe/Paris:20261028T093000
DTEND;TZID=Europe/Paris:20261028T094500
STATUS:CANCELLED
SUMMARY:Standup
END:VEVENT
END:VCALENDAR
`

func TestExpandRecurringEvent(t *testing.T) {
	paris := mustLoad(t, "Europe/Paris")
	cal := decodeCalendar(t, standupCalendar)

	// Two weeks across the end of daylight saving time (25 October 2026)
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, paris)
	to := time.Date(2026, 11, 2, 0, 0, 0, 0, paris)
	occurrences := expandObject("/cal/work/", "/cal/work/standup.ics", `"e1"`, cal, from, to, paris)
	sortOccurrences(occurrences)

	var got []string
	for _, o := range occurrences {
		got = append(got, time.UnixMilli(o.Start).In(paris).Format("Mon 02 15:04")+" "+o.Summary)
	}
	assert.Equal(t, []string{
		"Mon 19 09:30 Standup",
		// Wednesday 21 is excluded
		"Mon 26 11:00 Standup (moved)",
		// Wednesday 28 is cancelled
	}, got)

	first := occurrences[0]
	assert.Equal(t, "/cal/work/", first.Calendar)
	assert.Equal(t, "/cal/work/standup.ics", first.Path)
	assert.Equal(t, `"e1"`, first.ETag)
	assert.Equal(t, "standup@example.com", first.UID)
	assert.Equal(t, "Control room", first.Location)
	assert.Equal(t, RecurrenceCustom, first.Recurrence, "weekly on two days isn't a simple weekly event")
	assert.Equal(t, first.Start, first.RecurrenceID)
	assert.Equal(t, int64(15*60*1000), first.End-first.Start)
	assert.Equal(t, []int{5, 15}, first.Alarms, "the alarm related to the end rings 20 minutes before the end")
	assert.Equal(t, &EventLink{ChannelID: "abcdefghijklmnopqrstuvwxyz", Kind: LinkChannel}, first.Link)

	moved := occurrences[1]
	assert.Equal(t, time.Date(2026, 10, 26, 9, 30, 0, 0, paris).UnixMilli(), moved.RecurrenceID)

	// After the change to winter time, the local time holds
	occurrences = expandObject("/cal/work/", "/cal/work/standup.ics", "", cal, time.Date(2026, 11, 2, 0, 0, 0, 0, paris), time.Date(2026, 11, 3, 0, 0, 0, 0, paris), paris)
	require.Len(t, occurrences, 1)
	assert.Equal(t, "09:30", time.UnixMilli(occurrences[0].Start).In(paris).Format("15:04"))
}

func TestExpandSingleAndAllDayEvents(t *testing.T) {
	tokyo := mustLoad(t, "Asia/Tokyo")
	cal := decodeCalendar(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:review
DTSTAMP:20260901T000000Z
DTSTART:20261001T020000Z
DURATION:PT1H30M
SUMMARY:Calibration review
DESCRIPTION:Run 42 results\, cooling laser
END:VEVENT
BEGIN:VEVENT
UID:beamtime
DTSTAMP:20260901T000000Z
DTSTART;VALUE=DATE:20261001
DTEND;VALUE=DATE:20261003
SUMMARY:Beam time
END:VEVENT
BEGIN:VEVENT
UID:exchange
DTSTAMP:20260901T000000Z
DTSTART;TZID="Tokyo Standard Time":20261001T180000
SUMMARY:Reminder only
END:VEVENT
BEGIN:VEVENT
UID:later
DTSTAMP:20260901T000000Z
DTSTART:20261101T020000Z
SUMMARY:Next month
END:VEVENT
END:VCALENDAR
`)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, tokyo)
	to := time.Date(2026, 10, 2, 0, 0, 0, 0, tokyo)
	occurrences := expandObject("/c/", "/c/x.ics", "", cal, from, to, tokyo)
	sortOccurrences(occurrences)
	require.Len(t, occurrences, 3)

	beam := occurrences[0]
	assert.True(t, beam.AllDay)
	assert.Equal(t, "2026-10-01", beam.StartDate)
	assert.Equal(t, "2026-10-03", beam.EndDate)
	assert.Equal(t, from.UnixMilli(), beam.Start)

	review := occurrences[1]
	assert.Equal(t, "Calibration review", review.Summary)
	assert.Equal(t, "Run 42 results, cooling laser", review.Description)
	assert.Equal(t, int64(90*60*1000), review.End-review.Start)
	assert.Empty(t, review.Recurrence)
	assert.Zero(t, review.RecurrenceID)

	exchange := occurrences[2]
	assert.Equal(t, "18:00", time.UnixMilli(exchange.Start).In(tokyo).Format("15:04"), "Windows time zone names are understood")
	assert.Equal(t, exchange.Start, exchange.End)
}

func TestAllDaySeriesUntilDate(t *testing.T) {
	newYork := mustLoad(t, "America/New_York")
	cal := decodeCalendar(t, `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:gym@example.com
DTSTAMP:20260901T000000Z
DTSTART;VALUE=DATE:20261001
DTEND;VALUE=DATE:20261002
RRULE:FREQ=DAILY;UNTIL=20261003
SUMMARY:Gym
END:VEVENT
END:VCALENDAR
`)
	occurrences := expandObject("", "", "", cal, time.Date(2026, 9, 28, 0, 0, 0, 0, newYork), time.Date(2026, 10, 10, 0, 0, 0, 0, newYork), newYork)
	require.Len(t, occurrences, 3, "the UNTIL day is included")
	assert.Equal(t, "2026-10-03", occurrences[2].StartDate)
}
