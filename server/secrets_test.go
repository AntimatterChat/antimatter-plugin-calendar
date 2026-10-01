// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEncryptionKey(t *testing.T) {
	kv := newFakeKV()

	key, err := loadEncryptionKey(kv)
	require.NoError(t, err)
	assert.Len(t, key, encryptionKeySize)

	again, err := loadEncryptionKey(kv)
	require.NoError(t, err)
	assert.Equal(t, key, again, "the key is created once")

	kv.data[encryptionKeyKey] = []byte("short")
	_, err = loadEncryptionKey(kv)
	assert.Error(t, err)
}

func TestSecretBox(t *testing.T) {
	key, err := loadEncryptionKey(newFakeKV())
	require.NoError(t, err)
	box, err := newSecretBox(key)
	require.NoError(t, err)

	sealed, err := box.Seal("hunter2", "user1/imap")
	require.NoError(t, err)
	assert.NotContains(t, sealed, "hunter2")

	other, err := box.Seal("hunter2", "user1/imap")
	require.NoError(t, err)
	assert.NotEqual(t, sealed, other, "each seal uses a new nonce")

	plain, err := box.Open(sealed, "user1/imap")
	require.NoError(t, err)
	assert.Equal(t, "hunter2", plain)

	_, err = box.Open(sealed, "user2/imap")
	assert.Error(t, err, "a secret can't be opened in another context")

	_, err = box.Open("garbage", "user1/imap")
	assert.Error(t, err)
	_, err = box.Open(sealedPrefix+"AAAA", "user1/imap")
	assert.Error(t, err)
}
