// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// nextMeetingWindow is how far ahead the channel header looks for the next call.
const nextMeetingWindow = 7 * 24 * time.Hour

// nextMeeting is the call the channel header shows: the one live now, or else the next one
// within nextMeetingWindow.
type nextMeeting struct {
	MeetingID string `json:"meeting_id"`
	PostID    string `json:"post_id"`
	Summary   string `json:"summary"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
	Live      bool   `json:"live"`
	Recurring bool   `json:"recurring"`
}

// pickNextMeeting returns the call of a channel to show in its header at now: a live one first
// (the earliest started), else the earliest next occurrence starting within nextMeetingWindow.
// Cancelled calls, cancelled occurrences (EXDATE) and other channels' calls are skipped; nil if
// there's nothing to show.
func pickNextMeeting(meetings []*Meeting, channelID string, now time.Time) *nextMeeting {
	var best *nextMeeting
	for _, m := range meetings {
		if m.ChannelID != channelID || m.Cancelled {
			continue
		}
		start, end, ok := nextMeetingOccurrence(m, now)
		if !ok {
			continue
		}
		live := !start.After(now) && end.After(now)
		if !live && start.After(now.Add(nextMeetingWindow)) {
			continue
		}
		candidate := &nextMeeting{
			MeetingID: m.ID, PostID: m.PostID, Summary: m.Summary,
			Start: start.UnixMilli(), End: end.UnixMilli(), Live: live, Recurring: m.recurring(),
		}
		if best == nil || nextMeetingBefore(candidate, best) {
			best = candidate
		}
	}
	return best
}

// nextMeetingBefore orders the candidates for the channel header: live first, then the earliest
// start, then the meeting ID (so the choice is stable).
func nextMeetingBefore(a, b *nextMeeting) bool {
	if a.Live != b.Live {
		return a.Live
	}
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	return a.MeetingID < b.MeetingID
}

// handleNextChannelMeeting returns the call to show in a channel's header: GET
// /channels/{channelId}/meetings/next answers {"next": {...}}, or {"next": null} when there's
// none. It needs read_channel (404 otherwise, so a private channel's calls don't leak), and isn't
// for direct or group messages (400), where calls can't be scheduled from a channel's menu.
func (p *Plugin) handleNextChannelMeeting(w http.ResponseWriter, r *http.Request) {
	channelID := muxVar(r, "channelId")
	if !model.IsValidId(channelID) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid channel"))
		return
	}
	callerID := userID(r)
	if !p.API.HasPermissionToChannel(callerID, channelID, model.PermissionReadChannel) {
		p.writeError(w, newAPIError(http.StatusNotFound, "channel not found"))
		return
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil {
		p.writeError(w, newAPIError(http.StatusNotFound, "channel not found"))
		return
	}
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		p.writeError(w, newAPIError(http.StatusBadRequest, "direct messages have no scheduled calls"))
		return
	}

	var next *nextMeeting
	if channel.TeamId != "" && channel.DeleteAt == 0 {
		meetings, err := p.store.ListTeamMeetings(channel.TeamId)
		if err != nil {
			p.writeError(w, err)
			return
		}
		next = pickNextMeeting(meetings, channelID, time.Now())
	}
	p.writeJSON(w, map[string]*nextMeeting{"next": next})
}
