// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRemindersOf(t *testing.T) {
	start := time.Date(2026, 10, 5, 11, 0, 0, 0, time.UTC)
	occurrences := []Occurrence{
		{UID: "alarms", Start: start.UnixMilli(), Alarms: []int{5, 30}},
		{UID: "default", Start: start.UnixMilli()},
		{UID: "all-day", Start: start.UnixMilli(), AllDay: true},
	}

	reminders := remindersOf(occurrences, 10)
	require.Len(t, reminders, 3)
	assert.Equal(t, start.Add(-5*time.Minute).UnixMilli(), reminders[0].due.UnixMilli())
	assert.Equal(t, start.Add(-30*time.Minute).UnixMilli(), reminders[1].due.UnixMilli())
	assert.Equal(t, start.Add(-10*time.Minute).UnixMilli(), reminders[2].due.UnixMilli(), "events without alarms use the default")
	assert.Len(t, remindersOf(occurrences, 0), 2, "no default reminder")

	r := reminders[0]
	assert.False(t, r.isDue(r.due.Add(-time.Second)))
	assert.True(t, r.isDue(r.due))
	assert.True(t, r.isDue(r.due.Add(reminderGrace)))
	assert.False(t, r.isDue(r.due.Add(reminderGrace+time.Second)))

	assert.Equal(t, r.sentKey("user1"), r.sentKey("user1"))
	assert.NotEqual(t, r.sentKey("user1"), r.sentKey("user2"))
	assert.NotEqual(t, r.sentKey("user1"), reminders[1].sentKey("user1"))
	assert.LessOrEqual(t, len(r.sentKey("user1")), 50, "KV keys are short")
}

func TestReminderMessage(t *testing.T) {
	paris := mustLoad(t, "Europe/Paris")
	now := time.Date(2026, 10, 5, 10, 50, 0, 0, paris)
	o := Occurrence{Summary: "Calibration *review* @all", Location: "Hall B", Start: time.Date(2026, 10, 5, 11, 0, 0, 0, paris).UnixMilli()}

	assert.Equal(t, ":calendar: **Calibration \\*review\\* @\u200ball** starts in 10 minutes, at 11:00.\nLocation: Hall B", reminderMessage(o, now, paris, nil, ""))

	link := &EventLink{DisplayName: "Detector builds", Kind: LinkCall}
	assert.True(t, strings.HasSuffix(reminderMessage(o, now, paris, link, "https://chat.example.com/lab/channels/detector-builds"),
		"\nJoin the call in [Detector builds](https://chat.example.com/lab/channels/detector-builds)"))
	link.Kind = LinkChannel
	assert.True(t, strings.HasSuffix(reminderMessage(o, now, paris, link, ""), "\nJoin Detector builds"))

	assert.Contains(t, reminderMessage(o, now.Add(-50*time.Minute), paris, nil, ""), "starts in an hour, at 11:00")
	assert.Contains(t, reminderMessage(o, now.Add(-3*time.Hour), paris, nil, ""), "starts in 3 hours, at 11:00")
	assert.Contains(t, reminderMessage(o, now.Add(time.Hour), paris, nil, ""), "starts now, at 11:00")

	allDay := Occurrence{Summary: "Beam time", AllDay: true, Start: time.Date(2026, 10, 5, 0, 0, 0, 0, paris).UnixMilli()}
	assert.Contains(t, reminderMessage(allDay, now, paris, nil, ""), "starts today, all day")
}

func TestRemindersRun(t *testing.T) {
	e := newTestEnv(t)
	e.connect(t)
	e.p.botID = model.NewId()
	e.p.reminders = newReminderService(e.p)

	user := &model.User{Id: e.userID, Timezone: model.StringMap{"useAutomaticTimezone": "false", "manualTimezone": "Europe/Paris"}}
	e.api.On("GetUser", e.userID).Return(user, nil)
	dm := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect}
	e.api.On("GetDirectChannel", e.userID, e.p.botID).Return(dm, nil)

	paris := mustLoad(t, "Europe/Paris")
	alarm := 15
	w := e.request(t, http.MethodPost, "/api/v1/events", EventInput{
		Calendar: testWork, Summary: "Calibration review", Start: "2026-10-05T11:00:00+02:00", End: "2026-10-05T12:00:00+02:00",
		TimeZone: "Europe/Paris", Alarm: &alarm,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Nothing due yet
	e.p.reminders.run(time.Date(2026, 10, 5, 10, 40, 0, 0, paris))

	e.api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.UserId == e.p.botID && post.ChannelId == dm.Id &&
			strings.HasPrefix(post.Message, ":calendar: **Calibration review** starts in 15 minutes, at 11:00.")
	})).Return(&model.Post{}, nil).Once()
	e.p.reminders.run(time.Date(2026, 10, 5, 10, 45, 0, 0, paris))

	// Sent once, even when the events are read again
	e.p.reminders.run(time.Date(2026, 10, 5, 10, 46, 0, 0, paris))
	e.p.reminders.invalidate(e.userID)
	e.p.reminders.run(time.Date(2026, 10, 5, 10, 47, 0, 0, paris))

	// No reminders when they're turned off
	e.p.configuration = &configuration{AllowPrivateNetworks: true, AllowInsecureConnections: true}
	e.p.reminders.invalidate(e.userID)
	e.p.reminders.run(time.Date(2026, 10, 5, 10, 45, 0, 0, paris))
}
