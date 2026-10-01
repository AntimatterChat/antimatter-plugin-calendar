// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memBackend is an in-memory CalDAV backend with the calendars of one user, "ada".
type memBackend struct {
	mu        sync.Mutex
	calendars []caldav.Calendar
	objects   map[string]caldav.CalendarObject
	version   int
}

const (
	// go-webdav's server finds what a path is by its depth under the prefix
	testPrincipal = "/dav/ada/"
	testHomeSet   = "/dav/ada/calendars/"
	testWork      = "/dav/ada/calendars/work/"
	testTasks     = "/dav/ada/calendars/tasks/"
)

func newMemBackend() *memBackend {
	return &memBackend{
		calendars: []caldav.Calendar{
			{Path: testWork, Name: "Work", SupportedComponentSet: []string{ical.CompEvent}},
			{Path: testTasks, Name: "Tasks", SupportedComponentSet: []string{ical.CompToDo}},
		},
		objects: map[string]caldav.CalendarObject{},
	}
}

func (b *memBackend) CurrentUserPrincipal(context.Context) (string, error) { return testPrincipal, nil }
func (b *memBackend) CalendarHomeSetPath(context.Context) (string, error)  { return testHomeSet, nil }
func (b *memBackend) CreateCalendar(context.Context, *caldav.Calendar) error {
	return webdav.NewHTTPError(http.StatusForbidden, nil)
}

func (b *memBackend) ListCalendars(context.Context) ([]caldav.Calendar, error) {
	return b.calendars, nil
}

func (b *memBackend) GetCalendar(_ context.Context, path string) (*caldav.Calendar, error) {
	for _, cal := range b.calendars {
		if cal.Path == path {
			return &cal, nil
		}
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, nil)
}

func (b *memBackend) GetCalendarObject(_ context.Context, path string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	object, ok := b.objects[path]
	if !ok {
		return nil, webdav.NewHTTPError(http.StatusNotFound, nil)
	}
	return &object, nil
}

func (b *memBackend) ListCalendarObjects(_ context.Context, path string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var objects []caldav.CalendarObject
	for p, object := range b.objects {
		if strings.HasPrefix(p, path) {
			objects = append(objects, object)
		}
	}
	return objects, nil
}

func (b *memBackend) QueryCalendarObjects(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	objects, _ := b.ListCalendarObjects(ctx, path, nil)
	return caldav.Filter(query, objects)
}

func (b *memBackend) PutCalendarObject(_ context.Context, path string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	existing, exists := b.objects[path]
	if opts.IfNoneMatch.IsWildcard() && exists {
		return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, nil)
	}
	if opts.IfMatch.IsSet() {
		if etag, _ := opts.IfMatch.ETag(); !exists || etag != existing.ETag {
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, nil)
		}
	}
	b.version++
	object := caldav.CalendarObject{Path: path, ModTime: time.Now(), ETag: fmt.Sprintf("v%d", b.version), Data: cal}
	b.objects[path] = object
	return &object, nil
}

func (b *memBackend) DeleteCalendarObject(_ context.Context, path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.objects[path]; !ok {
		return webdav.NewHTTPError(http.StatusNotFound, nil)
	}
	delete(b.objects, path)
	return nil
}

func (b *memBackend) put(t *testing.T, path, data string) {
	t.Helper()
	_, err := b.PutCalendarObject(context.Background(), path, decodeCalendar(t, data), &caldav.PutCalendarObjectOptions{})
	require.NoError(t, err)
}

// startCalDAVServer serves a memory backend with basic auth (ada / secret), and
// /.well-known/caldav redirecting to /dav/.
func startCalDAVServer(t *testing.T) (*memBackend, *httptest.Server) {
	t.Helper()
	backend := newMemBackend()
	handler := &caldav.Handler{Backend: backend, Prefix: "/dav"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/caldav", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/dav/", func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "ada" || password != "secret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// go-webdav's server leaves the preconditions of deletions to the backend, which can't see them
		if ifMatch := r.Header.Get("If-Match"); r.Method == http.MethodDelete && ifMatch != "" {
			etag, _ := webdav.ConditionalMatch(ifMatch).ETag()
			backend.mu.Lock()
			object, ok := backend.objects[r.URL.Path]
			backend.mu.Unlock()
			if ok && object.ETag != etag {
				http.Error(w, "precondition failed", http.StatusPreconditionFailed)
				return
			}
		}
		handler.ServeHTTP(w, r)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return backend, server
}

var testDAVOptions = davOptions{dialer: newDialer(true), allowInsecure: true}

func TestDiscover(t *testing.T) {
	_, server := startCalDAVServer(t)
	ctx := context.Background()

	// From the CalDAV URL itself, and from the server's root through /.well-known/caldav
	for _, entered := range []string{server.URL + "/dav/", server.URL} {
		homeSet, err := newDAVClient("ada", "secret", testDAVOptions).discover(ctx, entered)
		require.NoError(t, err, entered)
		assert.Equal(t, server.URL+testHomeSet, homeSet.String())
	}

	_, err := newDAVClient("ada", "wrong", testDAVOptions).discover(ctx, server.URL+"/dav/")
	assert.Equal(t, errAuth, err)

	_, err = newDAVClient("ada", "secret", davOptions{dialer: newDialer(true)}).discover(ctx, server.URL)
	assert.Equal(t, errInsecureHTTP, err)

	_, err = newDAVClient("ada", "secret", davOptions{dialer: newDialer(false), allowInsecure: true}).discover(ctx, server.URL)
	assert.Equal(t, errPrivateAddress, err)
}

func TestCalendarsAndEvents(t *testing.T) {
	backend, server := startCalDAVServer(t)
	ctx := context.Background()
	client := newDAVClient("ada", "secret", testDAVOptions)
	homeSet, err := url.Parse(server.URL + testHomeSet)
	require.NoError(t, err)

	calendars, err := client.listCalendars(ctx, homeSet)
	require.NoError(t, err)
	assert.Equal(t, []Calendar{{ID: testWork, Name: "Work"}}, calendars, "calendars without events aren't listed")

	// go-webdav's server only knows IANA time zone names
	backend.put(t, testWork+"standup.ics", strings.ReplaceAll(standupCalendar, "/mozilla.org/20050126_1/", ""))
	backend.put(t, testWork+"old.ics", `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:old
DTSTAMP:20200101T000000Z
DTSTART:20200101T100000Z
SUMMARY:Old
END:VEVENT
END:VCALENDAR
`)

	paris := mustLoad(t, "Europe/Paris")
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, paris)
	objects, err := client.queryEvents(ctx, homeSet, testWork, from, from.AddDate(0, 0, 7))
	require.NoError(t, err)
	require.Len(t, objects, 1)
	assert.Equal(t, testWork+"standup.ics", objects[0].Path)

	_, err = client.queryEvents(ctx, homeSet, testWork, from, from.AddDate(2, 0, 0))
	assert.Equal(t, 400, statusFor(err))

	// Create, update, and delete with ETags
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	in := &EventInput{Summary: "Review", Start: "2026-10-05T09:00:00Z", End: "2026-10-05T10:00:00Z", TimeZone: "Europe/Paris"}
	cal, err := newEventObject(in, "new-event", now, "")
	require.NoError(t, err)
	_, err = client.putObject(ctx, homeSet, testWork+"new-event.ics", cal, "")
	require.NoError(t, err)
	_, err = client.putObject(ctx, homeSet, testWork+"new-event.ics", cal, "")
	assert.Equal(t, errChanged, err, "creating doesn't overwrite")

	object, err := client.getObject(ctx, homeSet, testWork+"new-event.ics")
	require.NoError(t, err)
	assert.NotEmpty(t, object.ETag)
	require.NoError(t, updateEventObject(object.Data, &EventInput{Summary: "Review (moved)", Start: "2026-10-05T11:00:00Z", End: "2026-10-05T12:00:00Z"}, now, ""))
	_, err = client.putObject(ctx, homeSet, testWork+"new-event.ics", object.Data, object.ETag)
	require.NoError(t, err)
	_, err = client.putObject(ctx, homeSet, testWork+"new-event.ics", object.Data, object.ETag)
	assert.Equal(t, errChanged, err, "the old version can't be overwritten")

	assert.Equal(t, errChanged, client.deleteObject(ctx, homeSet, testWork+"new-event.ics", object.ETag))
	object, err = client.getObject(ctx, homeSet, testWork+"new-event.ics")
	require.NoError(t, err)
	assert.Equal(t, "Review (moved)", textProp(masterEvent(object.Data), ical.PropSummary))
	require.NoError(t, client.deleteObject(ctx, homeSet, testWork+"new-event.ics", object.ETag))
	_, err = client.getObject(ctx, homeSet, testWork+"new-event.ics")
	assert.Equal(t, errNotFound, err)
}
