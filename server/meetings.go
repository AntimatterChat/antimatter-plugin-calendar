// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// meetingKeyPrefix prefixes the key of each scheduled meeting.
	meetingKeyPrefix = "meeting_"

	// meetingKeepAfter is how long a meeting (and the RSVPs on it) is kept in the store after it
	// ends, so the post can still show who was going after the fact.
	meetingKeepAfter = 30 * 24 * time.Hour
	meetingMinTTL    = 24 * time.Hour

	maxMeetingSummaryLength     = 1000
	maxMeetingDescriptionLength = 4000
	minMeetingDuration          = time.Minute
	maxMeetingDuration          = 24 * time.Hour

	// meetingReminderMinutes is how long before a scheduled call its reminder is sent.
	meetingReminderMinutes = 10
)

// RSVPStatus is how a member answered an invitation to a scheduled meeting.
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

// Meeting is a call scheduled for everyone in a channel: a post in the channel (the record
// everyone sees and answers from) plus this plugin's own copy, which keeps the organiser, the
// RSVPs and the iTIP SEQUENCE across edits and cancellations. It's independent of anyone's
// personal calendar: members who connect one and want invites also get this meeting by email.
type Meeting struct {
	// ID identifies the meeting, and is also the local part of its iCalendar UID (see
	// meetingUID): stable across edits, so an iTIP update or cancellation refers to the same
	// event as the original invitation.
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	PostID      string `json:"post_id"`
	OrganizerID string `json:"organizer_id"`

	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	// Start and End are in milliseconds.
	Start int64 `json:"start"`
	End   int64 `json:"end"`

	// Sequence is the iTIP SEQUENCE: it goes up every time the meeting's time, title or
	// description changes, or it's cancelled, so calendar apps know a later message updates
	// (rather than duplicates or predates) an earlier one.
	Sequence int `json:"sequence"`

	Cancelled bool `json:"cancelled"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`

	// RSVPs is keyed by user ID.
	RSVPs map[string]RSVP `json:"rsvps,omitempty"`
}

func meetingKey(id string) string {
	return meetingKeyPrefix + id
}

var (
	errMeetingNotFound  = newAPIError(http.StatusNotFound, "meeting not found")
	errMeetingCancelled = newAPIError(http.StatusConflict, "this meeting was cancelled")
)

// validateMeetingInput checks the title, description and time range of a meeting.
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

// meetingTTL is how long a meeting's key is kept, counted from now.
func meetingTTL(endMillis int64) int64 {
	ttl := time.Until(time.UnixMilli(endMillis).Add(meetingKeepAfter))
	if ttl < meetingMinTTL {
		ttl = meetingMinTTL
	}
	return int64(ttl / time.Second)
}

// CreateMeeting stores a new meeting.
func (s *Store) CreateMeeting(m *Meeting) error {
	data, err := json.Marshal(m)
	if err != nil {
		return errors.Wrap(err, "failed to encode the meeting")
	}
	if _, appErr := s.kv.KVSetWithOptions(meetingKey(m.ID), data, model.PluginKVSetOptions{ExpireInSeconds: meetingTTL(m.End)}); appErr != nil {
		return errors.Wrap(appErr, "failed to save the meeting")
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
		saved, appErr := s.kv.KVSetWithOptions(key, data, model.PluginKVSetOptions{Atomic: true, OldValue: old, ExpireInSeconds: meetingTTL(m.End)})
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

// SetRSVP records a member's answer to a meeting.
func (s *Store) SetRSVP(meetingID, userID string, status RSVPStatus) (*Meeting, error) {
	return s.UpdateMeeting(meetingID, func(m *Meeting) error {
		if m.Cancelled {
			return errMeetingCancelled
		}
		if m.RSVPs == nil {
			m.RSVPs = map[string]RSVP{}
		}
		m.RSVPs[userID] = RSVP{Status: status, UpdatedAt: model.GetMillis()}
		return nil
	})
}

// EditMeeting applies the organiser's changes to a meeting, bumping its SEQUENCE if anything an
// invitation describes actually changed.
func (s *Store) EditMeeting(meetingID, summary, description string, start, end int64) (*Meeting, error) {
	return s.UpdateMeeting(meetingID, func(m *Meeting) error {
		if m.Cancelled {
			return errMeetingCancelled
		}
		if m.Summary != summary || m.Description != description || m.Start != start || m.End != end {
			m.Sequence++
		}
		m.Summary, m.Description, m.Start, m.End = summary, description, start, end
		m.UpdatedAt = model.GetMillis()
		return nil
	})
}

// CancelMeeting marks a meeting as cancelled, bumping its SEQUENCE so iTIP cancellations are told
// apart from the invitations and updates that came before them. Cancelling twice is a no-op.
func (s *Store) CancelMeeting(meetingID string) (*Meeting, error) {
	return s.UpdateMeeting(meetingID, func(m *Meeting) error {
		if m.Cancelled {
			return nil
		}
		m.Cancelled = true
		m.Sequence++
		m.UpdatedAt = model.GetMillis()
		return nil
	})
}
