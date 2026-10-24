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

// notAnAdmin mocks the caller holding none of the roles that may cancel a meeting (see
// canCancelMeeting), for the tests whose response computes can_cancel for a plain member.
func (e *testEnv) notAnAdmin(channelID string) {
	e.api.On("HasPermissionTo", e.userID, model.PermissionManageSystem).Return(false).Maybe()
	e.api.On("GetChannelMember", channelID, e.userID).Return(&model.ChannelMember{}, nil).Maybe()
	e.api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID}, nil).Maybe()
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
	// A new call tells the channel's open clients too (the header's next call, the community
	// calendar), not only edits and RSVPs.
	e.api.On("PublishWebSocketEvent", meetingChangedEvent, mock.MatchedBy(func(data map[string]any) bool {
		return data["channel_id"] == channelID && data["id"] != ""
	}), &model.WebsocketBroadcast{ChannelId: channelID}).Return().Once()

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
	assert.Equal(t, start.Truncate(time.Second).UnixMilli(), stored.Start, "a call's times are kept to the second")
	assert.Zero(t, stored.End%1000)

	// An empty title is refused.
	bad := meetingBody(start)
	bad["summary"] = "   "
	w = e.request(t, http.MethodPost, "/api/v1/channels/"+channelID+"/meetings", bad)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleEditMeetingOrganizerOnly checks that editing stays organiser-only: not even a channel,
// community or system admin may edit it (only cancel it; see TestCanCancelMeeting).
func TestHandleEditMeetingOrganizerOnly(t *testing.T) {
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

	// The meeting is unchanged.
	stored, err := e.p.store.GetMeeting(m.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, stored.Sequence)
}

// TestCanCancelMeeting checks exactly who may cancel a meeting: its organiser, that channel's
// admin, that community's admin, and a system admin — each checked explicitly, never through a
// broad permission such as delete_others_posts — but not a plain member, and not someone who
// holds one of those admin roles somewhere else (a different channel or community).
func TestCanCancelMeeting(t *testing.T) {
	channelID := model.NewId()
	teamID := model.NewId()
	channel := &model.Channel{Id: channelID, TeamId: teamID}
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = model.NewId()

	testCases := map[string]struct {
		setup func(e *testEnv)
		want  bool
	}{
		"the organiser": {
			setup: func(e *testEnv) { e.userID = m.OrganizerID },
			want:  true,
		},
		"a system admin": {
			setup: func(e *testEnv) {
				e.api.On("HasPermissionTo", e.userID, model.PermissionManageSystem).Return(true)
			},
			want: true,
		},
		"that channel's admin": {
			setup: func(e *testEnv) {
				e.api.On("HasPermissionTo", e.userID, model.PermissionManageSystem).Return(false)
				e.api.On("GetChannelMember", channelID, e.userID).Return(&model.ChannelMember{SchemeAdmin: true}, nil)
			},
			want: true,
		},
		"that community's admin": {
			setup: func(e *testEnv) {
				e.api.On("HasPermissionTo", e.userID, model.PermissionManageSystem).Return(false)
				e.api.On("GetChannelMember", channelID, e.userID).Return(nil, model.NewAppError("test", "test.not_a_member", nil, "", http.StatusNotFound))
				e.api.On("GetChannel", channelID).Return(channel, nil)
				e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{SchemeAdmin: true}, nil)
			},
			want: true,
		},
		"a former admin of that community (left it)": {
			setup: func(e *testEnv) {
				e.api.On("HasPermissionTo", e.userID, model.PermissionManageSystem).Return(false)
				e.api.On("GetChannelMember", channelID, e.userID).Return(nil, model.NewAppError("test", "test.not_a_member", nil, "", http.StatusNotFound))
				e.api.On("GetChannel", channelID).Return(channel, nil)
				e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{SchemeAdmin: true, DeleteAt: 1}, nil)
			},
			want: false,
		},
		"a plain member": {
			setup: func(e *testEnv) {
				e.api.On("HasPermissionTo", e.userID, model.PermissionManageSystem).Return(false)
				e.api.On("GetChannelMember", channelID, e.userID).Return(&model.ChannelMember{SchemeAdmin: false}, nil)
				e.api.On("GetChannel", channelID).Return(channel, nil)
				e.api.On("GetTeamMember", teamID, e.userID).Return(&model.TeamMember{SchemeAdmin: false}, nil)
			},
			want: false,
		},
		"an admin of a different channel and community": {
			setup: func(e *testEnv) {
				e.api.On("HasPermissionTo", e.userID, model.PermissionManageSystem).Return(false)
				e.api.On("GetChannelMember", channelID, e.userID).Return(nil, model.NewAppError("test", "test.not_a_member", nil, "", http.StatusNotFound))
				e.api.On("GetChannel", channelID).Return(channel, nil)
				e.api.On("GetTeamMember", teamID, e.userID).Return(nil, model.NewAppError("test", "test.not_a_member", nil, "", http.StatusNotFound))
			},
			want: false,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			e := newMeetingTestEnv(t)
			e.userID = model.NewId()
			tc.setup(e)
			assert.Equal(t, tc.want, e.p.canCancelMeeting(e.userID, m))
		})
	}
}

// TestHandleCancelMeetingPermissions checks the HTTP-level consequences of canCancelMeeting: a
// plain member is refused (403), and a meeting in a channel the caller can't read at all looks
// like one that doesn't exist (404) even to someone who happens to be an admin elsewhere.
func TestHandleCancelMeetingPermissions(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = model.NewId()
	require.NoError(t, e.p.store.CreateMeeting(m))

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)
	e.api.On("HasPermissionTo", e.userID, model.PermissionManageSystem).Return(false)
	e.api.On("GetChannelMember", channelID, e.userID).Return(&model.ChannelMember{SchemeAdmin: false}, nil)
	e.api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID}, nil)

	w := e.request(t, http.MethodDelete, "/api/v1/meetings/"+m.ID, nil)
	assert.Equal(t, http.StatusForbidden, w.Code)

	stored, err := e.p.store.GetMeeting(m.ID)
	require.NoError(t, err)
	assert.False(t, stored.Cancelled)
}

// TestHandleCancelMeetingUnreadableChannel checks that a meeting in a channel the caller can't
// read at all looks like one that doesn't exist (404), even to an admin elsewhere.
func TestHandleCancelMeetingUnreadableChannel(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = model.NewId()
	require.NoError(t, e.p.store.CreateMeeting(m))

	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(false)
	w := e.request(t, http.MethodDelete, "/api/v1/meetings/"+m.ID, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
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
	e.notAnAdmin(channelID)

	w := e.request(t, http.MethodPost, "/api/v1/meetings/"+m.ID+"/rsvp", map[string]string{"status": "maybe"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	updated := decode[meetingView](t, w)
	assert.Equal(t, RSVPMaybe, updated.RSVPs[e.userID].Status)
	assert.Equal(t, RSVPMaybe, updated.MyRSVP, "the caller's own answer, for the next occurrence")
	assert.False(t, updated.MyRSVPNextOnly)
	assert.Equal(t, m.Start, updated.NextStart)
	assert.Equal(t, m.End, updated.NextEnd)
	assert.False(t, updated.CanCancel)

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
	e.api.On("GetConfig").Return(showEmailConfig(true))

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
	e.api.On("GetConfig").Return(showEmailConfig(true))

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

	e.p.sendMeetingInvites(m, channel, organizer, organizer)

	assert.Equal(t, "grace@example.com", sentTo, "only the member who connected a calendar account and opted in gets an email")
	assert.Contains(t, sentSubject, m.Summary)
	assert.Contains(t, sentBody, m.Summary)
	assert.Equal(t, "invite.ics", sentAttachmentName)
	assert.Contains(t, sentContentType, "method=REQUEST")
	assert.Contains(t, string(sentData), "METHOD:REQUEST")
	assert.Contains(t, string(sentData), "mailto:grace@example.com")
	assert.Contains(t, string(sentData), "mailto:ada@example.com")
}

// showEmailConfig is a server config that shows (or hides) email addresses, with a feedback
// address for the server's own emails.
func showEmailConfig(show bool) *model.Config {
	feedback := "feedback@example.com"
	return &model.Config{
		PrivacySettings: model.PrivacySettings{ShowEmailAddress: &show},
		EmailSettings:   model.EmailSettings{FeedbackEmail: &feedback},
	}
}

// TestOrganizerInviteeHidesTheAddress checks the organiser's iTIP address: their own while the
// server shows email addresses, otherwise the server's notification address (or a no-reply one on
// the site's host), never theirs.
func TestOrganizerInviteeHidesTheAddress(t *testing.T) {
	organizer := &model.User{Id: model.NewId(), Username: "ada", FirstName: "Ada", Email: "ada@example.com"}
	siteURL := "https://chat.example.org"
	empty := ""
	hidden := false
	for name, tc := range map[string]struct {
		cfg  *model.Config
		want string
	}{
		"shown":                         {cfg: showEmailConfig(true), want: "ada@example.com"},
		"hidden":                        {cfg: showEmailConfig(false), want: "feedback@example.com"},
		"hidden, no feedback address":   {cfg: &model.Config{PrivacySettings: model.PrivacySettings{ShowEmailAddress: &hidden}, EmailSettings: model.EmailSettings{FeedbackEmail: &empty}, ServiceSettings: model.ServiceSettings{SiteURL: &siteURL}}, want: "noreply@chat.example.org"},
		"no config (treated as hidden)": {cfg: nil, want: "noreply@localhost"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newMeetingTestEnv(t)
			e.api.On("GetConfig").Return(tc.cfg)
			party := e.p.organizerInvitee(organizer)
			assert.Equal(t, tc.want, party.Email)
			assert.Equal(t, organizer.GetFullName(), party.Name)
		})
	}
}

// TestMeetingRoutesUnreadableChannel checks every route on one meeting for a caller who can't
// read its channel (a private channel they aren't in, or one they left): each answers 404, as if
// it didn't exist, and nothing is changed, even for the call's own organiser.
func TestMeetingRoutesUnreadableChannel(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = e.userID
	require.NoError(t, e.p.store.CreateMeeting(m))
	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(false)

	minutes := 60
	for name, req := range map[string]struct {
		method, path string
		body         any
	}{
		"get":      {http.MethodGet, "", nil},
		"ics":      {http.MethodGet, "/ics", nil},
		"rsvp":     {http.MethodPost, "/rsvp", map[string]string{"status": "going"}},
		"reminder": {http.MethodPost, "/reminder", map[string]any{"minutes": minutes}},
		"edit":     {http.MethodPut, "", meetingBody(time.Now().Add(2 * time.Hour))},
		"cancel":   {http.MethodDelete, "", nil},
	} {
		w := e.request(t, req.method, "/api/v1/meetings/"+m.ID+req.path, req.body)
		assert.Equal(t, http.StatusNotFound, w.Code, name)
	}

	stored, err := e.p.store.GetMeeting(m.ID)
	require.NoError(t, err)
	assert.False(t, stored.Cancelled)
	assert.Equal(t, 0, stored.Sequence)
	assert.Empty(t, stored.RSVPs)
	assert.Empty(t, stored.ReminderOverrides)
}

// TestMeetingViewOnlyTheCallersOwn checks that a meeting's view carries the caller's own RSVP and
// reminder choice and nobody else's, whoever asks (the organiser included).
func TestMeetingViewOnlyTheCallersOwn(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	other := model.NewId()
	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = e.userID
	m.RSVPs = map[string]RSVP{e.userID: {Status: RSVPGoing}, other: {Status: RSVPNo}}
	m.NextRSVPs = map[string]occurrenceRSVP{other: {RSVP: RSVP{Status: RSVPMaybe}, OccurrenceStart: m.Start}}
	m.ReminderOverrides = map[string]int{e.userID: 60, other: 5}
	require.NoError(t, e.p.store.CreateMeeting(m))
	e.api.On("HasPermissionToChannel", e.userID, channelID, model.PermissionReadChannel).Return(true)

	w := e.request(t, http.MethodGet, "/api/v1/meetings/"+m.ID, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), other, "no other member's id, answer or reminder is in the response")

	view := decode[meetingView](t, w)
	assert.Equal(t, map[string]RSVP{e.userID: {Status: RSVPGoing}}, view.RSVPs)
	assert.Empty(t, view.NextRSVPs)
	require.NotNil(t, view.MyReminderOverride)
	assert.Equal(t, 60, *view.MyReminderOverride)
	assert.Equal(t, RSVPGoing, view.MyRSVP)
}

// TestMeetingInvitesListOnlyTheirRecipient checks that every invitation, and every occurrence's
// cancellation, lists its recipient alone as an attendee: no member's email address (or answer)
// reaches another member through the .ics, whatever their privacy settings.
func TestMeetingInvitesListOnlyTheirRecipient(t *testing.T) {
	e := newMeetingTestEnv(t)
	channelID := model.NewId()
	channel := &model.Channel{Id: channelID, Name: "standup", DisplayName: "Standup", Type: model.ChannelTypeOpen}
	organizer := &model.User{Id: model.NewId(), Username: "ada", Email: "ada@example.com", Locale: "en"}

	members := map[string]string{model.NewId(): "grace@example.com", model.NewId(): "katherine@example.com"}
	var channelMembers model.ChannelMembers
	for id, email := range members {
		require.NoError(t, e.p.store.SaveAccount(id, &Account{
			URL: "https://dav.example.com", Username: email, HomeSetURL: "https://dav.example.com/cal/", Password: "x", EmailInvites: true,
		}))
		channelMembers = append(channelMembers, model.ChannelMember{ChannelId: channelID, UserId: id})
		e.api.On("GetUser", id).Return(&model.User{Id: id, Email: email, Username: email, Locale: "en"}, nil)
	}
	e.api.On("GetChannel", channelID).Return(channel, nil)
	e.api.On("GetChannelMembers", channelID, 0, 200).Return(channelMembers, nil)
	e.api.On("GetConfig").Return(showEmailConfig(false))
	e.api.On("HasPermissionToChannel", organizer.Id, channelID, model.PermissionReadChannel).Return(true)

	sent := map[string]string{}
	e.api.On("SendMailWithAttachment", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8")).
		Run(func(args mock.Arguments) {
			data, _ := args.Get(5).([]byte)
			sent[args.String(0)] = string(data)
		}).
		Return(nil)

	m := testMeeting()
	m.ChannelID = channelID
	m.OrganizerID = organizer.Id

	check := func(t *testing.T) {
		t.Helper()
		require.Len(t, sent, len(members))
		for to, ics := range sent {
			assert.Contains(t, ics, "mailto:"+to)
			assert.NotContains(t, ics, organizer.Email, "the organiser's address is hidden while the server hides email addresses")
			assert.Contains(t, ics, "mailto:feedback@example.com")
			for _, other := range members {
				if other != to {
					assert.NotContains(t, ics, other, "another member's address must not be in %s's invitation", to)
				}
			}
		}
	}

	t.Run("invitation", func(t *testing.T) {
		clear(sent)
		e.p.sendMeetingInvites(m, channel, organizer, organizer)
		check(t)
	})
	t.Run("an occurrence's cancellation", func(t *testing.T) {
		clear(sent)
		start := time.UnixMilli(m.Start)
		e.p.sendOccurrenceCancellationEmails(m, channel, organizer, organizer, false, start, start.Add(time.Hour))
		check(t)
	})
}

// TestMeetingViewNextOnlyRSVP checks the view's own-RSVP fields for a series: a "next call only"
// answer shows as the caller's RSVP for the next occurrence, flagged as such.
func TestMeetingViewNextOnlyRSVP(t *testing.T) {
	e := newMeetingTestEnv(t)
	m := testRecurringMeeting("FREQ=WEEKLY")
	m.RSVPs = map[string]RSVP{e.userID: {Status: RSVPGoing}}
	m.NextRSVPs = map[string]occurrenceRSVP{e.userID: {RSVP: RSVP{Status: RSVPNo}, OccurrenceStart: m.Start}}
	e.notAnAdmin(m.ChannelID)

	view := e.p.newMeetingView(e.userID, m)
	assert.Equal(t, m.Start, view.NextStart)
	assert.Equal(t, RSVPNo, view.MyRSVP)
	assert.True(t, view.MyRSVPNextOnly)

	cancelled := testMeeting()
	cancelled.Cancelled = true
	view = e.p.newMeetingView(e.userID, cancelled)
	assert.Zero(t, view.NextStart, "a cancelled call has no next occurrence")
}
