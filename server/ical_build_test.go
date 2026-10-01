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

func TestBuildEvent(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	alarm := 10
	in := &EventInput{
		Calendar:   "/cal/work/",
		Summary:    " Firmware sync ",
		Location:   "Lab",
		Start:      "2026-10-05T13:00:00+02:00",
		End:        "2026-10-05T13:30:00+02:00",
		TimeZone:   "Europe/Paris",
		Recurrence: RecurrenceWeekly,
		Alarm:      &alarm,
		Link:       &EventLink{ChannelID: "abcdefghijklmnopqrstuvwxyz", Kind: LinkCall},
	}
	require.NoError(t, in.validate())
	cal, err := newEventObject(in, "uid-1", now, "https://chat.example.com/lab/channels/detector-builds")
	require.NoError(t, err)

	data := encodeCalendar(t, cal)
	assert.Contains(t, data, "BEGIN:VTIMEZONE\r\nTZID:Europe/Paris\r\n")
	assert.Contains(t, data, "DTSTART;TZID=Europe/Paris:20261005T130000\r\n")
	assert.Contains(t, data, "RRULE:FREQ=WEEKLY\r\n")
	assert.Contains(t, data, "TRIGGER:-PT10M\r\n")
	assert.Contains(t, data, "X-ANTIMATTER-CHANNEL-ID:abcdefghijklmnopqrstuvwxyz\r\n")
	assert.Contains(t, data, "X-ANTIMATTER-LINK:call\r\n")
	assert.Contains(t, data, "URL:https://chat.example.com/lab/channels/detector-builds\r\n")
	assert.NotContains(t, data, "VALUE=TEXT")

	// What's written reads back
	paris := mustLoad(t, "Europe/Paris")
	cal = decodeCalendar(t, strings.ReplaceAll(data, "\r\n", "\n"))
	occurrences := expandObject("/cal/work/", "/cal/work/uid-1.ics", "", cal, now, now.AddDate(0, 0, 14), paris)
	require.Len(t, occurrences, 2)
	assert.Equal(t, "Firmware sync", occurrences[0].Summary)
	assert.Equal(t, RecurrenceWeekly, occurrences[0].Recurrence)
	assert.Equal(t, []int{10}, occurrences[0].Alarms)
	assert.Equal(t, LinkCall, occurrences[0].Link.Kind)
	assert.Equal(t, time.Date(2026, 10, 12, 13, 0, 0, 0, paris).UnixMilli(), occurrences[1].Start)

	// Edit the series: no repeat, no alarm, no link, all day
	edit := &EventInput{Summary: "Firmware day", AllDay: true, Start: "2026-10-06", End: "2026-10-07", TimeZone: "Europe/Paris"}
	require.NoError(t, edit.validate())
	require.NoError(t, updateEventObject(cal, edit, now.Add(time.Hour), ""))
	data = encodeCalendar(t, cal)
	data = data[strings.Index(data, "BEGIN:VEVENT"):]
	assert.Contains(t, data, "DTSTART;VALUE=DATE:20261006\r\n")
	assert.Contains(t, data, "SEQUENCE:1\r\n")
	assert.NotContains(t, data, "RRULE")
	assert.NotContains(t, data, "VALARM")
	assert.NotContains(t, data, "X-ANTIMATTER")
	assert.NotContains(t, data, "URL:")

	_, _, _, err = (&EventInput{Summary: "x", Start: "2026-10-06T10:00:00Z", End: "2026-10-06T09:00:00Z"}).times()
	assert.Error(t, err)
	assert.Error(t, (&EventInput{Summary: "  ", Start: "2026-10-06T10:00:00Z", End: "2026-10-06T11:00:00Z"}).validate())
	assert.Error(t, (&EventInput{Summary: "x", Start: "2026-10-06T10:00:00Z", End: "2026-10-06T11:00:00Z", Recurrence: "hourly"}).validate())
}

func TestExcludeOccurrence(t *testing.T) {
	paris := mustLoad(t, "Europe/Paris")
	cal := decodeCalendar(t, standupCalendar)

	require.NoError(t, excludeOccurrence(cal, time.Date(2026, 10, 19, 9, 30, 0, 0, paris), paris, time.Now()))
	require.NoError(t, excludeOccurrence(cal, time.Date(2026, 10, 26, 9, 30, 0, 0, paris), paris, time.Now()))

	occurrences := expandObject("", "", "", cal, time.Date(2026, 10, 19, 0, 0, 0, 0, paris), time.Date(2026, 11, 2, 0, 0, 0, 0, paris), paris)
	assert.Empty(t, occurrences, "the occurrence and the override of the other one are gone")

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
	assert.Error(t, excludeOccurrence(single, time.Now(), paris, time.Now()))
}

func TestVTimezone(t *testing.T) {
	tz := vtimezone(mustLoad(t, "Europe/Paris"), 2026)
	require.Len(t, tz.Children, 2)
	daylight, standard := tz.Children[0], tz.Children[1]
	assert.Equal(t, ical.CompTimezoneDaylight, daylight.Name)
	assert.Equal(t, "20260329T020000", daylight.Props.Get(ical.PropDateTimeStart).Value)
	assert.Equal(t, "+0100", daylight.Props.Get(ical.PropTimezoneOffsetFrom).Value)
	assert.Equal(t, "+0200", daylight.Props.Get(ical.PropTimezoneOffsetTo).Value)
	assert.Equal(t, "FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU", daylight.Props.Get(ical.PropRecurrenceRule).Value)
	assert.Equal(t, ical.CompTimezoneStandard, standard.Name)
	assert.Equal(t, "20261025T030000", standard.Props.Get(ical.PropDateTimeStart).Value)
	assert.Equal(t, "FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU", standard.Props.Get(ical.PropRecurrenceRule).Value)

	tz = vtimezone(mustLoad(t, "America/New_York"), 2026)
	require.Len(t, tz.Children, 2)
	assert.Equal(t, "FREQ=YEARLY;BYMONTH=3;BYDAY=2SU", tz.Children[0].Props.Get(ical.PropRecurrenceRule).Value)
	assert.Equal(t, "FREQ=YEARLY;BYMONTH=11;BYDAY=1SU", tz.Children[1].Props.Get(ical.PropRecurrenceRule).Value)

	tz = vtimezone(mustLoad(t, "Asia/Tokyo"), 2026)
	require.Len(t, tz.Children, 1)
	assert.Equal(t, "+0900", tz.Children[0].Props.Get(ical.PropTimezoneOffsetTo).Value)
}
