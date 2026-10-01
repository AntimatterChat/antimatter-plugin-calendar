// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// encryptionKeyKey is the KV key of the key that encrypts the users' passwords. It's created
	// the first time the plugin starts.
	encryptionKeyKey  = "encryption_key"
	encryptionKeySize = 32

	// sealedPrefix versions the format of the sealed secrets.
	sealedPrefix = "v1:"
)

// loadEncryptionKey returns the plugin's encryption key, creating it if needed. Creating it is
// atomic, so that the servers of a cluster agree on one key.
func loadEncryptionKey(kv kvAPI) ([]byte, error) {
	for range atomicUpdateAttempts {
		key, appErr := kv.KVGet(encryptionKeyKey)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to read the encryption key")
		}
		if key != nil {
			if len(key) != encryptionKeySize {
				return nil, errors.New("the stored encryption key is invalid")
			}
			return key, nil
		}

		key = make([]byte, encryptionKeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, errors.Wrap(err, "failed to generate an encryption key")
		}
		saved, appErr := kv.KVSetWithOptions(encryptionKeyKey, key, model.PluginKVSetOptions{Atomic: true, OldValue: nil})
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to save the encryption key")
		}
		if saved {
			return key, nil
		}
		// Another server created it first: read it back.
	}
	return nil, errors.New("failed to create the encryption key")
}

// secretBox encrypts and decrypts secrets (AES-256-GCM). Each secret is bound to a context (the
// user and the field it belongs to), so a sealed secret can't be moved to another user or field.
type secretBox struct {
	aead cipher.AEAD
}

func newSecretBox(key []byte) (*secretBox, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.Wrap(err, "invalid encryption key")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.Wrap(err, "failed to set up the encryption")
	}
	return &secretBox{aead: aead}, nil
}

// Seal encrypts a secret for the given context.
func (b *secretBox) Seal(secret, context string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", errors.Wrap(err, "failed to generate a nonce")
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(secret), []byte(context))
	return sealedPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a secret sealed for the given context.
func (b *secretBox) Open(sealed, context string) (string, error) {
	encoded, ok := strings.CutPrefix(sealed, sealedPrefix)
	if !ok {
		return "", errors.New("unknown secret format")
	}
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.Wrap(err, "invalid secret encoding")
	}
	if len(data) < b.aead.NonceSize() {
		return "", errors.New("secret too short")
	}
	nonce, ciphertext := data[:b.aead.NonceSize()], data[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, ciphertext, []byte(context))
	if err != nil {
		return "", errors.New("failed to decrypt the secret")
	}
	return string(plain), nil
}
