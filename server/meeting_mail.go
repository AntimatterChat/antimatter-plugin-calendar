// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"html"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

// The messages of a meeting's iTIP email. Their translations are in assets/i18n.
var (
	msgMeetingEmailSubject          = &i18n.Message{ID: "calendar.meeting.email.subject", Other: "Invitation: {{.Summary}}"}
	msgMeetingEmailUpdatedSubject   = &i18n.Message{ID: "calendar.meeting.email.updated_subject", Other: "Updated invitation: {{.Summary}}"}
	msgMeetingEmailCancelledSubject = &i18n.Message{ID: "calendar.meeting.email.cancelled_subject", Other: "Cancelled: {{.Summary}}"}
	msgMeetingEmailBody             = &i18n.Message{ID: "calendar.meeting.email.body", Other: "<p>{{.Organizer}} invited you to <b>{{.Summary}}</b>, {{.When}}.</p><p>Open the attached invitation to add it to your calendar, or answer from the message in the channel.</p>"}
	msgMeetingEmailCancelledBody    = &i18n.Message{ID: "calendar.meeting.email.cancelled_body", Other: "<p>{{.Organizer}} cancelled <b>{{.Summary}}</b>, which was scheduled for {{.When}}.</p>"}
	// msgMeetingEmailCancelledByBody is used instead of msgMeetingEmailCancelledBody when the
	// organiser didn't cancel it themselves (a channel, community or system admin did).
	msgMeetingEmailCancelledByBody = &i18n.Message{ID: "calendar.meeting.email.cancelled_by_body", Other: "<p>{{.Canceller}} cancelled <b>{{.Summary}}</b> (organised by {{.Organizer}}), which was scheduled for {{.When}}.</p>"}
	msgMeetingEmailOpenChannel     = &i18n.Message{ID: "calendar.meeting.email.open_channel", Other: "Open the channel"}
)

// meetingEmailSubject is the subject of a meeting's iTIP email, in a recipient's language.
func meetingEmailSubject(t *translator, m *Meeting, isUpdate bool) string {
	switch {
	case m.Cancelled:
		return t.T(msgMeetingEmailCancelledSubject, map[string]any{"Summary": m.Summary})
	case isUpdate:
		return t.T(msgMeetingEmailUpdatedSubject, map[string]any{"Summary": m.Summary})
	default:
		return t.T(msgMeetingEmailSubject, map[string]any{"Summary": m.Summary})
	}
}

// meetingEmailBody is the HTML body of a meeting's iTIP email, in a recipient's language and time
// zone. actor is who made the change: when a cancellation wasn't the organiser's own, the email
// says who cancelled it, as well as who organised it.
func meetingEmailBody(t *translator, m *Meeting, organizer, actor *model.User, loc *time.Location, permalink string) string {
	when := meetingWhen(t, time.UnixMilli(m.Start).In(loc), time.UnixMilli(m.End).In(loc))
	data := map[string]any{
		"Organizer": html.EscapeString(organizer.GetFullName()),
		"Summary":   html.EscapeString(m.Summary),
		"When":      html.EscapeString(when),
	}
	message := msgMeetingEmailBody
	switch {
	case m.Cancelled && actor != nil && actor.Id != organizer.Id:
		message = msgMeetingEmailCancelledByBody
		data["Canceller"] = html.EscapeString(actor.GetFullName())
	case m.Cancelled:
		message = msgMeetingEmailCancelledBody
	}
	body := t.T(message, data)
	if permalink != "" && !m.Cancelled {
		body += fmt.Sprintf(`<p><a href="%s">%s</a></p>`, html.EscapeString(permalink), html.EscapeString(t.T(msgMeetingEmailOpenChannel, nil)))
	}
	return body
}

// sendMeetingInvites emails the iTIP invitation (or update, or cancellation) of a meeting to the
// channel's members who connected a calendar account and asked for email invitations. Members
// without one still see, join and RSVP to the meeting from its post; this is the extra channel
// for those who want it in their own calendar and inbox too. actor is who made the change (see
// meetingEmailBody).
func (p *Plugin) sendMeetingInvites(m *Meeting, channel *model.Channel, organizer, actor *model.User) {
	ids, err := p.channelMemberIDs(m.ChannelID)
	if err != nil {
		p.API.LogWarn("Failed to list a meeting's invitees", "err", err.Error())
		return
	}

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
		recipients[id] = user
	}
	if len(recipients) == 0 {
		return
	}

	organizerParty := p.organizerInvitee(organizer)
	permalink := p.permalink(p.channelLink(m.OrganizerID, channel.Id))
	now := time.Now()
	isUpdate := m.Sequence > 0 && !m.Cancelled

	for id, user := range recipients {
		loc, err := time.LoadLocation(user.GetPreferredTimezone())
		if err != nil {
			loc = time.UTC
		}
		t := newTranslator(p.translations, user.Locale)

		// Each recipient's invitation lists them alone as an attendee: the other members'
		// addresses and answers are never sent to anyone.
		you := invitee{Email: user.Email, Name: user.GetFullName(), RSVP: m.RSVPs[id].Status}
		opts := meetingICSOptions{ReminderMinutes: m.effectiveReminderMinutes(id)}
		cal := buildMeetingICS(m, organizerParty, []invitee{you}, now, permalink, opts)
		data, err := encodeICS(cal)
		if err != nil {
			p.API.LogWarn("Failed to build a meeting's invitation", "err", err.Error())
			return
		}

		subject := meetingEmailSubject(t, m, isUpdate)
		body := meetingEmailBody(t, m, organizer, actor, loc, permalink)
		if appErr := p.API.SendMailWithAttachment(user.Email, subject, body, "invite.ics", icsContentType(m.Cancelled), data); appErr != nil {
			p.API.LogWarn("Failed to email a meeting's invitation", "user_id", id, "err", appErr.Error())
		}
	}
}
