// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newMeetingTestEnv is newTestEnv with a bot ID set, as OnActivate would, for the handlers that
// post as the Calendar bot.
func newMeetingTestEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t)
	e.p.botID = model.NewId()
	return e
}

func meetingBody(start time.Time) map[string]any {
	return map[string]any{
		"summary":     "Team sync",
		"description": "Weekly catch-up",
		"start":       start.UnixMilli(),
		"end":         start.Add(30 * time.Minute).UnixMilli(),
	}
}

// TestHandleCreateMeetingPermissions checks that scheduling a call needs permission to post in
// the channel: this is what keeps a private channel's meeting from anyone outside it.
func TestHandleCreateMeetingPermissions(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionCreatePost).Return(false)
	w := e.request(t, http.MethodPost, "/api/v1/channels/"+channelID+"/meetings", meetingBody(time.Now().Add(time.Hour)))
	assert.Equal(t, http.StatusForbidden, w.Code)

	w = e.request(t, http.MethodPost, "/api/v1/channels/not-a-valid-id/meetings", meetingBody(time.Now().Add(time.Hour)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleCreateMeeting checks that scheduling posts in the channel as the Calendar bot, saves
// the meeting with the caller as its organiser, and validates its input.
func TestHandleCreateMeeting(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	channel := &model.Channel{Id: channelID, Name: "standup", DisplayName: "Standup", Type: model.ChannelTypeOpen}
	organizer := &model.User{Id: e.userID, Username: "ada", Email: "ada@example.com", Locale: "en"}

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionCreatePost).Return(true)
	e.api.On("GetChannel", channelID).Return(channel, nil)
	e.api.On("GetUser", e.userID).Return(organizer, nil)
	e.api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		return post.UserId == e.p.botID && post.ChannelId == channelID && post.Type == meetingPostType
	})).Return(&model.Post{Id: model.NewId()}, nil)

	start := time.Now().Add(time.Hour)
	w := e.request(t, http.MethodPost, "/api/v1/channels/"+channelID+"/meetings", meetingBody(start))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	m := decode[Meeting](t, w)
	assert.Equal(t, e.userID, m.OrganizerID)
	assert.Equal(t, channelID, m.ChannelID)
	assert.Equal(t, "Team sync", m.Summary)
	assert.Equal(t, 0, m.Sequence)
	assert.Empty(t, m.RSVPs)

	stored, err := e.p.store.GetMeeting(m.ID)
	require.NoError(t, err)
	assert.Equal(t, m.PostID, stored.PostID)

	// An empty title is refused.
	bad := meetingBody(start)
	bad["summary"] = "   "
	w = e.request(t, http.MethodPost, "/api/v1/channels/"+channelID+"/meetings", bad)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleEditAndCancelMeetingOrganizerOnly checks that only the organiser may edit or cancel a
// meeting, as the brief asks: not even another member, or a channel or system admin.
func TestHandleEditAndCancelMeetingOrganizerOnly(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	organizerID := model.NewId()
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = organizerID
	require.NoError(t, e.p.store.CreateMeeting(m))

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)

	w := e.request(t, http.MethodPut, "/api/v1/meetings/"+m.ID, meetingBody(time.Now().Add(2*time.Hour)))
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = e.request(t, http.MethodDelete, "/api/v1/meetings/"+m.ID, nil)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// The meeting is unchanged.
	stored, err := e.p.store.GetMeeting(m.ID)
	require.NoError(t, err)
	assert.False(t, stored.Cancelled)
	assert.Equal(t, 0, stored.Sequence)
}

// TestHandleEditMeetingAsOrganizer checks a successful edit: the SEQUENCE goes up, the post is
// refreshed, and other clients are told.
func TestHandleEditMeetingAsOrganizer(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	channel := &model.Channel{Id: channelID, Name: "standup", DisplayName: "Standup", Type: model.ChannelTypeOpen}
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = e.userID
	require.NoError(t, e.p.store.CreateMeeting(m))

	organizer := &model.User{Id: e.userID, Username: "ada", Email: "ada@example.com", Locale: "en"}
	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)
	e.api.On("GetChannel", channelID).Return(channel, nil)
	e.api.On("GetUser", e.userID).Return(organizer, nil)
	e.api.On("GetPost", m.PostID).Return(&model.Post{Id: m.PostID, ChannelId: channelID}, nil)
	e.api.On("UpdatePost", mock.AnythingOfType("*model.Post")).Return(&model.Post{}, nil)
	e.api.On("PublishWebSocketEvent", meetingChangedEvent, mock.Anything, &model.WebsocketBroadcast{ChannelId: channelID}).Return()

	newStart := time.UnixMilli(m.Start).Add(time.Hour)
	w := e.request(t, http.MethodPut, "/api/v1/meetings/"+m.ID, meetingBody(newStart))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	updated := decode[Meeting](t, w)
	assert.Equal(t, 1, updated.Sequence)
	assert.Equal(t, newStart.UnixMilli(), updated.Start)
}

// TestHandleCancelMeetingAsOrganizer checks a successful cancellation.
func TestHandleCancelMeetingAsOrganizer(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	channel := &model.Channel{Id: channelID, Name: "standup", DisplayName: "Standup", Type: model.ChannelTypeOpen}
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = e.userID
	require.NoError(t, e.p.store.CreateMeeting(m))

	organizer := &model.User{Id: e.userID, Username: "ada", Email: "ada@example.com", Locale: "en"}
	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)
	e.api.On("GetChannel", channelID).Return(channel, nil)
	e.api.On("GetUser", e.userID).Return(organizer, nil)
	e.api.On("GetPost", m.PostID).Return(&model.Post{Id: m.PostID, ChannelId: channelID}, nil)
	e.api.On("UpdatePost", mock.AnythingOfType("*model.Post")).Return(&model.Post{}, nil)
	e.api.On("PublishWebSocketEvent", meetingChangedEvent, mock.Anything, &model.WebsocketBroadcast{ChannelId: channelID}).Return()

	w := e.request(t, http.MethodDelete, "/api/v1/meetings/"+m.ID, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	cancelled := decode[Meeting](t, w)
	assert.True(t, cancelled.Cancelled)
}

// TestHandleSetRSVPPermissions checks that RSVP needs only to be able to read the channel (unlike
// scheduling, which needs posting rights): someone who can see a read-only channel can still say
// whether they're going.
func TestHandleSetRSVPPermissions(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	m := testMeeting()
	m.ChannelID = channelID
	require.NoError(t, e.p.store.CreateMeeting(m))

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(false)
	w := e.request(t, http.MethodPost, "/api/v1/meetings/"+m.ID+"/rsvp", map[string]string{"status": "going"})
	assert.Equal(t, http.StatusNotFound, w.Code, "a meeting in a channel they can't see looks the same as one that doesn't exist")
}

// TestHandleSetRSVP checks a successful RSVP, that it's per member, and that an invalid status is
// refused.
func TestHandleSetRSVP(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	m := testMeeting()
	m.ChannelID = channelID
	require.NoError(t, e.p.store.CreateMeeting(m))

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)
	e.api.On("PublishWebSocketEvent", meetingChangedEvent, mock.Anything, &model.WebsocketBroadcast{ChannelId: channelID}).Return()

	w := e.request(t, http.MethodPost, "/api/v1/meetings/"+m.ID+"/rsvp", map[string]string{"status": "maybe"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	updated := decode[Meeting](t, w)
	assert.Equal(t, RSVPMaybe, updated.RSVPs[e.userID].Status)

	w = e.request(t, http.MethodPost, "/api/v1/meetings/"+m.ID+"/rsvp", map[string]string{"status": "sure!"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleMeetingNotFoundAndChannelVisibility checks that a meeting in a channel the caller
// can't read (for example, a private channel they aren't a member of) looks the same as a
// missing one: 404, not 403, so its existence isn't leaked.
func TestHandleGetMeetingChannelVisibility(t *testing.T) {
	e := newMeetingTestEnv(t)

	w := e.request(t, http.MethodGet, "/api/v1/meetings/"+model.NewId(), nil)
	assert.Equal(t, http.StatusNotFound, w.Code)

	channelID := model.NewId()
	m := testMeeting()
	m.ChannelID = channelID
	require.NoError(t, e.p.store.CreateMeeting(m))

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(false)
	w = e.request(t, http.MethodGet, "/api/v1/meetings/"+m.ID, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleMeetingICS checks the "add to calendar" download: a member who can read the channel
// gets the meeting's current REQUEST, as an attachment, with their own iTIP address in it.
func TestHandleMeetingICS(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	channel := &model.Channel{Id: channelID, Name: "standup", DisplayName: "Standup", Type: model.ChannelTypeOpen}
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = model.NewId()
	require.NoError(t, e.p.store.CreateMeeting(m))

	organizer := &model.User{Id: m.OrganizerID, Username: "ada", Email: "ada@example.com", Locale: "en"}
	viewer := &model.User{Id: e.userID, Username: "grace", Email: "grace@example.com", Locale: "en"}

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)
	e.api.On("GetChannel", channelID).Return(channel, nil)
	e.api.On("GetUser", m.OrganizerID).Return(organizer, nil)
	e.api.On("GetUser", e.userID).Return(viewer, nil)

	w := e.request(t, http.MethodGet, "/api/v1/meetings/"+m.ID+"/ics", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "text/calendar")
	assert.Contains(t, w.Header().Get("Content-Type"), "method=REQUEST")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	body := w.Body.String()
	assert.Contains(t, body, "METHOD:REQUEST")
	assert.Contains(t, body, meetingUID(m.ID))
	assert.Contains(t, body, "mailto:ada@example.com")
	assert.Contains(t, body, "mailto:grace@example.com")
}

// TestSendMeetingInvites checks that only the channel's members who connected a calendar account
// and asked for email invitations get one, each with the correct iTIP attachment.
func TestSendMeetingInvites(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	channel := &model.Channel{Id: channelID, Name: "standup", DisplayName: "Standup", Type: model.ChannelTypeOpen}
	organizer := &model.User{Id: model.NewId(), Username: "ada", Email: "ada@example.com", Locale: "en"}

	memberOptedIn := model.NewId()
	memberNoAccount := model.NewId()
	memberOptedOut := model.NewId()

	require.NoError(t, e.p.store.SaveAccount(memberOptedIn, &Account{
		URL: "https://dav.example.com", Username: "grace", HomeSetURL: "https://dav.example.com/cal/", Password: "x", EmailInvites: true,
	}))
	require.NoError(t, e.p.store.SaveAccount(memberOptedOut, &Account{
		URL: "https://dav.example.com", Username: "alan", HomeSetURL: "https://dav.example.com/cal/", Password: "x", EmailInvites: false,
	}))

	e.api.On("HasPermissionToChannel", organizer.Id, channelID, model.PermissionReadChannel).Return(true)
	e.api.On("GetChannel", channelID).Return(channel, nil)
	e.api.On("GetChannelMembers", channelID, 0, 200).Return(model.ChannelMembers{
		{ChannelId: channelID, UserId: memberOptedIn},
		{ChannelId: channelID, UserId: memberNoAccount},
		{ChannelId: channelID, UserId: memberOptedOut},
	}, nil)
	e.api.On("GetUser", memberOptedIn).Return(&model.User{Id: memberOptedIn, Email: "grace@example.com", Username: "grace", Locale: "en"}, nil)

	var sentTo, sentSubject, sentBody, sentAttachmentName, sentContentType string
	var sentData []byte
	e.api.On("SendMailWithAttachment", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8")).
		Run(func(args mock.Arguments) {
			sentTo = args.String(0)
			sentSubject = args.String(1)
			sentBody = args.String(2)
			sentAttachmentName = args.String(3)
			sentContentType = args.String(4)
			sentData, _ = args.Get(5).([]byte)
		}).
		Return(nil)

	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = organizer.Id

	e.p.sendMeetingInvites(m, channel, organizer)

	assert.Equal(t, "grace@example.com", sentTo, "only the member who connected a calendar account and opted in gets an email")
	assert.Contains(t, sentSubject, m.Summary)
	assert.Contains(t, sentBody, m.Summary)
	assert.Equal(t, "invite.ics", sentAttachmentName)
	assert.Contains(t, sentContentType, "method=REQUEST")
	assert.Contains(t, string(sentData), "METHOD:REQUEST")
	assert.Contains(t, string(sentData), "mailto:grace@example.com")
	assert.Contains(t, string(sentData), "mailto:ada@example.com")
}
