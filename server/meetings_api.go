// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pkg/errors"
)

const (
	// meetingPostType marks the post of a scheduled meeting, so Fusion shows its Join, add to
	// calendar and RSVP buttons.
	meetingPostType = "custom_calendar_meeting"
	// meetingPropKey is the post prop that carries the meeting's ID.
	meetingPropKey = "meeting_id"

	// maxMeetingMembers bounds how many channel members are read to invite and remind, so a huge
	// channel can't make scheduling or the reminder job run away.
	maxMeetingMembers = 2000

	// meetingChangedEvent tells open clients to read a meeting again: another member RSVP'd, or
	// the organiser edited or cancelled it.
	meetingChangedEvent = "meeting_changed"
)

// meetingInput is a meeting as the "Schedule a call" dialog sends it.
type meetingInput struct {
	Summary     string `json:"summary"`
	Description string `json:"description"`
	// Start and End are in milliseconds.
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// rsvpInput is an RSVP as the post's buttons send it.
type rsvpInput struct {
	Status RSVPStatus `json:"status"`
}

// muxVar returns a path variable of a mux-routed request.
func muxVar(r *http.Request, name string) string {
	return mux.Vars(r)[name]
}

// channelMemberIDs returns the IDs of the members of a channel, up to maxMeetingMembers.
func (p *Plugin) channelMemberIDs(channelID string) ([]string, error) {
	var ids []string
	const perPage = 200
	for page := 0; len(ids) < maxMeetingMembers; page++ {
		members, appErr := p.API.GetChannelMembers(channelID, page, perPage)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to list the channel's members")
		}
		for _, m := range members {
			ids = append(ids, m.UserId)
		}
		if len(members) < perPage {
			break
		}
	}
	if len(ids) > maxMeetingMembers {
		ids = ids[:maxMeetingMembers]
	}
	return ids, nil
}

// handleCreateMeeting schedules a call for everyone in a channel: a post with the time, a Join
// button and RSVP, reminders before it starts, and iTIP email invitations to the members who want
// them.
func (p *Plugin) handleCreateMeeting(w http.ResponseWriter, r *http.Request) {
	channelID := muxVar(r, "channelId")
	if !model.IsValidId(channelID) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid channel"))
		return
	}
	if !p.API.HasPermissionToChannel(userID(r), channelID, model.PermissionCreatePost) {
		p.writeError(w, newAPIError(http.StatusForbidden, "you can't post in this channel"))
		return
	}

	var in meetingInput
	if err := readJSON(w, r, &in); err != nil {
		p.writeError(w, err)
		return
	}
	in.Summary, in.Description = strings.TrimSpace(in.Summary), strings.TrimSpace(in.Description)
	if err := validateMeetingInput(in.Summary, in.Description, time.UnixMilli(in.Start), time.UnixMilli(in.End)); err != nil {
		p.writeError(w, err)
		return
	}

	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "channel not found"))
		return
	}

	organizer, appErr := p.API.GetUser(userID(r))
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "failed to read your profile"))
		return
	}

	now := model.GetMillis()
	m := &Meeting{
		ID:          model.NewId(),
		ChannelID:   channelID,
		OrganizerID: organizer.Id,
		Summary:     in.Summary,
		Description: in.Description,
		Start:       in.Start,
		End:         in.End,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	post := &model.Post{
		UserId:    p.botID,
		ChannelId: channelID,
		Message:   p.meetingMessage(m, organizer, msgMeetingScheduled),
		Type:      meetingPostType,
		Props:     model.StringInterface{meetingPropKey: m.ID},
	}
	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "failed to post the meeting"))
		return
	}
	m.PostID = created.Id

	if err := p.store.CreateMeeting(m); err != nil {
		if delErr := p.API.DeletePost(created.Id); delErr != nil {
			p.API.LogWarn("Failed to clean up a meeting's post after the meeting failed to save", "err", delErr.Error())
		}
		p.writeError(w, err)
		return
	}

	p.sendInvitesAsync(m, channel, organizer)
	p.writeJSON(w, m)
}

// handleGetMeeting returns a meeting, for members who can read its channel.
func (p *Plugin) handleGetMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := p.loadReadableMeeting(w, r)
	if err != nil {
		return
	}
	p.writeJSON(w, m)
}

// handleEditMeeting applies the organiser's changes and re-sends iTIP updates.
func (p *Plugin) handleEditMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := p.loadOrganizedMeeting(w, r)
	if err != nil {
		return
	}

	var in meetingInput
	if err := readJSON(w, r, &in); err != nil {
		p.writeError(w, err)
		return
	}
	in.Summary, in.Description = strings.TrimSpace(in.Summary), strings.TrimSpace(in.Description)
	if err := validateMeetingInput(in.Summary, in.Description, time.UnixMilli(in.Start), time.UnixMilli(in.End)); err != nil {
		p.writeError(w, err)
		return
	}

	updated, err := p.store.EditMeeting(m.ID, in.Summary, in.Description, in.Start, in.End)
	if err != nil {
		p.writeError(w, err)
		return
	}

	channel, appErr := p.API.GetChannel(updated.ChannelID)
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "channel not found"))
		return
	}
	organizer, appErr := p.API.GetUser(updated.OrganizerID)
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "failed to read the organiser's profile"))
		return
	}

	p.updateMeetingPost(updated, organizer, msgMeetingUpdated)
	p.notifyMeetingChanged(updated)
	p.sendInvitesAsync(updated, channel, organizer)
	p.writeJSON(w, updated)
}

// handleCancelMeeting cancels a meeting and sends iTIP cancellations.
func (p *Plugin) handleCancelMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := p.loadOrganizedMeeting(w, r)
	if err != nil {
		return
	}

	cancelled, err := p.store.CancelMeeting(m.ID)
	if err != nil {
		p.writeError(w, err)
		return
	}

	channel, appErr := p.API.GetChannel(cancelled.ChannelID)
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "channel not found"))
		return
	}
	organizer, appErr := p.API.GetUser(cancelled.OrganizerID)
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "failed to read the organiser's profile"))
		return
	}

	p.updateMeetingPost(cancelled, organizer, msgMeetingCancelledPost)
	p.notifyMeetingChanged(cancelled)
	p.sendInvitesAsync(cancelled, channel, organizer)
	p.writeJSON(w, cancelled)
}

// handleSetRSVP records a member's answer to a meeting.
func (p *Plugin) handleSetRSVP(w http.ResponseWriter, r *http.Request) {
	m, err := p.loadReadableMeeting(w, r)
	if err != nil {
		return
	}

	var in rsvpInput
	if err := readJSON(w, r, &in); err != nil {
		p.writeError(w, err)
		return
	}
	if !validRSVPStatus(in.Status) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid RSVP"))
		return
	}

	updated, err := p.store.SetRSVP(m.ID, userID(r), in.Status)
	if err != nil {
		p.writeError(w, err)
		return
	}
	p.notifyMeetingChanged(updated)
	p.writeJSON(w, updated)
}

// handleMeetingICS returns the current iTIP invitation of a meeting, for its "add to calendar"
// button: an attendee without an email invitation (or without a connected calendar account) can
// still add it to their own calendar this way.
func (p *Plugin) handleMeetingICS(w http.ResponseWriter, r *http.Request) {
	m, err := p.loadReadableMeeting(w, r)
	if err != nil {
		return
	}
	channel, appErr := p.API.GetChannel(m.ChannelID)
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "channel not found"))
		return
	}
	organizer, appErr := p.API.GetUser(m.OrganizerID)
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "failed to read the organiser's profile"))
		return
	}
	viewer, appErr := p.API.GetUser(userID(r))
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "failed to read your profile"))
		return
	}

	you := invitee{Email: viewer.Email, Name: viewer.GetFullName(), RSVP: m.RSVPs[viewer.Id].Status}
	cal := buildMeetingICS(m, p.organizerInvitee(organizer), []invitee{you}, time.Now(), p.permalink(p.channelLink(userID(r), channel.Id)))
	data, err := encodeICS(cal)
	if err != nil {
		p.writeError(w, errors.Wrap(err, "failed to build the invitation"))
		return
	}

	w.Header().Set("Content-Type", icsContentType(m))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.ics"`, m.ID))
	_, _ = w.Write(data)
}

// loadReadableMeeting loads a meeting the caller may at least read (they can read its channel).
func (p *Plugin) loadReadableMeeting(w http.ResponseWriter, r *http.Request) (*Meeting, error) {
	id := muxVar(r, "meetingId")
	m, err := p.store.GetMeeting(id)
	if err != nil {
		p.writeError(w, err)
		return nil, err
	}
	if m == nil {
		err := errMeetingNotFound
		p.writeError(w, err)
		return nil, err
	}
	if !p.API.HasPermissionToChannel(userID(r), m.ChannelID, model.PermissionReadChannel) {
		// Not found, not forbidden: a meeting in a channel the caller can't see (a private
		// channel they aren't a member of) must look exactly like one that doesn't exist.
		err := errMeetingNotFound
		p.writeError(w, err)
		return nil, err
	}
	return m, nil
}

// loadOrganizedMeeting loads a meeting the caller organised: only the organiser may edit or
// cancel a meeting.
func (p *Plugin) loadOrganizedMeeting(w http.ResponseWriter, r *http.Request) (*Meeting, error) {
	m, err := p.loadReadableMeeting(w, r)
	if err != nil {
		return nil, err
	}
	if m.OrganizerID != userID(r) {
		err := newAPIError(http.StatusForbidden, "only the organiser can change this meeting")
		p.writeError(w, err)
		return nil, err
	}
	return m, nil
}

// notifyMeetingChanged tells the channel's open clients to read a meeting again.
func (p *Plugin) notifyMeetingChanged(m *Meeting) {
	p.API.PublishWebSocketEvent(meetingChangedEvent, map[string]any{"id": m.ID}, &model.WebsocketBroadcast{ChannelId: m.ChannelID})
}

// sendInvitesAsync starts sending a meeting's iTIP email invitations, off the request. A test
// that doesn't set p.inviteSender exercises everything else without them.
func (p *Plugin) sendInvitesAsync(m *Meeting, channel *model.Channel, organizer *model.User) {
	if p.inviteSender != nil {
		p.inviteSender(m, channel, organizer)
	}
}

// updateMeetingPost refreshes a meeting's post text after an edit or a cancellation.
func (p *Plugin) updateMeetingPost(m *Meeting, organizer *model.User, message *i18n.Message) {
	post, appErr := p.API.GetPost(m.PostID)
	if appErr != nil {
		p.API.LogWarn("Failed to read a meeting's post", "err", appErr.Error())
		return
	}
	post.Message = p.meetingMessage(m, organizer, message)
	if _, appErr := p.API.UpdatePost(post); appErr != nil {
		p.API.LogWarn("Failed to update a meeting's post", "err", appErr.Error())
	}
}

// organizerInvitee returns an organiser as an iTIP party.
func (p *Plugin) organizerInvitee(organizer *model.User) invitee {
	return invitee{Email: organizer.Email, Name: organizer.GetFullName()}
}

// The messages of a meeting's post. Their translations are in assets/i18n.
var (
	msgMeetingScheduled     = &i18n.Message{ID: "calendar.meeting.scheduled_post", Other: ":calendar: **{{.Summary}}** is scheduled for {{.When}}."}
	msgMeetingUpdated       = &i18n.Message{ID: "calendar.meeting.updated_post", Other: ":calendar: **{{.Summary}}** was updated — now {{.When}}."}
	msgMeetingCancelledPost = &i18n.Message{ID: "calendar.meeting.cancelled_post", Other: ":calendar: ~~{{.Summary}}~~ ({{.When}}) was cancelled."}
	msgMeetingWhen          = &i18n.Message{ID: "calendar.meeting.when", Other: "{{.Date}}, {{.Start}}–{{.End}}"}
)

// meetingWhen returns an absolute, translated description of a meeting's time (as opposed to the
// reminders' relative "in 10 minutes": the post is read well before, and after, the meeting).
func meetingWhen(t *translator, start, end time.Time) string {
	date := t.T(msgDate, map[string]any{
		"Weekday": t.T(msgWeekdays[start.Weekday()], nil),
		"Day":     start.Day(),
		"Month":   t.T(msgMonths[start.Month()-1], nil),
	})
	return t.T(msgMeetingWhen, map[string]any{"Date": date, "Start": start.Format("15:04"), "End": end.Format("15:04")})
}

// meetingMessage is the text of a meeting's post, in the organiser's language and time zone (the
// post is shared by the whole channel, so it can't follow each viewer's own language the way a
// reminder DM does; Fusion's own post component shows the time in the viewer's time zone).
func (p *Plugin) meetingMessage(m *Meeting, organizer *model.User, template *i18n.Message) string {
	loc, err := time.LoadLocation(organizer.GetPreferredTimezone())
	if err != nil {
		loc = time.UTC
	}
	t := newTranslator(p.translations, organizer.Locale)
	when := meetingWhen(t, time.UnixMilli(m.Start).In(loc), time.UnixMilli(m.End).In(loc))
	return t.T(template, map[string]any{"Summary": escapeMarkdown(m.Summary), "When": when})
}
