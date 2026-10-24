// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pkg/errors"
	"github.com/teambition/rrule-go"
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
	// Start and End are in milliseconds (the series' anchor occurrence, for a recurring call).
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	// TimeZone is the IANA zone a recurring call's occurrences are expanded in; ignored for a
	// one-off call.
	TimeZone string `json:"time_zone"`
	// RecurrenceRule is an RFC 5545 RRULE value, "" for a one-off call.
	RecurrenceRule string `json:"recurrence_rule"`
	// ReminderMinutes is how long before each occurrence its reminder is sent, from
	// reminderChoicesMinutes.
	ReminderMinutes int `json:"reminder_minutes"`
}

// rsvpInput is an RSVP as the post's buttons send it. Scope is "" for the whole series (the
// default), or "next" for the next occurrence only.
type rsvpInput struct {
	Status RSVPStatus `json:"status"`
	Scope  string     `json:"scope"`
}

// reminderOverrideInput is a member's own reminder choice for a meeting. A nil Minutes clears it
// ("Default"): the member then uses the meeting's own setting.
type reminderOverrideInput struct {
	Minutes *int `json:"minutes"`
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
	in.Start, in.End = wholeSeconds(in.Start), wholeSeconds(in.End)
	if err := validateMeetingInput(in.Summary, in.Description, time.UnixMilli(in.Start), time.UnixMilli(in.End)); err != nil {
		p.writeError(w, err)
		return
	}
	if !validReminderMinutes(in.ReminderMinutes) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid reminder"))
		return
	}
	if _, err := parseRecurrenceRule(in.RecurrenceRule, time.UnixMilli(in.Start)); err != nil {
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
	reminderMinutes := in.ReminderMinutes
	m := &Meeting{
		ID:              model.NewId(),
		ChannelID:       channelID,
		TeamID:          channel.TeamId,
		OrganizerID:     organizer.Id,
		Summary:         in.Summary,
		Description:     in.Description,
		Start:           in.Start,
		End:             in.End,
		TimeZone:        in.TimeZone,
		RecurrenceRule:  in.RecurrenceRule,
		ReminderMinutes: &reminderMinutes,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	post := &model.Post{
		UserId:    p.botID,
		ChannelId: channelID,
		Message:   p.meetingMessage(m, organizer, msgMeetingScheduled, nil),
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

	// Open clients refresh what shows this channel's calls (the header's next call, the community
	// calendar), not just the posts of meetings they already know.
	p.notifyMeetingChanged(m)
	p.sendInvitesAsync(m, channel, organizer, organizer)
	p.writeMeetingView(w, r, m)
}

// wholeSeconds drops the milliseconds of a time in milliseconds since the epoch. A call's times
// are kept to the second, as iCalendar has them: a recurring series is expanded from its DTSTART
// to the second, so a start with milliseconds would never equal its own first occurrence.
func wholeSeconds(ms int64) int64 {
	return time.UnixMilli(ms).Truncate(time.Second).UnixMilli()
}

// handleGetMeeting returns a meeting, for members who can read its channel.
func (p *Plugin) handleGetMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := p.loadReadableMeeting(w, r)
	if err != nil {
		return
	}
	p.writeMeetingView(w, r, m)
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
	in.Start, in.End = wholeSeconds(in.Start), wholeSeconds(in.End)
	if err := validateMeetingInput(in.Summary, in.Description, time.UnixMilli(in.Start), time.UnixMilli(in.End)); err != nil {
		p.writeError(w, err)
		return
	}
	if !validReminderMinutes(in.ReminderMinutes) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid reminder"))
		return
	}
	if _, err := parseRecurrenceRule(in.RecurrenceRule, time.UnixMilli(in.Start)); err != nil {
		p.writeError(w, err)
		return
	}

	updated, err := p.store.EditMeeting(m.ID, in.Summary, in.Description, in.Start, in.End, in.TimeZone, in.RecurrenceRule, in.ReminderMinutes)
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

	p.updateMeetingPost(updated, organizer, msgMeetingUpdated, nil)
	p.notifyMeetingChanged(updated)
	p.sendInvitesAsync(updated, channel, organizer, organizer)
	p.writeMeetingView(w, r, updated)
}

// handleCancelMeeting cancels a meeting (scope=occurrence cancels only the next occurrence of a
// recurring call, as an EXDATE; anything else, the default, cancels the whole series) and sends
// iTIP cancellations. Unlike editing, cancelling isn't organiser-only: that channel's admins, that
// community's admins and system admins may also cancel it (but not edit it), checked explicitly
// (see canCancelMeeting).
func (p *Plugin) handleCancelMeeting(w http.ResponseWriter, r *http.Request) {
	m, err := p.loadReadableMeeting(w, r)
	if err != nil {
		return
	}
	if !p.canCancelMeeting(userID(r), m) {
		p.writeError(w, newAPIError(http.StatusForbidden, "you can't cancel this meeting"))
		return
	}
	canceller, appErr := p.API.GetUser(userID(r))
	if appErr != nil {
		p.writeError(w, errors.Wrap(appErr, "failed to read your profile"))
		return
	}

	var cancelled *Meeting
	var occurrenceStart, occurrenceEnd time.Time
	if r.URL.Query().Get("scope") == "occurrence" && m.recurring() {
		var ok bool
		occurrenceStart, occurrenceEnd, ok = nextMeetingOccurrence(m, time.Now())
		if !ok {
			p.writeError(w, newAPIError(http.StatusConflict, "this series has no more occurrences to cancel"))
			return
		}
		cancelled, err = p.store.CancelOccurrence(m.ID, occurrenceStart.UnixMilli())
	} else {
		cancelled, err = p.store.CancelMeeting(m.ID)
	}
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
	byAdmin := canceller.Id != cancelled.OrganizerID

	if !occurrenceStart.IsZero() {
		// One occurrence of the series was cancelled: the series goes on, so the post is
		// refreshed to its (new) next occurrence rather than marked cancelled.
		p.refreshRecurringPost(cancelled)
		p.notifyMeetingChanged(cancelled)
		p.sendOccurrenceCancellation(cancelled, channel, organizer, canceller, byAdmin, occurrenceStart, occurrenceEnd)
		p.writeMeetingView(w, r, cancelled)
		return
	}

	template, data := msgMeetingCancelledPost, map[string]any(nil)
	if byAdmin {
		template, data = msgMeetingCancelledByPost, map[string]any{"Canceller": escapeMarkdown(canceller.GetFullName())}
	}
	p.updateMeetingPost(cancelled, organizer, template, data)
	p.notifyMeetingChanged(cancelled)
	p.sendInvitesAsync(cancelled, channel, organizer, canceller)
	p.writeMeetingView(w, r, cancelled)
}

// canCancelMeeting returns whether a user may cancel a meeting: its organiser, an admin of its
// channel or its community, or a system admin. Checked explicitly against each of those (not a
// broad permission such as delete_others_posts), since cancelling is only open to this specific
// set of people.
func (p *Plugin) canCancelMeeting(userID string, m *Meeting) bool {
	if userID == m.OrganizerID {
		return true
	}
	if p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		return true
	}
	if member, appErr := p.API.GetChannelMember(m.ChannelID, userID); appErr == nil && member != nil && member.SchemeAdmin {
		return true
	}
	channel, appErr := p.API.GetChannel(m.ChannelID)
	if appErr != nil || channel.TeamId == "" {
		return false
	}
	if teamMember, appErr := p.API.GetTeamMember(channel.TeamId, userID); appErr == nil && teamMember != nil && teamMember.SchemeAdmin {
		return true
	}
	return false
}

// handleSetRSVP records a member's answer to a meeting: to the whole series, or (scope=next) to
// its next occurrence only.
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
	if in.Scope != "" && in.Scope != "next" {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid RSVP scope"))
		return
	}

	var occurrenceStart int64
	if in.Scope == "next" {
		start, _, ok := nextMeetingOccurrence(m, time.Now())
		if !ok {
			p.writeError(w, newAPIError(http.StatusConflict, "this series has no more occurrences"))
			return
		}
		occurrenceStart = start.UnixMilli()
	}

	updated, err := p.store.SetRSVP(m.ID, userID(r), in.Status, occurrenceStart)
	if err != nil {
		p.writeError(w, err)
		return
	}
	p.notifyMeetingChanged(updated)
	p.writeMeetingView(w, r, updated)
}

// handleSetReminderOverride sets, or (a nil minutes) clears, a member's own reminder choice for a
// meeting: it needs only read_channel (as RSVP does), and never sends a new invitation email (only
// the member's own reminder is affected).
func (p *Plugin) handleSetReminderOverride(w http.ResponseWriter, r *http.Request) {
	m, err := p.loadReadableMeeting(w, r)
	if err != nil {
		return
	}

	var in reminderOverrideInput
	if err := readJSON(w, r, &in); err != nil {
		p.writeError(w, err)
		return
	}
	if in.Minutes != nil && !validReminderMinutes(*in.Minutes) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid reminder"))
		return
	}

	updated, err := p.store.SetReminderOverride(m.ID, userID(r), in.Minutes)
	if err != nil {
		p.writeError(w, err)
		return
	}
	p.notifyMeetingChanged(updated)
	p.writeMeetingView(w, r, updated)
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

	rsvp, _ := m.effectiveRSVP(viewer.Id, m.Start)
	you := invitee{Email: viewer.Email, Name: viewer.GetFullName(), RSVP: rsvp.Status}
	opts := meetingICSOptions{ReminderMinutes: m.effectiveReminderMinutes(viewer.Id)}
	cal := buildMeetingICS(m, p.organizerInvitee(organizer), []invitee{you}, time.Now(), p.permalink(p.channelLink(userID(r), channel.Id)), opts)
	data, err := encodeICS(cal)
	if err != nil {
		p.writeError(w, errors.Wrap(err, "failed to build the invitation"))
		return
	}

	w.Header().Set("Content-Type", icsContentType(m.Cancelled))
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
	return p.refreshRecurringPost(m), nil
}

// loadOrganizedMeeting loads a meeting the caller organised: only the organiser may edit a
// meeting (cancelling is wider; see canCancelMeeting).
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

// notifyMeetingChanged tells the channel's open clients to read a meeting again. It names the
// channel too, so a client showing another channel's next call needn't read anything.
func (p *Plugin) notifyMeetingChanged(m *Meeting) {
	p.API.PublishWebSocketEvent(meetingChangedEvent, map[string]any{"id": m.ID, "channel_id": m.ChannelID}, &model.WebsocketBroadcast{ChannelId: m.ChannelID})
}

// sendInvitesAsync starts sending a meeting's iTIP email invitations, off the request. A test
// that doesn't set p.inviteSender exercises everything else without them. actor is who made the
// change: the organiser scheduling or editing it, or whoever cancelled it (the organiser or an
// admin), told apart in the email when it wasn't the organiser.
func (p *Plugin) sendInvitesAsync(m *Meeting, channel *model.Channel, organizer, actor *model.User) {
	if p.inviteSender != nil {
		p.inviteSender(m, channel, organizer, actor)
	}
}

// meetingView is a meeting as the API returns it: can_cancel is computed for the caller (the
// server decides who may cancel a meeting, never the client), and my_reminder_override is only
// ever the caller's own (the full ReminderOverrides map, everyone's, is never sent to anyone).
type meetingView struct {
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	TeamID      string `json:"team_id,omitempty"`
	PostID      string `json:"post_id"`
	OrganizerID string `json:"organizer_id"`

	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	Start       int64  `json:"start"`
	End         int64  `json:"end"`

	TimeZone              string  `json:"time_zone,omitempty"`
	RecurrenceRule        string  `json:"recurrence_rule,omitempty"`
	ExceptionDates        []int64 `json:"exception_dates,omitempty"`
	PostedOccurrenceStart int64   `json:"posted_occurrence_start,omitempty"`
	PostedOccurrenceEnd   int64   `json:"posted_occurrence_end,omitempty"`

	ReminderMinutes    int  `json:"reminder_minutes"`
	MyReminderOverride *int `json:"my_reminder_override,omitempty"`

	Sequence  int  `json:"sequence"`
	Cancelled bool `json:"cancelled"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`

	RSVPs     map[string]RSVP           `json:"rsvps,omitempty"`
	NextRSVPs map[string]occurrenceRSVP `json:"next_rsvps,omitempty"`

	// NextStart and NextEnd are the occurrence that's live or next now (the one the post shows,
	// and that a "next call only" RSVP or "this call" cancellation applies to), computed by the
	// server so no client has to expand the RRULE; absent once the call (or the series) is over,
	// and for a cancelled one.
	NextStart int64 `json:"next_start,omitempty"`
	NextEnd   int64 `json:"next_end,omitempty"`
	// MyRSVP is the caller's own answer for that occurrence (their "next call only" answer if
	// they gave one for it, else their answer for the series); MyRSVPNextOnly says it's the
	// former.
	MyRSVP         RSVPStatus `json:"my_rsvp,omitempty"`
	MyRSVPNextOnly bool       `json:"my_rsvp_next_only,omitempty"`

	CanCancel bool `json:"can_cancel"`
}

// newMeetingView builds the view of a meeting for a specific caller.
func (p *Plugin) newMeetingView(callerID string, m *Meeting) *meetingView {
	view := &meetingView{
		ID: m.ID, ChannelID: m.ChannelID, TeamID: m.TeamID, PostID: m.PostID, OrganizerID: m.OrganizerID,
		Summary: m.Summary, Description: m.Description, Start: m.Start, End: m.End,
		TimeZone: m.TimeZone, RecurrenceRule: m.RecurrenceRule, ExceptionDates: m.ExceptionDates,
		PostedOccurrenceStart: m.PostedOccurrenceStart, PostedOccurrenceEnd: m.PostedOccurrenceEnd,
		ReminderMinutes: m.meetingReminderMinutes(),
		Sequence:        m.Sequence, Cancelled: m.Cancelled,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		RSVPs: m.RSVPs, NextRSVPs: m.NextRSVPs,
		CanCancel: p.canCancelMeeting(callerID, m),
	}
	if minutes, ok := m.ReminderOverrides[callerID]; ok {
		view.MyReminderOverride = &minutes
	}

	occurrence := m.Start
	if !m.Cancelled {
		if start, end, ok := nextMeetingOccurrence(m, time.Now()); ok {
			view.NextStart, view.NextEnd = start.UnixMilli(), end.UnixMilli()
			occurrence = view.NextStart
		}
	}
	if rsvp, ok := m.effectiveRSVP(callerID, occurrence); ok {
		view.MyRSVP = rsvp.Status
		next, nextOK := m.NextRSVPs[callerID]
		view.MyRSVPNextOnly = nextOK && next.OccurrenceStart == occurrence
	}
	return view
}

// writeMeetingView answers a request with a meeting, as its caller alone may see it.
func (p *Plugin) writeMeetingView(w http.ResponseWriter, r *http.Request, m *Meeting) {
	p.writeJSON(w, p.newMeetingView(userID(r), m))
}

// updateMeetingPost refreshes a meeting's post text after an edit or a cancellation. extra fills
// in the template beyond the meeting's own summary and time (for example, who cancelled it).
func (p *Plugin) updateMeetingPost(m *Meeting, organizer *model.User, message *i18n.Message, extra map[string]any) {
	post, appErr := p.API.GetPost(m.PostID)
	if appErr != nil {
		p.API.LogWarn("Failed to read a meeting's post", "err", appErr.Error())
		return
	}
	post.Message = p.meetingMessage(m, organizer, message, extra)
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
	// msgMeetingCancelledByPost is used instead of msgMeetingCancelledPost when the organiser
	// didn't cancel it themselves (a channel, community or system admin did).
	msgMeetingCancelledByPost = &i18n.Message{ID: "calendar.meeting.cancelled_by_post", Other: ":calendar: ~~{{.Summary}}~~ ({{.When}}) was cancelled by {{.Canceller}}."}
	msgMeetingWhen            = &i18n.Message{ID: "calendar.meeting.when", Other: "{{.Date}}, {{.Start}}–{{.End}}"}
	// msgMeetingRecurringPost is shown instead of msgMeetingScheduled/Updated for a recurring
	// series, once its post has moved on to a later occurrence: the repeat rule, in words, plus
	// the next occurrence's time.
	msgMeetingRecurringPost = &i18n.Message{ID: "calendar.meeting.recurring_post", Other: ":calendar: **{{.Summary}}** repeats {{.Repeat}} — next {{.When}}."}

	msgRepeatDaily        = &i18n.Message{ID: "calendar.meeting.repeat.daily", Other: "every day"}
	msgRepeatEveryNDays   = &i18n.Message{ID: "calendar.meeting.repeat.every_n_days", One: "every day", Other: "every {{.Count}} days"}
	msgRepeatWeekly       = &i18n.Message{ID: "calendar.meeting.repeat.weekly", Other: "every week"}
	msgRepeatWeeklyOn     = &i18n.Message{ID: "calendar.meeting.repeat.weekly_on", Other: "every week on {{.Days}}"}
	msgRepeatEveryNWeeks  = &i18n.Message{ID: "calendar.meeting.repeat.every_n_weeks", One: "every week", Other: "every {{.Count}} weeks"}
	msgRepeatMonthly      = &i18n.Message{ID: "calendar.meeting.repeat.monthly", Other: "every month"}
	msgRepeatEveryNMonths = &i18n.Message{ID: "calendar.meeting.repeat.every_n_months", One: "every month", Other: "every {{.Count}} months"}
	msgRepeatYearly       = &i18n.Message{ID: "calendar.meeting.repeat.yearly", Other: "every year"}
	msgRepeatEveryNYears  = &i18n.Message{ID: "calendar.meeting.repeat.every_n_years", One: "every year", Other: "every {{.Count}} years"}
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

// weekdayName translates an rrule weekday (0 = Monday) with the existing full weekday messages
// (indexed by time.Weekday, 0 = Sunday).
func weekdayName(t *translator, rruleDay int) string {
	return t.T(msgWeekdays[(rruleDay+1)%7], nil)
}

// recurrenceWords is a translated, human-readable description of a recurrence rule ("every week
// on Monday, Wednesday"), shown on a recurring call's post instead of the raw RRULE.
func recurrenceWords(t *translator, option *rrule.ROption) string {
	if option == nil {
		return ""
	}
	interval := option.Interval
	if interval <= 0 {
		interval = 1
	}
	switch option.Freq {
	case rrule.DAILY:
		if interval == 1 {
			return t.T(msgRepeatDaily, nil)
		}
		return t.T(msgRepeatEveryNDays, map[string]any{"Count": interval})
	case rrule.WEEKLY:
		if len(option.Byweekday) > 0 {
			days := make([]string, 0, len(option.Byweekday))
			for _, wd := range option.Byweekday {
				days = append(days, weekdayName(t, wd.Day()))
			}
			return t.T(msgRepeatWeeklyOn, map[string]any{"Days": strings.Join(days, ", ")})
		}
		if interval == 1 {
			return t.T(msgRepeatWeekly, nil)
		}
		return t.T(msgRepeatEveryNWeeks, map[string]any{"Count": interval})
	case rrule.MONTHLY:
		if interval == 1 {
			return t.T(msgRepeatMonthly, nil)
		}
		return t.T(msgRepeatEveryNMonths, map[string]any{"Count": interval})
	case rrule.YEARLY:
		if interval == 1 {
			return t.T(msgRepeatYearly, nil)
		}
		return t.T(msgRepeatEveryNYears, map[string]any{"Count": interval})
	default:
		return ""
	}
}

// meetingMessage is the text of a meeting's post, in the organiser's language and time zone (the
// post is shared by the whole channel, so it can't follow each viewer's own language the way a
// reminder DM does; Fusion's own post component shows the time in the viewer's time zone). The
// time shown is the occurrence the post currently displays (PostedOccurrenceStart/End, or the
// series' anchor before it's ever rolled over). extra fills in the template beyond the meeting's
// own summary and time.
func (p *Plugin) meetingMessage(m *Meeting, organizer *model.User, template *i18n.Message, extra map[string]any) string {
	loc, err := time.LoadLocation(organizer.GetPreferredTimezone())
	if err != nil {
		loc = time.UTC
	}
	t := newTranslator(p.translations, organizer.Locale)
	start, end := m.Start, m.End
	if m.recurring() && m.PostedOccurrenceStart != 0 {
		start, end = m.PostedOccurrenceStart, m.PostedOccurrenceEnd
	}
	when := meetingWhen(t, time.UnixMilli(start).In(loc), time.UnixMilli(end).In(loc))
	data := map[string]any{"Summary": escapeMarkdown(m.Summary), "When": when}
	for k, v := range extra {
		data[k] = v
	}
	return t.T(template, data)
}

// refreshRecurringPost moves a recurring meeting's shared post on to its next occurrence, once
// the one it showed has ended. Called from the next read of the meeting (loadReadableMeeting):
// the simplest reliable place for it, rather than a periodic job. A no-op for a one-off meeting,
// a cancelled one, or one that already shows its current (or next) occurrence.
func (p *Plugin) refreshRecurringPost(m *Meeting) *Meeting {
	if !m.recurring() || m.Cancelled {
		return m
	}
	start, end, ok := nextMeetingOccurrence(m, time.Now())
	displayed := m.PostedOccurrenceStart
	if displayed == 0 {
		displayed = m.Start
	}
	if !ok || start.UnixMilli() == displayed {
		return m
	}
	updated, err := p.store.RefreshPostedOccurrence(m.ID, start.UnixMilli(), end.UnixMilli())
	if err != nil {
		p.API.LogWarn("Failed to move a recurring call's post to its next occurrence", "err", err.Error())
		return m
	}
	organizer, appErr := p.API.GetUser(updated.OrganizerID)
	if appErr != nil {
		return updated
	}
	t := newTranslator(p.translations, organizer.Locale)
	option, _ := parseRecurrenceRule(updated.RecurrenceRule, time.UnixMilli(updated.Start).In(meetingTimeZone(updated)))
	p.updateMeetingPost(updated, organizer, msgMeetingRecurringPost, map[string]any{"Repeat": recurrenceWords(t, option)})
	return updated
}

// The messages of an occurrence cancellation email. Their translations are in assets/i18n.
var (
	msgMeetingEmailOccurrenceCancelledSubject = &i18n.Message{ID: "calendar.meeting.email.occurrence_cancelled_subject", Other: "One occurrence cancelled: {{.Summary}}"}
	msgMeetingEmailOccurrenceCancelledBody    = &i18n.Message{ID: "calendar.meeting.email.occurrence_cancelled_body", Other: "<p>{{.Organizer}} cancelled the {{.When}} occurrence of <b>{{.Summary}}</b>. The rest of the series goes on.</p>"}
	msgMeetingEmailOccurrenceCancelledByBody  = &i18n.Message{ID: "calendar.meeting.email.occurrence_cancelled_by_body", Other: "<p>{{.Canceller}} cancelled the {{.When}} occurrence of <b>{{.Summary}}</b> (organised by {{.Organizer}}). The rest of the series goes on.</p>"}
)

// sendOccurrenceCancellation emails the channel's eligible members an iTIP CANCEL for one
// occurrence of a recurring call (RECURRENCE-ID), without touching the rest of the series. Kept
// apart from sendInvitesAsync/sendMeetingInvites, which only ever describe the whole series.
func (p *Plugin) sendOccurrenceCancellation(m *Meeting, channel *model.Channel, organizer, canceller *model.User, byAdmin bool, occurrenceStart, occurrenceEnd time.Time) {
	if p.occurrenceCancelSender != nil {
		p.occurrenceCancelSender(m, channel, organizer, canceller, byAdmin, occurrenceStart, occurrenceEnd)
	}
}

// sendOccurrenceCancellationEmails is the real implementation behind sendOccurrenceCancellation.
func (p *Plugin) sendOccurrenceCancellationEmails(m *Meeting, channel *model.Channel, organizer, canceller *model.User, byAdmin bool, occurrenceStart, occurrenceEnd time.Time) {
	ids, err := p.channelMemberIDs(m.ChannelID)
	if err != nil {
		p.API.LogWarn("Failed to list a meeting's invitees", "err", err.Error())
		return
	}

	var invitees []invitee
	recipients := map[string]*model.User{}
	for _, id := range ids {
		account, err := p.store.GetAccount(id)
		if err != nil || account == nil || !account.EmailInvites {
			continue
		}
		user, appErr := p.API.GetUser(id)
		if appErr != nil || user.Email == "" || user.DeleteAt != 0 {
			continue
		}
		rsvp, _ := m.effectiveRSVP(id, occurrenceStart.UnixMilli())
		invitees = append(invitees, invitee{Email: user.Email, Name: user.GetFullName(), RSVP: rsvp.Status})
		recipients[id] = user
	}
	if len(invitees) == 0 {
		return
	}

	organizerParty := p.organizerInvitee(organizer)
	permalink := p.permalink(p.channelLink(m.OrganizerID, channel.Id))
	now := time.Now()

	for _, user := range recipients {
		loc, err := time.LoadLocation(user.GetPreferredTimezone())
		if err != nil {
			loc = time.UTC
		}
		t := newTranslator(p.translations, user.Locale)

		opts := meetingICSOptions{OccurrenceStart: occurrenceStart, OccurrenceEnd: occurrenceEnd}
		cal := buildMeetingICS(m, organizerParty, invitees, now, permalink, opts)
		data, err := encodeICS(cal)
		if err != nil {
			p.API.LogWarn("Failed to build an occurrence's cancellation", "err", err.Error())
			return
		}

		subject := t.T(msgMeetingEmailOccurrenceCancelledSubject, map[string]any{"Summary": m.Summary})
		when := meetingWhen(t, occurrenceStart.In(loc), occurrenceEnd.In(loc))
		data2 := map[string]any{"Organizer": html.EscapeString(organizer.GetFullName()), "Summary": html.EscapeString(m.Summary), "When": html.EscapeString(when)}
		bodyMessage := msgMeetingEmailOccurrenceCancelledBody
		if byAdmin {
			bodyMessage = msgMeetingEmailOccurrenceCancelledByBody
			data2["Canceller"] = html.EscapeString(canceller.GetFullName())
		}
		body := t.T(bodyMessage, data2)
		if appErr := p.API.SendMailWithAttachment(user.Email, subject, body, "invite.ics", icsContentType(true), data); appErr != nil {
			p.API.LogWarn("Failed to email an occurrence's cancellation", "err", appErr.Error())
		}
	}
}
