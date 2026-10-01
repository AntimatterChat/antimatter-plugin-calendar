// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/nicksnyder/go-i18n/v2/i18n"
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
	en := newTranslator(nil, "en")
	paris := mustLoad(t, "Europe/Paris")
	now := time.Date(2026, 10, 5, 10, 50, 0, 0, paris)
	o := Occurrence{Summary: "Calibration *review* @all", Location: "Hall B", Start: time.Date(2026, 10, 5, 11, 0, 0, 0, paris).UnixMilli()}

	assert.Equal(t, ":calendar: **Calibration \\*review\\* @\u200ball** starts in 10 minutes, at 11:00.\nLocation: Hall B", reminderMessage(en, o, now, paris, nil, ""))

	link := &EventLink{DisplayName: "Detector builds", Kind: LinkCall}
	assert.True(t, strings.HasSuffix(reminderMessage(en, o, now, paris, link, "https://chat.example.com/lab/channels/detector-builds"),
		"\nJoin the call in [Detector builds](https://chat.example.com/lab/channels/detector-builds)"))
	link.Kind = LinkChannel
	assert.True(t, strings.HasSuffix(reminderMessage(en, o, now, paris, link, ""), "\nJoin Detector builds"))

	assert.Contains(t, reminderMessage(en, o, now.Add(-50*time.Minute), paris, nil, ""), "starts in an hour, at 11:00")
	assert.Contains(t, reminderMessage(en, o, now.Add(-3*time.Hour), paris, nil, ""), "starts in 3 hours, at 11:00")
	assert.Contains(t, reminderMessage(en, o, now.Add(time.Hour), paris, nil, ""), "starts now, at 11:00")

	allDay := Occurrence{Summary: "Beam time", AllDay: true, Start: time.Date(2026, 10, 5, 0, 0, 0, 0, paris).UnixMilli()}
	assert.Contains(t, reminderMessage(en, allDay, now, paris, nil, ""), "starts today, all day")
	assert.Contains(t, reminderMessage(en, allDay, now.AddDate(0, 0, -1), paris, nil, ""), "starts Monday 5 October, all day")
	o.Start = now.Add(time.Minute).UnixMilli()
	assert.Contains(t, reminderMessage(en, o, now, paris, nil, ""), "starts in 1 minute, at 10:51")
}

func TestTranslatedReminderMessage(t *testing.T) {
	bundle, err := newTranslations(filepath.Join("..", translationsDir))
	require.NoError(t, err)
	paris := mustLoad(t, "Europe/Paris")
	now := time.Date(2026, 10, 5, 10, 50, 0, 0, paris)
	o := Occurrence{Summary: "Revue", Location: "Hall B", Start: time.Date(2026, 10, 5, 11, 0, 0, 0, paris).UnixMilli()}
	link := &EventLink{DisplayName: "Détecteurs", Kind: LinkCall}

	fr := newTranslator(bundle, "fr")
	assert.Equal(t, ":calendar: **Revue** commence dans 10 minutes, à 11:00.\nLieu : Hall B\nRejoindre l’appel dans Détecteurs", reminderMessage(fr, o, now, paris, link, ""))
	assert.Contains(t, reminderMessage(fr, o, now.Add(9*time.Minute), paris, nil, ""), "dans 1 minute, à 11:00")
	allDay := Occurrence{Summary: "Faisceau", AllDay: true, Start: time.Date(2026, 10, 6, 0, 0, 0, 0, paris).UnixMilli()}
	assert.Contains(t, reminderMessage(fr, allDay, now, paris, nil, ""), "commence mardi 6 octobre, toute la journée")

	de := newTranslator(bundle, "de")
	assert.Contains(t, reminderMessage(de, allDay, now, paris, nil, ""), "beginnt Dienstag, 6. Oktober, ganztägig")
	assert.Contains(t, reminderMessage(de, o, now.Add(-3*time.Hour), paris, nil, ""), "beginnt in 3 Stunden, um 11:00")

	es := newTranslator(bundle, "es")
	assert.Contains(t, reminderMessage(es, o, now, paris, nil, ""), "empieza en 10 minutos, a las 11:00")

	// Regional locales use their language, and unknown ones English
	assert.Contains(t, reminderMessage(newTranslator(bundle, "fr-CA"), o, now, paris, nil, ""), "commence dans 10 minutes")
	assert.Contains(t, reminderMessage(newTranslator(bundle, "ja"), o, now, paris, nil, ""), "starts in 10 minutes")

	// Every translation has every message
	for _, tag := range bundle.LanguageTags() {
		for _, message := range append(append([]*i18n.Message{msgStarts, msgUntitled, msgTodayAllDay, msgDateAllDay, msgNow, msgInMinutes, msgInAnHour, msgInHours, msgLocation, msgJoinCall, msgJoinChannel, msgDate}, msgWeekdays[:]...), msgMonths[:]...) {
			_, translated, err := i18n.NewLocalizer(bundle, tag.String()).LocalizeWithTag(&i18n.LocalizeConfig{MessageID: message.ID, PluralCount: 2})
			if tag.String() != "en" {
				assert.NoError(t, err, "%s %s", tag, message.ID)
				assert.Equal(t, tag, translated, "%s %s", tag, message.ID)
			}
		}
	}
}

func TestRemindersRun(t *testing.T) {
	e := newTestEnv(t)
	e.connect(t)
	e.p.botID = model.NewId()
	e.p.reminders = newReminderService(e.p)
	translations, err := newTranslations(filepath.Join("..", translationsDir))
	require.NoError(t, err)
	e.p.translations = translations

	user := &model.User{Id: e.userID, Locale: "fr", Timezone: model.StringMap{"useAutomaticTimezone": "false", "manualTimezone": "Europe/Paris"}}
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
			strings.HasPrefix(post.Message, ":calendar: **Calibration review** commence dans 15 minutes, à 11:00.")
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
