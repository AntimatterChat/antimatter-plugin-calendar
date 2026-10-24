// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nextTestMeeting(id, channelID string, start time.Time, duration time.Duration) *Meeting {
	return &Meeting{
		ID: id, ChannelID: channelID, TeamID: "team1", PostID: "post-" + id, Summary: id,
		Start: start.UnixMilli(), End: start.Add(duration).UnixMilli(),
	}
}

// TestPickNextMeeting checks what the channel header shows: the live call first, else the next
// one within 7 days; never another channel's, a cancelled call, a cancelled occurrence (the
// series' next one instead), or a call that's over.
func TestPickNextMeeting(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	const channelID = "channel1"

	t.Run("nothing to show", func(t *testing.T) {
		assert.Nil(t, pickNextMeeting(nil, channelID, now))
		over := nextTestMeeting("over", channelID, now.Add(-2*time.Hour), time.Hour)
		assert.Nil(t, pickNextMeeting([]*Meeting{over}, channelID, now), "a call that's over")
		far := nextTestMeeting("far", channelID, now.Add(nextMeetingWindow+time.Minute), time.Hour)
		assert.Nil(t, pickNextMeeting([]*Meeting{far}, channelID, now), "beyond 7 days")
		other := nextTestMeeting("other", "channel2", now.Add(time.Hour), time.Hour)
		assert.Nil(t, pickNextMeeting([]*Meeting{other}, channelID, now), "another channel's call")
		cancelled := nextTestMeeting("cancelled", channelID, now.Add(time.Hour), time.Hour)
		cancelled.Cancelled = true
		assert.Nil(t, pickNextMeeting([]*Meeting{cancelled}, channelID, now), "a cancelled call")
	})

	t.Run("the earliest within 7 days", func(t *testing.T) {
		later := nextTestMeeting("later", channelID, now.Add(3*24*time.Hour), time.Hour)
		sooner := nextTestMeeting("sooner", channelID, now.Add(2*time.Hour), time.Hour)
		edge := nextTestMeeting("edge", channelID, now.Add(nextMeetingWindow), time.Hour)
		next := pickNextMeeting([]*Meeting{later, edge, sooner}, channelID, now)
		require.NotNil(t, next)
		assert.Equal(t, "sooner", next.MeetingID)
		assert.Equal(t, "post-sooner", next.PostID)
		assert.Equal(t, now.Add(2*time.Hour).UnixMilli(), next.Start)
		assert.Equal(t, now.Add(3*time.Hour).UnixMilli(), next.End)
		assert.False(t, next.Live)

		next = pickNextMeeting([]*Meeting{edge}, channelID, now)
		require.NotNil(t, next, "exactly 7 days ahead is still shown")
	})

	t.Run("live first", func(t *testing.T) {
		live := nextTestMeeting("live", channelID, now.Add(-10*time.Minute), time.Hour)
		soon := nextTestMeeting("soon", channelID, now.Add(5*time.Minute), time.Hour)
		next := pickNextMeeting([]*Meeting{soon, live}, channelID, now)
		require.NotNil(t, next)
		assert.Equal(t, "live", next.MeetingID)
		assert.True(t, next.Live)

		// It's no longer live at its end.
		next = pickNextMeeting([]*Meeting{live}, channelID, now.Add(50*time.Minute))
		assert.Nil(t, next)
	})

	t.Run("a series skips its cancelled occurrence", func(t *testing.T) {
		series := nextTestMeeting("series", channelID, now.Add(time.Hour), 30*time.Minute)
		series.RecurrenceRule = "FREQ=DAILY"
		series.ExceptionDates = []int64{now.Add(time.Hour).UnixMilli()}
		next := pickNextMeeting([]*Meeting{series}, channelID, now)
		require.NotNil(t, next)
		assert.Equal(t, now.Add(25*time.Hour).UnixMilli(), next.Start, "the next day's, not the cancelled one")
		assert.True(t, next.Recurring)
	})

	t.Run("a series' live occurrence", func(t *testing.T) {
		series := nextTestMeeting("series", channelID, now.Add(-3*24*time.Hour-15*time.Minute), 30*time.Minute)
		series.RecurrenceRule = "FREQ=DAILY"
		next := pickNextMeeting([]*Meeting{series}, channelID, now)
		require.NotNil(t, next)
		assert.True(t, next.Live)
		assert.Equal(t, now.Add(-15*time.Minute).UnixMilli(), next.Start)
	})
}

// TestHandleNextChannelMeeting checks the channel header's endpoint: read_channel or a 404 that
// says nothing of the channel's calls, refused for direct and group messages, and the next call
// (or null) otherwise.
func TestHandleNextChannelMeeting(t *testing.T) {
	path := func(channelID string) string { return "/api/v1/channels/" + channelID + "/meetings/next" }

	t.Run("invalid channel", func(t *testing.T) {
		e := newMeetingTestEnv(t)
		w := e.request(t, http.MethodGet, path("nope"), nil)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("a channel the caller can't read", func(t *testing.T) {
		e := newMeetingTestEnv(t)
		m := indexedMeeting(t, e, "team1", model.NewId(), "Private plans")
		e.api.On("HasPermissionToChannel", e.userID, m.ChannelID, model.PermissionReadChannel).Return(false)
		w := e.request(t, http.MethodGet, path(m.ChannelID), nil)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NotContains(t, w.Body.String(), "Private plans")
	})

	for _, channelType := range []model.ChannelType{model.ChannelTypeDirect, model.ChannelTypeGroup} {
		t.Run("refused in "+string(channelType), func(t *testing.T) {
			e := newMeetingTestEnv(t)
			channelID := model.NewId()
			e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)
			e.api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: channelType}, nil)
			w := e.request(t, http.MethodGet, path(channelID), nil)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}

	t.Run("the next call", func(t *testing.T) {
		e := newMeetingTestEnv(t)
		m := indexedMeeting(t, e, "team1", model.NewId(), "Standup")
		e.api.On("HasPermissionToChannel", e.userID, m.ChannelID, model.PermissionReadChannel).Return(true)
		e.api.On("GetChannel", m.ChannelID).Return(&model.Channel{Id: m.ChannelID, TeamId: "team1", Type: model.ChannelTypeOpen}, nil)
		w := e.request(t, http.MethodGet, path(m.ChannelID), nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		got := decode[map[string]*nextMeeting](t, w)
		require.NotNil(t, got["next"])
		assert.Equal(t, m.ID, got["next"].MeetingID)
		assert.Equal(t, m.PostID, got["next"].PostID)
		assert.Equal(t, "Standup", got["next"].Summary)
		assert.Equal(t, m.Start, got["next"].Start)
		assert.Equal(t, m.End, got["next"].End)
		assert.False(t, got["next"].Live)
	})

	t.Run("nothing to show", func(t *testing.T) {
		e := newMeetingTestEnv(t)
		channelID := model.NewId()
		e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)
		e.api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, TeamId: "team1", Type: model.ChannelTypePrivate}, nil)
		w := e.request(t, http.MethodGet, path(channelID), nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"next": null}`, w.Body.String())
	})

	t.Run("an archived channel shows nothing", func(t *testing.T) {
		e := newMeetingTestEnv(t)
		m := indexedMeeting(t, e, "team1", model.NewId(), "Standup")
		e.api.On("HasPermissionToChannel", e.userID, m.ChannelID, model.PermissionReadChannel).Return(true)
		e.api.On("GetChannel", m.ChannelID).Return(&model.Channel{Id: m.ChannelID, TeamId: "team1", Type: model.ChannelTypeOpen, DeleteAt: 1}, nil)
		w := e.request(t, http.MethodGet, path(m.ChannelID), nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"next": null}`, w.Body.String())
	})
}
