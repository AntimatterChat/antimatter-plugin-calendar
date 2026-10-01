// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*Store, *fakeKV) {
	t.Helper()
	kv := newFakeKV()
	key, err := loadEncryptionKey(kv)
	require.NoError(t, err)
	box, err := newSecretBox(key)
	require.NoError(t, err)
	return NewStore(kv, box), kv
}

func testAccount() *Account {
	return &Account{
		URL:           "https://dav.example.com",
		Username:      "ada",
		HomeSetURL:    "https://dav.example.com/calendars/ada/",
		RemindMinutes: 10,
		Password:      "app-password",
	}
}

func TestStoreAccount(t *testing.T) {
	store, kv := newTestStore(t)

	account, err := store.GetAccount("user1")
	require.NoError(t, err)
	assert.Nil(t, account)

	require.NoError(t, store.SaveAccount("user1", testAccount()))
	require.NoError(t, store.SaveAccount("user2", testAccount()))
	assert.NotContains(t, string(kv.data[accountKey("user1")]), "app-password")

	account, err = store.GetAccount("user1")
	require.NoError(t, err)
	assert.Equal(t, testAccount(), account)

	users, err := store.ListAccountUsers()
	require.NoError(t, err)
	assert.Equal(t, []string{"user1", "user2"}, users)

	// A stored account copied to another user can't be decrypted
	kv.data[accountKey("user3")] = kv.data[accountKey("user1")]
	_, err = store.GetAccount("user3")
	assert.Error(t, err)

	require.NoError(t, store.DeleteAccount("user1"))
	account, err = store.GetAccount("user1")
	require.NoError(t, err)
	assert.Nil(t, account)
}

func TestAccountValidate(t *testing.T) {
	assert.NoError(t, testAccount().Validate())

	for name, change := range map[string]func(a *Account){
		"url":            func(a *Account) { a.URL = "ftp://dav.example.com" },
		"url user":       func(a *Account) { a.URL = "https://ada:pw@dav.example.com" },
		"empty url":      func(a *Account) { a.URL = "" },
		"username":       func(a *Account) { a.Username = "" },
		"password":       func(a *Account) { a.Password = "" },
		"reminder":       func(a *Account) { a.RemindMinutes = -1 },
		"reminder range": func(a *Account) { a.RemindMinutes = maxRemindMinutes + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			a := testAccount()
			change(a)
			assert.Error(t, a.Validate())
		})
	}

	u, err := normalizeURL(" dav.example.com/caldav ")
	require.NoError(t, err)
	assert.Equal(t, "https://dav.example.com/caldav", u.String())
}
