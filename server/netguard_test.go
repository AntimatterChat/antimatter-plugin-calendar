// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPrivateIP(t *testing.T) {
	for ip, private := range map[string]bool{
		"127.0.0.1":        true,
		"10.1.2.3":         true,
		"172.16.0.1":       true,
		"192.168.1.1":      true,
		"169.254.169.254":  true,
		"100.64.0.1":       true,
		"0.0.0.0":          true,
		"224.0.0.1":        true,
		"::1":              true,
		"fe80::1":          true,
		"fd00::1":          true,
		"::ffff:127.0.0.1": true,
		"93.184.215.14":    false,
		"2606:4700::1":     false,
	} {
		assert.Equal(t, private, isPrivateIP(net.ParseIP(ip)), ip)
	}
}

func TestDialerRefusesPrivateAddresses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	_, err = newDialer(false).Dial("tcp", listener.Addr().String())
	require.Error(t, err)
	assert.True(t, errors.Is(err, errPrivateAddress))

	conn, err := newDialer(true).Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	conn.Close()
}
