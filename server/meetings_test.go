// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testMeeting() *Meeting {
	// To the second, as the API stores a call's times (see wholeSeconds).
	now := time.Now().Truncate(time.Second)
	return &Meeting{
		ID:          "meeting1",
		ChannelID:   "channel1",
		TeamID:      "team1",
		PostID:      "post1",
		OrganizerID: "organizer1",
		Summary:     "Team sync",
		Description: "Weekly catch-up",
		Start:       now.Add(time.Hour).UnixMilli(),
		End:         now.Add(2 * time.Hour).UnixMilli(),
		CreatedAt:   now.UnixMilli(),
		UpdatedAt:   now.UnixMilli(),
	}
}

func TestStoreMeetingCreateAndGet(t *testing.T) {
	store, _ := newTestStore(t)

	got, err := store.GetMeeting("meeting1")
	require.NoError(t, err)
	assert.Nil(t, got)

	m := testMeeting()
	require.NoError(t, store.CreateMeeting(m))

	got, err = store.GetMeeting("meeting1")
	require.NoError(t, err)
	assert.Equal(t, m, got)
}

func TestStoreMeetingEditBumpsSequenceOnlyOnChange(t *testing.T) {
	store, _ := newTestStore(t)
	m := testMeeting()
	require.NoError(t, store.CreateMeeting(m))

	// Editing without changing anything doesn't bump the SEQUENCE: iTIP readers would otherwise
	// treat an unrelated save as a new update.
	updated, err := store.EditMeeting(m.ID, m.Summary, m.Description, m.Start, m.End, "", "", defaultReminderMinutes)
	require.NoError(t, err)
	assert.Equal(t, 0, updated.Sequence)

	// Changing the time does.
	newEnd := m.End + int64(time.Hour/time.Millisecond)
	updated, err = store.EditMeeting(m.ID, m.Summary, m.Description, m.Start, newEnd, "", "", defaultReminderMinutes)
	require.NoError(t, err)
	assert.Equal(t, 1, updated.Sequence)
	assert.Equal(t, newEnd, updated.End)

	// Editing a meeting that doesn't exist fails.
	_, err = store.EditMeeting("no-such-meeting", "x", "", m.Start, m.End, "", "", defaultReminderMinutes)
	assert.ErrorIs(t, err, errMeetingNotFound)
}

func TestStoreMeetingCancel(t *testing.T) {
	store, _ := newTestStore(t)
	m := testMeeting()
	require.NoError(t, store.CreateMeeting(m))

	cancelled, err := store.CancelMeeting(m.ID)
	require.NoError(t, err)
	assert.True(t, cancelled.Cancelled)
	assert.Equal(t, 1, cancelled.Sequence)

	// Cancelling twice is a no-op: the SEQUENCE doesn't bump again.
	cancelledAgain, err := store.CancelMeeting(m.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, cancelledAgain.Sequence)

	// A cancelled meeting can no longer be edited or RSVP'd to.
	_, err = store.EditMeeting(m.ID, "new title", m.Description, m.Start, m.End, "", "", defaultReminderMinutes)
	assert.ErrorIs(t, err, errMeetingCancelled)
	_, err = store.SetRSVP(m.ID, "user1", RSVPGoing, 0)
	assert.ErrorIs(t, err, errMeetingCancelled)
}

// TestStoreMeetingCancelDropsItFromTheTeamIndex checks that cancelling a meeting removes it from
// its community's calendar index: a cancelled meeting is never shown there.
func TestStoreMeetingCancelDropsItFromTheTeamIndex(t *testing.T) {
	store, _ := newTestStore(t)
	m := testMeeting()
	require.NoError(t, store.CreateMeeting(m))

	meetings, err := store.ListTeamMeetings(m.TeamID)
	require.NoError(t, err)
	require.Len(t, meetings, 1)

	_, err = store.CancelMeeting(m.ID)
	require.NoError(t, err)

	meetings, err = store.ListTeamMeetings(m.TeamID)
	require.NoError(t, err)
	assert.Empty(t, meetings)
}

// TestStoreMeetingCancelOccurrence checks that cancelling one occurrence adds an EXDATE and bumps
// the SEQUENCE, without cancelling the rest of the series, and that it refuses duplicates.
func TestStoreMeetingCancelOccurrence(t *testing.T) {
	store, _ := newTestStore(t)
	m := testMeeting()
	m.RecurrenceRule = "FREQ=WEEKLY"
	require.NoError(t, store.CreateMeeting(m))

	second := m.Start + int64(7*24*time.Hour/time.Millisecond)
	updated, err := store.CancelOccurrence(m.ID, second)
	require.NoError(t, err)
	assert.False(t, updated.Cancelled)
	assert.Equal(t, []int64{second}, updated.ExceptionDates)
	assert.Equal(t, 1, updated.Sequence)

	// The same occurrence again is a no-op.
	updated, err = store.CancelOccurrence(m.ID, second)
	require.NoError(t, err)
	assert.Equal(t, 1, updated.Sequence)
	assert.Equal(t, []int64{second}, updated.ExceptionDates)
}

func TestStoreMeetingRSVP(t *testing.T) {
	store, _ := newTestStore(t)
	m := testMeeting()
	require.NoError(t, store.CreateMeeting(m))

	updated, err := store.SetRSVP(m.ID, "user1", RSVPGoing, 0)
	require.NoError(t, err)
	assert.Equal(t, RSVPGoing, updated.RSVPs["user1"].Status)
	assert.NotZero(t, updated.RSVPs["user1"].UpdatedAt)

	// A second RSVP from another member doesn't disturb the first.
	updated, err = store.SetRSVP(m.ID, "user2", RSVPMaybe, 0)
	require.NoError(t, err)
	assert.Equal(t, RSVPGoing, updated.RSVPs["user1"].Status)
	assert.Equal(t, RSVPMaybe, updated.RSVPs["user2"].Status)

	// Changing your mind overwrites your own RSVP only.
	updated, err = store.SetRSVP(m.ID, "user1", RSVPNo, 0)
	require.NoError(t, err)
	assert.Equal(t, RSVPNo, updated.RSVPs["user1"].Status)
	assert.Equal(t, RSVPMaybe, updated.RSVPs["user2"].Status)
}

// TestStoreMeetingNextOnlyRSVP checks that an occurrence-scoped RSVP overrides the series-level
// one only for that exact occurrence, and that effectiveRSVP falls back correctly.
func TestStoreMeetingNextOnlyRSVP(t *testing.T) {
	store, _ := newTestStore(t)
	m := testMeeting()
	m.RecurrenceRule = "FREQ=WEEKLY"
	require.NoError(t, store.CreateMeeting(m))
	_, err := store.SetRSVP(m.ID, "user1", RSVPGoing, 0)
	require.NoError(t, err)

	second := m.Start + int64(7*24*time.Hour/time.Millisecond)
	updated, err := store.SetRSVP(m.ID, "user1", RSVPNo, second)
	require.NoError(t, err)

	rsvp, ok := updated.effectiveRSVP("user1", m.Start)
	require.True(t, ok)
	assert.Equal(t, RSVPGoing, rsvp.Status, "the first occurrence keeps the series-level RSVP")

	rsvp, ok = updated.effectiveRSVP("user1", second)
	require.True(t, ok)
	assert.Equal(t, RSVPNo, rsvp.Status, "the overridden occurrence uses the override")

	third := second + int64(7*24*time.Hour/time.Millisecond)
	rsvp, ok = updated.effectiveRSVP("user1", third)
	require.True(t, ok)
	assert.Equal(t, RSVPGoing, rsvp.Status, "a later occurrence the override wasn't for falls back to the series-level RSVP")
}

func TestStoreListMeetingIDs(t *testing.T) {
	store, _ := newTestStore(t)
	require.NoError(t, store.CreateMeeting(testMeeting()))
	m2 := testMeeting()
	m2.ID = "meeting2"
	require.NoError(t, store.CreateMeeting(m2))

	ids, err := store.ListMeetingIDs()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"meeting1", "meeting2"}, ids)
}

// TestStoreListTeamMeetingsDropsStaleIDs checks that a meeting ID whose meeting has expired or
// been deleted is dropped from the community's index on read, rather than returned or kept.
func TestStoreListTeamMeetingsDropsStaleIDs(t *testing.T) {
	store, kv := newTestStore(t)
	m := testMeeting()
	require.NoError(t, store.CreateMeeting(m))

	// Simulate the meeting's own key having expired, leaving the index stale.
	kv.mu.Lock()
	delete(kv.data, meetingKey(m.ID))
	kv.mu.Unlock()

	meetings, err := store.ListTeamMeetings(m.TeamID)
	require.NoError(t, err)
	assert.Empty(t, meetings)

	// The stale ID is gone from the index now too, not just skipped this once.
	meetings, err = store.ListTeamMeetings(m.TeamID)
	require.NoError(t, err)
	assert.Empty(t, meetings)
	data, appErr := kv.KVGet(teamIndexKey(m.TeamID))
	require.Nil(t, appErr)
	assert.JSONEq(t, "[]", string(data))
}

func TestValidateMeetingInput(t *testing.T) {
	now := time.Now()
	assert.NoError(t, validateMeetingInput("Standup", "", now, now.Add(30*time.Minute)))

	for name, run := range map[string]func() error{
		"empty title":   func() error { return validateMeetingInput("  ", "", now, now.Add(time.Hour)) },
		"ends before":   func() error { return validateMeetingInput("x", "", now, now.Add(-time.Minute)) },
		"zero duration": func() error { return validateMeetingInput("x", "", now, now) },
		"too long":      func() error { return validateMeetingInput("x", "", now, now.Add(25*time.Hour)) },
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, run())
		})
	}
}

func TestValidReminderMinutes(t *testing.T) {
	for _, minutes := range reminderChoicesMinutes {
		assert.True(t, validReminderMinutes(minutes), "choice %d must be valid", minutes)
	}
	for _, minutes := range []int{1, 2, 7, 20, 45, 90, 2880, -2} {
		assert.False(t, validReminderMinutes(minutes), "%d isn't one of the fixed choices", minutes)
	}
}

// TestMeetingReminderMinutesDefault checks that a meeting saved before the reminder choice
// existed (a nil ReminderMinutes) keeps the old fixed default.
func TestMeetingReminderMinutesDefault(t *testing.T) {
	m := testMeeting()
	assert.Equal(t, defaultReminderMinutes, m.meetingReminderMinutes())
	assert.Equal(t, defaultReminderMinutes, m.effectiveReminderMinutes("user1"))

	m.ReminderMinutes = new(30)
	assert.Equal(t, 30, m.meetingReminderMinutes())
	assert.Equal(t, 30, m.effectiveReminderMinutes("user1"), "a member without an override uses the meeting's setting")

	m.ReminderOverrides = map[string]int{"user1": noReminderMinutes}
	assert.Equal(t, noReminderMinutes, m.effectiveReminderMinutes("user1"), "a member's own override wins")
	assert.Equal(t, 30, m.effectiveReminderMinutes("user2"), "only overridden for the member who set it")
}

func TestMeetingTTLHasAMinimum(t *testing.T) {
	// An event that already ended still keeps its key for a while, so the post can still show
	// who RSVP'd after the fact, instead of expiring at once.
	ttl := meetingTTL(time.Now().Add(-365 * 24 * time.Hour).UnixMilli())
	assert.GreaterOrEqual(t, ttl, int64(meetingMinTTL/time.Second))
}

// TestMeetingTTLEndlessSeries checks that a series with no COUNT or UNTIL has no TTL until it's
// cancelled, and gets one (30 days) as soon as it is.
func TestMeetingTTLEndlessSeries(t *testing.T) {
	store, kv := newTestStore(t)
	m := testMeeting()
	m.RecurrenceRule = "FREQ=WEEKLY"
	require.NoError(t, store.CreateMeeting(m))

	opts, ok := kv.options[meetingKey(m.ID)]
	require.True(t, ok)
	assert.Zero(t, opts.ExpireInSeconds, "an endless series has no TTL")

	cancelled, err := store.CancelMeeting(m.ID)
	require.NoError(t, err)
	_ = cancelled
	opts, ok = kv.options[meetingKey(m.ID)]
	require.True(t, ok)
	assert.Greater(t, opts.ExpireInSeconds, int64(0), "a cancelled series gets a TTL (30 days)")
	assert.LessOrEqual(t, opts.ExpireInSeconds, int64(31*24*time.Hour/time.Second))
}

// TestMeetingTTLBoundedSeries checks that a series with a COUNT or UNTIL gets a TTL counted from
// its last occurrence, like a one-off meeting, even before it's cancelled.
func TestMeetingTTLBoundedSeries(t *testing.T) {
	store, kv := newTestStore(t)
	m := testMeeting()
	m.RecurrenceRule = "FREQ=DAILY;COUNT=3"
	require.NoError(t, store.CreateMeeting(m))

	opts, ok := kv.options[meetingKey(m.ID)]
	require.True(t, ok)
	assert.Greater(t, opts.ExpireInSeconds, int64(0), "a bounded series gets a TTL even before it's cancelled")
}
