// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// feedEnv is a community with a public and a private channel, a member (the test env's user)
// who can read both, and their feed of it. Its state fields change what the mocked server
// answers from then on.
type feedEnv struct {
	*testEnv
	teamID      string
	otherTeamID string
	publicID    string
	privateID   string
	organizerID string
	otherUserID string

	mu          sync.Mutex
	member      bool
	deactivated bool
	bot         bool
	inPrivate   bool
	teamDeleted bool
	showEmails  bool
}

func (f *feedEnv) set(change func(f *feedEnv)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *feedEnv) get(read func(f *feedEnv) bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return read(f)
}

func newFeedEnv(t *testing.T) *feedEnv {
	t.Helper()
	f := &feedEnv{
		testEnv:     newMeetingTestEnv(t),
		teamID:      model.NewId(),
		otherTeamID: model.NewId(),
		publicID:    model.NewId(),
		privateID:   model.NewId(),
		organizerID: model.NewId(),
		otherUserID: model.NewId(),
		member:      true,
		inPrivate:   true,
	}
	api := f.api

	api.On("GetConfig").Return(func() *model.Config {
		cfg := &model.Config{}
		cfg.SetDefaults()
		cfg.ServiceSettings.SiteURL = model.NewPointer("https://chat.example.com")
		cfg.EmailSettings.FeedbackEmail = model.NewPointer("")
		cfg.PrivacySettings.ShowEmailAddress = model.NewPointer(f.get(func(f *feedEnv) bool { return f.showEmails }))
		return cfg
	}).Maybe()
	api.On("GetUser", f.userID).Return(func(string) (*model.User, *model.AppError) {
		u := &model.User{Id: f.userID, Username: "maya", Email: "maya@example.com", FirstName: "Maya", LastName: "Rossi", Locale: "en"}
		if f.get(func(f *feedEnv) bool { return f.deactivated }) {
			u.DeleteAt = model.GetMillis()
		}
		u.IsBot = f.get(func(f *feedEnv) bool { return f.bot })
		return u, nil
	}).Maybe()
	api.On("GetUser", f.organizerID).Return(&model.User{Id: f.organizerID, Username: "ada", Email: "ada@secret.example", FirstName: "Ada", LastName: "Lovelace"}, nil).Maybe()
	api.On("GetTeam", f.teamID).Return(func(string) (*model.Team, *model.AppError) {
		team := &model.Team{Id: f.teamID, Name: "lab", DisplayName: "Lumen Labs"}
		if f.get(func(f *feedEnv) bool { return f.teamDeleted }) {
			team.DeleteAt = model.GetMillis()
		}
		return team, nil
	}).Maybe()
	api.On("GetTeamMember", f.teamID, f.userID).Return(func(string, string) (*model.TeamMember, *model.AppError) {
		if !f.get(func(f *feedEnv) bool { return f.member }) {
			return &model.TeamMember{TeamId: f.teamID, UserId: f.userID, DeleteAt: model.GetMillis()}, nil
		}
		return &model.TeamMember{TeamId: f.teamID, UserId: f.userID}, nil
	}).Maybe()

	api.On("GetChannel", f.publicID).Return(&model.Channel{Id: f.publicID, TeamId: f.teamID, Type: model.ChannelTypeOpen, Name: "standup", DisplayName: "Standup"}, nil).Maybe()
	api.On("GetChannelMember", f.publicID, f.userID).Return(&model.ChannelMember{ChannelId: f.publicID, UserId: f.userID}, nil).Maybe()
	api.On("HasPermissionToChannel", f.userID, f.publicID, model.PermissionReadChannel).Return(true).Maybe()

	api.On("GetChannel", f.privateID).Return(&model.Channel{Id: f.privateID, TeamId: f.teamID, Type: model.ChannelTypePrivate, Name: "board", DisplayName: "Board room"}, nil).Maybe()
	api.On("GetChannelMember", f.privateID, f.userID).Return(func(string, string) (*model.ChannelMember, *model.AppError) {
		if !f.get(func(f *feedEnv) bool { return f.inPrivate }) {
			return nil, errNotAMember
		}
		return &model.ChannelMember{ChannelId: f.privateID, UserId: f.userID}, nil
	}).Maybe()
	api.On("HasPermissionToChannel", f.userID, f.privateID, model.PermissionReadChannel).Return(func(string, string, *model.Permission) bool {
		return f.get(func(f *feedEnv) bool { return f.inPrivate })
	}).Maybe()
	api.On("HasPermissionToTeam", f.userID, f.teamID, model.PermissionReadPublicChannel).Return(true).Maybe()
	return f
}

// createFeed makes the member's feed through the API, and returns the path the app serves it at.
func (f *feedEnv) createFeed(t *testing.T) string {
	t.Helper()
	w := f.request(t, http.MethodPost, "/api/v1/teams/"+f.teamID+"/feed", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	link := decode[feedLink](t, w)
	prefix := "/plugins/" + manifest.Id
	require.True(t, strings.HasPrefix(link.Path, prefix+"/feeds/"), link.Path)
	assert.Equal(t, "https://chat.example.com"+link.Path, link.URL)
	return strings.TrimPrefix(link.Path, prefix)
}

func tokenOf(path string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "/feeds/"), ".ics")
}

// fetch reads a feed as a calendar app does: without a session.
func (f *feedEnv) fetch(t *testing.T, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	f.p.ServeHTTP(nil, w, r)
	return w
}

// meeting stores a one-off call starting in `in`, in a channel of the community.
func (f *feedEnv) meeting(t *testing.T, channelID, summary string, in time.Duration) *Meeting {
	t.Helper()
	m := indexedMeeting(t, f.testEnv, f.teamID, channelID, summary)
	start := time.Now().Truncate(time.Second).Add(in)
	_, err := f.p.store.UpdateMeeting(m.ID, func(m *Meeting) error {
		m.OrganizerID = f.organizerID
		m.Start, m.End = start.UnixMilli(), start.Add(30*time.Minute).UnixMilli()
		return nil
	})
	require.NoError(t, err)
	got, err := f.p.store.GetMeeting(m.ID)
	require.NoError(t, err)
	return got
}

func decodeFeed(t *testing.T, w *httptest.ResponseRecorder) *ical.Calendar {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	cal, err := ical.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode()
	require.NoError(t, err)
	return cal
}

// unfolded returns a feed's text with its folded lines joined again (RFC 5545 folds lines at 75
// octets), so a search can't miss a value split across two lines.
func unfolded(w *httptest.ResponseRecorder) string {
	return strings.ReplaceAll(w.Body.String(), "\r\n ", "")
}

func feedEvents(cal *ical.Calendar) map[string]ical.Event {
	events := map[string]ical.Event{}
	for _, ev := range cal.Events() {
		events[ev.Props.Get(ical.PropUID).Value] = ev
	}
	return events
}

// assertTokenNeverLogged checks that no call to the server's API (its logs included) was ever
// given a feed token.
func assertTokenNeverLogged(t *testing.T, f *feedEnv, tokens ...string) {
	t.Helper()
	for _, call := range f.api.Calls {
		for _, arg := range call.Arguments {
			text := fmt.Sprint(arg)
			for _, token := range tokens {
				assert.NotContains(t, text, token, "%s was given a feed token", call.Method)
			}
		}
	}
}

func randomToken(t *testing.T) string {
	t.Helper()
	raw := make([]byte, feedTokenBytes)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// TestFeedContent checks what a feed carries: the community's name and refresh hints, one event
// per call or series with its iTIP UID, RRULE, EXDATE and time zone, the channel and the post's
// permalink, the member's own PARTSTAT and reminder; and that it reads back through the real
// decoder.
func TestFeedContent(t *testing.T) {
	f := newFeedEnv(t)
	oneOff := f.meeting(t, f.publicID, "Standup", 2*time.Hour)
	_, err := f.p.store.UpdateMeeting(oneOff.ID, func(m *Meeting) error {
		m.Description = "Agenda in the header"
		m.RSVPs = map[string]RSVP{f.userID: {Status: RSVPGoing}}
		m.ReminderOverrides = map[string]int{f.userID: 15}
		return nil
	})
	require.NoError(t, err)

	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)
	seriesStart := time.Now().In(paris).Truncate(time.Hour).Add(26 * time.Hour)
	skipped := seriesStart.AddDate(0, 0, 7)
	series := f.meeting(t, f.privateID, "Board weekly", 0)
	_, err = f.p.store.UpdateMeeting(series.ID, func(m *Meeting) error {
		m.Start, m.End = seriesStart.UnixMilli(), seriesStart.Add(time.Hour).UnixMilli()
		m.TimeZone = "Europe/Paris"
		m.RecurrenceRule = "FREQ=WEEKLY;COUNT=4"
		m.ExceptionDates = []int64{skipped.UnixMilli()}
		return nil
	})
	require.NoError(t, err)

	cancelled := f.meeting(t, f.publicID, "Cancelled call", time.Hour)
	_, err = f.p.store.CancelMeeting(cancelled.ID)
	require.NoError(t, err)

	path := f.createFeed(t)
	w := f.fetch(t, path)
	assert.Equal(t, "text/calendar; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "private, max-age=900", w.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, strconv.Itoa(w.Body.Len()), w.Header().Get("Content-Length"))

	head := httptest.NewRecorder()
	f.p.ServeHTTP(nil, head, httptest.NewRequest(http.MethodHead, path, nil))
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.Bytes(), "HEAD has no body")
	assert.Equal(t, w.Header().Get("ETag"), head.Header().Get("ETag"))
	cal := decodeFeed(t, w)
	body := unfolded(w)

	assert.Equal(t, "Lumen Labs", textProp(cal.Component, "X-WR-CALNAME"))
	assert.Contains(t, body, "REFRESH-INTERVAL;VALUE=DURATION:PT1H\r\n")
	assert.Contains(t, body, "X-PUBLISHED-TTL:PT1H\r\n")
	assert.Nil(t, cal.Props.Get(ical.PropMethod), "a feed is no iTIP message")

	events := feedEvents(cal)
	require.Len(t, events, 2, "the cancelled call is left out")

	ev, ok := events[meetingUID(oneOff.ID)]
	require.True(t, ok, "a stable UID, the iTIP one")
	assert.Equal(t, "Standup", textProp(ev.Component, ical.PropSummary))
	start, err := ev.DateTimeStart(nil)
	require.NoError(t, err)
	assert.Equal(t, oneOff.Start, start.UnixMilli())
	assert.True(t, strings.HasSuffix(ev.Props.Get(ical.PropDateTimeStart).Value, "Z"), "a one-off call in UTC")
	description := textProp(ev.Component, ical.PropDescription)
	assert.Contains(t, description, "Agenda in the header")
	assert.Contains(t, description, "Channel: Standup")
	assert.Contains(t, description, "https://chat.example.com/lab/pl/"+oneOff.PostID)
	assert.Equal(t, "https://chat.example.com/lab/pl/"+oneOff.PostID, ev.Props.Get(ical.PropURL).Value)
	attendees := ev.Props.Values(ical.PropAttendee)
	require.Len(t, attendees, 1, "only the member")
	assert.Equal(t, "mailto:maya@example.com", attendees[0].Value)
	assert.Equal(t, "ACCEPTED", attendees[0].Params.Get(ical.ParamParticipationStatus))
	require.Len(t, ev.Children, 1)
	assert.Equal(t, "-PT15M", ev.Children[0].Props.Get(ical.PropTrigger).Value, "the member's own reminder")
	// Emails hidden: the server's no-reply address, not the organiser's.
	assert.Equal(t, "mailto:noreply@chat.example.com", ev.Props.Get(ical.PropOrganizer).Value)
	assert.Equal(t, "Ada Lovelace", ev.Props.Get(ical.PropOrganizer).Params.Get(ical.ParamCommonName))

	ev, ok = events[meetingUID(series.ID)]
	require.True(t, ok)
	dtstart := ev.Props.Get(ical.PropDateTimeStart)
	assert.Equal(t, "Europe/Paris", dtstart.Params.Get(ical.PropTimezoneID), "a series in its own time zone")
	assert.Contains(t, body, "BEGIN:VTIMEZONE\r\nTZID:Europe/Paris\r\n")
	assert.Equal(t, "FREQ=WEEKLY;COUNT=4", ev.Props.Get(ical.PropRecurrenceRule).Value)
	assert.Contains(t, textProp(ev.Component, ical.PropDescription), "Channel: Board room")
	assert.Equal(t, "NEEDS-ACTION", ev.Props.Get(ical.PropAttendee).Params.Get(ical.ParamParticipationStatus))
	assert.Equal(t, "-PT10M", ev.Children[0].Props.Get(ical.PropTrigger).Value, "the call's own reminder")

	set, err := ev.RecurrenceSet(paris)
	require.NoError(t, err)
	var got []int64
	for _, occurrence := range set.All() {
		got = append(got, occurrence.UnixMilli())
	}
	assert.Equal(t, []int64{seriesStart.UnixMilli(), seriesStart.AddDate(0, 0, 14).UnixMilli(), seriesStart.AddDate(0, 0, 21).UnixMilli()}, got,
		"four weeks, the cancelled one as EXDATE")

	t.Run("organiser's own address when the server shows emails", func(t *testing.T) {
		f.set(func(f *feedEnv) { f.showEmails = true })
		path := f.createFeed(t)
		ev := feedEvents(decodeFeed(t, f.fetch(t, path)))[meetingUID(oneOff.ID)]
		assert.Equal(t, "mailto:ada@secret.example", ev.Props.Get(ical.PropOrganizer).Value)
	})
}

// TestFeedHasNoOtherAttendees is a privacy regression test: nobody else's address, answer or
// reminder is ever in a feed.
func TestFeedHasNoOtherAttendees(t *testing.T) {
	f := newFeedEnv(t)
	m := f.meeting(t, f.publicID, "Standup", time.Hour)
	_, err := f.p.store.UpdateMeeting(m.ID, func(m *Meeting) error {
		m.RSVPs = map[string]RSVP{f.otherUserID: {Status: RSVPNo}, f.organizerID: {Status: RSVPMaybe}}
		m.NextRSVPs = map[string]occurrenceRSVP{f.otherUserID: {RSVP: RSVP{Status: RSVPGoing}, OccurrenceStart: m.Start}}
		m.ReminderOverrides = map[string]int{f.otherUserID: 1440}
		return nil
	})
	require.NoError(t, err)

	w := f.fetch(t, f.createFeed(t))
	cal := decodeFeed(t, w)
	body := unfolded(w)
	ev := feedEvents(cal)[meetingUID(m.ID)]

	attendees := ev.Props.Values(ical.PropAttendee)
	require.Len(t, attendees, 1)
	assert.Equal(t, "mailto:maya@example.com", attendees[0].Value)
	assert.Equal(t, "NEEDS-ACTION", attendees[0].Params.Get(ical.ParamParticipationStatus), "the member hasn't answered; others' answers aren't theirs")
	assert.Equal(t, 1, strings.Count(body, "ATTENDEE"))
	assert.Equal(t, 1, strings.Count(body, "PARTSTAT="))
	for _, leak := range []string{"ada@secret.example", f.otherUserID, f.organizerID, "DECLINED", "TENTATIVE", "-PT1440M"} {
		assert.NotContains(t, body, leak)
	}
}

// TestFeedNotFoundIsUniform is the security regression test of the feed's failures: an unknown
// token, a reset one, a deactivated member, a bot, a member who left, a deleted community, feeds
// turned off and a malformed token all answer the very same 404, so none can be told apart.
func TestFeedNotFoundIsUniform(t *testing.T) {
	reference := func(t *testing.T) *httptest.ResponseRecorder {
		f := newFeedEnv(t)
		return f.fetch(t, "/feeds/"+randomToken(t)+".ics")
	}(t)
	require.Equal(t, http.StatusNotFound, reference.Code)
	require.Equal(t, feedNotFoundBody, reference.Body.String())

	cases := map[string]func(t *testing.T, f *feedEnv) string{
		"unknown token": func(t *testing.T, _ *feedEnv) string { return "/feeds/" + randomToken(t) + ".ics" },
		"reset token": func(t *testing.T, f *feedEnv) string {
			old := f.createFeed(t)
			f.createFeed(t)
			return old
		},
		"deactivated member": func(t *testing.T, f *feedEnv) string {
			path := f.createFeed(t)
			f.set(func(f *feedEnv) { f.deactivated = true })
			return path
		},
		"a bot": func(t *testing.T, f *feedEnv) string {
			path := f.createFeed(t)
			f.set(func(f *feedEnv) { f.bot = true })
			return path
		},
		"left the community": func(t *testing.T, f *feedEnv) string {
			path := f.createFeed(t)
			f.set(func(f *feedEnv) { f.member = false })
			return path
		},
		"community deleted": func(t *testing.T, f *feedEnv) string {
			path := f.createFeed(t)
			f.set(func(f *feedEnv) { f.teamDeleted = true })
			return path
		},
		"feeds turned off": func(t *testing.T, f *feedEnv) string {
			path := f.createFeed(t)
			f.p.setConfiguration(&configuration{AllowCalendarFeeds: false})
			return path
		},
		"malformed token": func(_ *testing.T, _ *feedEnv) string { return "/feeds/not-a-token.ics" },
		"no .ics":         func(t *testing.T, f *feedEnv) string { return strings.TrimSuffix(f.createFeed(t), ".ics") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFeedEnv(t)
			f.meeting(t, f.publicID, "Standup", time.Hour)
			w := f.fetch(t, setup(t, f))
			assert.Equal(t, reference.Code, w.Code)
			assert.Equal(t, reference.Body.Bytes(), w.Body.Bytes(), "byte-identical")
			assert.Equal(t, reference.Header(), w.Header())
			assert.NotContains(t, w.Body.String(), "Standup")
		})
	}
}

// TestFeedResetRevokes checks that a reset kills the old token at once and the new one works, and
// that a record left behind by a failed delete still doesn't bring the old token back.
func TestFeedResetRevokes(t *testing.T) {
	f := newFeedEnv(t)
	f.meeting(t, f.publicID, "Standup", time.Hour)
	old := f.createFeed(t)
	require.Equal(t, http.StatusOK, f.fetch(t, old).Code)

	w := f.request(t, http.MethodGet, "/api/v1/teams/"+f.teamID+"/feed", nil)
	require.Equal(t, http.StatusOK, w.Code)
	status := decode[feedStatus](t, w)
	assert.True(t, status.Exists)
	assert.NotZero(t, status.CreatedAt)
	assert.NotContains(t, w.Body.String(), tokenOf(old), "the address is never shown again")

	current := f.createFeed(t)
	assert.NotEqual(t, old, current)
	assert.Equal(t, http.StatusNotFound, f.fetch(t, old).Code)
	assert.Equal(t, http.StatusOK, f.fetch(t, current).Code)

	kv := f.p.store.kv.(*fakeKV)
	oldHash := feedTokenHash(tokenOf(old))
	data, _ := kv.KVGet(feedRecordKey(oldHash))
	assert.Nil(t, data, "the old record is deleted")

	// Even if deleting it had failed, the pointer no longer names it.
	_, appErr := kv.KVSetWithOptions(feedRecordKey(oldHash), []byte(`{"user_id":"`+f.userID+`","team_id":"`+f.teamID+`","created_at":1}`), model.PluginKVSetOptions{})
	require.Nil(t, appErr)
	assert.Equal(t, http.StatusNotFound, f.fetch(t, old).Code)

	// A store that fails is logged, but never with the token.
	f.p.store = NewStore(failingKV{}, nil)
	assert.Equal(t, http.StatusNotFound, f.fetch(t, current).Code)
	assertTokenNeverLogged(t, f, tokenOf(old), tokenOf(current))
}

// failingKV is a store whose every call fails.
type failingKV struct{}

var errKVDown = model.NewAppError("test", "test.kv_down", nil, "", http.StatusInternalServerError)

func (failingKV) KVGet(string) ([]byte, *model.AppError) { return nil, errKVDown }
func (failingKV) KVSetWithOptions(string, []byte, model.PluginKVSetOptions) (bool, *model.AppError) {
	return false, errKVDown
}
func (failingKV) KVDelete(string) *model.AppError             { return errKVDown }
func (failingKV) KVList(int, int) ([]string, *model.AppError) { return nil, errKVDown }

// TestFeedFollowsAccessNow checks that every read applies the read rule again: once the member
// leaves a private channel, its calls drop out of the next read; calls of another community never
// show, nor those of a channel moved to another community.
func TestFeedFollowsAccessNow(t *testing.T) {
	f := newFeedEnv(t)
	public := f.meeting(t, f.publicID, "Standup", time.Hour)
	private := f.meeting(t, f.privateID, "Board meeting", 2*time.Hour)

	// Another community's call, in its own index.
	elsewhere := indexedMeeting(t, f.testEnv, f.otherTeamID, model.NewId(), "Other community's call")
	// A call indexed here whose channel has moved to the other community.
	movedChannel := model.NewId()
	f.api.On("GetChannel", movedChannel).Return(&model.Channel{Id: movedChannel, TeamId: f.otherTeamID, Type: model.ChannelTypeOpen, Name: "moved"}, nil).Maybe()
	f.api.On("GetChannelMember", movedChannel, f.userID).Return(&model.ChannelMember{}, nil).Maybe()
	f.api.On("HasPermissionToChannel", f.userID, movedChannel, model.PermissionReadChannel).Return(true).Maybe()
	moved := indexedMeeting(t, f.testEnv, f.teamID, movedChannel, "Moved call")

	path := f.createFeed(t)
	w := f.fetch(t, path)
	events := feedEvents(decodeFeed(t, w))
	assert.Contains(t, events, meetingUID(public.ID))
	assert.Contains(t, events, meetingUID(private.ID))
	assert.NotContains(t, events, meetingUID(elsewhere.ID))
	assert.NotContains(t, events, meetingUID(moved.ID))
	assert.NotContains(t, unfolded(w), "Other community's call")
	assert.NotContains(t, unfolded(w), "Moved call")

	f.set(func(f *feedEnv) { f.inPrivate = false })
	w = f.fetch(t, path)
	events = feedEvents(decodeFeed(t, w))
	assert.Contains(t, events, meetingUID(public.ID))
	assert.NotContains(t, events, meetingUID(private.ID), "dropped once the member left the private channel")
	assert.NotContains(t, unfolded(w), "Board meeting")
}

// TestFeedStoresOnlyTheHash scans every write to the store for the plain token: only its
// SHA-256 is ever saved.
func TestFeedStoresOnlyTheHash(t *testing.T) {
	f := newFeedEnv(t)
	first := tokenOf(f.createFeed(t))
	second := tokenOf(f.createFeed(t))

	kv := f.p.store.kv.(*fakeKV)
	writes := kv.allWrites()
	require.NotEmpty(t, writes)
	for _, token := range []string{first, second} {
		for _, write := range writes {
			assert.NotContains(t, write.key, token)
			assert.NotContains(t, string(write.value), token)
			raw, err := base64.RawURLEncoding.DecodeString(token)
			require.NoError(t, err)
			assert.False(t, bytes.Contains(write.value, raw), "nor its raw bytes")
		}
	}
	data, appErr := kv.KVGet(feedRecordKey(feedTokenHash(second)))
	require.Nil(t, appErr)
	require.NotNil(t, data, "the record is keyed by the token's SHA-256")
	var record feedRecord
	require.NoError(t, json.Unmarshal(data, &record))
	assert.Equal(t, f.userID, record.UserID)
	assert.Equal(t, f.teamID, record.TeamID)
	assert.NotZero(t, record.CreatedAt)
}

// TestRoutesRequireUser walks every route of the router: each refuses a request without the
// user's session (401), except the calendar feed, the one route served without it.
func TestRoutesRequireUser(t *testing.T) {
	e := newMeetingTestEnv(t)
	variable := regexp.MustCompile(`\{[^}]+\}`)
	var open []string
	checked := 0
	err := e.p.router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, err := route.GetPathTemplate()
		if err != nil {
			return nil
		}
		methods, err := route.GetMethods()
		if err != nil {
			// The /api/v1 prefix itself, not a route of its own.
			return nil
		}
		path := variable.ReplaceAllString(template, model.NewId())
		for _, method := range methods {
			r := httptest.NewRequest(method, path, strings.NewReader("{}"))
			w := httptest.NewRecorder()
			e.p.ServeHTTP(nil, w, r)
			if template == feedRoute {
				open = append(open, method+" "+template)
				assert.Equal(t, http.StatusNotFound, w.Code, "%s %s", method, template)
				continue
			}
			checked++
			assert.Equal(t, http.StatusUnauthorized, w.Code, "%s %s must need the user's session", method, template)
		}
		return nil
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"GET " + feedRoute, "HEAD " + feedRoute}, open, "the feed is the only route without a session")
	assert.GreaterOrEqual(t, checked, 21, "every API route was walked")

	// The paths known today, in case a route ever moves out of the walk.
	for _, path := range []string{"/api/v1/config", "/api/v1/account", "/api/v1/meetings", "/api/v1/teams/" + model.NewId() + "/feed"} {
		w := httptest.NewRecorder()
		e.p.ServeHTTP(nil, w, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusUnauthorized, w.Code, path)
	}
}

// TestFeedSettingOff checks the "Allow calendar feeds" setting: off, every feed answers 404,
// feeds can't be made or read, and the web app is told to hide Subscribe.
func TestFeedSettingOff(t *testing.T) {
	f := newFeedEnv(t)
	f.meeting(t, f.publicID, "Standup", time.Hour)
	path := f.createFeed(t)
	require.Equal(t, http.StatusOK, f.fetch(t, path).Code)

	w := f.request(t, http.MethodGet, "/api/v1/config", nil)
	assert.Contains(t, w.Body.String(), `"calendar_feeds_enabled":true`)

	f.p.setConfiguration(&configuration{EnableReminders: true, AllowCalendarFeeds: false})
	w = f.fetch(t, path)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, feedNotFoundBody, w.Body.String())
	assert.Equal(t, http.StatusNotFound, f.request(t, http.MethodGet, "/api/v1/teams/"+f.teamID+"/feed", nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(t, http.MethodPost, "/api/v1/teams/"+f.teamID+"/feed", nil).Code)
	w = f.request(t, http.MethodGet, "/api/v1/config", nil)
	assert.Contains(t, w.Body.String(), `"calendar_feeds_enabled":false`)
}

// TestFeedRoutesNeedMembership checks who may make a feed: an active member of the community, not
// a former member or a bot, and only with a valid community ID.
func TestFeedRoutesNeedMembership(t *testing.T) {
	f := newFeedEnv(t)
	assert.Equal(t, http.StatusBadRequest, f.request(t, http.MethodPost, "/api/v1/teams/nope/feed", nil).Code)

	f.set(func(f *feedEnv) { f.member = false })
	assert.Equal(t, http.StatusForbidden, f.request(t, http.MethodGet, "/api/v1/teams/"+f.teamID+"/feed", nil).Code)
	assert.Equal(t, http.StatusForbidden, f.request(t, http.MethodPost, "/api/v1/teams/"+f.teamID+"/feed", nil).Code)

	f.set(func(f *feedEnv) { f.member, f.bot = true, true })
	assert.Equal(t, http.StatusForbidden, f.request(t, http.MethodPost, "/api/v1/teams/"+f.teamID+"/feed", nil).Code)

	f.set(func(f *feedEnv) { f.bot = false })
	w := f.request(t, http.MethodGet, "/api/v1/teams/"+f.teamID+"/feed", nil)
	assert.JSONEq(t, `{"exists": false}`, w.Body.String())
}

// TestFeedRevokedByHooks checks that leaving the community, or being deactivated, deletes the
// feed: rejoining (or being reactivated) doesn't bring the old link back.
func TestFeedRevokedByHooks(t *testing.T) {
	f := newFeedEnv(t)
	f.meeting(t, f.publicID, "Standup", time.Hour)

	path := f.createFeed(t)
	f.p.UserHasLeftTeam(nil, &model.TeamMember{TeamId: f.teamID, UserId: f.userID}, nil)
	assert.Equal(t, http.StatusNotFound, f.fetch(t, path).Code, "still a member as far as the mock goes: the hook alone revoked it")

	path = f.createFeed(t)
	otherTeam := model.NewId()
	_, _, err := f.p.store.CreateFeed(f.userID, otherTeam, time.Now())
	require.NoError(t, err)
	f.p.UserHasBeenDeactivated(nil, &model.User{Id: f.userID})
	assert.Equal(t, http.StatusNotFound, f.fetch(t, path).Code)
	pointer, err := f.p.store.GetFeedPointer(f.userID, otherTeam)
	require.NoError(t, err)
	assert.Nil(t, pointer, "every community's feed")
}

// TestFeedRateLimit checks the limit on reading one feed: 6 reads, then 429 with Retry-After;
// an unknown token is limited the same way.
func TestFeedRateLimit(t *testing.T) {
	f := newFeedEnv(t)
	for _, path := range []string{f.createFeed(t), "/feeds/" + randomToken(t) + ".ics"} {
		for i := range feedRateBurst {
			w := f.fetch(t, path)
			assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "read %d", i+1)
		}
		w := f.fetch(t, path)
		assert.Equal(t, http.StatusTooManyRequests, w.Code)
		retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, retry, 1)
		assert.LessOrEqual(t, retry, 10)
	}
}

// TestFeedLimiter checks the token bucket itself: a burst of 6, then one every 10 seconds, and
// that its memory stays bounded.
func TestFeedLimiter(t *testing.T) {
	l := newFeedLimiter()
	now := time.Unix(1_800_000_000, 0)
	for range feedRateBurst {
		ok, _ := l.allow("a", now)
		require.True(t, ok)
	}
	ok, wait := l.allow("a", now)
	assert.False(t, ok)
	assert.Equal(t, feedRateInterval, wait)
	ok, _ = l.allow("b", now)
	assert.True(t, ok, "per feed")

	ok, _ = l.allow("a", now.Add(feedRateInterval))
	assert.True(t, ok, "one more after 10 seconds")
	ok, _ = l.allow("a", now.Add(feedRateInterval))
	assert.False(t, ok)

	for i := range maxFeedLimiterEntries + 10 {
		l.allow(strconv.Itoa(i), now)
	}
	assert.LessOrEqual(t, len(l.buckets), maxFeedLimiterEntries)
}

// TestFeedETag checks the cache headers: the ETag is the same while nothing changes, a matching
// If-None-Match answers 304 without a body, and a change gives a new ETag.
func TestFeedETag(t *testing.T) {
	f := newFeedEnv(t)
	m := f.meeting(t, f.publicID, "Standup", time.Hour)
	path := f.createFeed(t)

	first := f.fetch(t, path)
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	assert.Regexp(t, `^"[0-9a-f]{32}"$`, etag)
	assert.Equal(t, etag, f.fetch(t, path).Header().Get("ETag"), "stable while nothing changes")

	for _, header := range []string{etag, "W/" + etag, `"other", ` + etag} {
		w := f.fetch(t, path, "If-None-Match", header)
		assert.Equal(t, http.StatusNotModified, w.Code, header)
		assert.Empty(t, w.Body.Bytes())
		assert.Equal(t, etag, w.Header().Get("ETag"))
	}

	_, err := f.p.store.SetRSVP(m.ID, f.userID, RSVPGoing, 0)
	require.NoError(t, err)
	w := f.fetch(t, path, "If-None-Match", etag)
	assert.Equal(t, http.StatusOK, w.Code, "the member's answer changed the feed")
	assert.NotEqual(t, etag, w.Header().Get("ETag"))
}

// TestFeedWindowAndCap checks which calls a feed carries: those with an occurrence from 30 days
// ago to a year ahead, at most 500, the earliest kept.
func TestFeedWindowAndCap(t *testing.T) {
	t.Run("window", func(t *testing.T) {
		f := newFeedEnv(t)
		tooOld := f.meeting(t, f.publicID, "Too old", -40*24*time.Hour)
		recent := f.meeting(t, f.publicID, "Last fortnight", -20*24*time.Hour)
		soon := f.meeting(t, f.publicID, "In ten months", 300*24*time.Hour)
		tooFar := f.meeting(t, f.publicID, "Too far", 400*24*time.Hour)

		// A series that started long ago but still runs is in.
		series := f.meeting(t, f.publicID, "Still weekly", -60*24*time.Hour)
		_, err := f.p.store.UpdateMeeting(series.ID, func(m *Meeting) error { m.RecurrenceRule = "FREQ=WEEKLY"; return nil })
		require.NoError(t, err)

		events := feedEvents(decodeFeed(t, f.fetch(t, f.createFeed(t))))
		assert.NotContains(t, events, meetingUID(tooOld.ID))
		assert.Contains(t, events, meetingUID(recent.ID))
		assert.Contains(t, events, meetingUID(soon.ID))
		assert.NotContains(t, events, meetingUID(tooFar.ID))
		assert.Contains(t, events, meetingUID(series.ID))
	})

	t.Run("cap", func(t *testing.T) {
		f := newFeedEnv(t)
		var last *Meeting
		for i := range maxFeedEntries + 5 {
			m := f.meeting(t, f.publicID, "Call "+strconv.Itoa(i), time.Duration(i+1)*time.Hour)
			last = m
		}
		events := feedEvents(decodeFeed(t, f.fetch(t, f.createFeed(t))))
		assert.Len(t, events, maxFeedEntries)
		assert.NotContains(t, events, meetingUID(last.ID), "the latest left out")
	})
}

// TestValidFeedToken checks the token's shape: 43 base64url characters of 32 bytes.
func TestValidFeedToken(t *testing.T) {
	token, hash, err := newFeedToken()
	require.NoError(t, err)
	assert.True(t, validFeedToken(token))
	assert.Len(t, token, 43)
	assert.Equal(t, feedTokenHash(token), hash)
	assert.False(t, validFeedToken(token[:42]))
	assert.False(t, validFeedToken(token+"A"))
	assert.False(t, validFeedToken(strings.Repeat("+", 43)))
	assert.False(t, validFeedToken(""))
}
