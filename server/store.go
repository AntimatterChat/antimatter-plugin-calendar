// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// accountKeyPrefix prefixes the key of the calendar account of each user.
	accountKeyPrefix = "account_"

	atomicUpdateAttempts = 5

	// defaultRemindMinutes is how long before an event without alarms its reminder is sent.
	defaultRemindMinutes = 10
	maxRemindMinutes     = 7 * 24 * 60
)

// kvAPI is the part of the plugin API used to store data (implemented by plugin.API).
type kvAPI interface {
	KVGet(key string) ([]byte, *model.AppError)
	KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError)
	KVDelete(key string) *model.AppError
	KVList(page, perPage int) ([]string, *model.AppError)
}

// Account is the calendar account of a user. The password is only kept decrypted in memory.
type Account struct {
	// URL is the CalDAV server URL the user entered.
	URL      string `json:"url"`
	Username string `json:"username"`

	// HomeSetURL is the calendar home set found on the server, where the user's calendars are.
	HomeSetURL string `json:"home_set_url"`

	// RemindMinutes is how long before events without alarms of their own the user is reminded
	// of them, 0 for no reminders.
	RemindMinutes int `json:"remind_minutes"`

	Password string `json:"-"`
}

// normalizeURL adds the https scheme to URLs entered without one.
func normalizeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid calendar server URL")
	}
	u.Fragment = ""
	return u, nil
}

// Validate checks the account settings, but not whether the server accepts them.
func (a *Account) Validate() error {
	if _, err := normalizeURL(a.URL); err != nil {
		return err
	}
	if a.Username == "" {
		return newAPIError(http.StatusBadRequest, "a username is required")
	}
	if a.Password == "" {
		return newAPIError(http.StatusBadRequest, "a password is required")
	}
	if a.RemindMinutes < 0 || a.RemindMinutes > maxRemindMinutes {
		return newAPIError(http.StatusBadRequest, "invalid reminder time")
	}
	return nil
}

// storedAccount is an account as stored in the KV store, with an encrypted password.
type storedAccount struct {
	Account
	PasswordSealed string `json:"password"`
	UpdatedAt      int64  `json:"updated_at"`
}

// Store keeps the users' calendar accounts in the plugin's KV store.
type Store struct {
	kv  kvAPI
	box *secretBox
}

func NewStore(kv kvAPI, box *secretBox) *Store {
	return &Store{kv: kv, box: box}
}

func accountKey(userID string) string {
	return accountKeyPrefix + userID
}

func secretContext(userID string) string {
	return userID + "/caldav"
}

// GetAccount returns the calendar account of a user, or nil if the user has none.
func (s *Store) GetAccount(userID string) (*Account, error) {
	data, appErr := s.kv.KVGet(accountKey(userID))
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to read the calendar account")
	}
	if data == nil {
		return nil, nil
	}

	var stored storedAccount
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, errors.Wrap(err, "failed to decode the calendar account")
	}
	account := stored.Account

	var err error
	if account.Password, err = s.box.Open(stored.PasswordSealed, secretContext(userID)); err != nil {
		return nil, errors.Wrap(err, "failed to decrypt the calendar password")
	}
	return &account, nil
}

// SaveAccount stores the calendar account of a user, encrypting the password.
func (s *Store) SaveAccount(userID string, account *Account) error {
	stored := storedAccount{Account: *account, UpdatedAt: model.GetMillis()}

	var err error
	if stored.PasswordSealed, err = s.box.Seal(account.Password, secretContext(userID)); err != nil {
		return err
	}

	data, err := json.Marshal(stored)
	if err != nil {
		return errors.Wrap(err, "failed to encode the calendar account")
	}
	if _, appErr := s.kv.KVSetWithOptions(accountKey(userID), data, model.PluginKVSetOptions{}); appErr != nil {
		return errors.Wrap(appErr, "failed to save the calendar account")
	}
	return nil
}

// DeleteAccount forgets the calendar account of a user.
func (s *Store) DeleteAccount(userID string) error {
	if appErr := s.kv.KVDelete(accountKey(userID)); appErr != nil {
		return errors.Wrap(appErr, "failed to delete the calendar account")
	}
	return nil
}

// ListAccountUsers returns the users who have a calendar account.
func (s *Store) ListAccountUsers() ([]string, error) {
	const perPage = 1000
	var users []string
	for page := 0; ; page++ {
		keys, appErr := s.kv.KVList(page, perPage)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to list the calendar accounts")
		}
		for _, key := range keys {
			if userID, ok := strings.CutPrefix(key, accountKeyPrefix); ok {
				users = append(users, userID)
			}
		}
		if len(keys) < perPage {
			return users, nil
		}
	}
}
