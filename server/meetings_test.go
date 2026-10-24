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
	now := time.Now()
	return &Meeting{
		ID:          "meeting1",
		ChannelID:   "channel1",
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
	updated, err := store.EditMeeting(m.ID, m.Summary, m.Description, m.Start, m.End)
	require.NoError(t, err)
	assert.Equal(t, 0, updated.Sequence)

	// Changing the time does.
	newEnd := m.End + int64(time.Hour/time.Millisecond)
	updated, err = store.EditMeeting(m.ID, m.Summary, m.Description, m.Start, newEnd)
	require.NoError(t, err)
	assert.Equal(t, 1, updated.Sequence)
	assert.Equal(t, newEnd, updated.End)

	// Editing a meeting that doesn't exist fails.
	_, err = store.EditMeeting("no-such-meeting", "x", "", m.Start, m.End)
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
	_, err = store.EditMeeting(m.ID, "new title", m.Description, m.Start, m.End)
	assert.ErrorIs(t, err, errMeetingCancelled)
	_, err = store.SetRSVP(m.ID, "user1", RSVPGoing)
	assert.ErrorIs(t, err, errMeetingCancelled)
}

func TestStoreMeetingRSVP(t *testing.T) {
	store, _ := newTestStore(t)
	m := testMeeting()
	require.NoError(t, store.CreateMeeting(m))

	updated, err := store.SetRSVP(m.ID, "user1", RSVPGoing)
	require.NoError(t, err)
	assert.Equal(t, RSVPGoing, updated.RSVPs["user1"].Status)
	assert.NotZero(t, updated.RSVPs["user1"].UpdatedAt)

	// A second RSVP from another member doesn't disturb the first.
	updated, err = store.SetRSVP(m.ID, "user2", RSVPMaybe)
	require.NoError(t, err)
	assert.Equal(t, RSVPGoing, updated.RSVPs["user1"].Status)
	assert.Equal(t, RSVPMaybe, updated.RSVPs["user2"].Status)

	// Changing your mind overwrites your own RSVP only.
	updated, err = store.SetRSVP(m.ID, "user1", RSVPNo)
	require.NoError(t, err)
	assert.Equal(t, RSVPNo, updated.RSVPs["user1"].Status)
	assert.Equal(t, RSVPMaybe, updated.RSVPs["user2"].Status)
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

func TestMeetingTTLHasAMinimum(t *testing.T) {
	// An event that already ended still keeps its key for a while, so the post can still show
	// who RSVP'd after the fact, instead of expiring at once.
	ttl := meetingTTL(time.Now().Add(-365 * 24 * time.Hour).UnixMilli())
	assert.GreaterOrEqual(t, ttl, int64(meetingMinTTL/time.Second))
}
