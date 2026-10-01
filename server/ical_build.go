// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

const (
	productID = "-//Antimatter//Calendar//EN"

	maxSummaryLength     = 1000
	maxDescriptionLength = 20000
	maxEventDuration     = 366 * 24 * time.Hour
)

// EventInput is an event as the event editor sends it.
type EventInput struct {
	Calendar    string `json:"calendar"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Location    string `json:"location"`
	AllDay      bool   `json:"all_day"`
	// Start and End are RFC 3339 times, or dates (2006-01-02) for all-day events, whose end is
	// exclusive.
	Start string `json:"start"`
	End   string `json:"end"`
	// TimeZone is the IANA time zone the event is written in, so that it keeps its local time
	// when it repeats across daylight saving time changes.
	TimeZone   string `json:"time_zone"`
	Recurrence string `json:"recurrence"`
	// Alarm is the number of minutes before the start at which the event's alarm rings, nil for
	// no alarm.
	Alarm *int       `json:"alarm"`
	Link  *EventLink `json:"link"`
}

// times returns the start and end of the event, in its time zone.
func (in *EventInput) times() (start, end time.Time, loc *time.Location, err error) {
	loc = time.UTC
	if in.TimeZone != "" {
		if loc, err = time.LoadLocation(in.TimeZone); err != nil {
			return start, end, nil, newAPIError(http.StatusBadRequest, "unknown time zone")
		}
	}
	if in.AllDay {
		start, err = time.ParseInLocation(dateFormat, in.Start, loc)
		if err == nil {
			end, err = time.ParseInLocation(dateFormat, in.End, loc)
		}
		if err == nil && !end.After(start) {
			end = start.AddDate(0, 0, 1)
		}
	} else {
		start, err = time.Parse(time.RFC3339, in.Start)
		if err == nil {
			end, err = time.Parse(time.RFC3339, in.End)
		}
		start, end = start.In(loc), end.In(loc)
	}
	if err != nil {
		return start, end, nil, newAPIError(http.StatusBadRequest, "invalid event start or end")
	}
	if end.Before(start) || end.Sub(start) > maxEventDuration {
		return start, end, nil, newAPIError(http.StatusBadRequest, "the event must end after it starts, within a year")
	}
	return start, end, loc, nil
}

func (in *EventInput) validate() error {
	in.Summary = strings.TrimSpace(in.Summary)
	if in.Summary == "" {
		return newAPIError(http.StatusBadRequest, "the event needs a title")
	}
	if len(in.Summary) > maxSummaryLength || len(in.Location) > maxSummaryLength || len(in.Description) > maxDescriptionLength {
		return newAPIError(http.StatusBadRequest, "the event's text is too long")
	}
	switch in.Recurrence {
	case RecurrenceNone, RecurrenceDaily, RecurrenceWeekly, RecurrenceMonthly, RecurrenceYearly, RecurrenceCustom:
	default:
		return newAPIError(http.StatusBadRequest, "invalid recurrence")
	}
	if in.Alarm != nil && (*in.Alarm < 0 || *in.Alarm > maxRemindMinutes) {
		return newAPIError(http.StatusBadRequest, "invalid alarm")
	}
	if in.Link != nil && in.Link.Kind != LinkChannel && in.Link.Kind != LinkCall {
		return newAPIError(http.StatusBadRequest, "invalid link")
	}
	_, _, _, err := in.times()
	return err
}

func setTime(props ical.Props, name string, t time.Time, allDay bool) {
	prop := ical.NewProp(name)
	if allDay {
		prop.SetDate(t)
	} else {
		// UTC times are written as such, others with their time zone
		prop.SetDateTime(t)
	}
	props.Set(prop)
}

// setRaw sets a property to a value that needs no escaping, without a value type.
func setRaw(props ical.Props, name, value string) {
	prop := ical.NewProp(name)
	prop.Value = value
	props.Set(prop)
}

func setOrDeleteText(props ical.Props, name, value string) {
	if strings.TrimSpace(value) == "" {
		props.Del(name)
		return
	}
	props.SetText(name, value)
}

// applyInput writes the editor's fields into an event, keeping the properties it doesn't know
// (attendees, organizer...). permalink is the URL of the linked channel.
func applyInput(ev *ical.Component, in *EventInput, start, end time.Time, now time.Time, permalink string) {
	ev.Props.SetText(ical.PropSummary, in.Summary)
	setOrDeleteText(ev.Props, ical.PropDescription, in.Description)
	setOrDeleteText(ev.Props, ical.PropLocation, in.Location)

	setTime(ev.Props, ical.PropDateTimeStart, start, in.AllDay)
	setTime(ev.Props, ical.PropDateTimeEnd, end, in.AllDay)
	ev.Props.Del(ical.PropDuration)

	switch in.Recurrence {
	case RecurrenceNone:
		ev.Props.Del(ical.PropRecurrenceRule)
		ev.Props.Del(ical.PropExceptionDates)
		ev.Props.Del(ical.PropRecurrenceDates)
	case RecurrenceCustom:
		// Kept as it is
	default:
		prop := ical.NewProp(ical.PropRecurrenceRule)
		prop.SetValueType(ical.ValueRecurrence)
		prop.Value = "FREQ=" + strings.ToUpper(in.Recurrence)
		ev.Props.Set(prop)
	}

	// The editor shows one alarm
	children := ev.Children[:0]
	for _, child := range ev.Children {
		if child.Name != ical.CompAlarm {
			children = append(children, child)
		}
	}
	ev.Children = children
	if in.Alarm != nil {
		alarm := ical.NewComponent(ical.CompAlarm)
		alarm.Props.SetText(ical.PropAction, "DISPLAY")
		alarm.Props.SetText(ical.PropDescription, in.Summary)
		trigger := ical.NewProp(ical.PropTrigger)
		trigger.Value = fmt.Sprintf("-PT%dM", *in.Alarm)
		alarm.Props.Set(trigger)
		ev.Children = append(ev.Children, alarm)
	}

	oldLink := eventLink(ev)
	if in.Link != nil && in.Link.ChannelID != "" {
		setRaw(ev.Props, propChannelID, in.Link.ChannelID)
		setRaw(ev.Props, propLinkKind, in.Link.Kind)
		if permalink != "" {
			uri := ical.NewProp(ical.PropURL)
			uri.SetValueType(ical.ValueURI)
			uri.Value = permalink
			ev.Props.Set(uri)
		}
	} else {
		ev.Props.Del(propChannelID)
		ev.Props.Del(propLinkKind)
		if oldLink != nil {
			// The URL pointed at the channel
			ev.Props.Del(ical.PropURL)
		}
	}

	stamp := ical.NewProp(ical.PropDateTimeStamp)
	stamp.SetDateTime(now.UTC())
	ev.Props.Set(stamp)
	modified := ical.NewProp(ical.PropLastModified)
	modified.SetDateTime(now.UTC())
	ev.Props.Set(modified)
}

// ensureTimeZone adds the VTIMEZONE of a time zone to a calendar, as RFC 5545 requires for each
// TZID it uses.
func ensureTimeZone(cal *ical.Calendar, loc *time.Location, year int) {
	if loc == time.UTC {
		return
	}
	for _, child := range cal.Children {
		if child.Name == ical.CompTimezone && textProp(child, ical.PropTimezoneID) == loc.String() {
			return
		}
	}
	// Time zones go before the events
	cal.Children = append([]*ical.Component{vtimezone(loc, year)}, cal.Children...)
}

// newEventObject builds the calendar object of a new event.
func newEventObject(in *EventInput, uid string, now time.Time, permalink string) (*ical.Calendar, error) {
	start, end, loc, err := in.times()
	if err != nil {
		return nil, err
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, productID)

	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	created := ical.NewProp(ical.PropCreated)
	created.SetDateTime(now.UTC())
	ev.Props.Set(created)
	applyInput(ev.Component, in, start, end, now, permalink)
	cal.Children = append(cal.Children, ev.Component)

	if !in.AllDay {
		ensureTimeZone(cal, loc, start.Year())
	}
	return cal, nil
}

// masterEvent returns the event of an object that isn't an override of a recurrence.
func masterEvent(cal *ical.Calendar) *ical.Component {
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent && child.Props.Get(ical.PropRecurrenceID) == nil {
			return child
		}
	}
	return nil
}

var errNoEvent = newAPIError(http.StatusNotFound, "event not found")

// updateEventObject applies an edit to the event of an object (to the whole series of a recurring
// event).
func updateEventObject(cal *ical.Calendar, in *EventInput, now time.Time, permalink string) error {
	ev := masterEvent(cal)
	if ev == nil {
		return errNoEvent
	}
	start, end, loc, err := in.times()
	if err != nil {
		return err
	}
	if in.Recurrence == RecurrenceCustom && ev.Props.Get(ical.PropRecurrenceRule) == nil {
		in.Recurrence = RecurrenceNone
	}

	applyInput(ev, in, start, end, now, permalink)
	sequence := 0
	if prop := ev.Props.Get(ical.PropSequence); prop != nil {
		sequence, _ = strconv.Atoi(prop.Value)
	}
	seq := ical.NewProp(ical.PropSequence)
	seq.SetValueType(ical.ValueInt)
	seq.Value = strconv.Itoa(sequence + 1)
	ev.Props.Set(seq)

	if !in.AllDay {
		ensureTimeZone(cal, loc, start.Year())
	}
	return nil
}

// excludeOccurrence removes one occurrence from a recurring event.
func excludeOccurrence(cal *ical.Calendar, recurrenceID time.Time, now time.Time) error {
	ev := masterEvent(cal)
	if ev == nil || ev.Props.Get(ical.PropRecurrenceRule) == nil {
		return errNoEvent
	}
	startProp := ev.Props.Get(ical.PropDateTimeStart)
	_, allDay, err := propTime(startProp, time.UTC)
	if err != nil {
		return errNoEvent
	}

	exdate := ical.NewProp(ical.PropExceptionDates)
	if allDay {
		exdate.SetDate(recurrenceID)
	} else {
		exdate.SetDateTime(recurrenceID.UTC())
	}
	ev.Props.Add(exdate)

	// Drop the override of that occurrence, if any
	children := cal.Children[:0]
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent && child.Props.Get(ical.PropRecurrenceID) != nil {
			if t, _, err := propTime(child.Props.Get(ical.PropRecurrenceID), time.UTC); err == nil && t.Equal(recurrenceID) {
				continue
			}
		}
		children = append(children, child)
	}
	cal.Children = children

	stamp := ical.NewProp(ical.PropDateTimeStamp)
	stamp.SetDateTime(now.UTC())
	ev.Props.Set(stamp)
	return nil
}

func formatOffset(seconds int) string {
	sign := '+'
	if seconds < 0 {
		sign = '-'
		seconds = -seconds
	}
	return fmt.Sprintf("%c%02d%02d", sign, seconds/3600, seconds%3600/60)
}

// vtimezone describes a time zone as a VTIMEZONE, with the rules of its daylight saving time
// changes in the given year.
func vtimezone(loc *time.Location, year int) *ical.Component {
	tz := ical.NewComponent(ical.CompTimezone)
	tz.Props.SetText(ical.PropTimezoneID, loc.String())

	jan1 := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	var observances []*ical.Component
	t := jan1
	for range 4 {
		_, boundary := t.ZoneBounds()
		if boundary.IsZero() || boundary.Year() != year {
			break
		}
		_, offsetFrom := boundary.Add(-time.Second).Zone()
		name, offsetTo := boundary.Zone()

		kind := ical.CompTimezoneStandard
		if boundary.IsDST() {
			kind = ical.CompTimezoneDaylight
		}
		observance := ical.NewComponent(kind)
		local := boundary.UTC().Add(time.Duration(offsetFrom) * time.Second)
		setRaw(observance.Props, ical.PropDateTimeStart, local.Format("20060102T150405"))
		setRaw(observance.Props, ical.PropTimezoneOffsetFrom, formatOffset(offsetFrom))
		setRaw(observance.Props, ical.PropTimezoneOffsetTo, formatOffset(offsetTo))
		observance.Props.SetText(ical.PropTimezoneName, name)

		// The same weekday of the month every year: the nth, or the last one
		daysInMonth := time.Date(local.Year(), local.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		nth := strconv.Itoa((local.Day()-1)/7 + 1)
		if local.Day()+7 > daysInMonth {
			nth = "-1"
		}
		rule := ical.NewProp(ical.PropRecurrenceRule)
		rule.SetValueType(ical.ValueRecurrence)
		rule.Value = fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%s%s", int(local.Month()), nth, strings.ToUpper(local.Weekday().String()[:2]))
		observance.Props.Set(rule)

		observances = append(observances, observance)
		t = boundary
	}

	if len(observances) == 0 {
		name, offset := jan1.Zone()
		standard := ical.NewComponent(ical.CompTimezoneStandard)
		setRaw(standard.Props, ical.PropDateTimeStart, "19700101T000000")
		setRaw(standard.Props, ical.PropTimezoneOffsetFrom, formatOffset(offset))
		setRaw(standard.Props, ical.PropTimezoneOffsetTo, formatOffset(offset))
		standard.Props.SetText(ical.PropTimezoneName, name)
		observances = append(observances, standard)
	}
	tz.Children = observances
	return tz
}
