// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net"
	"net/http"
	"syscall"
	"time"
)

const (
	dialTimeout = 15 * time.Second
	keepAlive   = 30 * time.Second
)

var errPrivateAddress = newAPIError(http.StatusForbidden, "connecting to servers on private networks isn't allowed on this server")

// cgnat is the shared address space of carrier-grade NATs (RFC 6598), not covered by IsPrivate.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// isPrivateIP returns whether an address is on a private, loopback or link-local network, or
// isn't a unicast address.
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

// newDialer returns the dialer used to connect to calendar servers. Unless private networks are
// allowed, it refuses to connect to private addresses. The check runs on the resolved address
// of each connection, so a name that resolves to a private address is refused too.
func newDialer(allowPrivate bool) *net.Dialer {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || isPrivateIP(ip) {
				return errPrivateAddress
			}
			return nil
		}
	}
	return dialer
}
