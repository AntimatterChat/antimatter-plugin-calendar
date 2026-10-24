// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// meetingKeyPrefix prefixes the key of each scheduled meeting.
	meetingKeyPrefix = "meeting_"
	// teamIndexKeyPrefix prefixes the key of each community's index of meeting IDs (for the
	// community calendar view and the channel header's next call, so they don't have to scan
	// every meeting in the store). Deliberately not under meetingKeyPrefix, or ListMeetingIDs
	// would take an index for a meeting. The index is only a hint: every read checks each
	// meeting's channel, community and cancellation again, so an entry left behind by a failed
	// update never shows anything it shouldn't.
	teamIndexKeyPrefix = "meetidx_team_"

	// meetingKeepAfter is how long a meeting (and the RSVPs on it) is kept in the store after it
	// ends, so the post can still show who was going after the fact. A series that never ends (no
	// COUNT or UNTIL) has no TTL until it's cancelled, since there's no "after it ends"; it then
	// gets the same TTL as any other cancelled meeting.
	meetingKeepAfter = 30 * 24 * time.Hour
	meetingMinTTL    = 24 * time.Hour

	maxMeetingSummaryLength     = 1000
	maxMeetingDescriptionLength = 4000
	minMeetingDuration          = time.Minute
	maxMeetingDuration          = 24 * time.Hour

	// defaultReminderMinutes is used for a meeting whose ReminderMinutes is unset: nil, as for
	// every meeting stored before the per-call reminder choice existed, or a user override that
	// was cleared ("Default").
	defaultReminderMinutes = 10
	// noReminderMinutes marks "no reminder" (ReminderMinutes or an override set to it).
	noReminderMinutes = -1
)

// reminderChoicesMinutes are the only reminder times a meeting, or a member's override of it, may
// be set to: none, at start, 5/10/15/30 minutes, 1 hour, 1 day before.
var reminderChoicesMinutes = []int{noReminderMinutes, 0, 5, 10, 15, 30, 60, 1440}

func validReminderMinutes(minutes int) bool {
	return slices.Contains(reminderChoicesMinutes, minutes)
}

// RSVPStatus is how a member answered an invitation to a scheduled meeting (or one occurrence of
// a recurring one).
type RSVPStatus string

const (
	RSVPGoing RSVPStatus = "going"
	RSVPMaybe RSVPStatus = "maybe"
	RSVPNo    RSVPStatus = "no"
)

func validRSVPStatus(status RSVPStatus) bool {
	switch status {
	case RSVPGoing, RSVPMaybe, RSVPNo:
		return true
	default:
		return false
	}
}

// RSVP is one member's answer to a meeting, and when they gave it.
type RSVP struct {
	Status    RSVPStatus `json:"status"`
	UpdatedAt int64      `json:"updated_at"`
}

// occurrenceRSVP overrides a member's series-level RSVP for one occurrence only: the one that was
// "next" when they answered. Once that occurrence is in the past, a later occurrence's effective
// RSVP no longer matches OccurrenceStart, so it falls back to the series-level one again, with no
// separate cleanup needed.
type occurrenceRSVP struct {
	RSVP
	OccurrenceStart int64 `json:"occurrence_start"`
}

// Meeting is a call scheduled for everyone in a channel: a post in the channel (the record
// everyone sees and answers from) plus this plugin's own copy, which keeps the organiser, the
// RSVPs and the iTIP SEQUENCE across edits and cancellations. It's independent of anyone's
// personal calendar: members who connect one and want invites also get this meeting by email.
type Meeting struct {
	// ID identifies the meeting, and is also the local part of its iCalendar UID (see
	// meetingUID): stable across edits, so an iTIP update or cancellation refers to the same
	// event as the original invitation.
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	// TeamID is the channel's community, cached for the per-community index (teamIndexKey).
	// A channel moved to another community fires no hook an app can see, so the move is caught
	// on reads (the community list finds the channel elsewhere) and by an hourly pass
	// (reindexMovedMeetings), and the meeting is then re-indexed (MoveMeetingTeam).
	TeamID      string `json:"team_id,omitempty"`
	PostID      string `json:"post_id"`
	OrganizerID string `json:"organizer_id"`

	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	// Start and End are the series' anchor occurrence (its only occurrence, when it doesn't
	// repeat), in milliseconds. Every occurrence lasts End-Start.
	Start int64 `json:"start"`
	End   int64 `json:"end"`

	// TimeZone is the IANA zone a recurring meeting's occurrences are expanded in (so a weekly
	// call keeps its local time of day across a DST change); empty means UTC. Not used for a
	// one-off meeting, whose Start/End are already absolute instants.
	TimeZone string `json:"time_zone,omitempty"`
	// RecurrenceRule is an RFC 5545 RRULE value ("FREQ=WEEKLY;BYDAY=MO,WE"), empty for a one-off
	// meeting.
	RecurrenceRule string `json:"recurrence_rule,omitempty"`
	// ExceptionDates are EXDATE: the original starts (milliseconds) of occurrences cancelled on
	// their own, without cancelling the series.
	ExceptionDates []int64 `json:"exception_dates,omitempty"`
	// PostedOccurrenceStart and PostedOccurrenceEnd are the occurrence the shared post currently
	// shows; 0 means the series' anchor (Start/End) still hasn't rolled over to a later one.
	PostedOccurrenceStart int64 `json:"posted_occurrence_start,omitempty"`
	PostedOccurrenceEnd   int64 `json:"posted_occurrence_end,omitempty"`

	// ReminderMinutes is how long before each occurrence its reminder is sent, chosen from
	// reminderChoicesMinutes; nil means a meeting stored before this choice existed, or simply
	// unset, which keeps the previous fixed default (defaultReminderMinutes).
	ReminderMinutes *int `json:"reminder_minutes,omitempty"`
	// ReminderOverrides is each member's own reminder choice for this meeting, keyed by user ID,
	// overriding ReminderMinutes for them only; a member without an entry here uses the meeting's
	// own setting. Kept with the meeting (not the RSVP), since it doesn't change per occurrence.
	ReminderOverrides map[string]int `json:"reminder_overrides,omitempty"`

	// Sequence is the iTIP SEQUENCE: it goes up every time the meeting's time, title, description
	// or recurrence changes, or an occurrence or the series is cancelled, so calendar apps know a
	// later message updates (rather than duplicates or predates) an earlier one. Kept as one
	// counter for the whole series, not one per occurrence.
	Sequence int `json:"sequence"`

	Cancelled bool `json:"cancelled"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`

	// RSVPs is the series-level answer of each member, keyed by user ID: "an answer applies to
	// the whole series" by default.
	RSVPs map[string]RSVP `json:"rsvps,omitempty"`
	// NextRSVPs is a member's override of their own RSVP for one occurrence only (see
	// occurrenceRSVP), keyed by user ID.
	NextRSVPs map[string]occurrenceRSVP `json:"next_rsvps,omitempty"`
}

// recurring returns whether a meeting is a series (as opposed to a one-off meeting).
func (m *Meeting) recurring() bool {
	return m.RecurrenceRule != ""
}

// meetingReminderMinutes returns the meeting's own reminder choice, ignoring any member's
// override: the fixed default for one saved before the choice existed.
func (m *Meeting) meetingReminderMinutes() int {
	if m.ReminderMinutes != nil {
		return *m.ReminderMinutes
	}
	return defaultReminderMinutes
}

// effectiveReminderMinutes returns how long before an occurrence a member's reminder is sent:
// their own override if they set one, else the meeting's own setting (see meetingReminderMinutes).
func (m *Meeting) effectiveReminderMinutes(userID string) int {
	if minutes, ok := m.ReminderOverrides[userID]; ok {
		return minutes
	}
	return m.meetingReminderMinutes()
}

// effectiveRSVP returns a member's RSVP for a specific occurrence: their override of it if they
// set one for that exact occurrence, else their series-level RSVP.
func (m *Meeting) effectiveRSVP(userID string, occurrenceStart int64) (RSVP, bool) {
	if next, ok := m.NextRSVPs[userID]; ok && next.OccurrenceStart == occurrenceStart {
		return next.RSVP, true
	}
	rsvp, ok := m.RSVPs[userID]
	return rsvp, ok
}

func meetingKey(id string) string {
	return meetingKeyPrefix + id
}

func teamIndexKey(teamID string) string {
	return teamIndexKeyPrefix + teamID
}

var (
	errMeetingNotFound  = newAPIError(http.StatusNotFound, "meeting not found")
	errMeetingCancelled = newAPIError(http.StatusConflict, "this meeting was cancelled")
)

// validateMeetingInput checks the title, description and time range of a meeting (one occurrence
// of it, for a recurring one).
func validateMeetingInput(summary, description string, start, end time.Time) error {
	if strings.TrimSpace(summary) == "" {
		return newAPIError(http.StatusBadRequest, "the meeting needs a title")
	}
	if len(summary) > maxMeetingSummaryLength || len(description) > maxMeetingDescriptionLength {
		return newAPIError(http.StatusBadRequest, "the meeting's text is too long")
	}
	if !end.After(start) {
		return newAPIError(http.StatusBadRequest, "the meeting must end after it starts")
	}
	duration := end.Sub(start)
	if duration < minMeetingDuration || duration > maxMeetingDuration {
		return newAPIError(http.StatusBadRequest, "invalid meeting duration")
	}
	return nil
}

// meetingTTL is how long a meeting's key is kept, counted from now. endMillis is the last
// occurrence's end for a series that has one, or 0 for one that never ends (no TTL yet: the key
// is kept until the series is cancelled).
func meetingTTL(endMillis int64) int64 {
	if endMillis == 0 {
		return 0
	}
	ttl := max(time.Until(time.UnixMilli(endMillis).Add(meetingKeepAfter)), meetingMinTTL)
	return int64(ttl / time.Second)
}

// kvSetOptions returns the options a meeting is saved with: no expiry while it's an endless
// series that isn't cancelled yet, otherwise one counted from its last occurrence (or now, once
// cancelled).
func (m *Meeting) kvSetOptions(lastOccurrenceEnd int64) model.PluginKVSetOptions {
	if !m.Cancelled && m.recurring() && lastOccurrenceEnd == 0 {
		return model.PluginKVSetOptions{}
	}
	end := lastOccurrenceEnd
	if m.Cancelled || end == 0 {
		end = model.GetMillis()
	}
	return model.PluginKVSetOptions{ExpireInSeconds: meetingTTL(end)}
}

// CreateMeeting stores a new meeting, and indexes it under its community.
func (s *Store) CreateMeeting(m *Meeting) error {
	data, err := json.Marshal(m)
	if err != nil {
		return errors.Wrap(err, "failed to encode the meeting")
	}
	options := m.kvSetOptions(seriesLastOccurrenceEnd(m))
	if _, appErr := s.kv.KVSetWithOptions(meetingKey(m.ID), data, options); appErr != nil {
		return errors.Wrap(appErr, "failed to save the meeting")
	}
	if m.TeamID != "" {
		if err := s.addToTeamIndex(m.TeamID, m.ID); err != nil {
			return err
		}
	}
	return nil
}

// GetMeeting returns a meeting, nil if it doesn't exist (any more).
func (s *Store) GetMeeting(id string) (*Meeting, error) {
	data, appErr := s.kv.KVGet(meetingKey(id))
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to read the meeting")
	}
	if data == nil {
		return nil, nil
	}
	var m Meeting
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, errors.Wrap(err, "failed to decode the meeting")
	}
	return &m, nil
}

// UpdateMeeting applies update to a stored meeting and saves it, retrying if another request
// changes the meeting at the same time (for example two RSVPs, or an edit and an RSVP, at once).
func (s *Store) UpdateMeeting(id string, update func(*Meeting) error) (*Meeting, error) {
	key := meetingKey(id)
	for range atomicUpdateAttempts {
		old, appErr := s.kv.KVGet(key)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to read the meeting")
		}
		if old == nil {
			return nil, errMeetingNotFound
		}
		var m Meeting
		if err := json.Unmarshal(old, &m); err != nil {
			return nil, errors.Wrap(err, "failed to decode the meeting")
		}
		if err := update(&m); err != nil {
			return nil, err
		}
		data, err := json.Marshal(m)
		if err != nil {
			return nil, errors.Wrap(err, "failed to encode the meeting")
		}
		options := m.kvSetOptions(seriesLastOccurrenceEnd(&m))
		options.Atomic, options.OldValue = true, old
		saved, appErr := s.kv.KVSetWithOptions(key, data, options)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to save the meeting")
		}
		if saved {
			return &m, nil
		}
		// Changed by someone else between the read and the write: read it again and retry.
	}
	return nil, errors.New("failed to update the meeting, try again")
}

// ListMeetingIDs returns the IDs of every meeting in the store (ended ones included, until their
// key expires).
func (s *Store) ListMeetingIDs() ([]string, error) {
	const perPage = 1000
	var ids []string
	for page := 0; ; page++ {
		keys, appErr := s.kv.KVList(page, perPage)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to list the meetings")
		}
		for _, key := range keys {
			if id, ok := strings.CutPrefix(key, meetingKeyPrefix); ok {
				ids = append(ids, id)
			}
		}
		if len(keys) < perPage {
			return ids, nil
		}
	}
}

// addToTeamIndex adds a meeting to its community's index, if it isn't there yet.
func (s *Store) addToTeamIndex(teamID, meetingID string) error {
	return s.updateTeamIndex(teamID, func(ids []string) []string {
		if slices.Contains(ids, meetingID) {
			return ids
		}
		return append(ids, meetingID)
	})
}

// removeFromTeamIndex removes a meeting from its community's index (once it's cancelled: the
// community calendar view never shows a cancelled meeting).
func (s *Store) removeFromTeamIndex(teamID, meetingID string) error {
	return s.updateTeamIndex(teamID, func(ids []string) []string {
		kept := ids[:0]
		for _, id := range ids {
			if id != meetingID {
				kept = append(kept, id)
			}
		}
		return kept
	})
}

// updateTeamIndex applies change to a community's index of meeting IDs and saves it, retrying on
// a concurrent change (the same optimistic-concurrency pattern as UpdateMeeting).
func (s *Store) updateTeamIndex(teamID string, change func([]string) []string) error {
	key := teamIndexKey(teamID)
	for range atomicUpdateAttempts {
		old, appErr := s.kv.KVGet(key)
		if appErr != nil {
			return errors.Wrap(appErr, "failed to read the community's scheduled calls")
		}
		var ids []string
		if old != nil {
			if err := json.Unmarshal(old, &ids); err != nil {
				return errors.Wrap(err, "failed to decode the community's scheduled calls")
			}
		}
		updated := change(ids)
		data, err := json.Marshal(updated)
		if err != nil {
			return errors.Wrap(err, "failed to encode the community's scheduled calls")
		}
		saved, appErr := s.kv.KVSetWithOptions(key, data, model.PluginKVSetOptions{Atomic: true, OldValue: old})
		if appErr != nil {
			return errors.Wrap(appErr, "failed to save the community's scheduled calls")
		}
		if saved {
			return nil
		}
	}
	return errors.New("failed to update the community's scheduled calls, try again")
}

// ListTeamMeetings returns the meetings indexed under a community, dropping (and forgetting)
// any ID whose meeting has expired or been deleted, so a stale index entry left by a TTL expiry
// doesn't linger.
func (s *Store) ListTeamMeetings(teamID string) ([]*Meeting, error) {
	data, appErr := s.kv.KVGet(teamIndexKey(teamID))
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to read the community's scheduled calls")
	}
	if data == nil {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, errors.Wrap(err, "failed to decode the community's scheduled calls")
	}

	var meetings []*Meeting
	var stale []string
	for _, id := range ids {
		m, err := s.GetMeeting(id)
		if err != nil {
			continue
		}
		if m == nil {
			stale = append(stale, id)
			continue
		}
		meetings = append(meetings, m)
	}
	for _, id := range stale {
		if err := s.removeFromTeamIndex(teamID, id); err != nil {
			// Best effort: left for the next read to try again.
			break
		}
	}
	return meetings, nil
}

// MoveMeetingTeam records that a meeting's channel is now in another community (or in none, for
// an empty newTeamID), and moves it from the old community's index to the new one's.
func (s *Store) MoveMeetingTeam(meetingID, newTeamID string) (*Meeting, error) {
	var oldTeamID string
	m, err := s.UpdateMeeting(meetingID, func(m *Meeting) error {
		oldTeamID = m.TeamID
		m.TeamID = newTeamID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if oldTeamID != "" && oldTeamID != newTeamID {
		if err := s.removeFromTeamIndex(oldTeamID, meetingID); err != nil {
			return m, err
		}
	}
	if newTeamID != "" && !m.Cancelled {
		if err := s.addToTeamIndex(newTeamID, meetingID); err != nil {
			return m, err
		}
	}
	return m, nil
}

// SetRSVP records a member's answer to a meeting: for the whole series (scope is ""), or just the
// occurrence starting at occurrenceStart ("next").
func (s *Store) SetRSVP(meetingID, userID string, status RSVPStatus, occurrenceStart int64) (*Meeting, error) {
	return s.UpdateMeeting(meetingID, func(m *Meeting) error {
		if m.Cancelled {
			return errMeetingCancelled
		}
		now := model.GetMillis()
		if occurrenceStart != 0 {
			if m.NextRSVPs == nil {
				m.NextRSVPs = map[string]occurrenceRSVP{}
			}
			m.NextRSVPs[userID] = occurrenceRSVP{RSVP: RSVP{Status: status, UpdatedAt: now}, OccurrenceStart: occurrenceStart}
			return nil
		}
		if m.RSVPs == nil {
			m.RSVPs = map[string]RSVP{}
		}
		m.RSVPs[userID] = RSVP{Status: status, UpdatedAt: now}
		return nil
	})
}

// SetReminderOverride sets (or, with minutes nil, clears: "Default") a member's own reminder
// choice for a meeting.
func (s *Store) SetReminderOverride(meetingID, userID string, minutes *int) (*Meeting, error) {
	return s.UpdateMeeting(meetingID, func(m *Meeting) error {
		if m.Cancelled {
			return errMeetingCancelled
		}
		if minutes == nil {
			delete(m.ReminderOverrides, userID)
			return nil
		}
		if m.ReminderOverrides == nil {
			m.ReminderOverrides = map[string]int{}
		}
		m.ReminderOverrides[userID] = *minutes
		return nil
	})
}

// EditMeeting applies the organiser's changes to a meeting's series, bumping its SEQUENCE if
// anything an invitation describes actually changed.
func (s *Store) EditMeeting(meetingID, summary, description string, start, end int64, timeZone, recurrenceRule string, reminderMinutes int) (*Meeting, error) {
	return s.UpdateMeeting(meetingID, func(m *Meeting) error {
		if m.Cancelled {
			return errMeetingCancelled
		}
		if m.Summary != summary || m.Description != description || m.Start != start || m.End != end ||
			m.TimeZone != timeZone || m.RecurrenceRule != recurrenceRule {
			m.Sequence++
			// The series' shape changed: start showing its (new) anchor occurrence again.
			m.PostedOccurrenceStart, m.PostedOccurrenceEnd = 0, 0
		}
		m.Summary, m.Description, m.Start, m.End = summary, description, start, end
		m.TimeZone, m.RecurrenceRule = timeZone, recurrenceRule
		m.ReminderMinutes = &reminderMinutes
		m.UpdatedAt = model.GetMillis()
		return nil
	})
}

// CancelMeeting marks a meeting's whole series as cancelled, bumping its SEQUENCE so iTIP
// cancellations are told apart from the invitations and updates that came before them, and drops
// it from its community's calendar index. Cancelling twice is a no-op.
func (s *Store) CancelMeeting(meetingID string) (*Meeting, error) {
	m, err := s.UpdateMeeting(meetingID, func(m *Meeting) error {
		if m.Cancelled {
			return nil
		}
		m.Cancelled = true
		m.Sequence++
		m.UpdatedAt = model.GetMillis()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if m.TeamID != "" {
		if indexErr := s.removeFromTeamIndex(m.TeamID, m.ID); indexErr != nil {
			return m, indexErr
		}
	}
	return m, nil
}

// CancelOccurrence adds one occurrence to a recurring meeting's exception dates (EXDATE), without
// cancelling the rest of the series.
func (s *Store) CancelOccurrence(meetingID string, occurrenceStart int64) (*Meeting, error) {
	return s.UpdateMeeting(meetingID, func(m *Meeting) error {
		if m.Cancelled {
			return errMeetingCancelled
		}
		if slices.Contains(m.ExceptionDates, occurrenceStart) {
			return nil
		}
		m.ExceptionDates = append(m.ExceptionDates, occurrenceStart)
		m.Sequence++
		m.UpdatedAt = model.GetMillis()
		return nil
	})
}

// RefreshPostedOccurrence records which occurrence a recurring meeting's shared post currently
// shows, without bumping its SEQUENCE: moving on to the next occurrence of an unchanged series
// isn't a change an iTIP client needs to hear about.
func (s *Store) RefreshPostedOccurrence(meetingID string, start, end int64) (*Meeting, error) {
	return s.UpdateMeeting(meetingID, func(m *Meeting) error {
		m.PostedOccurrenceStart, m.PostedOccurrenceEnd = start, end
		return nil
	})
}

// seriesLastOccurrenceEnd returns the end of a meeting's last occurrence for the KV TTL: 0 for a
// one-off meeting that already carries its own End, and 0 for a series with no COUNT or UNTIL
// (kvSetOptions then gives it no expiry).
func seriesLastOccurrenceEnd(m *Meeting) int64 {
	if !m.recurring() {
		return m.End
	}
	last, ok := lastMeetingOccurrence(m)
	if !ok {
		return 0
	}
	return last.Add(meetingDuration(m)).UnixMilli()
}
