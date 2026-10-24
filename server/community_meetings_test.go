// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errNotAMember = model.NewAppError("test", "test.not_a_member", nil, "", http.StatusNotFound)

func communityPath(teamID string, from, to time.Time) string {
	return fmt.Sprintf("/api/v1/meetings?team_id=%s&from=%d&to=%d", teamID, from.UnixMilli(), to.UnixMilli())
}

// indexedMeeting stores a meeting in a channel, indexed under a community.
func indexedMeeting(t *testing.T, e *testEnv, teamID, channelID, summary string) *Meeting {
	t.Helper()
	m := testMeeting()
	m.ID = model.NewId()
	m.PostID = model.NewId()
	m.TeamID = teamID
	m.ChannelID = channelID
	m.Summary = summary
	require.NoError(t, e.p.store.CreateMeeting(m))
	return m
}

func teamIndexIDs(t *testing.T, e *testEnv, teamID string) []string {
	t.Helper()
	meetings, err := e.p.store.ListTeamMeetings(teamID)
	require.NoError(t, err)
	ids := make([]string, 0, len(meetings))
	for _, m := range meetings {
		ids = append(ids, m.ID)
	}
	return ids
}

func summariesOf(occurrences []communityOccurrence) []string {
	summaries := make([]string, 0, len(occurrences))
	for _, o := range occurrences {
		summaries = append(summaries, o.Summary)
	}
	return summaries
}

// TestListCommunityMeetingsValidation checks the range rules: milliseconds, from not after to,
// and at most 100 days, each refused with a 400 before anything is read.
func TestListCommunityMeetingsValidation(t *testing.T) {
	e := newMeetingTestEnv(t)
	teamID := model.NewId()
	now := time.Now()

	for name, path := range map[string]string{
		"invalid community":    communityPath("not-an-id", now, now.Add(time.Hour)),
		"no range":             "/api/v1/meetings?team_id=" + teamID,
		"not milliseconds":     "/api/v1/meetings?team_id=" + teamID + "&from=2026-10-24T00:00:00Z&to=2026-10-25T00:00:00Z",
		"from after to":        communityPath(teamID, now.Add(time.Hour), now),
		"longer than 100 days": communityPath(teamID, now, now.Add(maxCommunityRange+time.Minute)),
	} {
		t.Run(name, func(t *testing.T) {
			w := e.request(t, http.MethodGet, path, nil)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}
}

// TestListCommunityMeetingsRequiresMembership checks that only a member of the community reads
// its calendar: anyone else, or a former member, gets a 403 and none of its calls.
func TestListCommunityMeetingsRequiresMembership(t *testing.T) {
	now := time.Now()

	t.Run("not a member", func(t *testing.T) {
		e := newMeetingTestEnv(t)
		teamID := model.NewId()
		indexedMeeting(t, e, teamID, model.NewId(), "Secret plans")
		e.api.On("GetTeamMember", teamID, e.userID).Return(nil, errNotAMember)

		w := e.request(t, http.MethodGet, communityPath(teamID, now, now.Add(24*time.Hour)), nil)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.NotContains(t, w.Body.String(), "Secret plans")
	})

	t.Run("a former member", func(t *testing.T) {
		e := newMeetingTestEnv(t)
		teamID := model.NewId()
		indexedMeeting(t, e, teamID, model.NewId(), "Secret plans")
		e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{TeamId: teamID, UserId: e.userID, DeleteAt: model.GetMillis()}, nil)

		w := e.request(t, http.MethodGet, communityPath(teamID, now, now.Add(24*time.Hour)), nil)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.NotContains(t, w.Body.String(), "Secret plans")
	})

	t.Run("a range of exactly 100 days is allowed", func(t *testing.T) {
		e := newMeetingTestEnv(t)
		teamID := model.NewId()
		e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{TeamId: teamID, UserId: e.userID}, nil)

		w := e.request(t, http.MethodGet, communityPath(teamID, now, now.Add(maxCommunityRange)), nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"occurrences": [], "truncated": false}`, w.Body.String())
	})
}

// TestListCommunityMeetingsVisibility is the privacy and RBAC regression test of the community
// calendar: a member sees the calls of the channels they're in, and of the community's public
// channels, and of private channels only when they're a member; never an archived channel's, a
// direct or group message's, another community's, a cancelled call, or a call outside the range.
func TestListCommunityMeetingsVisibility(t *testing.T) {
	e := newMeetingTestEnv(t)
	teamID := model.NewId()
	otherTeamID := model.NewId()
	now := time.Now()
	e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{TeamId: teamID, UserId: e.userID}, nil)

	// Every case's caller may read the community's public channels (the guest rule, where they
	// may not, is TestListCommunityMeetingsGuestPublicChannel), so a case hidden here is hidden
	// by its channel alone. readChannel is what HasPermissionToChannel(read_channel) answers,
	// as it would for a system admin even outside a private channel.
	type channelCase struct {
		channel     model.Channel
		member      bool
		readChannel bool
		visible     bool
	}
	cases := map[string]channelCase{
		"public, member":                        {channel: model.Channel{Type: model.ChannelTypeOpen}, member: true, readChannel: true, visible: true},
		"public, not a member":                  {channel: model.Channel{Type: model.ChannelTypeOpen}, visible: true},
		"private, member":                       {channel: model.Channel{Type: model.ChannelTypePrivate}, member: true, readChannel: true, visible: true},
		"private, not a member (even an admin)": {channel: model.Channel{Type: model.ChannelTypePrivate}, readChannel: true},
		"private, member without read_channel":  {channel: model.Channel{Type: model.ChannelTypePrivate}, member: true},
		"public, member without read_channel":   {channel: model.Channel{Type: model.ChannelTypeOpen}, member: true},
		"archived public, member":               {channel: model.Channel{Type: model.ChannelTypeOpen, DeleteAt: 1}, member: true, readChannel: true},
		"archived private, member":              {channel: model.Channel{Type: model.ChannelTypePrivate, DeleteAt: 1}, member: true, readChannel: true},
		"direct message, member":                {channel: model.Channel{Type: model.ChannelTypeDirect}, member: true, readChannel: true},
		"group message, member":                 {channel: model.Channel{Type: model.ChannelTypeGroup}, member: true, readChannel: true},
		"a board, member":                       {channel: model.Channel{Type: model.ChannelTypeOpenBoard}, member: true, readChannel: true},
		"moved to another community, member":    {channel: model.Channel{Type: model.ChannelTypeOpen, TeamId: otherTeamID}, member: true, readChannel: true},
	}

	var want []string
	var movedID string
	for name, c := range cases {
		channelID := model.NewId()
		channel := c.channel
		channel.Id = channelID
		channel.Name = strings.ReplaceAll(name, " ", "-")
		channel.DisplayName = name
		if channel.TeamId == "" {
			channel.TeamId = teamID
		}
		m := indexedMeeting(t, e, teamID, channelID, name)
		if channel.TeamId != teamID {
			movedID = m.ID
		}
		if c.visible {
			want = append(want, name)
		}

		e.api.On("GetChannel", channelID).Return(&channel, nil)
		if c.member {
			e.api.On("GetChannelMember", channelID, e.userID).Return(&model.ChannelMember{ChannelId: channelID, UserId: e.userID}, nil).Maybe()
		} else {
			e.api.On("GetChannelMember", channelID, e.userID).Return(nil, errNotAMember).Maybe()
		}
		e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(c.readChannel).Maybe()
	}
	e.api.On("HasPermissionToTeam", e.userID, teamID, model.PermissionReadPublicChannel).Return(true).Maybe()

	// A cancelled call and calls outside the range, in a channel the caller may read.
	readable := model.NewId()
	e.api.On("GetChannel", readable).Return(&model.Channel{Id: readable, TeamId: teamID, Type: model.ChannelTypeOpen, Name: "readable"}, nil)
	e.api.On("GetChannelMember", readable, e.userID).Return(&model.ChannelMember{}, nil)
	e.api.On("HasPermissionToChannel", e.userID, readable, model.PermissionReadChannel).Return(true)
	cancelled := indexedMeeting(t, e, teamID, readable, "Cancelled call")
	_, err := e.p.store.UpdateMeeting(cancelled.ID, func(m *Meeting) error { m.Cancelled = true; return nil })
	require.NoError(t, err)
	tooLate := indexedMeeting(t, e, teamID, readable, "Next month")
	_, err = e.p.store.UpdateMeeting(tooLate.ID, func(m *Meeting) error {
		m.Start = now.Add(40 * 24 * time.Hour).UnixMilli()
		m.End = now.Add(40*24*time.Hour + time.Hour).UnixMilli()
		return nil
	})
	require.NoError(t, err)
	want = append(want, "In range")
	indexedMeeting(t, e, teamID, readable, "In range")

	w := e.request(t, http.MethodGet, communityPath(teamID, now, now.Add(7*24*time.Hour)), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	got := decode[communityMeetingsResponse](t, w)

	assert.ElementsMatch(t, want, summariesOf(got.Occurrences))
	assert.False(t, got.Truncated)
	for _, o := range got.Occurrences {
		assert.NotEmpty(t, o.PostID)
		assert.NotEmpty(t, o.ChannelName)
		assert.Contains(t, []string{string(model.ChannelTypeOpen), string(model.ChannelTypePrivate)}, o.ChannelType)
	}

	// The cancelled call is dropped from the index, and the call whose channel moved is now in
	// its new community's index, not this one's.
	ids := teamIndexIDs(t, e, teamID)
	assert.NotContains(t, ids, cancelled.ID)
	assert.NotContains(t, ids, movedID)
	assert.Contains(t, teamIndexIDs(t, e, otherTeamID), movedID)
	moved, err := e.p.store.GetMeeting(movedID)
	require.NoError(t, err)
	assert.Equal(t, otherTeamID, moved.TeamID)
}

// TestListCommunityMeetingsGuestPublicChannel checks the public-channel rule on its own: a
// community member who isn't in a public channel sees its calls only if they may read the
// community's public channels (a guest may not).
func TestListCommunityMeetingsGuestPublicChannel(t *testing.T) {
	for name, readPublic := range map[string]bool{"member": true, "guest": false} {
		t.Run(name, func(t *testing.T) {
			e := newMeetingTestEnv(t)
			teamID := model.NewId()
			channelID := model.NewId()
			now := time.Now()
			indexedMeeting(t, e, teamID, channelID, "Town hall")
			e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{TeamId: teamID, UserId: e.userID}, nil)
			e.api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, TeamId: teamID, Type: model.ChannelTypeOpen, Name: "town-hall"}, nil)
			e.api.On("GetChannelMember", channelID, e.userID).Return(nil, errNotAMember)
			e.api.On("HasPermissionToTeam", e.userID, teamID, model.PermissionReadPublicChannel).Return(readPublic)

			w := e.request(t, http.MethodGet, communityPath(teamID, now, now.Add(24*time.Hour)), nil)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			got := decode[communityMeetingsResponse](t, w)
			if readPublic {
				assert.Equal(t, []string{"Town hall"}, summariesOf(got.Occurrences))
			} else {
				assert.Empty(t, got.Occurrences)
			}
		})
	}
}

// TestListCommunityMeetingsRecurrence checks that a series is expanded within the range only,
// that a cancelled occurrence (EXDATE) is left out, and that each occurrence carries the caller's
// own RSVP for it (a "next call only" answer for that one), never anyone else's.
func TestListCommunityMeetingsRecurrence(t *testing.T) {
	e := newMeetingTestEnv(t)
	teamID := model.NewId()
	channelID := model.NewId()
	otherUser := model.NewId()
	e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{TeamId: teamID, UserId: e.userID}, nil)
	e.api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, TeamId: teamID, Type: model.ChannelTypeOpen, Name: "standup"}, nil)
	e.api.On("GetChannelMember", channelID, e.userID).Return(&model.ChannelMember{}, nil)
	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)

	start := time.Now().Add(time.Hour).Truncate(time.Minute)
	m := indexedMeeting(t, e, teamID, channelID, "Weekly")
	week := 7 * 24 * time.Hour
	_, err := e.p.store.UpdateMeeting(m.ID, func(m *Meeting) error {
		m.Start, m.End = start.UnixMilli(), start.Add(30*time.Minute).UnixMilli()
		m.RecurrenceRule = "FREQ=WEEKLY"
		m.ExceptionDates = []int64{start.Add(2 * week).UnixMilli()}
		m.RSVPs = map[string]RSVP{e.userID: {Status: RSVPGoing}, otherUser: {Status: RSVPNo}}
		m.NextRSVPs = map[string]occurrenceRSVP{e.userID: {RSVP: RSVP{Status: RSVPMaybe}, OccurrenceStart: start.Add(week).UnixMilli()}}
		return nil
	})
	require.NoError(t, err)

	// Four weeks from just before the first occurrence: weeks 0, 1 and 3 (week 2 is cancelled);
	// week 4 starts exactly at the end of the range, so it's out.
	from := start.Add(-time.Minute)
	w := e.request(t, http.MethodGet, communityPath(teamID, from, start.Add(4*week)), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), otherUser, "nobody else's RSVP is in the response")
	got := decode[communityMeetingsResponse](t, w)

	require.Len(t, got.Occurrences, 3)
	assert.Equal(t, start.UnixMilli(), got.Occurrences[0].Start)
	assert.Equal(t, start.Add(30*time.Minute).UnixMilli(), got.Occurrences[0].End)
	assert.Equal(t, RSVPGoing, got.Occurrences[0].MyRSVP)
	assert.Equal(t, start.Add(week).UnixMilli(), got.Occurrences[1].Start)
	assert.Equal(t, RSVPMaybe, got.Occurrences[1].MyRSVP, "the next-call-only answer, for that occurrence alone")
	assert.Equal(t, start.Add(3*week).UnixMilli(), got.Occurrences[2].Start)
	assert.Equal(t, RSVPGoing, got.Occurrences[2].MyRSVP)
	for _, o := range got.Occurrences {
		assert.True(t, o.Recurring)
	}
}

// TestListCommunityMeetingsCapped checks that a response carries at most
// maxCommunityOccurrences, the earliest ones, in order, and says it was truncated.
func TestListCommunityMeetingsCapped(t *testing.T) {
	e := newMeetingTestEnv(t)
	teamID := model.NewId()
	channelID := model.NewId()
	e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{TeamId: teamID, UserId: e.userID}, nil)
	e.api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, TeamId: teamID, Type: model.ChannelTypeOpen, Name: "busy"}, nil)
	e.api.On("GetChannelMember", channelID, e.userID).Return(&model.ChannelMember{}, nil)
	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)

	// Six daily series over 100 days: about 600 occurrences.
	start := time.Now().Add(time.Hour).Truncate(time.Minute)
	for i := range 6 {
		m := indexedMeeting(t, e, teamID, channelID, fmt.Sprintf("Daily %d", i))
		_, err := e.p.store.UpdateMeeting(m.ID, func(m *Meeting) error {
			m.Start = start.Add(time.Duration(i) * time.Minute).UnixMilli()
			m.End = m.Start + int64(15*time.Minute/time.Millisecond)
			m.RecurrenceRule = "FREQ=DAILY"
			return nil
		})
		require.NoError(t, err)
	}

	w := e.request(t, http.MethodGet, communityPath(teamID, start.Add(-time.Minute), start.Add(maxCommunityRange-time.Hour)), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	got := decode[communityMeetingsResponse](t, w)
	assert.True(t, got.Truncated)
	require.Len(t, got.Occurrences, maxCommunityOccurrences)
	for i := 1; i < len(got.Occurrences); i++ {
		assert.LessOrEqual(t, got.Occurrences[i-1].Start, got.Occurrences[i].Start)
	}
	assert.Equal(t, start.UnixMilli(), got.Occurrences[0].Start)
}

// TestListCommunityMeetingsStaleIndex checks that a meeting whose key expired is dropped from the
// community's index on read, and never shown.
func TestListCommunityMeetingsStaleIndex(t *testing.T) {
	e := newMeetingTestEnv(t)
	teamID := model.NewId()
	m := indexedMeeting(t, e, teamID, model.NewId(), "Expired")
	e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{TeamId: teamID, UserId: e.userID}, nil)
	require.Nil(t, e.p.store.kv.KVDelete(meetingKey(m.ID)))

	now := time.Now()
	w := e.request(t, http.MethodGet, communityPath(teamID, now, now.Add(24*time.Hour)), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Empty(t, decode[communityMeetingsResponse](t, w).Occurrences)
	assert.Empty(t, teamIndexIDs(t, e, teamID))
}

// TestStoreMoveMeetingTeam checks that a meeting moved to another community leaves the old
// index for the new one, and that one moved out of any community (an empty team) leaves both.
func TestStoreMoveMeetingTeam(t *testing.T) {
	store, _ := newTestStore(t)
	m := testMeeting()
	require.NoError(t, store.CreateMeeting(m))

	moved, err := store.MoveMeetingTeam(m.ID, "team2")
	require.NoError(t, err)
	assert.Equal(t, "team2", moved.TeamID)
	old, err := store.ListTeamMeetings("team1")
	require.NoError(t, err)
	assert.Empty(t, old)
	now, err := store.ListTeamMeetings("team2")
	require.NoError(t, err)
	require.Len(t, now, 1)
	assert.Equal(t, m.ID, now[0].ID)

	_, err = store.MoveMeetingTeam(m.ID, "")
	require.NoError(t, err)
	now, err = store.ListTeamMeetings("team2")
	require.NoError(t, err)
	assert.Empty(t, now)
}

// TestStoreListMeetingIDsSkipsTheIndexes checks that a community's index is never taken for a
// meeting by the reminder job's listing.
func TestStoreListMeetingIDsSkipsTheIndexes(t *testing.T) {
	store, _ := newTestStore(t)
	require.NoError(t, store.CreateMeeting(testMeeting()))

	ids, err := store.ListMeetingIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"meeting1"}, ids)
}

// TestReindexMovedMeetings checks the hourly pass that catches a channel moved to another
// community: the meeting moves to that community's index, and the pass doesn't run again
// within the hour.
func TestReindexMovedMeetings(t *testing.T) {
	e := newMeetingTestEnv(t)
	m := testMeeting()
	require.NoError(t, e.p.store.CreateMeeting(m))
	e.api.On("GetChannel", m.ChannelID).Return(&model.Channel{Id: m.ChannelID, TeamId: "team2", Type: model.ChannelTypeOpen}, nil).Once()

	now := time.Now()
	e.p.reindexMovedMeetings(now)
	assert.Empty(t, teamIndexIDs(t, e, "team1"))
	assert.Equal(t, []string{m.ID}, teamIndexIDs(t, e, "team2"))

	// Within the hour: nothing is read again (GetChannel is expected once only).
	e.p.reindexMovedMeetings(now.Add(30 * time.Minute))
}
