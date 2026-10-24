// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// A calendar feed is a private ICS link to the scheduled calls of one community, as one member
// may see them: a calendar app subscribes to it and reads it without signing in, so the link's
// token is all it carries. Each member has at most one feed per community.
//
// Only the token's SHA-256 is stored, never the token itself: as feedRecordKey(hash), mapping it
// to its member and community, plus a pointer from the member and community to the hash
// (feedPointerKey), so a reset can find and delete the old one. A feed is valid only while that
// pointer still names its hash: a reset (or leaving the community) moves or deletes the pointer
// first, which revokes the old token at once even if deleting its record then fails.

const (
	// feedRecordKeyPrefix prefixes the record of each feed token, keyed by the token's SHA-256.
	feedRecordKeyPrefix = "feedtok_"
	// feedPointerKeyPrefix prefixes each member's pointer to their feed of a community.
	feedPointerKeyPrefix = "feedptr_"

	// feedTokenBytes is the size of a feed token, before base64url: 256 random bits.
	feedTokenBytes = 32
)

// feedTokenLength is the length of a feed token in unpadded base64url.
var feedTokenLength = base64.RawURLEncoding.EncodedLen(feedTokenBytes)

// feedRecord is what a feed token's hash maps to.
type feedRecord struct {
	UserID    string `json:"user_id"`
	TeamID    string `json:"team_id"`
	CreatedAt int64  `json:"created_at"`
}

// feedPointer is a member's pointer to their feed of a community: the hash of its token.
type feedPointer struct {
	Hash      string `json:"hash"`
	CreatedAt int64  `json:"created_at"`
}

func feedRecordKey(hash string) string {
	return feedRecordKeyPrefix + hash
}

func feedPointerKey(userID, teamID string) string {
	return feedPointerKeyPrefix + userID + "_" + teamID
}

// newFeedToken returns a new feed token (32 bytes from crypto/rand, base64url) and its hash.
func newFeedToken() (token, hash string, err error) {
	raw := make([]byte, feedTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", errors.Wrap(err, "failed to make a feed token")
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, feedTokenHash(token), nil
}

// feedTokenHash is the hex SHA-256 of a feed token: the only form of it that's ever stored.
func feedTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// validFeedToken returns whether a string has the shape of a feed token (so anything else is
// refused before the store is read).
func validFeedToken(token string) bool {
	if len(token) != feedTokenLength {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == feedTokenBytes
}

// GetFeedPointer returns a member's pointer to their feed of a community, nil if they have none.
func (s *Store) GetFeedPointer(userID, teamID string) (*feedPointer, error) {
	data, appErr := s.kv.KVGet(feedPointerKey(userID, teamID))
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to read the calendar feed")
	}
	if data == nil {
		return nil, nil
	}
	var pointer feedPointer
	if err := json.Unmarshal(data, &pointer); err != nil {
		return nil, errors.Wrap(err, "failed to decode the calendar feed")
	}
	return &pointer, nil
}

// CreateFeed gives a member a new feed of a community and returns its token, which is never stored
// and can't be read again. A feed they had before is revoked: the pointer moves to the new hash
// first (which kills the old token), then the old record is deleted.
func (s *Store) CreateFeed(userID, teamID string, now time.Time) (string, *feedPointer, error) {
	token, hash, err := newFeedToken()
	if err != nil {
		return "", nil, err
	}
	record, err := json.Marshal(feedRecord{UserID: userID, TeamID: teamID, CreatedAt: now.UnixMilli()})
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to encode the calendar feed")
	}
	// A new hash can't already exist (256 random bits); Atomic with no old value makes sure.
	saved, appErr := s.kv.KVSetWithOptions(feedRecordKey(hash), record, model.PluginKVSetOptions{Atomic: true, OldValue: nil})
	if appErr != nil || !saved {
		return "", nil, errors.New("failed to save the calendar feed")
	}

	pointer := &feedPointer{Hash: hash, CreatedAt: now.UnixMilli()}
	data, err := json.Marshal(pointer)
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to encode the calendar feed")
	}
	key := feedPointerKey(userID, teamID)
	for range atomicUpdateAttempts {
		old, appErr := s.kv.KVGet(key)
		if appErr != nil {
			break
		}
		saved, appErr := s.kv.KVSetWithOptions(key, data, model.PluginKVSetOptions{Atomic: true, OldValue: old})
		if appErr != nil {
			break
		}
		if !saved {
			// Another reset at the same time: read the pointer again.
			continue
		}
		if old != nil {
			var previous feedPointer
			if json.Unmarshal(old, &previous) == nil && previous.Hash != "" && previous.Hash != hash {
				// Already revoked by the pointer's move; this only tidies the store.
				_ = s.kv.KVDelete(feedRecordKey(previous.Hash))
			}
		}
		return token, pointer, nil
	}
	_ = s.kv.KVDelete(feedRecordKey(hash))
	return "", nil, errors.New("failed to save the calendar feed, try again")
}

// DeleteFeed revokes a member's feed of a community, if they have one: the pointer first, then
// the record.
func (s *Store) DeleteFeed(userID, teamID string) error {
	pointer, err := s.GetFeedPointer(userID, teamID)
	if err != nil || pointer == nil {
		return err
	}
	if appErr := s.kv.KVDelete(feedPointerKey(userID, teamID)); appErr != nil {
		return errors.Wrap(appErr, "failed to delete the calendar feed")
	}
	if pointer.Hash != "" {
		if appErr := s.kv.KVDelete(feedRecordKey(pointer.Hash)); appErr != nil {
			return errors.Wrap(appErr, "failed to delete the calendar feed")
		}
	}
	return nil
}

// DeleteUserFeeds revokes every feed of a member (once they're deactivated).
func (s *Store) DeleteUserFeeds(userID string) error {
	const perPage = 1000
	prefix := feedPointerKeyPrefix + userID + "_"
	var teams []string
	for page := 0; ; page++ {
		keys, appErr := s.kv.KVList(page, perPage)
		if appErr != nil {
			return errors.Wrap(appErr, "failed to list the calendar feeds")
		}
		for _, key := range keys {
			if teamID, ok := strings.CutPrefix(key, prefix); ok {
				teams = append(teams, teamID)
			}
		}
		if len(keys) < perPage {
			break
		}
	}
	for _, teamID := range teams {
		if err := s.DeleteFeed(userID, teamID); err != nil {
			return err
		}
	}
	return nil
}

// LookupFeed returns the record of a feed token's hash, nil when the token is unknown or was
// revoked: its member's pointer for that community must still name this very hash (compared in
// constant time).
func (s *Store) LookupFeed(hash string) (*feedRecord, error) {
	data, appErr := s.kv.KVGet(feedRecordKey(hash))
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to read the calendar feed")
	}
	if data == nil {
		return nil, nil
	}
	var record feedRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, errors.Wrap(err, "failed to decode the calendar feed")
	}
	if record.UserID == "" || record.TeamID == "" {
		return nil, nil
	}
	pointer, err := s.GetFeedPointer(record.UserID, record.TeamID)
	if err != nil {
		return nil, err
	}
	if pointer == nil || subtle.ConstantTimeCompare([]byte(pointer.Hash), []byte(hash)) != 1 {
		return nil, nil
	}
	return &record, nil
}

const (
	// feedRateBurst and feedRateInterval limit how often one feed is read: a burst of 6, then
	// one every 10 seconds (about 6 a minute). Calendar apps read a feed every hour or so.
	feedRateBurst    = 6
	feedRateInterval = 10 * time.Second
	// maxFeedLimiterEntries bounds the limiter's memory: past it, idle feeds are forgotten.
	maxFeedLimiterEntries = 10000
)

// feedLimiter limits the reads of each feed, by its token's hash, in this server's memory (each
// server of a cluster counts its own): a token bucket per hash.
type feedLimiter struct {
	mu      sync.Mutex
	buckets map[string]*feedBucket
}

type feedBucket struct {
	tokens float64
	last   time.Time
}

func newFeedLimiter() *feedLimiter {
	return &feedLimiter{buckets: map[string]*feedBucket{}}
}

// allow takes one read of a feed at now, and returns whether it may go ahead and, when it may
// not, how long until it may.
func (l *feedLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxFeedLimiterEntries {
			l.sweep(now)
		}
		b = &feedBucket{tokens: feedRateBurst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = math.Min(feedRateBurst, b.tokens+elapsed.Seconds()/feedRateInterval.Seconds())
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) * float64(feedRateInterval))
	return false, wait
}

// sweep forgets the feeds whose bucket is full again (nothing to remember), then, if that's not
// enough, any others: the limiter's memory stays bounded whatever tokens are tried.
func (l *feedLimiter) sweep(now time.Time) {
	full := feedRateBurst * feedRateInterval
	for key, b := range l.buckets {
		if now.Sub(b.last) >= full {
			delete(l.buckets, key)
		}
	}
	for key := range l.buckets {
		if len(l.buckets) < maxFeedLimiterEntries/2 {
			break
		}
		delete(l.buckets, key)
	}
}
