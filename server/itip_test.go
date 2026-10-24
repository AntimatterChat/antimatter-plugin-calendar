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
	"github.com/teambition/rrule-go"
)

func testICSMeeting() *Meeting {
	return &Meeting{
		ID:          "meeting1",
		ChannelID:   "channelid1234567890123456",
		OrganizerID: "organizer1",
		Summary:     "Firmware sync",
		Description: "Weekly catch-up",
		Start:       time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC).UnixMilli(),
		End:         time.Date(2026, 10, 27, 14, 30, 0, 0, time.UTC).UnixMilli(),
		Sequence:    0,
	}
}

// TestBuildMeetingICSRequest checks the iTIP fields of a fresh invitation (RFC 5545 and RFC 5546):
// METHOD:REQUEST, a stable UID, DTSTAMP, SEQUENCE, one ORGANIZER and one ATTENDEE per invitee
// (the organiser included, as the chair), each with a mailto: address, and the RSVP and
// PARTSTAT parameters a REQUEST needs.
func TestBuildMeetingICSRequest(t *testing.T) {
	m := testICSMeeting()
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	organizer := invitee{Email: "ada@example.com", Name: "Ada Lovelace"}
	attendees := []invitee{
		{Email: "grace@example.com", Name: "Grace Hopper", RSVP: RSVPGoing},
		{Email: "alan@example.com", Name: "Alan Turing", RSVP: RSVPMaybe},
		{Email: "margaret@example.com", Name: "Margaret Hamilton"}, // hasn't answered yet
	}

	cal := buildMeetingICS(m, organizer, attendees, now, "https://chat.example.com/lab/channels/standup", meetingICSOptions{ReminderMinutes: noReminderMinutes})
	require.Len(t, cal.Children, 1)
	ev := cal.Children[0]

	assert.Equal(t, "2.0", cal.Props.Get(ical.PropVersion).Value)
	assert.Equal(t, "REQUEST", cal.Props.Get(ical.PropMethod).Value)
	assert.Equal(t, meetingUID("meeting1"), ev.Props.Get(ical.PropUID).Value)
	assert.Equal(t, "0", ev.Props.Get(ical.PropSequence).Value)
	assert.Equal(t, "20261020T090000Z", ev.Props.Get(ical.PropDateTimeStamp).Value)
	assert.Equal(t, "Firmware sync", ev.Props.Get(ical.PropSummary).Value)
	assert.Equal(t, "Weekly catch-up", ev.Props.Get(ical.PropDescription).Value)
	assert.Equal(t, "20261027T140000Z", ev.Props.Get(ical.PropDateTimeStart).Value)
	assert.Equal(t, "20261027T143000Z", ev.Props.Get(ical.PropDateTimeEnd).Value)
	assert.Equal(t, "https://chat.example.com/lab/channels/standup", ev.Props.Get(ical.PropURL).Value)
	assert.Nil(t, ev.Props.Get(ical.PropStatus), "a live meeting carries no STATUS:CANCELLED")

	organizerProp := ev.Props.Get(ical.PropOrganizer)
	require.NotNil(t, organizerProp)
	assert.Equal(t, "mailto:ada@example.com", organizerProp.Value)
	assert.Equal(t, "Ada Lovelace", organizerProp.Params.Get(ical.ParamCommonName))

	attendeeProps := ev.Props.Values(ical.PropAttendee)
	require.Len(t, attendeeProps, 4, "the organiser (as chair) plus the three invited members")

	byEmail := map[string]ical.Prop{}
	for _, p := range attendeeProps {
		byEmail[strings.TrimPrefix(p.Value, "mailto:")] = p
	}

	chair := byEmail["ada@example.com"]
	assert.Equal(t, "CHAIR", chair.Params.Get(ical.ParamRole))
	assert.Equal(t, "ACCEPTED", chair.Params.Get(ical.ParamParticipationStatus))

	grace := byEmail["grace@example.com"]
	assert.Equal(t, "REQ-PARTICIPANT", grace.Params.Get(ical.ParamRole))
	assert.Equal(t, "ACCEPTED", grace.Params.Get(ical.ParamParticipationStatus), "RSVPGoing maps to ACCEPTED")
	assert.Equal(t, "TRUE", grace.Params.Get(ical.ParamRSVP))
	assert.Equal(t, "Grace Hopper", grace.Params.Get(ical.ParamCommonName))

	alan := byEmail["alan@example.com"]
	assert.Equal(t, "TENTATIVE", alan.Params.Get(ical.ParamParticipationStatus), "RSVPMaybe maps to TENTATIVE")

	margaret := byEmail["margaret@example.com"]
	assert.Equal(t, "NEEDS-ACTION", margaret.Params.Get(ical.ParamParticipationStatus), "no RSVP yet maps to NEEDS-ACTION")

	// The channel it schedules a call in travels with the invitation, as the rest of the
	// calendar app's own events do.
	assert.Equal(t, m.ChannelID, ev.Props.Get(propChannelID).Value)
	assert.Equal(t, LinkCall, ev.Props.Get(propLinkKind).Value)

	// What's built must be valid iCalendar text that reads back.
	data := encodeCalendar(t, cal)
	assert.Contains(t, data, "BEGIN:VCALENDAR\r\n")
	assert.Contains(t, data, "METHOD:REQUEST\r\n")
	back := decodeCalendar(t, strings.ReplaceAll(data, "\r\n", "\n"))
	require.Len(t, back.Children, 1)
	assert.Equal(t, meetingUID("meeting1"), textProp(back.Children[0], ical.PropUID))
}

// TestBuildMeetingICSUpdateBumpsSequence checks that editing a meeting carries its SEQUENCE
// forward, so calendar apps apply it as an update to the same event rather than a new one.
func TestBuildMeetingICSUpdateBumpsSequence(t *testing.T) {
	m := testICSMeeting()
	m.Sequence = 3
	cal := buildMeetingICS(m, invitee{Email: "ada@example.com"}, nil, time.Now(), "", meetingICSOptions{ReminderMinutes: noReminderMinutes})
	assert.Equal(t, "REQUEST", cal.Props.Get(ical.PropMethod).Value)
	assert.Equal(t, "3", cal.Children[0].Props.Get(ical.PropSequence).Value)
}

// TestBuildMeetingICSCancel checks the iTIP fields of a cancellation (RFC 5546): METHOD:CANCEL,
// the event's STATUS set to CANCELLED, the same UID, and a SEQUENCE higher than any invitation or
// update that came before it. Attendees keep their RSVP's PARTSTAT, but RSVP itself isn't asked
// for again.
func TestBuildMeetingICSCancel(t *testing.T) {
	m := testICSMeeting()
	m.Cancelled = true
	m.Sequence = 2
	cal := buildMeetingICS(m, invitee{Email: "ada@example.com", Name: "Ada Lovelace"},
		[]invitee{{Email: "grace@example.com", RSVP: RSVPGoing}}, time.Now(), "", meetingICSOptions{ReminderMinutes: noReminderMinutes})
	ev := cal.Children[0]

	assert.Equal(t, "CANCEL", cal.Props.Get(ical.PropMethod).Value)
	assert.Equal(t, "CANCELLED", ev.Props.Get(ical.PropStatus).Value)
	assert.Equal(t, meetingUID(m.ID), ev.Props.Get(ical.PropUID).Value)
	assert.Equal(t, "2", ev.Props.Get(ical.PropSequence).Value)

	for _, p := range ev.Props.Values(ical.PropAttendee) {
		assert.Empty(t, p.Params.Get(ical.ParamRSVP), "a cancellation doesn't ask attendees to RSVP")
	}

	grace := ev.Props.Values(ical.PropAttendee)[1]
	assert.Equal(t, "ACCEPTED", grace.Params.Get(ical.ParamParticipationStatus), "their last RSVP is kept on the cancellation")
}

func TestICSContentType(t *testing.T) {
	assert.Contains(t, icsContentType(false), "method=REQUEST")
	assert.Contains(t, icsContentType(true), "method=CANCEL")
}

// TestBuildMeetingICSRecurring checks that a recurring series' REQUEST carries its RRULE and
// EXDATEs, and that its VALARM matches the recipient's effective reminder.
func TestBuildMeetingICSRecurring(t *testing.T) {
	m := testICSMeeting()
	m.RecurrenceRule = "FREQ=WEEKLY;BYDAY=MO,WE"
	second := time.UnixMilli(m.Start).AddDate(0, 0, 7).UnixMilli()
	m.ExceptionDates = []int64{second}

	cal := buildMeetingICS(m, invitee{Email: "ada@example.com"}, nil, time.Now(), "", meetingICSOptions{ReminderMinutes: 15})
	ev := cal.Children[0]

	assert.Equal(t, m.RecurrenceRule, ev.Props.Get(ical.PropRecurrenceRule).Value)
	exdates := ev.Props.Values(ical.PropExceptionDates)
	require.Len(t, exdates, 1)
	assert.Equal(t, time.UnixMilli(second).UTC().Format("20060102T150405Z"), exdates[0].Value)

	require.Len(t, ev.Children, 1, "one VALARM for the recipient's effective reminder")
	assert.Equal(t, "VALARM", ev.Children[0].Name)
	assert.Equal(t, "-PT15M", ev.Children[0].Props.Get(ical.PropTrigger).Value)

	// Round-trips through the real decoder.
	data := encodeCalendar(t, cal)
	back := decodeCalendar(t, strings.ReplaceAll(data, "\r\n", "\n"))
	rule, err := back.Children[0].Props.RecurrenceRule()
	require.NoError(t, err)
	assert.Equal(t, rrule.WEEKLY, rule.Freq)
}

// TestBuildMeetingICSNoReminderHasNoValarm checks that noReminderMinutes ("None") omits the
// VALARM entirely, rather than sending one with a strange trigger.
func TestBuildMeetingICSNoReminderHasNoValarm(t *testing.T) {
	m := testICSMeeting()
	cal := buildMeetingICS(m, invitee{Email: "ada@example.com"}, nil, time.Now(), "", meetingICSOptions{ReminderMinutes: noReminderMinutes})
	assert.Empty(t, cal.Children[0].Children, "no VALARM when the recipient's reminder is None")
}

// TestBuildMeetingICSOccurrenceCancel checks that cancelling one occurrence of a series (as
// opposed to the whole series) carries RECURRENCE-ID, that occurrence's own time, and the series
// UID, per RFC 5546.
func TestBuildMeetingICSOccurrenceCancel(t *testing.T) {
	m := testICSMeeting()
	m.RecurrenceRule = "FREQ=WEEKLY"
	m.Sequence = 1
	occStart := time.UnixMilli(m.Start).AddDate(0, 0, 7)
	occEnd := occStart.Add(30 * time.Minute)

	cal := buildMeetingICS(m, invitee{Email: "ada@example.com"}, nil, time.Now(), "", meetingICSOptions{
		OccurrenceStart: occStart, OccurrenceEnd: occEnd, ReminderMinutes: noReminderMinutes,
	})
	ev := cal.Children[0]

	assert.Equal(t, "CANCEL", cal.Props.Get(ical.PropMethod).Value)
	assert.Equal(t, meetingUID(m.ID), ev.Props.Get(ical.PropUID).Value, "the series UID, not a new one")
	assert.Equal(t, occStart.UTC().Format("20060102T150405Z"), ev.Props.Get(ical.PropRecurrenceID).Value)
	assert.Equal(t, occStart.UTC().Format("20060102T150405Z"), ev.Props.Get(ical.PropDateTimeStart).Value)
	assert.Equal(t, "CANCELLED", ev.Props.Get(ical.PropStatus).Value)
	assert.Nil(t, ev.Props.Get(ical.PropRecurrenceRule), "an occurrence override carries no RRULE of its own")

	data := encodeCalendar(t, cal)
	back := decodeCalendar(t, strings.ReplaceAll(data, "\r\n", "\n"))
	assert.Equal(t, meetingUID(m.ID), textProp(back.Children[0], ical.PropUID))
}

func TestMeetingUIDStableAcrossEdits(t *testing.T) {
	// The UID must stay the same across REQUEST updates and the final CANCEL: it's how calendar
	// apps know they're all the same event.
	m := testICSMeeting()
	uid1 := meetingUID(m.ID)
	m.Sequence++
	uid2 := meetingUID(m.ID)
	assert.Equal(t, uid1, uid2)
}
