// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type testEnv struct {
	p       *Plugin
	api     *plugintest.API
	backend *memBackend
	server  *httptest.Server
	userID  string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	api := &plugintest.API{}
	t.Cleanup(func() { api.AssertExpectations(t) })
	for _, level := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
		for n := 1; n <= 7; n += 2 {
			args := make([]any, n)
			for i := range args {
				args[i] = mock.Anything
			}
			api.On(level, args...).Maybe()
		}
	}

	kv := newFakeKV()
	key, err := loadEncryptionKey(kv)
	require.NoError(t, err)
	box, err := newSecretBox(key)
	require.NoError(t, err)

	p := &Plugin{
		MattermostPlugin: plugin.MattermostPlugin{API: api},
		configuration:    &configuration{AllowPrivateNetworks: true, AllowInsecureConnections: true, EnableReminders: true},
		store:            NewStore(kv, box),
	}
	p.router = p.newRouter()

	backend, server := startCalDAVServer(t)
	return &testEnv{p: p, api: api, backend: backend, server: server, userID: model.NewId()}
}

func (e *testEnv) request(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		require.NoError(t, err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(reqBody))
	r.Header.Set("Mattermost-User-Id", e.userID)
	w := httptest.NewRecorder()
	e.p.ServeHTTP(nil, w, r)
	return w
}

func (e *testEnv) connect(t *testing.T) {
	t.Helper()
	w := e.request(t, http.MethodPut, "/api/v1/account", accountRequest{
		accountView: accountView{URL: e.server.URL, Username: "ada", RemindMinutes: 10},
		Password:    "secret",
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v), w.Body.String())
	return v
}

func TestAPIAccount(t *testing.T) {
	e := newTestEnv(t)

	w := e.request(t, http.MethodGet, "/api/v1/account", nil)
	assert.JSONEq(t, `{"account": null}`, w.Body.String())
	w = e.request(t, http.MethodGet, "/api/v1/calendars", nil)
	assert.Equal(t, http.StatusConflict, w.Code)

	w = e.request(t, http.MethodPut, "/api/v1/account", accountRequest{accountView: accountView{URL: e.server.URL, Username: "ada"}, Password: "wrong"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "refused the username or password")

	e.connect(t)
	w = e.request(t, http.MethodGet, "/api/v1/account", nil)
	assert.JSONEq(t, `{"account": {"url": "`+e.server.URL+`", "username": "ada", "remind_minutes": 10}}`, w.Body.String())
	account, err := e.p.store.GetAccount(e.userID)
	require.NoError(t, err)
	assert.Equal(t, e.server.URL+testHomeSet, account.HomeSetURL)

	// Saving without the password keeps it
	w = e.request(t, http.MethodPut, "/api/v1/account", accountRequest{accountView: accountView{URL: e.server.URL + "/dav/", Username: "ada", RemindMinutes: 0}})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	account, err = e.p.store.GetAccount(e.userID)
	require.NoError(t, err)
	assert.Equal(t, "secret", account.Password)
	assert.Equal(t, 0, account.RemindMinutes)

	w = e.request(t, http.MethodGet, "/api/v1/calendars", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []Calendar{{ID: testWork, Name: "Work"}}, decode[[]Calendar](t, w))

	w = e.request(t, http.MethodDelete, "/api/v1/account", nil)
	require.Equal(t, http.StatusOK, w.Code)
	w = e.request(t, http.MethodGet, "/api/v1/account", nil)
	assert.JSONEq(t, `{"account": null}`, w.Body.String())
}

func TestAPIEvents(t *testing.T) {
	e := newTestEnv(t)
	e.connect(t)

	channel := &model.Channel{Id: model.NewId(), TeamId: model.NewId(), Name: "detector-builds", DisplayName: "Detector builds", Type: model.ChannelTypeOpen}
	e.api.On("HasPermissionToChannel", e.userID, channel.Id, model.PermissionReadChannel).Return(true)
	e.api.On("GetChannel", channel.Id).Return(channel, nil)
	e.api.On("GetTeam", channel.TeamId).Return(&model.Team{Id: channel.TeamId, Name: "lab"}, nil)
	siteURL := "https://chat.example.com"
	e.api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &siteURL}})
	secret := model.NewId()
	e.api.On("HasPermissionToChannel", e.userID, secret, model.PermissionReadChannel).Return(false)

	alarm := 5
	create := EventInput{
		Calendar:   testWork,
		Summary:    "Firmware sync",
		Start:      "2026-10-05T13:00:00+02:00",
		End:        "2026-10-05T13:30:00+02:00",
		TimeZone:   "Europe/Paris",
		Recurrence: RecurrenceWeekly,
		Alarm:      &alarm,
		Link:       &EventLink{ChannelID: channel.Id, Kind: LinkCall},
	}
	w := e.request(t, http.MethodPost, "/api/v1/events", create)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	created := decode[map[string]string](t, w)
	path := created["path"]
	assert.Regexp(t, `^`+testWork+`[a-z0-9]{26}\.ics$`, path)

	// Links to channels the user can't read are refused
	forbidden := create
	forbidden.Link = &EventLink{ChannelID: secret, Kind: LinkChannel}
	w = e.request(t, http.MethodPost, "/api/v1/events", forbidden)
	assert.Equal(t, http.StatusForbidden, w.Code)

	other := create
	other.Calendar = "/elsewhere/"
	w = e.request(t, http.MethodPost, "/api/v1/events", other)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Two weeks of events
	query := url.Values{"from": {"2026-10-05T00:00:00+02:00"}, "to": {"2026-10-19T00:00:00+02:00"}, "tz": {"Europe/Paris"}}
	w = e.request(t, http.MethodGet, "/api/v1/events?"+query.Encode(), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	list := decode[struct {
		Events []Occurrence    `json:"events"`
		Errors []calendarError `json:"errors"`
	}](t, w)
	assert.Empty(t, list.Errors)
	require.Len(t, list.Events, 2)
	first := list.Events[0]
	assert.Equal(t, "Firmware sync", first.Summary)
	assert.Equal(t, testWork, first.Calendar)
	assert.Equal(t, RecurrenceWeekly, first.Recurrence)
	assert.Equal(t, []int{5}, first.Alarms)
	assert.Equal(t, &EventLink{ChannelID: channel.Id, Kind: LinkCall, ChannelName: "detector-builds", DisplayName: "Detector builds", ChannelType: "O", TeamName: "lab"}, first.Link)

	// The editor gets the series
	w = e.request(t, http.MethodGet, "/api/v1/event?tz=Europe/Paris&path="+url.QueryEscape(path), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	details := decode[EventDetails](t, w)
	assert.Equal(t, "2026-10-05T13:00:00+02:00", details.Start)
	assert.Equal(t, "Europe/Paris", details.TimeZone)
	assert.Equal(t, RecurrenceWeekly, details.Recurrence)
	assert.Equal(t, &alarm, details.Alarm)
	assert.Equal(t, testWork, details.Calendar)
	assert.Equal(t, "detector-builds", details.Link.ChannelName)

	// Edit, then a stale edit
	edit := create
	edit.Summary = "Firmware sync (moved)"
	edit.Start, edit.End = "2026-10-05T14:00:00+02:00", "2026-10-05T14:30:00+02:00"
	w = e.request(t, http.MethodPut, "/api/v1/event?path="+url.QueryEscape(path)+"&etag="+url.QueryEscape(details.ETag), edit)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = e.request(t, http.MethodPut, "/api/v1/event?path="+url.QueryEscape(path)+"&etag="+url.QueryEscape(details.ETag), edit)
	assert.Equal(t, http.StatusConflict, w.Code)

	// Delete the first occurrence, then the series
	occurrence := time.Date(2026, 10, 5, 14, 0, 0, 0, mustLoad(t, "Europe/Paris")).UnixMilli()
	w = e.request(t, http.MethodDelete, "/api/v1/event?tz=Europe/Paris&path="+url.QueryEscape(path)+"&occurrence="+jsonNumber(occurrence), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = e.request(t, http.MethodGet, "/api/v1/events?"+query.Encode(), nil)
	list.Events = nil
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list.Events, 1)
	assert.Equal(t, "Firmware sync (moved)", list.Events[0].Summary)

	w = e.request(t, http.MethodDelete, "/api/v1/event?path="+url.QueryEscape(path), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = e.request(t, http.MethodGet, "/api/v1/event?path="+url.QueryEscape(path), nil)
	assert.Equal(t, http.StatusNotFound, w.Code)

	w = e.request(t, http.MethodGet, "/api/v1/event?path="+url.QueryEscape("/elsewhere/x.ics"), nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = e.request(t, http.MethodGet, "/api/v1/events?from=bad&to=bad", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func jsonNumber(n int64) string {
	data, _ := json.Marshal(n)
	return string(data)
}
