// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"time"

	"github.com/teambition/rrule-go"
)

const (
	// maxMeetingOccurrences bounds how many occurrences of a series are ever expanded or
	// enumerated at once, so a request can't make the server expand without bound.
	maxMeetingOccurrences = 500
	// maxMeetingHorizon bounds how far ahead a series is ever expanded, and the latest UNTIL (or
	// implied end) it may have.
	maxMeetingHorizon = 2 * 365 * 24 * time.Hour
)

// meetingDuration is the fixed length of every occurrence of a meeting.
func meetingDuration(m *Meeting) time.Duration {
	return time.Duration(m.End-m.Start) * time.Millisecond
}

// meetingTimeZone returns the time zone a meeting's occurrences are expanded in (UTC if unset or
// invalid).
func meetingTimeZone(m *Meeting) *time.Location {
	if m.TimeZone != "" {
		if loc, err := time.LoadLocation(m.TimeZone); err == nil {
			return loc
		}
	}
	return time.UTC
}

// parseRecurrenceRule validates an RRULE for a meeting: it must parse, repeat no more often than
// daily (RFC 5545's FREQ), and stay within the bounds this plugin expands series to (COUNT and
// UNTIL included), so a request can't make the server expand without bound.
func parseRecurrenceRule(value string, dtstart time.Time) (*rrule.ROption, error) {
	if value == "" {
		return nil, nil
	}
	option, err := rrule.StrToROption(value)
	if err != nil || option == nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid recurrence rule")
	}
	switch option.Freq {
	case rrule.DAILY, rrule.WEEKLY, rrule.MONTHLY, rrule.YEARLY:
	default:
		return nil, newAPIError(http.StatusBadRequest, "the call can't repeat more often than daily")
	}
	if option.Count < 0 || option.Count > maxMeetingOccurrences {
		return nil, newAPIError(http.StatusBadRequest, "invalid recurrence count")
	}
	if !option.Until.IsZero() && option.Until.After(dtstart.Add(maxMeetingHorizon)) {
		return nil, newAPIError(http.StatusBadRequest, "the recurrence ends too far in the future")
	}
	return option, nil
}

// meetingOccurrenceSet returns the recurrence set of a meeting's series (nil for a one-off
// meeting).
func meetingOccurrenceSet(m *Meeting) (*rrule.Set, error) {
	if !m.recurring() {
		return nil, nil
	}
	loc := meetingTimeZone(m)
	dtstart := time.UnixMilli(m.Start).In(loc)
	option, err := parseRecurrenceRule(m.RecurrenceRule, dtstart)
	if err != nil {
		return nil, err
	}
	option.Dtstart = dtstart
	r, err := rrule.NewRRule(*option)
	if err != nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid recurrence rule")
	}
	set := &rrule.Set{}
	set.RRule(r)
	set.DTStart(dtstart)
	for _, ex := range m.ExceptionDates {
		set.ExDate(time.UnixMilli(ex).In(loc))
	}
	return set, nil
}

// meetingOccurrences returns the starts of a meeting's occurrences overlapping [from, to),
// bounded to maxMeetingOccurrences and maxMeetingHorizon.
func meetingOccurrences(m *Meeting, from, to time.Time) ([]time.Time, error) {
	duration := meetingDuration(m)
	if !m.recurring() {
		start := time.UnixMilli(m.Start)
		if start.Before(to) && start.Add(duration).After(from) {
			return []time.Time{start}, nil
		}
		return nil, nil
	}
	set, err := meetingOccurrenceSet(m)
	if err != nil {
		return nil, err
	}
	if horizon := from.Add(maxMeetingHorizon); to.After(horizon) {
		to = horizon
	}

	var starts []time.Time
	next := set.Iterator()
	for range maxMeetingOccurrences {
		t, ok := next()
		if !ok || !t.Before(to) {
			break
		}
		if t.Add(duration).After(from) {
			starts = append(starts, t)
		}
	}
	return starts, nil
}

// nextMeetingOccurrence returns the occurrence of a meeting that's current or next at now: the
// one that hasn't ended yet. ok is false once the series (or the one-off meeting) is over, or it
// has no occurrence left within maxMeetingHorizon.
func nextMeetingOccurrence(m *Meeting, now time.Time) (start, end time.Time, ok bool) {
	duration := meetingDuration(m)
	starts, err := meetingOccurrences(m, now.Add(-duration), now.Add(maxMeetingHorizon))
	if err != nil || len(starts) == 0 {
		return start, end, false
	}
	for _, s := range starts {
		if e := s.Add(duration); e.After(now) {
			return s, e, true
		}
	}
	return start, end, false
}

// lastMeetingOccurrence returns the last occurrence of a bounded series (one with a COUNT or an
// UNTIL). ok is false for a one-off meeting (its own End is used instead) or a series with
// neither, which never ends on its own.
func lastMeetingOccurrence(m *Meeting) (last time.Time, ok bool) {
	if !m.recurring() {
		return last, false
	}
	loc := meetingTimeZone(m)
	dtstart := time.UnixMilli(m.Start).In(loc)
	option, err := parseRecurrenceRule(m.RecurrenceRule, dtstart)
	if err != nil || option == nil {
		return last, false
	}
	if option.Count == 0 && option.Until.IsZero() {
		return last, false
	}
	set, err := meetingOccurrenceSet(m)
	if err != nil || set == nil {
		return last, false
	}
	found := set.Before(dtstart.Add(maxMeetingHorizon), true)
	if found.IsZero() {
		return last, false
	}
	return found, true
}
