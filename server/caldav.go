// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/pkg/errors"
)

const (
	requestTimeout  = 30 * time.Second
	maxResponseSize = 20 << 20
	maxRedirects    = 5
	maxQueryWindow  = 400 * 24 * time.Hour
)

var (
	errAuth         = newAPIError(http.StatusBadRequest, "the calendar server refused the username or password")
	errForbidden    = newAPIError(http.StatusForbidden, "the calendar server doesn't let you do this")
	errNotFound     = newAPIError(http.StatusNotFound, "not found on the calendar server")
	errChanged      = newAPIError(http.StatusConflict, "the event changed on the calendar server, reload it and try again")
	errNotCalDAV    = newAPIError(http.StatusBadRequest, "no CalDAV calendars were found at this address")
	errInsecureHTTP = newAPIError(http.StatusForbidden, "unencrypted connections to calendar servers aren't allowed on this server")
)

// davOptions say how to connect to CalDAV servers.
type davOptions struct {
	dialer        *net.Dialer
	allowInsecure bool
	// tlsConfig is nil for the default TLS configuration (tests set their own CA).
	tlsConfig *tls.Config
}

// limitedBody bounds the size of the responses of calendar servers.
type limitedBody struct {
	io.Reader
	io.Closer
}

type limitedTransport struct {
	base http.RoundTripper
}

func (t limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = limitedBody{Reader: io.LimitReader(resp.Body, maxResponseSize), Closer: resp.Body}
	return resp, nil
}

// davClient talks to the CalDAV server of a user, with their credentials.
type davClient struct {
	http          *http.Client
	username      string
	password      string
	allowInsecure bool

	// lastStatus is the status of the last failed response, which go-webdav's errors hide.
	mu         sync.Mutex
	lastStatus int
}

func newDAVClient(username, password string, opts davOptions) *davClient {
	transport := &http.Transport{
		DialContext:           opts.dialer.DialContext,
		TLSClientConfig:       opts.tlsConfig,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: requestTimeout,
		// No proxy: the private network check applies to the calendar server itself
		Proxy: nil,
	}
	c := &davClient{username: username, password: password, allowInsecure: opts.allowInsecure}
	c.http = &http.Client{
		Transport: limitedTransport{base: transport},
		Timeout:   requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && !opts.allowInsecure {
				return errInsecureHTTP
			}
			// Redirects keep the credentials on the same host only
			if req.URL.Host == via[0].URL.Host {
				req.SetBasicAuth(username, password)
			}
			return nil
		},
	}
	return c
}

// Do sends a request with the user's credentials. It implements webdav.HTTPClient.
func (c *davClient) Do(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" && !c.allowInsecure {
		return nil, errInsecureHTTP
	}
	req.SetBasicAuth(c.username, c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		c.mu.Lock()
		c.lastStatus = resp.StatusCode
		c.mu.Unlock()
	}
	return resp, nil
}

// wrap turns the error of a request into one to show the user.
func (c *davClient) wrap(err error, what string) error {
	if err == nil {
		return nil
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	c.mu.Lock()
	status := c.lastStatus
	c.lastStatus = 0
	c.mu.Unlock()
	return statusError(status, err, what)
}

func statusError(status int, err error, what string) error {
	switch status {
	case http.StatusUnauthorized:
		return errAuth
	case http.StatusForbidden:
		return errForbidden
	case http.StatusNotFound, http.StatusGone:
		return errNotFound
	case http.StatusPreconditionFailed:
		return errChanged
	}
	if errors.Is(err, errPrivateAddress) {
		return errPrivateAddress
	}
	if errors.Is(err, errInsecureHTTP) {
		return errInsecureHTTP
	}
	return &apiError{status: http.StatusBadGateway, err: errors.Wrapf(err, "couldn't %s on the calendar server", what)}
}

// multistatus is the part of a WebDAV multistatus response the plugin reads.
type multistatus struct {
	Responses []struct {
		Href     string `xml:"DAV: href"`
		Propstat []struct {
			Status string `xml:"DAV: status"`
			Prop   struct {
				CurrentUserPrincipal struct {
					Href string `xml:"DAV: href"`
				} `xml:"DAV: current-user-principal"`
				CalendarHomeSet struct {
					Href string `xml:"DAV: href"`
				} `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set"`
				DisplayName  string `xml:"DAV: displayname"`
				ResourceType struct {
					Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
				} `xml:"DAV: resourcetype"`
				Components struct {
					Comps []struct {
						Name string `xml:"name,attr"`
					} `xml:"urn:ietf:params:xml:ns:caldav comp"`
				} `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
				Color      string `xml:"http://apple.com/ns/ical/ calendar-color"`
				Privileges struct {
					Privileges []struct {
						Write    *struct{} `xml:"DAV: write"`
						WriteAll *struct{} `xml:"DAV: all"`
						Content  *struct{} `xml:"DAV: write-content"`
					} `xml:"DAV: privilege"`
				} `xml:"DAV: current-user-privilege-set"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

// propfind sends a PROPFIND and decodes its multistatus response.
func (c *davClient) propfind(ctx context.Context, target string, depth string, body string) (*multistatus, error) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", depth)
	resp, err := c.Do(req)
	if err != nil {
		return nil, statusError(0, err, "connect")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, statusError(resp.StatusCode, errors.Errorf("unexpected response %s", resp.Status), "read the calendars")
	}
	var ms multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, errors.Wrap(err, "invalid response from the calendar server")
	}
	return &ms, nil
}

func resolve(base *url.URL, href string) (*url.URL, error) {
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return nil, err
	}
	return base.ResolveReference(ref), nil
}

// findHomeSet finds the calendar home set of the user from a CalDAV URL: the URL's principal,
// then the principal's home set.
func (c *davClient) findHomeSet(ctx context.Context, endpoint *url.URL) (*url.URL, error) {
	ms, err := c.propfind(ctx, endpoint.String(), "0", `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:current-user-principal/><c:calendar-home-set/></d:prop></d:propfind>`)
	if err != nil {
		return nil, err
	}

	var principal, homeSet string
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			if href := ps.Prop.CalendarHomeSet.Href; href != "" {
				homeSet = href
			}
			if href := ps.Prop.CurrentUserPrincipal.Href; href != "" {
				principal = href
			}
		}
	}
	if homeSet != "" {
		return resolve(endpoint, homeSet)
	}
	if principal == "" {
		return nil, errNotCalDAV
	}

	principalURL, err := resolve(endpoint, principal)
	if err != nil {
		return nil, errNotCalDAV
	}
	ms, err = c.propfind(ctx, principalURL.String(), "0", `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><c:calendar-home-set/></d:prop></d:propfind>`)
	if err != nil {
		return nil, err
	}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			if href := ps.Prop.CalendarHomeSet.Href; href != "" {
				return resolve(principalURL, href)
			}
		}
	}
	return nil, errNotCalDAV
}

// wellKnownTarget returns where a server's /.well-known/caldav redirects to.
func (c *davClient) wellKnownTarget(ctx context.Context, base *url.URL) (*url.URL, error) {
	wellKnown := &url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/.well-known/caldav"}
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", wellKnown.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "0")
	if req.URL.Scheme != "https" && !c.allowInsecure {
		return nil, errInsecureHTTP
	}
	req.SetBasicAuth(c.username, c.password)

	noRedirect := *c.http
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if location := resp.Header.Get("Location"); location != "" {
			return resolve(wellKnown, location)
		}
	}
	return wellKnown, nil
}

// discover finds the calendar home set of the user from the URL they entered: the URL itself,
// then the server's /.well-known/caldav (RFC 6764), then the DNS SRV record of the domain.
func (c *davClient) discover(ctx context.Context, raw string) (*url.URL, error) {
	endpoint, err := normalizeURL(raw)
	if err != nil {
		return nil, err
	}

	homeSet, err := c.findHomeSet(ctx, endpoint)
	if err == nil || errors.Is(err, errAuth) || errors.Is(err, errPrivateAddress) || errors.Is(err, errInsecureHTTP) {
		return homeSet, err
	}
	firstErr := err

	if target, wkErr := c.wellKnownTarget(ctx, endpoint); wkErr == nil {
		if homeSet, err = c.findHomeSet(ctx, target); err == nil || errors.Is(err, errAuth) {
			return homeSet, err
		}
	}

	if endpoint.Path == "" || endpoint.Path == "/" {
		if found, srvErr := caldav.DiscoverContextURL(ctx, endpoint.Hostname()); srvErr == nil {
			if target, err := normalizeURL(found); err == nil {
				if homeSet, err = c.findHomeSet(ctx, target); err == nil || errors.Is(err, errAuth) {
					return homeSet, err
				}
			}
		}
	}
	return nil, firstErr
}

// Calendar is a calendar of the user.
type Calendar struct {
	// ID is the calendar's path on the server.
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	ReadOnly    bool   `json:"read_only"`
	Description string `json:"description,omitempty"`
}

// listCalendars returns the calendars of a home set that hold events.
func (c *davClient) listCalendars(ctx context.Context, homeSet *url.URL) ([]Calendar, error) {
	ms, err := c.propfind(ctx, homeSet.String(), "1", `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:a="http://apple.com/ns/ical/"><d:prop><d:displayname/><d:resourcetype/><c:supported-calendar-component-set/><a:calendar-color/><d:current-user-privilege-set/></d:prop></d:propfind>`)
	if err != nil {
		return nil, err
	}

	calendars := []Calendar{}
	for _, r := range ms.Responses {
		href, err := resolve(homeSet, r.Href)
		if err != nil {
			continue
		}
		var cal Calendar
		isCalendar, hasEvents, privileges, writable := false, true, false, false
		for _, ps := range r.Propstat {
			if !strings.Contains(ps.Status, " 200 ") {
				continue
			}
			prop := ps.Prop
			if prop.ResourceType.Calendar != nil {
				isCalendar = true
			}
			if prop.DisplayName != "" {
				cal.Name = prop.DisplayName
			}
			if len(prop.Components.Comps) > 0 {
				hasEvents = false
				for _, comp := range prop.Components.Comps {
					if strings.EqualFold(comp.Name, ical.CompEvent) {
						hasEvents = true
					}
				}
			}
			if prop.Color != "" {
				cal.Color = normalizeColor(prop.Color)
			}
			for _, p := range prop.Privileges.Privileges {
				privileges = true
				if p.Write != nil || p.WriteAll != nil || p.Content != nil {
					writable = true
				}
			}
		}
		if !isCalendar || !hasEvents {
			continue
		}
		cal.ID = href.Path
		if cal.Name == "" {
			cal.Name = path.Base(strings.TrimSuffix(href.Path, "/"))
		}
		cal.ReadOnly = privileges && !writable
		calendars = append(calendars, cal)
	}
	return calendars, nil
}

// normalizeColor turns the #RRGGBBAA colors of Apple's calendar-color into CSS colors.
func normalizeColor(color string) string {
	color = strings.TrimSpace(color)
	if len(color) == 9 && strings.HasPrefix(color, "#") {
		color = color[:7]
	}
	if len(color) != 7 || !strings.HasPrefix(color, "#") {
		return ""
	}
	for _, r := range color[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return ""
		}
	}
	return color
}

// calendarClient returns a go-webdav client for the server of a home set.
func (c *davClient) calendarClient(homeSet *url.URL) (*caldav.Client, error) {
	return caldav.NewClient(c, (&url.URL{Scheme: homeSet.Scheme, Host: homeSet.Host, Path: "/"}).String())
}

// queryEvents returns the calendar objects of a calendar with events between from and to.
func (c *davClient) queryEvents(ctx context.Context, homeSet *url.URL, calendar string, from, to time.Time) ([]caldav.CalendarObject, error) {
	if to.Sub(from) > maxQueryWindow {
		return nil, newAPIError(http.StatusBadRequest, "the time range is too long")
	}
	client, err := c.calendarClient(homeSet)
	if err != nil {
		return nil, err
	}
	objects, err := client.QueryCalendar(ctx, calendar, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true, AllComps: true},
		CompFilter: caldav.CompFilter{
			Name:  ical.CompCalendar,
			Comps: []caldav.CompFilter{{Name: ical.CompEvent, Start: from.UTC(), End: to.UTC()}},
		},
	})
	return objects, c.wrap(err, "read the events")
}

// getObject returns a calendar object.
func (c *davClient) getObject(ctx context.Context, homeSet *url.URL, objectPath string) (*caldav.CalendarObject, error) {
	client, err := c.calendarClient(homeSet)
	if err != nil {
		return nil, err
	}
	object, err := client.GetCalendarObject(ctx, objectPath)
	return object, c.wrap(err, "read the event")
}

// ETags are kept unquoted, as go-webdav reads them, and quoted in conditional requests.
func quoteETag(etag string) string {
	if strings.HasPrefix(etag, `"`) || strings.HasPrefix(etag, "W/") {
		return etag
	}
	return strconv.Quote(etag)
}

func unquoteETag(etag string) string {
	if unquoted, err := strconv.Unquote(etag); err == nil {
		return unquoted
	}
	return etag
}

func objectURL(homeSet *url.URL, objectPath string) string {
	return (&url.URL{Scheme: homeSet.Scheme, Host: homeSet.Host, Path: objectPath}).String()
}

// putObject writes a calendar object. With an ETag, it only replaces that version of the object;
// without, it only creates a new one. It returns the new ETag, if the server says it.
func (c *davClient) putObject(ctx context.Context, homeSet *url.URL, objectPath string, cal *ical.Calendar, etag string) (string, error) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", errors.Wrap(err, "failed to encode the event")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, objectURL(homeSet, objectPath), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", ical.MIMEType+"; charset=utf-8")
	if etag != "" {
		req.Header.Set("If-Match", quoteETag(etag))
	} else {
		req.Header.Set("If-None-Match", "*")
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", statusError(0, err, "save the event")
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", statusError(resp.StatusCode, errors.Errorf("unexpected response %s", resp.Status), "save the event")
	}
	return unquoteETag(resp.Header.Get("ETag")), nil
}

// deleteObject deletes a calendar object, if it's still at that version.
func (c *davClient) deleteObject(ctx context.Context, homeSet *url.URL, objectPath, etag string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, objectURL(homeSet, objectPath), http.NoBody)
	if err != nil {
		return err
	}
	if etag != "" {
		req.Header.Set("If-Match", quoteETag(etag))
	}
	resp, err := c.Do(req)
	if err != nil {
		return statusError(0, err, "delete the event")
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return statusError(resp.StatusCode, errors.Errorf("unexpected response %s", resp.Status), "delete the event")
	}
	return nil
}
