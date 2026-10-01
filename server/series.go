// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// What an edit of an occurrence of a recurring event applies to
const (
	// ScopeThis edits the occurrence alone, with an override (RECURRENCE-ID).
	ScopeThis = "this"
	// ScopeFollowing edits the occurrence and the following ones: the series ends before it, and
	// a new series starts with it.
	ScopeFollowing = "following"
)

// errFirstOccurrence is returned when a series would be split at its first occurrence: the whole
// series is edited then.
var errFirstOccurrence = newAPIError(http.StatusBadRequest, "this is the first occurrence of the series")

// recurringMaster returns the master event of a recurring event.
func recurringMaster(cal *ical.Calendar) (*ical.Component, error) {
	ev := masterEvent(cal)
	if ev == nil || ev.Props.Get(ical.PropDateTimeStart) == nil ||
		ev.Props.Get(ical.PropRecurrenceRule) == nil && ev.Props.Get(ical.PropRecurrenceDates) == nil {
		return nil, errNoEvent
	}
	return ev, nil
}

// findOverride returns the override of an occurrence of a recurring event, nil if it has none.
func findOverride(cal *ical.Calendar, recurrenceID time.Time, loc *time.Location) *ical.Component {
	for _, child := range cal.Children {
		if child.Name != ical.CompEvent || child.Props.Get(ical.PropRecurrenceID) == nil {
			continue
		}
		if t, _, err := propTime(child.Props.Get(ical.PropRecurrenceID), loc); err == nil && t.Equal(recurrenceID) {
			return child
		}
	}
	return nil
}

// recurrenceIDProp returns the RECURRENCE-ID of an occurrence, written like the start of its series
// (RFC 5545 section 3.8.4.4): a date, a UTC time, or a time in the series' time zone.
func recurrenceIDProp(start *ical.Prop, recurrenceID time.Time, loc *time.Location) *ical.Prop {
	prop := ical.NewProp(ical.PropRecurrenceID)
	startTime, allDay, err := propTime(start, loc)
	switch {
	case allDay:
		prop.SetDate(recurrenceID.In(loc))
	case err != nil || strings.HasSuffix(strings.TrimSpace(start.Value), "Z"):
		prop.Value = recurrenceID.UTC().Format("20060102T150405Z")
	default:
		// The time zone as the series names it, or a floating time
		if tzid := start.Params.Get(ical.PropTimezoneID); tzid != "" {
			prop.Params.Set(ical.PropTimezoneID, tzid)
		}
		prop.Value = recurrenceID.In(startTime.Location()).Format("20060102T150405")
	}
	return prop
}

// bumpSequence increments the SEQUENCE of an event, which tells other clients it changed.
func bumpSequence(ev *ical.Component) {
	sequence := 0
	if prop := ev.Props.Get(ical.PropSequence); prop != nil {
		sequence, _ = strconv.Atoi(prop.Value)
	}
	seq := ical.NewProp(ical.PropSequence)
	seq.SetValueType(ical.ValueInt)
	seq.Value = strconv.Itoa(sequence + 1)
	ev.Props.Set(seq)
}

// overrideOccurrence applies an edit to one occurrence of a recurring event, with an override: a
// copy of the series' event for that occurrence, identified by its original start.
func overrideOccurrence(cal *ical.Calendar, recurrenceID time.Time, loc *time.Location, in *EventInput, now time.Time, permalink string) error {
	master, err := recurringMaster(cal)
	if err != nil {
		return err
	}
	ev := findOverride(cal, recurrenceID, loc)
	if ev == nil {
		ev = ical.NewComponent(ical.CompEvent)
		for name, props := range master.Props {
			switch name {
			case ical.PropRecurrenceRule, ical.PropExceptionDates, ical.PropRecurrenceDates, "EXRULE":
				continue
			}
			ev.Props[name] = append([]ical.Prop(nil), props...)
		}
		ev.Props.Set(recurrenceIDProp(master.Props.Get(ical.PropDateTimeStart), recurrenceID, loc))
		cal.Children = append(cal.Children, ev)
	}

	// An override doesn't repeat
	in.Recurrence = RecurrenceNone
	start, end, tzLoc, err := in.times()
	if err != nil {
		return err
	}
	applyInput(ev, in, start, end, now, permalink)
	bumpSequence(ev)
	if !in.AllDay {
		ensureTimeZone(cal, tzLoc, start.Year())
	}
	return nil
}

// ruleWithout returns the parts of a recurrence rule other than the given ones.
func ruleWithout(value string, names ...string) []string {
	var parts []string
	for part := range strings.SplitSeq(value, ";") {
		name, _, _ := strings.Cut(part, "=")
		keep := part != ""
		for _, n := range names {
			if strings.EqualFold(name, n) {
				keep = false
			}
		}
		if keep {
			parts = append(parts, part)
		}
	}
	return parts
}

// endSeriesBefore ends a recurring event before one of its occurrences, and drops the overrides
// of that occurrence and the following ones. It returns the rule the following occurrences
// repeat with, for a series that continues it: what's left of a COUNT, or the same UNTIL.
func endSeriesBefore(cal *ical.Calendar, recurrenceID time.Time, loc *time.Location, now time.Time) (string, error) {
	master, err := recurringMaster(cal)
	if err != nil {
		return "", err
	}
	ruleProp := master.Props.Get(ical.PropRecurrenceRule)
	if ruleProp == nil {
		return "", errNoEvent
	}
	start, allDay, err := propTime(master.Props.Get(ical.PropDateTimeStart), loc)
	if err != nil {
		return "", errNoEvent
	}
	if !recurrenceID.After(start) {
		return "", errFirstOccurrence
	}
	rule, err := master.Props.RecurrenceRule()
	if err != nil || rule == nil {
		return "", newAPIError(http.StatusUnprocessableEntity, "the repeat rule of this event can't be read")
	}

	parts := ruleWithout(ruleProp.Value, "COUNT", "UNTIL")
	remaining := strings.Join(parts, ";")
	switch {
	case rule.Count > 0:
		rule.Dtstart = start
		r, err := rrule.NewRRule(*rule)
		if err != nil {
			return "", newAPIError(http.StatusUnprocessableEntity, "the repeat rule of this event can't be read")
		}
		left := rule.Count - len(r.Between(start, recurrenceID.Add(-time.Second), true))
		remaining += ";COUNT=" + strconv.Itoa(max(left, 1))
	case !rule.Until.IsZero():
		for part := range strings.SplitSeq(ruleProp.Value, ";") {
			if name, _, _ := strings.Cut(part, "="); strings.EqualFold(name, "UNTIL") {
				remaining += ";" + part
			}
		}
	}

	// The last occurrence is the one before: for all-day events, the day before
	until := recurrenceID.Add(-time.Second).UTC().Format("20060102T150405Z")
	if allDay {
		until = recurrenceID.In(loc).AddDate(0, 0, -1).Format("20060102")
	}
	ended := ical.NewProp(ical.PropRecurrenceRule)
	ended.SetValueType(ical.ValueRecurrence)
	ended.Value = strings.Join(append(parts, "UNTIL="+until), ";")
	master.Props.Set(ended)

	children := cal.Children[:0]
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent && child.Props.Get(ical.PropRecurrenceID) != nil {
			if t, _, err := propTime(child.Props.Get(ical.PropRecurrenceID), loc); err == nil && !t.Before(recurrenceID) {
				continue
			}
		}
		children = append(children, child)
	}
	cal.Children = children

	bumpSequence(master)
	stamp := ical.NewProp(ical.PropDateTimeStamp)
	stamp.SetDateTime(now.UTC())
	master.Props.Set(stamp)
	return remaining, nil
}

// continueSeries returns the object of a new series that continues a recurring event from one of
// its occurrences, with the edit applied. rule is the rule the series repeated with, kept when
// the editor can't show it.
func continueSeries(in *EventInput, rule, uid string, now time.Time, permalink string) (*ical.Calendar, error) {
	cal, err := newEventObject(in, uid, now, permalink)
	if err != nil {
		return nil, err
	}
	if in.Recurrence == RecurrenceCustom && rule != "" {
		prop := ical.NewProp(ical.PropRecurrenceRule)
		prop.SetValueType(ical.ValueRecurrence)
		prop.Value = rule
		masterEvent(cal).Props.Set(prop)
	}
	return cal, nil
}

// occurrenceDetails returns one occurrence of a recurring event as the editor shows it: its
// override if it has one, or the series' event at that time. Its recurrence is the series'.
func occurrenceDetails(cal *ical.Calendar, recurrenceID time.Time, loc *time.Location) (*EventDetails, error) {
	master, err := recurringMaster(cal)
	if err != nil {
		return nil, err
	}
	series, err := componentDetails(master, loc)
	if err != nil {
		return nil, err
	}

	var details *EventDetails
	if override := findOverride(cal, recurrenceID, loc); override != nil {
		if details, err = componentDetails(override, loc); err != nil {
			return nil, err
		}
	} else {
		details = series
		start, end, allDay, err := eventTimes(master, loc)
		if err != nil {
			return nil, newAPIError(http.StatusUnprocessableEntity, "this event can't be read")
		}
		if allDay {
			days := int(end.Sub(start).Round(24*time.Hour) / (24 * time.Hour))
			day := recurrenceID.In(loc)
			details.Start, details.End = day.Format(dateFormat), day.AddDate(0, 0, days).Format(dateFormat)
		} else {
			occurrenceStart := recurrenceID.In(start.Location())
			details.Start, details.End = occurrenceStart.Format(time.RFC3339), occurrenceStart.Add(end.Sub(start)).Format(time.RFC3339)
		}
	}
	details.Recurrence = series.Recurrence
	details.RecurrenceID = recurrenceID.UnixMilli()
	return details, nil
}
