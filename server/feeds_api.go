// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pkg/errors"
)

const (
	// feedRoute is the path of a calendar feed, under /plugins/com.antimatterchat.calendar: the
	// only route of this app that's served without the user's session (see newRouter).
	feedRoute = "/feeds/{file}"
	feedPath  = "/feeds/"
	feedExt   = ".ics"

	// The calls a feed carries: from 30 days ago to a year ahead, at most 500 of them.
	feedPast       = 30 * 24 * time.Hour
	feedAhead      = 365 * 24 * time.Hour
	maxFeedEntries = 500

	feedContentType  = "text/calendar; charset=utf-8"
	feedCacheControl = "private, max-age=900"
	// feedRefresh is how often calendar apps are asked to read the feed again.
	feedRefresh = "PT1H"

	// feedNotFoundBody is the one answer to every feed that can't be served, whatever the reason
	// (unknown or reset token, a member who left or was deactivated, feeds turned off...), so
	// none of them can be told apart.
	feedNotFoundBody = "Not found\n"
)

var (
	errFeedNotFound = errors.New("feed not found")
	errFeedsOff     = newAPIError(http.StatusNotFound, "calendar feeds are turned off")

	// msgFeedChannel names a call's channel in its description, in the feed.
	msgFeedChannel = &i18n.Message{
		ID:          "calendar.feed.channel",
		Description: "Line of a scheduled call's description in a calendar feed (shown in the person's own calendar app): the channel the call is in.",
		Other:       "Channel: {{.Channel}}",
	}
)

// feedStatus is the caller's feed of a community, as the Subscribe dialog shows it.
type feedStatus struct {
	Exists    bool  `json:"exists"`
	CreatedAt int64 `json:"created_at,omitempty"`
}

// feedLink is a new feed: its address, shown once (only its hash is kept).
type feedLink struct {
	// URL is the full address, absent when the server has no Site URL (the client then builds it
	// from Path).
	URL       string `json:"url,omitempty"`
	Path      string `json:"path"`
	CreatedAt int64  `json:"created_at"`
}

func (p *Plugin) feedsAllowed() bool {
	return p.getConfiguration().AllowCalendarFeeds
}

func (p *Plugin) siteURL() string {
	if cfg := p.API.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		return strings.TrimSuffix(*cfg.ServiceSettings.SiteURL, "/")
	}
	return ""
}

// feedMember checks that the caller may have a feed of the community in the request: feeds are
// on, the community ID is valid, and the caller is an active member of it, not a bot.
func (p *Plugin) feedMember(r *http.Request) (string, error) {
	if !p.feedsAllowed() {
		return "", errFeedsOff
	}
	teamID := muxVar(r, "teamId")
	if !model.IsValidId(teamID) {
		return "", newAPIError(http.StatusBadRequest, "invalid community")
	}
	if !p.isCommunityMember(teamID, userID(r)) {
		return "", newAPIError(http.StatusForbidden, "you aren't a member of this community")
	}
	return teamID, nil
}

// handleGetFeed says whether the caller has a feed of a community: GET /teams/{teamId}/feed. Its
// address can't be read again (only its hash is kept): a reset makes a new one.
func (p *Plugin) handleGetFeed(w http.ResponseWriter, r *http.Request) {
	teamID, err := p.feedMember(r)
	if err != nil {
		p.writeError(w, err)
		return
	}
	pointer, err := p.store.GetFeedPointer(userID(r), teamID)
	if err != nil {
		p.writeError(w, err)
		return
	}
	status := feedStatus{}
	if pointer != nil {
		status = feedStatus{Exists: true, CreatedAt: pointer.CreatedAt}
	}
	w.Header().Set("Cache-Control", "no-store")
	p.writeJSON(w, status)
}

// handleCreateFeed makes the caller a new feed of a community, revoking the one they had:
// POST /teams/{teamId}/feed. It answers with the new address, the only time it's ever shown.
func (p *Plugin) handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	teamID, err := p.feedMember(r)
	if err != nil {
		p.writeError(w, err)
		return
	}
	user, appErr := p.API.GetUser(userID(r))
	if appErr != nil || user == nil || user.IsBot || user.DeleteAt != 0 {
		p.writeError(w, newAPIError(http.StatusForbidden, "this account can't have calendar feeds"))
		return
	}
	token, pointer, err := p.store.CreateFeed(user.Id, teamID, time.Now())
	if err != nil {
		p.writeError(w, err)
		return
	}
	link := feedLink{Path: "/plugins/" + manifest.Id + feedPath + token + feedExt, CreatedAt: pointer.CreatedAt}
	if site := p.siteURL(); site != "" {
		link.URL = site + link.Path
	}
	w.Header().Set("Cache-Control", "no-store")
	p.writeJSON(w, link)
}

// writeFeedNotFound answers a feed that can't be served, always the same way.
func writeFeedNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(feedNotFoundBody))
}

// handleFeed serves a calendar feed: GET /feeds/{token}.ics, without a session. Everything is
// checked again on every read, from the token alone: feeds are on, the token is current (not
// reset), its member exists, isn't deactivated or a bot, and is still an active member of its
// community; the calls are those GET /meetings would show them now (readableCommunityMeetings).
// Any failure is the same 404. The token is never logged.
func (p *Plugin) handleFeed(w http.ResponseWriter, r *http.Request) {
	if !p.feedsAllowed() {
		writeFeedNotFound(w)
		return
	}
	token, ok := strings.CutSuffix(mux.Vars(r)["file"], feedExt)
	if !ok || !validFeedToken(token) {
		writeFeedNotFound(w)
		return
	}
	hash := feedTokenHash(token)

	if allowed, wait := p.feedLimiter.allow(hash, time.Now()); !allowed {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("Too many requests\n"))
		return
	}

	body, err := p.buildFeed(hash, time.Now())
	if err != nil {
		if !errors.Is(err, errFeedNotFound) {
			// The error names neither the token nor its hash.
			p.API.LogWarn("Failed to build a calendar feed", "err", err.Error())
		}
		writeFeedNotFound(w)
		return
	}

	sum := sha256.Sum256(body)
	w.Header().Set("Content-Type", feedContentType)
	w.Header().Set("Cache-Control", feedCacheControl)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// ServeContent answers If-None-Match with 304 (against the ETag above), and HEAD without a body.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

// buildFeed builds the calendar of a feed token's hash, for its member as of now; errFeedNotFound
// when there's none to serve.
func (p *Plugin) buildFeed(hash string, now time.Time) ([]byte, error) {
	record, err := p.store.LookupFeed(hash)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, errFeedNotFound
	}
	user, appErr := p.API.GetUser(record.UserID)
	if appErr != nil || user == nil || user.DeleteAt != 0 || user.IsBot {
		return nil, errFeedNotFound
	}
	team, appErr := p.API.GetTeam(record.TeamID)
	if appErr != nil || team == nil || team.DeleteAt != 0 {
		return nil, errFeedNotFound
	}
	if !p.isCommunityMember(team.Id, user.Id) {
		return nil, errFeedNotFound
	}

	readable, err := p.readableCommunityMeetings(user.Id, team.Id)
	if err != nil {
		return nil, err
	}

	from, to := now.Add(-feedPast), now.Add(feedAhead)
	type entry struct {
		readableMeeting
		first time.Time
	}
	var entries []entry
	for _, rm := range readable {
		starts, err := meetingOccurrences(rm.meeting, from, to)
		if err != nil || len(starts) == 0 {
			continue
		}
		entries = append(entries, entry{readableMeeting: rm, first: starts[0]})
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].first.Equal(entries[j].first) {
			return entries[i].first.Before(entries[j].first)
		}
		return entries[i].meeting.ID < entries[j].meeting.ID
	})
	if len(entries) > maxFeedEntries {
		entries = entries[:maxFeedEntries]
	}

	name := team.DisplayName
	if name == "" {
		name = team.Name
	}
	cal := newFeedCalendar(name)
	t := newTranslator(p.translations, user.Locale)
	site := p.siteURL()
	organizers := map[string]*invitee{}
	for _, e := range entries {
		m := e.meeting
		organizer, seen := organizers[m.OrganizerID]
		if !seen {
			if u, appErr := p.API.GetUser(m.OrganizerID); appErr == nil && u != nil {
				party := p.organizerInvitee(u)
				organizer = &party
			}
			organizers[m.OrganizerID] = organizer
		}
		permalink := ""
		if site != "" && m.PostID != "" {
			permalink = site + "/" + team.Name + "/pl/" + m.PostID
		}
		addFeedEvent(cal, m, feedEventContext{
			channel:   e.channel,
			organizer: organizer,
			viewer:    user,
			permalink: permalink,
			t:         t,
		})
	}
	return encodeICS(cal)
}

// newFeedCalendar returns an empty feed calendar named after its community, asking calendar apps
// to read it again every hour.
func newFeedCalendar(name string) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, productID)
	cal.Props.SetText(ical.PropCalendarScale, "GREGORIAN")
	calName := ical.NewProp("X-WR-CALNAME")
	calName.SetText(name)
	calName.Params.Del(ical.ParamValue)
	cal.Props.Set(calName)
	// RFC 7986 requires VALUE=DURATION, which go-ical leaves out as the default type.
	refresh := ical.NewProp("REFRESH-INTERVAL")
	refresh.Params.Set(ical.ParamValue, string(ical.ValueDuration))
	refresh.Value = feedRefresh
	cal.Props.Set(refresh)
	setRaw(cal.Props, "X-PUBLISHED-TTL", feedRefresh)
	return cal
}

// feedEventContext is what a feed's event shows beside the meeting itself.
type feedEventContext struct {
	channel *model.Channel
	// organizer is nil when the organiser's profile can't be read: no ORGANIZER then.
	organizer *invitee
	// viewer is the feed's member: the only ATTENDEE, with their own answer.
	viewer    *model.User
	permalink string
	t         *translator
}

// addFeedEvent adds a meeting (a one-off call, or a whole series with its RRULE and EXDATEs) to a
// feed calendar, as its member alone may see it: their own PARTSTAT and reminder, nobody else's
// answers or addresses. Its UID is the meeting's iTIP UID, so a calendar that also got the email
// invitation shows one event. A series in a time zone is written in it (TZID, with its VTIMEZONE),
// so it keeps its local time across daylight saving changes; anything else in UTC.
func addFeedEvent(cal *ical.Calendar, m *Meeting, c feedEventContext) {
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, meetingUID(m.ID))

	stampMillis := m.UpdatedAt
	if stampMillis == 0 {
		stampMillis = m.CreatedAt
	}
	if stampMillis == 0 {
		stampMillis = m.Start
	}
	// Not now: the feed's body (and so its ETag) changes only when its calls do.
	stamp := ical.NewProp(ical.PropDateTimeStamp)
	stamp.SetDateTime(time.UnixMilli(stampMillis).UTC())
	ev.Props.Set(stamp)

	seq := ical.NewProp(ical.PropSequence)
	seq.SetValueType(ical.ValueInt)
	seq.Value = strconv.Itoa(m.Sequence)
	ev.Props.Set(seq)

	ev.Props.SetText(ical.PropSummary, m.Summary)

	channelName := c.channel.DisplayName
	if channelName == "" {
		channelName = c.channel.Name
	}
	var description []string
	if m.Description != "" {
		description = append(description, m.Description, "")
	}
	description = append(description, c.t.T(msgFeedChannel, map[string]any{"Channel": channelName}))
	if c.permalink != "" {
		description = append(description, c.permalink)
		uri := ical.NewProp(ical.PropURL)
		uri.SetValueType(ical.ValueURI)
		uri.Value = c.permalink
		ev.Props.Set(uri)
	}
	ev.Props.SetText(ical.PropDescription, strings.Join(description, "\n"))

	loc := time.UTC
	if m.recurring() {
		loc = meetingTimeZone(m)
	}
	start, end := time.UnixMilli(m.Start).In(loc), time.UnixMilli(m.End).In(loc)
	setTime(ev.Props, ical.PropDateTimeStart, start, false)
	setTime(ev.Props, ical.PropDateTimeEnd, end, false)
	if m.recurring() {
		rule := ical.NewProp(ical.PropRecurrenceRule)
		rule.SetValueType(ical.ValueRecurrence)
		rule.Value = m.RecurrenceRule
		ev.Props.Set(rule)
		for _, ex := range m.ExceptionDates {
			exdate := ical.NewProp(ical.PropExceptionDates)
			exdate.SetDateTime(time.UnixMilli(ex).In(loc))
			ev.Props.Add(exdate)
		}
		ensureTimeZone(cal, loc, start.Year())
	}

	if c.organizer != nil {
		ev.Props.Set(addressProp(ical.PropOrganizer, c.organizer.Email, c.organizer.Name))
	}
	// The member's own answer: for the whole series (a "next call only" answer would need an
	// override event of its own), or for the call.
	var rsvp RSVPStatus
	if m.recurring() {
		rsvp = m.RSVPs[c.viewer.Id].Status
	} else if answer, ok := m.effectiveRSVP(c.viewer.Id, m.Start); ok {
		rsvp = answer.Status
	}
	you := addressProp(ical.PropAttendee, c.viewer.Email, c.viewer.GetFullName())
	you.Params.Set(ical.ParamRole, "REQ-PARTICIPANT")
	you.Params.Set(ical.ParamParticipationStatus, partStat(rsvp))
	ev.Props.Add(you)

	if minutes := m.effectiveReminderMinutes(c.viewer.Id); minutes != noReminderMinutes {
		ev.Children = append(ev.Children, meetingAlarm(m.Summary, minutes))
	}

	cal.Children = append(cal.Children, ev.Component)
}

// UserHasLeftTeam revokes the member's feed of the community they left: rejoining doesn't bring
// it back. (Every read checks the membership anyway, in case this hook is missed.)
func (p *Plugin) UserHasLeftTeam(_ *plugin.Context, member *model.TeamMember, _ *model.User) {
	if p.store == nil || member == nil {
		return
	}
	if err := p.store.DeleteFeed(member.UserId, member.TeamId); err != nil {
		p.API.LogWarn("Failed to revoke the calendar feed of a member who left a community", "user_id", member.UserId, "err", err.Error())
	}
}

// UserHasBeenDeactivated revokes every feed of a deactivated user: reactivating them doesn't bring
// those back.
func (p *Plugin) UserHasBeenDeactivated(_ *plugin.Context, user *model.User) {
	if p.store == nil || user == nil {
		return
	}
	if err := p.store.DeleteUserFeeds(user.Id); err != nil {
		p.API.LogWarn("Failed to revoke the calendar feeds of a deactivated user", "user_id", user.Id, "err", err.Error())
	}
}
