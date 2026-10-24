// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// maxCommunityRange is the longest range the community calendar may ask for at once (a month
	// view with the days around it, with room to spare), so a request can't make the server expand
	// every series of a community over years.
	maxCommunityRange = 100 * 24 * time.Hour
	// maxCommunityOccurrences caps the occurrences one community calendar response carries; the
	// earliest are kept, and truncated says some were left out.
	maxCommunityOccurrences = 500

	// meetingReindexInterval is how often every meeting's channel is checked for a move to
	// another community (there's no hook for it), to keep the community indexes right.
	meetingReindexInterval = time.Hour
)

// communityOccurrence is one occurrence of a scheduled call, as the community calendar shows it:
// only ever from a channel the caller may read (see communityChannel), and with only the
// caller's own RSVP (nobody else's).
type communityOccurrence struct {
	MeetingID          string     `json:"meeting_id"`
	PostID             string     `json:"post_id"`
	ChannelID          string     `json:"channel_id"`
	ChannelName        string     `json:"channel_name"`
	ChannelDisplayName string     `json:"channel_display_name"`
	ChannelType        string     `json:"channel_type"`
	OrganizerID        string     `json:"organizer_id"`
	Summary            string     `json:"summary"`
	Start              int64      `json:"start"`
	End                int64      `json:"end"`
	Recurring          bool       `json:"recurring"`
	MyRSVP             RSVPStatus `json:"my_rsvp,omitempty"`
}

type communityMeetingsResponse struct {
	Occurrences []communityOccurrence `json:"occurrences"`
	// Truncated says there were more than maxCommunityOccurrences in the range: the latest were
	// left out.
	Truncated bool `json:"truncated"`
}

// millisParam parses a query parameter in milliseconds since the epoch.
func millisParam(value string) (time.Time, bool) {
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

// isCommunityMember returns whether a user is an active member of a community (team).
func (p *Plugin) isCommunityMember(teamID, userID string) bool {
	member, appErr := p.API.GetTeamMember(teamID, userID)
	return appErr == nil && member != nil && member.DeleteAt == 0
}

// communityChannel reads a meeting's channel for the community calendar of teamID, and returns
// whether the caller may see its calls there. The rules, each a regression test: the channel must
// still be in that community (not moved to another), not archived, and a public or private
// channel (never a direct or group message); a private channel only for its members; a public
// one for its members, or for a member of the community who may read its public channels (a
// guest may not). The channel is returned even when it's not readable, so the caller can tell a
// channel that moved to another community (and re-index its meetings) from one it may not see;
// it's never shown in that case.
func (p *Plugin) communityChannel(callerID, teamID, channelID string) (*model.Channel, bool) {
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil {
		return nil, false
	}
	if channel.TeamId != teamID || channel.DeleteAt != 0 {
		return channel, false
	}
	if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
		return channel, false
	}
	member, appErr := p.API.GetChannelMember(channelID, callerID)
	if appErr == nil && member != nil {
		// A member still needs read_channel: a channel's own permission overrides may take it.
		return channel, p.API.HasPermissionToChannel(callerID, channelID, model.PermissionReadChannel)
	}
	if channel.Type == model.ChannelTypeOpen {
		return channel, p.API.HasPermissionToTeam(callerID, teamID, model.PermissionReadPublicChannel)
	}
	return channel, false
}

// readableMeeting is a scheduled call of a community, with the channel it's shown from.
type readableMeeting struct {
	meeting *Meeting
	channel *model.Channel
}

// readableCommunityMeetings returns the calls of a community that callerID may see there, as of
// now: the read rule of the community calendar (GET /meetings) and of the calendar feeds
// (feeds.go) alike. Cancelled calls are left out, and so is every call of a channel callerID may
// not read in that community (communityChannel); a call whose channel moved to another community
// is re-indexed there, and left out too. The caller must have checked that callerID is a member
// of the community.
func (p *Plugin) readableCommunityMeetings(callerID, teamID string) ([]readableMeeting, error) {
	meetings, err := p.store.ListTeamMeetings(teamID)
	if err != nil {
		return nil, err
	}

	type access struct {
		channel  *model.Channel
		readable bool
	}
	channels := map[string]access{}
	var readable []readableMeeting
	for _, m := range meetings {
		if m.Cancelled {
			// Normally dropped from the index when cancelled; this catches a failed drop.
			if err := p.store.removeFromTeamIndex(teamID, m.ID); err != nil {
				p.API.LogWarn("Failed to drop a cancelled call from its community's index", "err", err.Error())
			}
			continue
		}
		a, seen := channels[m.ChannelID]
		if !seen {
			channel, ok := p.communityChannel(callerID, teamID, m.ChannelID)
			a = access{channel: channel, readable: ok}
			channels[m.ChannelID] = a
		}
		if a.channel != nil && a.channel.TeamId != teamID {
			p.reindexMeeting(m, a.channel.TeamId)
			continue
		}
		if !a.readable {
			continue
		}
		readable = append(readable, readableMeeting{meeting: m, channel: a.channel})
	}
	return readable, nil
}

// reindexMeeting moves a meeting whose channel is now in another community (or none) to that
// community's index. Best effort: the next read or the hourly pass tries again.
func (p *Plugin) reindexMeeting(m *Meeting, newTeamID string) {
	if _, err := p.store.MoveMeetingTeam(m.ID, newTeamID); err != nil {
		p.API.LogWarn("Failed to re-index a scheduled call whose channel moved", "meeting_id", m.ID, "err", err.Error())
	}
}

// handleListCommunityMeetings returns the occurrences of a community's scheduled calls in a
// range, for its calendar layer: GET /meetings?team_id=&from=&to= (milliseconds). The caller must
// be a member of the community (403 otherwise), and sees only the calls of the channels they may
// read there (communityChannel). Cancelled calls and cancelled occurrences are left out; a series
// is expanded only within the range, which may be at most maxCommunityRange long.
func (p *Plugin) handleListCommunityMeetings(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	teamID := query.Get("team_id")
	if !model.IsValidId(teamID) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid community"))
		return
	}
	from, okFrom := millisParam(query.Get("from"))
	to, okTo := millisParam(query.Get("to"))
	if !okFrom || !okTo {
		p.writeError(w, newAPIError(http.StatusBadRequest, "invalid range"))
		return
	}
	if from.After(to) {
		p.writeError(w, newAPIError(http.StatusBadRequest, "the range must start before it ends"))
		return
	}
	if to.Sub(from) > maxCommunityRange {
		p.writeError(w, newAPIError(http.StatusBadRequest, "the range is too long"))
		return
	}

	callerID := userID(r)
	if !p.isCommunityMember(teamID, callerID) {
		p.writeError(w, newAPIError(http.StatusForbidden, "you aren't a member of this community"))
		return
	}

	readable, err := p.readableCommunityMeetings(callerID, teamID)
	if err != nil {
		p.writeError(w, err)
		return
	}

	occurrences := []communityOccurrence{}
	for _, rm := range readable {
		m, channel := rm.meeting, rm.channel
		starts, err := meetingOccurrences(m, from, to)
		if err != nil {
			continue
		}
		duration := meetingDuration(m)
		for _, start := range starts {
			o := communityOccurrence{
				MeetingID: m.ID, PostID: m.PostID,
				ChannelID: channel.Id, ChannelName: channel.Name, ChannelDisplayName: channel.DisplayName, ChannelType: string(channel.Type),
				OrganizerID: m.OrganizerID, Summary: m.Summary,
				Start: start.UnixMilli(), End: start.Add(duration).UnixMilli(),
				Recurring: m.recurring(),
			}
			if rsvp, ok := m.effectiveRSVP(callerID, o.Start); ok {
				o.MyRSVP = rsvp.Status
			}
			occurrences = append(occurrences, o)
		}
	}

	sort.Slice(occurrences, func(i, j int) bool {
		if occurrences[i].Start != occurrences[j].Start {
			return occurrences[i].Start < occurrences[j].Start
		}
		return occurrences[i].MeetingID < occurrences[j].MeetingID
	})
	response := communityMeetingsResponse{Occurrences: occurrences}
	if len(occurrences) > maxCommunityOccurrences {
		response.Occurrences = occurrences[:maxCommunityOccurrences]
		response.Truncated = true
	}
	p.writeJSON(w, response)
}

// reindexMovedMeetings checks, at most every meetingReindexInterval, whether any meeting's channel
// moved to another community since it was indexed (no hook tells an app), and re-indexes it: a
// call moved into a community then shows in its calendar and header even before anyone reads it.
// Run by the cluster job, on one server.
func (p *Plugin) reindexMovedMeetings(now time.Time) {
	p.meetingReindexMu.Lock()
	if !p.meetingReindexAt.IsZero() && now.Sub(p.meetingReindexAt) < meetingReindexInterval {
		p.meetingReindexMu.Unlock()
		return
	}
	p.meetingReindexAt = now
	p.meetingReindexMu.Unlock()

	ids, err := p.store.ListMeetingIDs()
	if err != nil {
		p.API.LogWarn("Failed to list the scheduled calls to re-index", "err", err.Error())
		return
	}
	for _, id := range ids {
		m, err := p.store.GetMeeting(id)
		if err != nil || m == nil || m.Cancelled {
			continue
		}
		channel, appErr := p.API.GetChannel(m.ChannelID)
		if appErr != nil || channel == nil {
			continue
		}
		if channel.TeamId != m.TeamID {
			p.reindexMeeting(m, channel.TeamId)
		}
	}
}
