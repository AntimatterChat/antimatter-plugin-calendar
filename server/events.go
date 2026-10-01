// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

const (
	// maxRecurrenceSteps bounds the work of expanding one recurring event.
	maxRecurrenceSteps = 20000

	dateFormat = "2006-01-02"

	// The properties linking events to a channel of this server
	propChannelID = "X-ANTIMATTER-CHANNEL-ID"
	propLinkKind  = "X-ANTIMATTER-LINK"
)

// Link kinds
const (
	// LinkChannel links an event to a channel, voice channels included: joining the event opens
	// the channel, or joins the voice channel.
	LinkChannel = "channel"
	// LinkCall links an event to a call in a channel: joining the event starts or joins the call.
	LinkCall = "call"
)

// Recurrences the event editor offers
const (
	RecurrenceNone    = ""
	RecurrenceDaily   = "daily"
	RecurrenceWeekly  = "weekly"
	RecurrenceMonthly = "monthly"
	RecurrenceYearly  = "yearly"
	// RecurrenceCustom is a rule the editor can't show, kept as it is.
	RecurrenceCustom = "custom"
)

// EventLink links an event to a channel or a call of this server.
type EventLink struct {
	ChannelID string `json:"channel_id"`
	Kind      string `json:"kind"`

	// Filled in from the channel when the user can read it
	ChannelName string `json:"channel_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
	TeamName    string `json:"team_name,omitempty"`
}

// Occurrence is an occurrence of an event in a time range: the event itself, or one of its
// recurrences.
type Occurrence struct {
	// ID identifies the occurrence: its object, and its recurrence.
	ID       string `json:"id"`
	Calendar string `json:"calendar"`
	Path     string `json:"path"`
	ETag     string `json:"etag"`
	UID      string `json:"uid"`

	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`

	AllDay bool `json:"all_day"`
	// Start and End are the occurrence's instants, in milliseconds. All-day events start and end
	// at midnight in the time zone they were expanded in.
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	// StartDate and EndDate are the dates of all-day events (the end is exclusive).
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`

	// Recurrence is how the event repeats, "" if it doesn't.
	Recurrence string `json:"recurrence,omitempty"`
	// RecurrenceID is the original start of the occurrence of a recurring event, which identifies
	// it within the series.
	RecurrenceID int64 `json:"recurrence_id,omitempty"`

	// Alarms are the minutes before the start at which the event's alarms ring.
	Alarms []int      `json:"alarms,omitempty"`
	Link   *EventLink `json:"link,omitempty"`
}

var tzidSuffix = regexp.MustCompile(`([A-Za-z_]+/[A-Za-z_\-+0-9]+(?:/[A-Za-z_\-+0-9]+)?)$`)

// windowsZones maps the Windows time zone names Exchange writes in TZIDs to IANA zones.
var windowsZones = map[string]string{
	"utc":                            "UTC",
	"gmt standard time":              "Europe/London",
	"w. europe standard time":        "Europe/Berlin",
	"romance standard time":          "Europe/Paris",
	"central europe standard time":   "Europe/Budapest",
	"central european standard time": "Europe/Warsaw",
	"e. europe standard time":        "Europe/Chisinau",
	"fle standard time":              "Europe/Kiev",
	"gtb standard time":              "Europe/Bucharest",
	"russian standard time":          "Europe/Moscow",
	"eastern standard time":          "America/New_York",
	"central standard time":          "America/Chicago",
	"mountain standard time":         "America/Denver",
	"pacific standard time":          "America/Los_Angeles",
	"tokyo standard time":            "Asia/Tokyo",
	"china standard time":            "Asia/Shanghai",
	"india standard time":            "Asia/Kolkata",
	"aus eastern standard time":      "Australia/Sydney",
}

// loadTZID returns the location of a TZID: an IANA name, possibly behind a vendor prefix
// ("/mozilla.org/20050126_1/Europe/Paris"), or a Windows name.
func loadTZID(tzid string) (*time.Location, bool) {
	tzid = strings.Trim(tzid, `"`)
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc, true
	}
	if m := tzidSuffix.FindString(tzid); m != "" {
		if loc, err := time.LoadLocation(m); err == nil {
			return loc, true
		}
	}
	if name, ok := windowsZones[strings.ToLower(tzid)]; ok {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, true
		}
	}
	return nil, false
}

// parseTime parses a DATE or DATE-TIME value. Floating times, and times in a time zone that isn't
// known, are in loc.
func parseTime(value string, params ical.Params, loc *time.Location) (t time.Time, allDay bool, err error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(params.Get(ical.ParamValue), "DATE") || len(value) == len("20060102") {
		t, err = time.ParseInLocation("20060102", value, loc)
		return t, true, err
	}
	if strings.HasSuffix(value, "Z") {
		t, err = time.ParseInLocation("20060102T150405Z", value, time.UTC)
		return t, false, err
	}
	if tzid := params.Get(ical.PropTimezoneID); tzid != "" {
		if tzLoc, ok := loadTZID(tzid); ok {
			loc = tzLoc
		}
	}
	t, err = time.ParseInLocation("20060102T150405", value, loc)
	return t, false, err
}

func propTime(prop *ical.Prop, loc *time.Location) (time.Time, bool, error) {
	return parseTime(prop.Value, prop.Params, loc)
}

// propTimes parses a property holding a list of times (EXDATE, RDATE).
func propTimes(prop *ical.Prop, loc *time.Location) []time.Time {
	var times []time.Time
	for value := range strings.SplitSeq(prop.Value, ",") {
		if t, _, err := parseTime(value, prop.Params, loc); err == nil {
			times = append(times, t)
		}
	}
	return times
}

func textProp(comp *ical.Component, name string) string {
	text, err := comp.Props.Text(name)
	if err != nil {
		if prop := comp.Props.Get(name); prop != nil {
			return prop.Value
		}
	}
	return text
}

// eventTimes returns the start and end of an event. Events without an end last a day when they
// last all day, and no time otherwise.
func eventTimes(ev *ical.Component, loc *time.Location) (start, end time.Time, allDay bool, err error) {
	startProp := ev.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return start, end, false, fmt.Errorf("event without a start")
	}
	start, allDay, err = propTime(startProp, loc)
	if err != nil {
		return start, end, false, fmt.Errorf("invalid event start: %w", err)
	}

	switch {
	case ev.Props.Get(ical.PropDateTimeEnd) != nil:
		end, _, err = propTime(ev.Props.Get(ical.PropDateTimeEnd), loc)
		if err != nil {
			return start, end, false, fmt.Errorf("invalid event end: %w", err)
		}
	case ev.Props.Get(ical.PropDuration) != nil:
		duration, durErr := ev.Props.Get(ical.PropDuration).Duration()
		if durErr != nil {
			return start, end, false, fmt.Errorf("invalid event duration: %w", durErr)
		}
		if allDay && duration%(24*time.Hour) == 0 {
			end = start.AddDate(0, 0, int(duration/(24*time.Hour)))
		} else {
			end = start.Add(duration)
		}
	case allDay:
		end = start.AddDate(0, 0, 1)
	default:
		end = start
	}
	if end.Before(start) {
		end = start
	}
	return start, end, allDay, nil
}

// recurrenceKind returns how an event repeats, as the event editor shows it.
func recurrenceKind(rule *rrule.ROption) string {
	if rule == nil {
		return RecurrenceNone
	}
	simple := rule.Interval <= 1 && rule.Count == 0 && rule.Until.IsZero() &&
		len(rule.Bysetpos) == 0 && len(rule.Bymonth) == 0 && len(rule.Bymonthday) == 0 &&
		len(rule.Byyearday) == 0 && len(rule.Byweekno) == 0 && len(rule.Byweekday) == 0 &&
		len(rule.Byhour) == 0 && len(rule.Byminute) == 0 && len(rule.Bysecond) == 0
	if !simple {
		return RecurrenceCustom
	}
	switch rule.Freq {
	case rrule.DAILY:
		return RecurrenceDaily
	case rrule.WEEKLY:
		return RecurrenceWeekly
	case rrule.MONTHLY:
		return RecurrenceMonthly
	case rrule.YEARLY:
		return RecurrenceYearly
	default:
		return RecurrenceCustom
	}
}

// alarms returns the minutes before the start at which the alarms of an event ring.
func alarms(ev *ical.Component, start, end time.Time, loc *time.Location) []int {
	var minutes []int
	for _, child := range ev.Children {
		if child.Name != ical.CompAlarm {
			continue
		}
		trigger := child.Props.Get(ical.PropTrigger)
		if trigger == nil {
			continue
		}
		var at time.Time
		if strings.EqualFold(trigger.Params.Get(ical.ParamValue), "DATE-TIME") {
			t, _, err := propTime(trigger, loc)
			if err != nil {
				continue
			}
			at = t
		} else {
			offset, err := trigger.Duration()
			if err != nil {
				continue
			}
			if strings.EqualFold(trigger.Params.Get(ical.ParamRelated), "END") {
				at = end.Add(offset)
			} else {
				at = start.Add(offset)
			}
		}
		before := int(start.Sub(at) / time.Minute)
		if before >= 0 {
			minutes = append(minutes, before)
		}
	}
	sort.Ints(minutes)
	return minutes
}

// eventLink returns the channel an event is linked to, if any.
func eventLink(ev *ical.Component) *EventLink {
	channelID := textProp(ev, propChannelID)
	if channelID == "" {
		return nil
	}
	kind := strings.ToLower(textProp(ev, propLinkKind))
	if kind != LinkCall {
		kind = LinkChannel
	}
	return &EventLink{ChannelID: channelID, Kind: kind}
}

func newOccurrence(calendar, path, etag string, ev *ical.Component, start, end time.Time, allDay bool, loc *time.Location) Occurrence {
	o := Occurrence{
		Calendar:    calendar,
		Path:        path,
		ETag:        etag,
		UID:         textProp(ev, ical.PropUID),
		Summary:     textProp(ev, ical.PropSummary),
		Description: textProp(ev, ical.PropDescription),
		Location:    textProp(ev, ical.PropLocation),
		AllDay:      allDay,
		Alarms:      alarms(ev, start, end, loc),
		Link:        eventLink(ev),
	}
	if allDay {
		// The dates hold, whatever the time zone of the values
		o.StartDate, o.EndDate = start.Format(dateFormat), end.Format(dateFormat)
		startDay, _ := time.ParseInLocation(dateFormat, o.StartDate, loc)
		endDay, _ := time.ParseInLocation(dateFormat, o.EndDate, loc)
		start, end = startDay, endDay
	}
	o.Start, o.End = start.UnixMilli(), end.UnixMilli()
	o.ID = fmt.Sprintf("%s#%d", path, o.Start)
	return o
}

func cancelled(ev *ical.Component) bool {
	return strings.EqualFold(textProp(ev, ical.PropStatus), string(ical.EventCancelled))
}

func overlaps(start, end, from, to time.Time) bool {
	if end.Equal(start) {
		return !start.Before(from) && start.Before(to)
	}
	return start.Before(to) && end.After(from)
}

// expandObject returns the occurrences of the events of a calendar object between from and to.
// Floating times and all-day events are in loc.
func expandObject(calendar, path, etag string, cal *ical.Calendar, from, to time.Time, loc *time.Location) []Occurrence {
	var occurrences []Occurrence

	// A recurring event is a master event and overrides of some of its occurrences, which carry
	// the original start of the occurrence they replace as RECURRENCE-ID.
	var masters []*ical.Component
	overridden := map[string]map[int64]bool{}
	for _, ev := range cal.Children {
		if ev.Name != ical.CompEvent {
			continue
		}
		recurrenceID := ev.Props.Get(ical.PropRecurrenceID)
		if recurrenceID == nil {
			masters = append(masters, ev)
			continue
		}

		uid := textProp(ev, ical.PropUID)
		if originalStart, _, err := propTime(recurrenceID, loc); err == nil {
			if overridden[uid] == nil {
				overridden[uid] = map[int64]bool{}
			}
			overridden[uid][originalStart.Unix()] = true
		}
		if cancelled(ev) {
			continue
		}
		start, end, allDay, err := eventTimes(ev, loc)
		if err != nil || !overlaps(start, end, from, to) {
			continue
		}
		o := newOccurrence(calendar, path, etag, ev, start, end, allDay, loc)
		if originalStart, _, err := propTime(recurrenceID, loc); err == nil {
			o.RecurrenceID = originalStart.UnixMilli()
		}
		o.Recurrence = RecurrenceCustom
		occurrences = append(occurrences, o)
	}

	for _, ev := range masters {
		if cancelled(ev) {
			continue
		}
		start, end, allDay, err := eventTimes(ev, loc)
		if err != nil {
			continue
		}
		rule, err := ev.Props.RecurrenceRule()
		if err != nil || rule == nil || rule.Freq == rrule.MINUTELY || rule.Freq == rrule.SECONDLY {
			// Not recurring (or too often to show each time)
			if overlaps(start, end, from, to) {
				occurrences = append(occurrences, newOccurrence(calendar, path, etag, ev, start, end, allDay, loc))
			}
			continue
		}

		kind := recurrenceKind(rule)
		uid := textProp(ev, ical.PropUID)
		for _, occurrenceStart := range recurrences(ev, rule, start, end.Sub(start), from, to, loc) {
			if overridden[uid][occurrenceStart.Unix()] {
				continue
			}
			var occurrenceEnd time.Time
			if allDay {
				occurrenceEnd = occurrenceStart.AddDate(0, 0, int(end.Sub(start).Round(24*time.Hour)/(24*time.Hour)))
			} else {
				occurrenceEnd = occurrenceStart.Add(end.Sub(start))
			}
			o := newOccurrence(calendar, path, etag, ev, occurrenceStart, occurrenceEnd, allDay, loc)
			o.Recurrence = kind
			o.RecurrenceID = occurrenceStart.UnixMilli()
			occurrences = append(occurrences, o)
		}
	}
	return occurrences
}

// recurrences returns the starts of the recurrences of an event that overlap [from, to).
func recurrences(ev *ical.Component, rule *rrule.ROption, start time.Time, duration time.Duration, from, to time.Time, loc *time.Location) []time.Time {
	rule.Dtstart = start
	r, err := rrule.NewRRule(*rule)
	if err != nil {
		return nil
	}
	set := rrule.Set{}
	set.RRule(r)
	set.DTStart(start)
	for _, prop := range ev.Props.Values(ical.PropExceptionDates) {
		for _, t := range propTimes(&prop, loc) {
			set.ExDate(t)
		}
	}
	for _, prop := range ev.Props.Values(ical.PropRecurrenceDates) {
		for _, t := range propTimes(&prop, loc) {
			set.RDate(t)
		}
	}

	var starts []time.Time
	next := set.Iterator()
	for range maxRecurrenceSteps {
		t, ok := next()
		if !ok || !t.Before(to) {
			break
		}
		if overlaps(t, t.Add(duration), from, to) {
			starts = append(starts, t)
		}
	}
	return starts
}

// sortOccurrences sorts occurrences by start, all-day events first.
func sortOccurrences(occurrences []Occurrence) {
	sort.SliceStable(occurrences, func(i, j int) bool {
		a, b := occurrences[i], occurrences[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.AllDay != b.AllDay {
			return a.AllDay
		}
		return a.Summary < b.Summary
	})
}
