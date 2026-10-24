// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"fmt"
	"time"

	"github.com/emersion/go-ical"
)

const (
	// meetingUIDDomain is the host part of a meeting's iCalendar UID. It doesn't need to resolve
	// to anything (RFC 5545 only needs the UID to be globally unique), so it's kept constant
	// rather than built from the server's address, which can change.
	meetingUIDDomain = "antimatterchat.calendar"

	itipMethodRequest = "REQUEST"
	itipMethodCancel  = "CANCEL"

	// icsContentTypeRequest and icsContentTypeCancel are the MIME type of the attachment of an
	// iTIP invitation email (RFC 6047 "iMIP").
	icsContentTypeRequest = `text/calendar; method=REQUEST; charset=UTF-8`
	icsContentTypeCancel  = `text/calendar; method=CANCEL; charset=UTF-8`
)

// meetingUID is the iCalendar UID of a meeting: stable across edits, so an iTIP update or
// cancellation refers to the same event as the original invitation.
func meetingUID(id string) string {
	return id + "@" + meetingUIDDomain
}

// invitee is one recipient of an iTIP invitation: their address, display name, and (for a
// REQUEST) their current RSVP, if any.
type invitee struct {
	Email string
	Name  string
	RSVP  RSVPStatus
}

// partStat returns the PARTSTAT of an attendee's RSVP (RFC 5545 section 3.2.12).
func partStat(status RSVPStatus) string {
	switch status {
	case RSVPGoing:
		return "ACCEPTED"
	case RSVPMaybe:
		return "TENTATIVE"
	case RSVPNo:
		return "DECLINED"
	default:
		return "NEEDS-ACTION"
	}
}

// addressProp returns a property (ORGANIZER or ATTENDEE) set to a mailto: calendar address, with
// its common name if given.
func addressProp(name, email, commonName string) *ical.Prop {
	prop := ical.NewProp(name)
	prop.Value = "mailto:" + email
	if commonName != "" {
		prop.Params.Set(ical.ParamCommonName, commonName)
	}
	return prop
}

// meetingICSOptions describes which iTIP message buildMeetingICS builds: the whole series (the
// usual case), or one occurrence of a recurring series (a cancellation of just that occurrence),
// and the VALARM trigger of the recipient it's being built for.
type meetingICSOptions struct {
	// OccurrenceStart and OccurrenceEnd, both non-zero, mean this message is about one occurrence
	// of a recurring series only (RFC 5546 RECURRENCE-ID), not the series itself: cancelling one
	// occurrence without cancelling the rest of it.
	OccurrenceStart, OccurrenceEnd time.Time

	// ReminderMinutes is the recipient's own effective reminder choice: a VALARM with that
	// trigger is added to a REQUEST, noReminderMinutes (the default zero value) adds none.
	ReminderMinutes int
}

// buildMeetingICS returns the iTIP calendar object scheduling, updating or cancelling a meeting
// (RFC 5545 and RFC 5546): METHOD:REQUEST while it's live (METHOD:CANCEL with STATUS:CANCELLED
// once it's cancelled, or once opts describes a cancelled occurrence of it), ORGANIZER and one
// ATTENDEE per invitee, and the meeting's SEQUENCE so calendar apps apply updates and
// cancellations in order. A recurring series carries its RRULE and EXDATEs. permalink, if not
// empty, is the channel's URL.
func buildMeetingICS(m *Meeting, organizer invitee, attendees []invitee, now time.Time, permalink string, opts meetingICSOptions) *ical.Calendar {
	occurrence := !opts.OccurrenceStart.IsZero()
	cancelled := m.Cancelled || occurrence
	method := itipMethodRequest
	if cancelled {
		method = itipMethodCancel
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, productID)
	cal.Props.SetText(ical.PropMethod, method)

	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, meetingUID(m.ID))

	seq := ical.NewProp(ical.PropSequence)
	seq.SetValueType(ical.ValueInt)
	seq.Value = fmt.Sprintf("%d", m.Sequence)
	ev.Props.Set(seq)

	stamp := ical.NewProp(ical.PropDateTimeStamp)
	stamp.SetDateTime(now.UTC())
	ev.Props.Set(stamp)

	ev.Props.SetText(ical.PropSummary, m.Summary)
	if m.Description != "" {
		ev.Props.SetText(ical.PropDescription, m.Description)
	}

	start, end := time.UnixMilli(m.Start), time.UnixMilli(m.End)
	if occurrence {
		start, end = opts.OccurrenceStart, opts.OccurrenceEnd
		recurrenceID := ical.NewProp(ical.PropRecurrenceID)
		recurrenceID.SetDateTime(opts.OccurrenceStart.UTC())
		ev.Props.Set(recurrenceID)
	} else if m.recurring() {
		rule := ical.NewProp(ical.PropRecurrenceRule)
		rule.SetValueType(ical.ValueRecurrence)
		rule.Value = m.RecurrenceRule
		ev.Props.Set(rule)
		loc := meetingTimeZone(m)
		for _, ex := range m.ExceptionDates {
			exdate := ical.NewProp(ical.PropExceptionDates)
			exdate.SetDateTime(time.UnixMilli(ex).In(loc).UTC())
			ev.Props.Add(exdate)
		}
	}
	setTime(ev.Props, ical.PropDateTimeStart, start.UTC(), false)
	setTime(ev.Props, ical.PropDateTimeEnd, end.UTC(), false)

	if cancelled {
		ev.Props.SetText(ical.PropStatus, string(ical.EventCancelled))
	}
	if permalink != "" {
		uri := ical.NewProp(ical.PropURL)
		uri.SetValueType(ical.ValueURI)
		uri.Value = permalink
		ev.Props.Set(uri)
	}

	ev.Props.Set(addressProp(ical.PropOrganizer, organizer.Email, organizer.Name))

	chair := addressProp(ical.PropAttendee, organizer.Email, organizer.Name)
	chair.Params.Set(ical.ParamRole, "CHAIR")
	chair.Params.Set(ical.ParamParticipationStatus, "ACCEPTED")
	ev.Props.Add(chair)

	for _, a := range attendees {
		if a.Email == organizer.Email {
			// Already listed as the chair
			continue
		}
		prop := addressProp(ical.PropAttendee, a.Email, a.Name)
		prop.Params.Set(ical.ParamRole, "REQ-PARTICIPANT")
		prop.Params.Set(ical.ParamParticipationStatus, partStat(a.RSVP))
		if !cancelled {
			prop.Params.Set(ical.ParamRSVP, "TRUE")
		}
		ev.Props.Add(prop)
	}

	if !cancelled && opts.ReminderMinutes != noReminderMinutes {
		ev.Children = append(ev.Children, meetingAlarm(m.Summary, opts.ReminderMinutes))
	}

	setRaw(ev.Props, propChannelID, m.ChannelID)
	setRaw(ev.Props, propLinkKind, LinkCall)

	cal.Children = append(cal.Children, ev.Component)
	return cal
}

// meetingAlarm returns the VALARM of a meeting for one person: a display alarm, minutes before
// each occurrence.
func meetingAlarm(summary string, minutes int) *ical.Component {
	alarm := ical.NewComponent(ical.CompAlarm)
	alarm.Props.SetText(ical.PropAction, "DISPLAY")
	alarm.Props.SetText(ical.PropDescription, summary)
	trigger := ical.NewProp(ical.PropTrigger)
	trigger.Value = fmt.Sprintf("-PT%dM", minutes)
	alarm.Props.Set(trigger)
	return alarm
}

// encodeICS serializes a calendar object to bytes, as RFC 5545 requires (CRLF line endings).
func encodeICS(cal *ical.Calendar) ([]byte, error) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// icsContentType returns the MIME type of the attachment of a meeting's iTIP invitation email.
// cancelled is true for a METHOD:CANCEL message: the series itself, or one occurrence of it.
func icsContentType(cancelled bool) string {
	if cancelled {
		return icsContentTypeCancel
	}
	return icsContentTypeRequest
}
