// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// reminderTick is how often reminders are sent.
	reminderTick = time.Minute
	// reminderRefresh is how often the upcoming events of each user are read again. Events
	// changed in Antimatter are read again at once.
	reminderRefresh = 10 * time.Minute
	// reminderHorizon is how far ahead events are read.
	reminderHorizon = 25 * time.Hour
	// reminderGrace is how late a reminder is still sent, e.g. after a restart.
	reminderGrace = 10 * time.Minute
	// maxConcurrentRefreshes bounds the calendar servers queried at once.
	maxConcurrentRefreshes = 5

	sentKeyPrefix = "sent_"
	sentKeyTTL    = 3 * 24 * time.Hour
)

// reminder is a reminder of an occurrence of an event, due at a time.
type reminder struct {
	due        time.Time
	minutes    int
	occurrence Occurrence
}

// remindersOf returns the reminders of occurrences, from their own alarms or, for timed events
// without alarms, the user's default.
func remindersOf(occurrences []Occurrence, defaultMinutes int) []reminder {
	var reminders []reminder
	for _, o := range occurrences {
		minutes := o.Alarms
		if len(minutes) == 0 && !o.AllDay && defaultMinutes > 0 {
			minutes = []int{defaultMinutes}
		}
		start := time.UnixMilli(o.Start)
		for _, m := range minutes {
			reminders = append(reminders, reminder{due: start.Add(-time.Duration(m) * time.Minute), minutes: m, occurrence: o})
		}
	}
	return reminders
}

// isDue returns whether a reminder must be sent now.
func (r reminder) isDue(now time.Time) bool {
	return !r.due.After(now) && now.Sub(r.due) <= reminderGrace
}

// sentKey identifies a reminder, so that it's sent once.
func (r reminder) sentKey(userID string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%d|%d", userID, r.occurrence.Path, r.occurrence.UID, r.occurrence.Start, r.minutes))
	return sentKeyPrefix + hex.EncodeToString(sum[:16])
}

// userReminders are the upcoming reminders of a user.
type userReminders struct {
	fetchedAt time.Time
	reminders []reminder
}

// reminderService sends the users the reminders of their events.
type reminderService struct {
	p *Plugin

	mu    sync.Mutex
	users map[string]*userReminders
}

func newReminderService(p *Plugin) *reminderService {
	return &reminderService{p: p, users: map[string]*userReminders{}}
}

// invalidate makes the next run read the events of a user again.
func (s *reminderService) invalidate(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.users, userID)
}

// run sends the reminders that are due. It runs every minute, on one server of the cluster.
func (s *reminderService) run(now time.Time) {
	if !s.p.getConfiguration().EnableReminders {
		return
	}
	userIDs, err := s.p.store.ListAccountUsers()
	if err != nil {
		s.p.API.LogWarn("Failed to list the calendar accounts", "err", err.Error())
		return
	}

	var wg sync.WaitGroup
	slots := make(chan struct{}, maxConcurrentRefreshes)
	for _, userID := range userIDs {
		s.mu.Lock()
		cached, ok := s.users[userID]
		s.mu.Unlock()
		if ok && now.Sub(cached.fetchedAt) < reminderRefresh {
			s.sendDue(userID, cached.reminders, now)
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			reminders, err := s.fetch(userID, now)
			if err != nil {
				// The user's server may be down, or their password changed: try again later
				reminders = nil
			}
			s.mu.Lock()
			s.users[userID] = &userReminders{fetchedAt: now, reminders: reminders}
			s.mu.Unlock()
			s.sendDue(userID, reminders, now)
		}()
	}
	wg.Wait()

	// Forget the users who disconnected their account
	known := map[string]bool{}
	for _, userID := range userIDs {
		known[userID] = true
	}
	s.mu.Lock()
	for userID := range s.users {
		if !known[userID] {
			delete(s.users, userID)
		}
	}
	s.mu.Unlock()
}

// userLocation returns the time zone of a user.
func (s *reminderService) userLocation(userID string) *time.Location {
	user, appErr := s.p.API.GetUser(userID)
	if appErr != nil {
		return time.UTC
	}
	if loc, err := time.LoadLocation(user.GetPreferredTimezone()); err == nil {
		return loc
	}
	return time.UTC
}

// fetch reads the upcoming reminders of a user.
func (s *reminderService) fetch(userID string, now time.Time) ([]reminder, error) {
	session, err := s.p.openSession(userID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()

	calendars, err := session.client.listCalendars(ctx, session.homeSet)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(calendars))
	for _, calendar := range calendars {
		ids = append(ids, calendar.ID)
	}
	occurrences, _ := session.listOccurrences(ctx, ids, now.Add(-reminderGrace), now.Add(reminderHorizon), s.userLocation(userID))

	var upcoming []reminder
	for _, r := range remindersOf(occurrences, session.account.RemindMinutes) {
		if !r.due.Before(now.Add(-reminderGrace)) {
			upcoming = append(upcoming, r)
		}
	}
	return upcoming, nil
}

// sendDue sends the reminders that are due and weren't sent yet.
func (s *reminderService) sendDue(userID string, reminders []reminder, now time.Time) {
	for _, r := range reminders {
		if !r.isDue(now) {
			continue
		}
		// Claim the reminder: whichever server gets there first sends it
		if claimed, err := s.p.store.Claim(r.sentKey(userID), sentKeyTTL); err != nil || !claimed {
			continue
		}
		if err := s.send(userID, r, now); err != nil {
			s.p.API.LogWarn("Failed to send a calendar reminder", "user_id", userID, "err", err.Error())
		}
	}
}

// send posts a reminder in the user's direct channel with the Calendar bot.
func (s *reminderService) send(userID string, r reminder, now time.Time) error {
	channel, appErr := s.p.API.GetDirectChannel(userID, s.p.botID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get the direct channel")
	}

	link := r.occurrence.Link
	if link != nil {
		link = s.p.channelLink(userID, link.ChannelID)
		if link != nil {
			link.Kind = r.occurrence.Link.Kind
		}
	}
	message := reminderMessage(r.occurrence, now, s.userLocation(userID), link, s.p.permalink(link))

	post := &model.Post{
		UserId:    s.p.botID,
		ChannelId: channel.Id,
		Message:   message,
		Props: model.StringInterface{
			"antimatter_calendar_event": map[string]any{
				"path":  r.occurrence.Path,
				"start": r.occurrence.Start,
			},
		},
	}
	if _, appErr := s.p.API.CreatePost(post); appErr != nil {
		return errors.Wrap(appErr, "failed to post the reminder")
	}
	return nil
}

// escapeMarkdown keeps the text of an event from being read as Markdown or mentioning anyone.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "~", `\~`, "#", `\#`,
		"<", "&lt;", ">", "&gt;", "@", "@\u200b", "\n", " ",
	)
	return replacer.Replace(s)
}

// reminderMessage is the text of a reminder.
func reminderMessage(o Occurrence, now time.Time, loc *time.Location, link *EventLink, permalink string) string {
	start := time.UnixMilli(o.Start).In(loc)
	summary := escapeMarkdown(o.Summary)
	if summary == "" {
		summary = "(untitled event)"
	}

	var when string
	switch {
	case o.AllDay:
		when = "today, all day"
		if start.YearDay() != now.In(loc).YearDay() {
			when = start.Format("Monday 2 January") + ", all day"
		}
	case !start.After(now):
		when = "now, at " + start.Format("15:04")
	default:
		minutes := int(start.Sub(now).Round(time.Minute) / time.Minute)
		in := fmt.Sprintf("in %d minutes", minutes)
		switch {
		case minutes == 1:
			in = "in 1 minute"
		case minutes >= 120:
			in = fmt.Sprintf("in %d hours", minutes/60)
		case minutes >= 60:
			in = "in an hour"
		}
		when = in + ", at " + start.Format("15:04")
	}

	lines := []string{fmt.Sprintf(":calendar: **%s** starts %s.", summary, when)}
	if o.Location != "" {
		lines = append(lines, "Location: "+escapeMarkdown(o.Location))
	}
	if link != nil {
		name := escapeMarkdown(link.DisplayName)
		target := name
		if permalink != "" {
			target = fmt.Sprintf("[%s](%s)", name, permalink)
		}
		if link.Kind == LinkCall {
			lines = append(lines, "Join the call in "+target)
		} else {
			lines = append(lines, "Join "+target)
		}
	}
	return strings.Join(lines, "\n")
}
